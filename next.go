package workgraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type NextConfig struct {
	HomeDir          string
	DatabasePath     string
	Project          string
	Reason           string
	IncludeUntrusted bool
	Now              time.Time
	Clock            func() time.Time
	WatchInterval    time.Duration
}

type NextResult struct {
	Items   []NextItem `json:"items"`
	AsOf    time.Time  `json:"as_of"`
	Enabled bool       `json:"enabled"`
	Message string     `json:"-"`
}

type nextStoredEvent struct {
	ID      string
	Type    string
	At      time.Time
	Project string
	Actor   string
	Payload nextEventPayload
}

type nextEventPayload struct {
	NodeID           string         `json:"node_id"`
	Role             string         `json:"role"`
	HeadSHA          string         `json:"head_sha"`
	ActorKind        string         `json:"actor_kind"`
	ActorAssociation string         `json:"actor_association"`
	BaselineAt       string         `json:"baseline_at"`
	Baseline         map[string]any `json:"baseline"`
	State            map[string]any `json:"state"`
	Triage           map[string]any `json:"triage"`
	Untrusted        map[string]any `json:"untrusted"`
	ItemID           string         `json:"item_id"`
	Until            string         `json:"until"`
}

type nextSnapshot struct {
	result       NextResult
	events       []nextStoredEvent
	observations []githubPRObservation
}

func nextNow(config NextConfig) time.Time {
	if config.Clock != nil {
		return config.Clock()
	}
	if !config.Now.IsZero() {
		return config.Now
	}
	return time.Now()
}

func Next(config NextConfig) (NextResult, error) {
	snapshot, err := readNextSnapshot(config)
	return snapshot.result, err
}

func readNextSnapshot(config NextConfig) (nextSnapshot, error) {
	snapshot := nextSnapshot{result: NextResult{Items: []NextItem{}, AsOf: nextNow(config)}}
	path, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return snapshot, err
	}
	home, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return snapshot, err
	}
	runtime, err := readConnectorRuntimeFile(home)
	if err != nil {
		return snapshot, err
	}
	entry := runtime.entry(githubPRWatchConnectorID)
	interval := 5 * time.Minute
	if parsed, err := time.ParseDuration(entry.Interval); err == nil && parsed > 0 {
		interval = parsed
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return snapshot, err
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return snapshot, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('github_pr_watch_state','github_pr_observations')`).Scan(&exists); err != nil {
		return snapshot, err
	}
	if exists == 2 {
		var enabled bool
		err = tx.QueryRow(`SELECT enabled FROM github_pr_watch_state WHERE connector_id=?`, githubPRWatchConnectorID).Scan(&enabled)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return snapshot, err
		}
		snapshot.result.Enabled = enabled && connectorEnabled(runtime, githubPRWatchConnectorID)
	}
	if snapshot.result.Enabled {
		rows, err := tx.Query(`SELECT observation_json FROM github_pr_observations WHERE connector_id=? ORDER BY node_id,role`, githubPRWatchConnectorID)
		if err != nil {
			return snapshot, err
		}
		for rows.Next() {
			var raw string
			var observation githubPRObservation
			if err = rows.Scan(&raw); err == nil {
				err = json.Unmarshal([]byte(raw), &observation)
			}
			if err != nil {
				rows.Close()
				return snapshot, fmt.Errorf("read next observation: %w", err)
			}
			snapshot.observations = append(snapshot.observations, observation)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return snapshot, err
		}
	}
	snapshot.events, err = loadNextEvents(tx)
	if err != nil {
		return snapshot, err
	}
	if err = tx.Commit(); err != nil {
		return snapshot, err
	}
	for _, observation := range snapshot.observations {
		for _, item := range projectNextObservation(observation, snapshot.events, config, snapshot.result.AsOf, interval, entry.SetupState == "error") {
			if (config.Project != "" && item.Project != config.Project) || (config.Reason != "" && item.Reason != config.Reason) {
				continue
			}
			if nextHidden(item.ID, snapshot.events, snapshot.result.AsOf) != "" {
				continue
			}
			snapshot.result.Items = append(snapshot.result.Items, item)
		}
	}
	sort.Slice(snapshot.result.Items, func(i, j int) bool {
		a, b := snapshot.result.Items[i], snapshot.result.Items[j]
		rank := map[string]int{"waiting_on_you": 0, "blocked": 1, "ready": 2}
		if rank[a.Group] != rank[b.Group] {
			return rank[a.Group] < rank[b.Group]
		}
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		return a.ID < b.ID
	})
	capturedAt := parseNextTime(entry.LastSuccess)
	for _, observation := range snapshot.observations {
		if verifiedAt := parseNextTime(observation.VerifiedAt); verifiedAt.After(capturedAt) {
			capturedAt = verifiedAt
		}
	}
	snapshot.result.Message = nextMessage(snapshot.result, entry.SetupState == "error", capturedAt, interval)
	return snapshot, nil
}

func loadNextEvents(tx *sql.Tx) ([]nextStoredEvent, error) {
	rows, err := tx.Query(`SELECT id,type,timestamp,COALESCE(project,''),COALESCE(actor,''),payload_json FROM events WHERE (source='github' AND type LIKE 'github.pull_request.%') OR (source='workgraph' AND type IN ('next.item_done','next.item_snoozed')) ORDER BY timestamp,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []nextStoredEvent{}
	for rows.Next() {
		var event nextStoredEvent
		var at, raw string
		if err := rows.Scan(&event.ID, &event.Type, &at, &event.Project, &event.Actor, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &event.Payload); err != nil {
			return nil, fmt.Errorf("read next event %s: %w", event.ID, err)
		}
		if event.At, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func projectNextObservation(o githubPRObservation, events []nextStoredEvent, config NextConfig, now time.Time, interval time.Duration, setupError bool) []NextItem {
	if o.State != "OPEN" {
		return nil
	}
	var relevant []nextStoredEvent
	for _, e := range events {
		if e.Payload.NodeID == o.NodeID && e.Payload.Role == o.Role {
			relevant = append(relevant, e)
		}
	}
	items := []NextItem{}
	add := func(reason, typ, discriminator string, subject NextItemSubject, detail map[string]any, thread *githubPRThread) {
		event, ok := nextOpeningEvent(o, relevant, typ, discriminator)
		if !ok {
			return
		}
		suffix := discriminator
		if reason == nextReasonReply {
			suffix = subject.ID + ":" + discriminator
			if subject.Kind == "comment" {
				suffix = "conversation:" + discriminator
			}
		}
		if reason == nextReasonReviewRequested || reason == nextReasonConflicts || reason == nextReasonReadyToMerge {
			suffix = event.ID
		}
		asOf := parseNextTime(o.VerifiedAt)
		threshold := 3 * interval
		if reason == nextReasonReply || reason == nextReasonChangesRequested {
			asOf = parseNextTime(o.DetailFetchedAt)
			threshold += 30 * time.Minute
		}
		access := o.Access
		if access == "" {
			access = "ok"
		}
		unknown := nextUnknown(o)
		triage := map[string]any{"last_responder": nil, "previous_responder": nil, "agent_replies_in_thread": 0, "has_suggestion": false, "thread_depth": 0, "unknown": unknown}
		for _, key := range []string{"last_responder", "previous_responder", "has_suggestion", "thread_depth", "agent_replies_in_thread"} {
			if v, ok := event.Payload.Triage[key]; ok {
				triage[key] = v
			}
		}
		if thread != nil {
			triage["last_responder"] = thread.LastResponder
			triage["agent_replies_in_thread"] = thread.AgentReplies
			triage["thread_depth"] = thread.Depth
		}
		var headRepository *string
		if o.HeadRepository != "" {
			value := o.HeadRepository
			headRepository = &value
		}
		since := event.At
		if event.Type == githubPREventObserved {
			if at := parseNextTime(event.Payload.BaselineAt); !at.IsZero() {
				since = at
			} else if at := parseNextTime(o.BaselineAt); !at.IsZero() {
				since = at
			}
		}
		project := event.Project
		if project == "" {
			project = githubProjectName(o.Repository)
		}
		item := NextItem{
			Schema: nextItemSchemaV1,
			ID:     "github:" + reason + ":" + o.NodeID + ":" + suffix,
			Source: "github", Reason: reason, Group: nextGroup(reason),
			Role: o.Role, Project: project, Since: since,
			OpenedBy: event.ID, Evidence: []string{event.ID},
			Subject: subject, Detail: detail, Triage: triage,
			Freshness: NextItemFreshness{
				AsOf: asOf, Access: access,
				Stale: setupError || access == "unreachable" || asOf.IsZero() || now.Sub(asOf) > threshold,
			},
			PullRequest: NextItemPR{
				Repository: o.Repository, Number: o.Number, NodeID: o.NodeID,
				URL: o.URL, Title: o.Title, Branch: o.HeadRef, BaseRef: o.BaseRef,
				HeadSHA: o.HeadSHA, HeadRepository: headRepository,
				IsDraft: o.IsDraft, IsCrossRepository: o.IsCrossRepository,
			},
		}
		if event.Actor != "" && !(reason == nextReasonReply && event.Type == githubPREventObserved) {
			item.Actor = &NextItemActor{Login: event.Actor, Kind: event.Payload.ActorKind, Association: event.Payload.ActorAssociation}
		}
		if reason == nextReasonChangesRequested {
			item.Actor = &NextItemActor{Login: fmt.Sprint(detail["reviewer"]), Kind: "user"}
			for _, review := range *o.OpinionatedReviews {
				if review.ReviewID == discriminator {
					item.Actor.Kind = review.ActorKind
				}
			}
			item.Actor.Association = event.Payload.ActorAssociation
		}
		if config.IncludeUntrusted && event.Type != githubPREventObserved {
			item.Untrusted = event.Payload.Untrusted
		}
		if subject.Kind == "thread" || subject.Kind == "comment" {
			untrusted := nextItemUntrusted(event, subject.ID, discriminator)
			if url, ok := untrusted["comment_url"].(string); ok && url != "" {
				item.Subject.URL = url
			}
			if config.IncludeUntrusted {
				item.Untrusted = untrusted
			}
		}
		items = append(items, item)
	}
	pr := NextItemSubject{Kind: "pull_request", ID: o.NodeID, URL: o.URL}
	if o.Role == githubPRRoleReviewer {
		if r := o.ReviewRequest; r != nil && (r.Direct || len(r.Teams) > 0 || r.UnresolvedTeam) {
			add(nextReasonReviewRequested, githubPREventReviewRequested, "", pr, map[string]any{"request": r}, nil)
		}
		return items
	}
	if o.Threads != nil {
		for _, thread := range *o.Threads {
			if !thread.Unknown && !thread.IsResolved && !thread.IsOutdated && thread.Awaiting == "user" {
				add(nextReasonReply, githubPREventReviewComment, thread.LastCommentID, NextItemSubject{Kind: "thread", ID: thread.ThreadID, URL: o.URL, Path: thread.Path, Line: thread.Line}, map[string]any{}, &thread)
			}
		}
	}
	conversation := o.Conversation
	if conversation.LastHumanCommentID != "" && parseNextTime(conversation.LastHumanCommentAt).After(parseNextTime(conversation.LastUserCommentAt)) {
		event, ok := nextOpeningEvent(o, relevant, githubPREventReviewComment, conversation.LastHumanCommentID)
		if ok && (event.Type == githubPREventObserved || event.Payload.ActorKind == "user") {
			add(nextReasonReply, githubPREventReviewComment, conversation.LastHumanCommentID, NextItemSubject{Kind: "comment", ID: conversation.LastHumanCommentID, URL: o.URL}, map[string]any{}, nil)
		}
	}
	if o.OpinionatedReviews != nil {
		for _, review := range *o.OpinionatedReviews {
			if review.State == "CHANGES_REQUESTED" {
				add(nextReasonChangesRequested, githubPREventChangesRequested, review.ReviewID, NextItemSubject{Kind: "review", ID: review.ReviewID, URL: o.URL}, map[string]any{"review_id": review.ReviewID, "reviewer": review.Reviewer}, nil)
			}
		}
	}
	if o.ChecksState == "failed" {
		fingerprint := nextRunFingerprint(o, relevant)
		add(nextReasonChecksFailed, githubPREventChecksFailed, o.HeadSHA+":"+fingerprint, pr, map[string]any{"run_fingerprint": fingerprint, "failed_checks": nextFailedChecks(o.CheckRuns)}, nil)
	}
	if o.Mergeable == "CONFLICTING" {
		add(nextReasonConflicts, githubPREventMergeConflict, "", pr, map[string]any{"base_ref": o.BaseRef}, nil)
	}
	if o.Ready == "true" {
		add(nextReasonReadyToMerge, githubPREventReadyToMerge, "", pr, map[string]any{"merge_state_status": o.MergeStateStatus}, nil)
	}
	return items
}

func nextOpeningEvent(o githubPRObservation, events []nextStoredEvent, typ, discriminator string) (nextStoredEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Type == typ && (discriminator == "" || e.ID == typ+":"+o.NodeID+":"+discriminator) {
			return e, true
		}
	}
	for _, e := range events {
		if e.Type == githubPREventObserved && nextBaselineContains(e.Payload, typ, discriminator) {
			return e, true
		}
	}
	return nextStoredEvent{}, false
}

func nextBaselineContains(payload nextEventPayload, typ, id string) bool {
	baseline := payload.Baseline
	switch typ {
	case githubPREventReviewRequested:
		return payload.Role == githubPRRoleReviewer
	case githubPREventMergeConflict:
		return payload.State["mergeable"] == "CONFLICTING" || baseline["mergeable"] == "CONFLICTING"
	case githubPREventReadyToMerge:
		return payload.State["ready"] == true || payload.State["ready"] == "true" || baseline["ready"] == true || baseline["ready"] == "true"
	case githubPREventChecksFailed:
		return id == fmt.Sprint(baseline["head_sha"])+":"+fmt.Sprint(baseline["run_fingerprint"])
	case githubPREventChangesRequested, githubPREventReviewComment:
		return nextBaselineHasProviderID(baseline, id)
	}
	return false
}

func nextBaselineHasProviderID(value any, id string) bool {
	if id == "" {
		return false
	}
	switch value := value.(type) {
	case string:
		return value == id
	case []any:
		for _, child := range value {
			if nextBaselineHasProviderID(child, id) {
				return true
			}
		}
	case map[string]any:
		for key, child := range value {
			if key == id || nextBaselineHasProviderID(child, id) {
				return true
			}
		}
	}
	return false
}

func nextRunFingerprint(o githubPRObservation, events []nextStoredEvent) string {
	values := []string{}
	for _, run := range o.CheckRuns {
		if run.Status == "COMPLETED" || run.Status == "SUCCESS" || run.Status == "FAILURE" || run.Status == "ERROR" {
			values = append(values, run.ID+"@"+run.CompletedAt)
		}
	}
	sort.Strings(values)
	sum := sha256.Sum256([]byte(strings.Join(values, "\n")))
	fingerprint := hex.EncodeToString(sum[:])[:12]
	for _, event := range events {
		if event.ID == githubPREventChecksFailed+":"+o.NodeID+":"+o.HeadSHA+":"+fingerprint {
			return fingerprint
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Type == githubPREventChecksFailed && e.Payload.HeadSHA == o.HeadSHA {
			if value, ok := e.Payload.Triage["run_fingerprint"].(string); ok && value != "" {
				return value
			}
			prefix := githubPREventChecksFailed + ":" + o.NodeID + ":" + o.HeadSHA + ":"
			if strings.HasPrefix(e.ID, prefix) {
				return strings.TrimPrefix(e.ID, prefix)
			}
		}
		if e.Type == githubPREventObserved && e.Payload.Baseline["head_sha"] == o.HeadSHA {
			if value, ok := e.Payload.Baseline["run_fingerprint"].(string); ok && value != "" {
				return value
			}
		}
	}
	return fingerprint
}

func nextFailedChecks(runs []githubPRCheckRun) []map[string]any {
	failed := []map[string]any{}
	for _, run := range runs {
		switch run.Conclusion {
		case "FAILURE", "ERROR", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			failed = append(failed, map[string]any{"name": run.Name, "conclusion": run.Conclusion, "details_url": run.DetailsURL})
		}
	}
	sort.Slice(failed, func(i, j int) bool { return fmt.Sprint(failed[i]["name"]) < fmt.Sprint(failed[j]["name"]) })
	if len(failed) > 20 {
		failed = failed[:20]
	}
	return failed
}

func nextUnknown(o githubPRObservation) []string {
	unknown := []string{}
	if o.Role == githubPRRoleReviewer {
		return unknown
	}
	if o.ChecksState == "unknown" {
		unknown = append(unknown, "checks")
	}
	if o.Threads == nil {
		unknown = append(unknown, "threads")
	}
	if o.OpinionatedReviews == nil {
		unknown = append(unknown, "approval")
	}
	if o.Mergeable == "UNKNOWN" {
		unknown = append(unknown, "mergeable")
	}
	if o.Ready == "unknown" {
		unknown = append(unknown, "ready")
	}
	if o.TimelineCursor != "" {
		unknown = append(unknown, "comments", "reviews")
	}
	return unknown
}

func nextItemUntrusted(event nextStoredEvent, subjectID, commentID string) map[string]any {
	if event.Type != githubPREventObserved {
		return event.Payload.Untrusted
	}
	if threads, ok := event.Payload.Untrusted["threads"].([]any); ok {
		for _, raw := range threads {
			if thread, ok := raw.(map[string]any); ok && (thread["thread_id"] == subjectID || thread["last_comment_id"] == commentID) {
				copy := map[string]any{"trust": "untrusted"}
				for k, v := range thread {
					copy[k] = v
				}
				return copy
			}
		}
	}
	return nil
}

func parseNextTime(raw string) time.Time { at, _ := time.Parse(time.RFC3339Nano, raw); return at }
func nextGroup(reason string) string {
	switch reason {
	case nextReasonReply, nextReasonReviewRequested:
		return "waiting_on_you"
	case nextReasonReadyToMerge:
		return "ready"
	default:
		return "blocked"
	}
}
func githubProjectName(repository string) string {
	parts := strings.Split(repository, "/")
	return parts[len(parts)-1]
}

func nextHidden(id string, events []nextStoredEvent, now time.Time) string {
	snoozed := false
	for _, event := range events {
		if event.Payload.ItemID != id {
			continue
		}
		if event.Type == "next.item_done" {
			return nextCloseDone
		}
		if event.Type == "next.item_snoozed" && parseNextTime(event.Payload.Until).After(now) {
			snoozed = true
		}
	}
	if snoozed {
		return nextCloseSnoozed
	}
	return ""
}

func nextMessage(result NextResult, setupError bool, capturedAt time.Time, interval time.Duration) string {
	if !result.Enabled {
		return "No enabled next sources. Connect GitHub pull requests: workgraph github prs connect"
	}
	lines := []string{"Next (as of " + result.AsOf.Format("15:04") + ")"}
	if capturedAt.IsZero() {
		lines = append(lines, "Capture freshness: unknown")
	} else {
		lines = append(lines, "Capture freshness: "+capturedAt.Format(time.RFC3339Nano))
	}
	if setupError {
		lines = append(lines, "Warning: github.pr_watch setup error; run workgraph connectors validate github.pr_watch")
	}
	stale := capturedAt.IsZero() || result.AsOf.Sub(capturedAt) > 3*interval
	for _, item := range result.Items {
		stale = stale || item.Freshness.Stale
	}
	if stale {
		lines = append(lines, "Warning: github.pr_watch data is stale or unreachable; run workgraph github prs capture")
	}
	group := ""
	for _, item := range result.Items {
		if item.Group != group {
			group = item.Group
			lines = append(lines, map[string]string{"waiting_on_you": "Waiting on you", "blocked": "Blocked", "ready": "Ready"}[group])
		}
		lines = append(lines, "  "+nextItemLine(item))
	}
	if len(result.Items) == 0 {
		lines = append(lines, "Nothing is waiting on you.")
	}
	return strings.Join(lines, "\n")
}

func nextItemLine(item NextItem) string {
	reason := strings.ReplaceAll(item.Reason, "_", " ")
	switch item.Reason {
	case nextReasonReply:
		reason = "reply requested"
		if item.Actor != nil {
			reason = item.Actor.Login + " replied"
		}
		if item.Subject.Path != "" {
			reason += fmt.Sprintf(" on %s:%d", item.Subject.Path, item.Subject.Line)
		}
	case nextReasonReviewRequested:
		if request, ok := item.Detail["request"].(*githubPRReviewRequest); ok {
			if len(request.Teams) > 0 {
				reason += " via " + strings.Join(request.Teams, ", ")
			} else if request.UnresolvedTeam {
				reason += " via team (unresolved)"
			}
		}
	case nextReasonChangesRequested:
		reason += " by " + fmt.Sprint(item.Detail["reviewer"])
	case nextReasonChecksFailed:
		sha := item.PullRequest.HeadSHA
		if len(sha) > 7 {
			sha = sha[:7]
		}
		reason += " on " + sha
		if checks, ok := item.Detail["failed_checks"].([]map[string]any); ok {
			names := []string{}
			for _, check := range checks {
				names = append(names, fmt.Sprint(check["name"]))
			}
			reason += " (" + strings.Join(names, ", ") + ")"
		}
	}
	if item.PullRequest.IsDraft {
		reason += " [draft]"
	}
	if unknown, ok := item.Triage["unknown"].([]string); ok && len(unknown) > 0 {
		reason += " [unknown: " + strings.Join(unknown, ", ") + "]"
	}
	return fmt.Sprintf("#%d %s (%s): %s — %s [%s]", item.PullRequest.Number,
		nextDisplayText(item.PullRequest.Title), nextDisplayText(item.PullRequest.Repository),
		nextDisplayText(reason), nextDisplayText(item.Subject.URL), nextDisplayText(item.ID))
}

var nextANSISequence = regexp.MustCompile(
	`(?:\x1b\[|\x{009b})[0-?]*[ -/]*[@-~]|` +
		`(?:\x1b[\]PX^_]|[\x{0090}\x{0098}\x{009d}-\x{009f}])[^\x07\x1b\x{009c}]*(?:\x07|\x1b\\|\x{009c}|$)|` +
		`\x1b[ -/]*[0-~]`)

func nextDisplayText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u034f' {
			return -1
		}
		return r
	}, nextANSISequence.ReplaceAllString(value, ""))
}

func nextUnavailableMessage(err error) string {
	class := "projection error"
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &typeError) {
		class = "invalid JSON"
	}
	return "Waiting on you unavailable: " + class
}

func nextWaitingMessage(items []NextItem, limit int) string {
	if len(items) == 0 {
		return ""
	}
	lines := []string{"Waiting on you"}
	count := len(items)
	if limit > 0 && count > limit {
		count = limit
	}
	for _, item := range items[:count] {
		lines = append(lines, "  "+nextItemLine(item))
	}
	if count < len(items) {
		lines = append(lines, fmt.Sprintf("%d more: workgraph next", len(items)-count))
	}
	return strings.Join(lines, "\n")
}

func ExplainNext(config NextConfig, id string) (string, error) {
	result, err := Next(config)
	if err != nil {
		return "", err
	}
	for _, item := range result.Items {
		if item.ID == id {
			conditions := map[string]string{nextReasonReply: "The thread or conversation is awaiting the user.", nextReasonReviewRequested: "A review request for the user is open.", nextReasonChangesRequested: "The reviewer's latest opinionated review requests changes.", nextReasonChecksFailed: "Checks failed on the current head.", nextReasonConflicts: "The pull request has merge conflicts.", nextReasonReadyToMerge: "The pull request is ready to merge."}
			return fmt.Sprintf("Rule: %s\nCondition: %s\nOpened by: %s\nEvidence: %s\nObserved at: %s", item.Reason, conditions[item.Reason], item.OpenedBy, strings.Join(item.Evidence, ", "), item.Freshness.AsOf.Format(time.RFC3339Nano)), nil
		}
	}
	return "", fmt.Errorf("next item %q is not open", id)
}

func DoneNext(config NextConfig, id string) error { return recordNextAction(config, id, 0) }
func SnoozeNext(config NextConfig, id string, duration time.Duration) error {
	if duration <= 0 {
		return errors.New("snooze duration must be positive")
	}
	return recordNextAction(config, id, duration)
}
func recordNextAction(config NextConfig, id string, duration time.Duration) error {
	result, err := Next(config)
	if err != nil {
		return err
	}
	found := false
	project := ""
	for _, item := range result.Items {
		if item.ID == id {
			found = true
			project = item.Project
			break
		}
	}
	if !found {
		return fmt.Errorf("next item %q is not open", id)
	}
	at := nextNow(config).UTC()
	typ := "next.item_done"
	eventID := typ + ":" + id
	payload := map[string]any{"item_id": id}
	if duration > 0 {
		typ = "next.item_snoozed"
		until := at.Add(duration).Format(time.RFC3339Nano)
		eventID = typ + ":" + id + ":" + until
		payload["until"] = until
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	path, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO events(id,source,type,timestamp,payload_json,project,created_at,involvement_json) VALUES (?,'workgraph',?,?,?,?,?,'[]') ON CONFLICT(id) DO NOTHING`, eventID, typ, at.Format(time.RFC3339Nano), string(raw), project, at.Format(time.RFC3339Nano))
	return err
}

type NextWatchEvent struct {
	Op      string     `json:"op"`
	Item    *NextItem  `json:"item,omitempty"`
	ID      string     `json:"id,omitempty"`
	Reason  string     `json:"reason,omitempty"`
	Initial bool       `json:"initial,omitempty"`
	AsOf    *time.Time `json:"as_of,omitempty"`
}

func WatchNext(ctx context.Context, config NextConfig, output io.Writer) error {
	interval := config.WatchInterval
	if interval <= 0 {
		interval = time.Second
	}
	encoder := json.NewEncoder(output)
	previous, err := readNextSnapshot(config)
	if err != nil {
		return err
	}
	for _, item := range previous.result.Items {
		if err := encoder.Encode(NextWatchEvent{Op: "opened", Item: &item, Initial: true}); err != nil {
			return err
		}
	}
	if err := encoder.Encode(NextWatchEvent{Op: "synced", AsOf: &previous.result.AsOf}); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current, err := readNextSnapshot(config)
			if err != nil {
				return err
			}
			before, after := map[string]NextItem{}, map[string]NextItem{}
			for _, item := range previous.result.Items {
				before[item.ID] = item
			}
			for _, item := range current.result.Items {
				after[item.ID] = item
			}
			for _, item := range previous.result.Items {
				if _, ok := after[item.ID]; !ok {
					if err := encoder.Encode(NextWatchEvent{Op: "closed", ID: item.ID, Reason: nextClosedReason(item, current)}); err != nil {
						return err
					}
				}
			}
			for _, item := range current.result.Items {
				old, ok := before[item.ID]
				op := "opened"
				if ok {
					a, _ := json.Marshal(old)
					b, _ := json.Marshal(item)
					if string(a) == string(b) {
						continue
					}
					op = "changed"
				}
				if err := encoder.Encode(NextWatchEvent{Op: op, Item: &item}); err != nil {
					return err
				}
			}
			previous = current
		}
	}
}

func nextClosedReason(item NextItem, current nextSnapshot) string {
	if reason := nextHidden(item.ID, current.events, current.result.AsOf); reason != "" {
		return reason
	}
	if !current.result.Enabled {
		return nextCloseOutOfScope
	}
	for _, o := range current.observations {
		if o.NodeID == item.PullRequest.NodeID && o.Role == item.Role {
			if o.State == "MERGED" {
				return nextCloseMerged
			}
			if o.State == "CLOSED" {
				return nextCloseClosed
			}
			if item.Reason == nextReasonReviewRequested && o.ReviewRequest == nil {
				return nextCloseOutOfScope
			}
			return nextCloseResolved
		}
	}
	for i := len(current.events) - 1; i >= 0; i-- {
		e := current.events[i]
		if e.Payload.NodeID != item.PullRequest.NodeID || e.At.Before(item.Since) {
			continue
		}
		switch e.Type {
		case githubPREventMerged:
			return nextCloseMerged
		case githubPREventClosed:
			return nextCloseClosed
		case githubPREventReviewRequestRemoved:
			if e.Payload.Role == item.Role && item.Reason == nextReasonReviewRequested {
				return nextCloseOutOfScope
			}
		case githubPREventUnwatched:
			if e.Payload.Role != item.Role {
				continue
			}
			if exit, ok := e.Payload.Triage["exit"].(string); ok && (exit == nextCloseLeftSearch || exit == nextCloseUnreachable || exit == nextCloseOutOfScope) {
				return exit
			}
		}
	}

	return nextCloseOutOfScope
}

func nextNeedsUserTransition(typ, payload string) bool {
	switch typ {
	case githubPREventReviewComment, githubPREventChangesRequested, githubPREventChecksFailed, githubPREventMergeConflict, githubPREventReadyToMerge, githubPREventReviewRequested:
		var parsed nextEventPayload
		return json.Unmarshal([]byte(payload), &parsed) == nil && parsed.Triage["needs_user"] == true
	default:
		return false
	}
}
