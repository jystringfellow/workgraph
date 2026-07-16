package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	associationCandidateLimit = 200
	associationWindow         = 7 * 24 * time.Hour
	associationMinimumScore   = 60
)

var (
	associationURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
	associationSHAPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	associationStopTokens = map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "as": true,
		"at": true, "be": true, "by": true, "email": true, "for": true,
		"from": true, "in": true, "into": true, "is": true, "issue": true,
		"it": true, "meeting": true, "message": true, "of": true, "on": true,
		"or": true, "project": true, "pull": true, "request": true,
		"status": true, "task": true, "the": true, "to": true,
		"update": true, "updated": true, "with": true, "work": true,
		"working": true,
	}
	associationGenericProjects = map[string]bool{
		"code": true, "developer": true, "documents": true, "downloads": true,
		"projects": true, "repos": true, "source": true, "unknown": true,
		"work": true,
	}
)

// AssociationExplainConfig controls deterministic local association inspection.
type AssociationExplainConfig struct {
	HomeDir      string
	DatabasePath string
	EventID      string
}

// AssociationExplainResult describes the bounded candidate evaluation for one event.
type AssociationExplainResult struct {
	Event                AssociationEvent
	Candidates           []AssociationCandidate
	CandidatesConsidered int
	Message              string
}

// AssociationEvent is the stored event identity needed for association inspection.
type AssociationEvent struct {
	ID        string
	Source    string
	Type      string
	Timestamp time.Time
	Project   string
	Summary   string
	Payload   string
}

// AssociationCandidate is one qualifying, explainable cross-source event pair.
type AssociationCandidate struct {
	RelatedEventID string
	SuggestionID   string
	PatternKey     string
	EventIDs       []string
	Score          int
	Confidence     string
	Status         string
	Reasons        []string
	MatchedSignals []string
	EvidenceJSON   string
	TimeDistance   time.Duration
}

type associationEvidence struct {
	EventIDs       []string `json:"event_ids"`
	Sources        []string `json:"sources"`
	Score          int      `json:"score"`
	Confidence     string   `json:"confidence"`
	MatchedSignals []string `json:"matched_signals"`
	Reasons        []string `json:"reasons"`
	WindowBefore   string   `json:"window_before"`
	WindowAfter    string   `json:"window_after"`
	CandidateLimit int      `json:"candidate_limit"`
}

type associationSignals struct {
	URLs         map[string]bool
	Repositories map[string]bool
	IssueRefs    map[string]bool
	Commits      map[string]bool
	Branches     map[string]bool
	TitleTokens  map[string]bool
	Project      string
}

// ExplainEventAssociations evaluates a bounded local candidate set and stores
// qualifying pairs in the shared suggestion lifecycle.
func ExplainEventAssociations(config AssociationExplainConfig) (AssociationExplainResult, error) {
	eventID := strings.TrimSpace(config.EventID)
	if eventID == "" {
		return AssociationExplainResult{}, errors.New("event id is required")
	}
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return AssociationExplainResult{}, err
	}
	defer db.Close()
	if err := expireSnoozedSuggestions(db, time.Now().UTC()); err != nil {
		return AssociationExplainResult{}, err
	}

	target, err := readAssociationEvent(db, eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AssociationExplainResult{}, fmt.Errorf("event %q not found", eventID)
		}
		return AssociationExplainResult{}, err
	}
	candidates, err := loadAssociationCandidates(db, target)
	if err != nil {
		return AssociationExplainResult{}, err
	}

	result := AssociationExplainResult{
		Event:                target,
		CandidatesConsidered: len(candidates),
	}
	for _, candidateEvent := range candidates {
		candidate, ok, err := evaluateAssociationPair(target, candidateEvent)
		if err != nil {
			return AssociationExplainResult{}, err
		}
		if !ok {
			continue
		}
		status, err := coalesceAssociationSuggestion(db, target, candidateEvent, &candidate)
		if err != nil {
			return AssociationExplainResult{}, err
		}
		candidate.Status = status
		result.Candidates = append(result.Candidates, candidate)
	}

	sort.Slice(result.Candidates, func(i, j int) bool {
		left, right := result.Candidates[i], result.Candidates[j]
		if left.Score != right.Score {
			return left.Score > right.Score
		}
		if left.TimeDistance != right.TimeDistance {
			return left.TimeDistance < right.TimeDistance
		}
		return left.RelatedEventID < right.RelatedEventID
	})
	result.Message = associationExplainMessage(result)
	return result, nil
}

func readAssociationEvent(db *sql.DB, eventID string) (AssociationEvent, error) {
	var event AssociationEvent
	var timestamp string
	var project, summary sql.NullString
	err := db.QueryRow(`SELECT id, source, type, timestamp, project, summary, payload_json FROM events WHERE id = ?`, eventID).Scan(
		&event.ID, &event.Source, &event.Type, &timestamp, &project, &summary, &event.Payload,
	)
	if err != nil {
		return AssociationEvent{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return AssociationEvent{}, fmt.Errorf("parse event timestamp %q: %w", event.ID, err)
	}
	event.Timestamp = parsed.UTC()
	if project.Valid {
		event.Project = project.String
	}
	if summary.Valid {
		event.Summary = summary.String
	}
	return event, nil
}

func loadAssociationCandidates(db *sql.DB, target AssociationEvent) ([]AssociationEvent, error) {
	start := target.Timestamp.Add(-associationWindow).UTC().Format(time.RFC3339Nano)
	end := target.Timestamp.Add(associationWindow).UTC().Format(time.RFC3339Nano)
	rows, err := db.Query(`SELECT id, source, type, timestamp, project, summary, payload_json
		FROM events
		WHERE id != ? AND source != ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY ABS(julianday(timestamp) - julianday(?)) ASC, timestamp ASC, id ASC
		LIMIT ?`, target.ID, target.Source, start, end, target.Timestamp.UTC().Format(time.RFC3339Nano), associationCandidateLimit)
	if err != nil {
		return nil, fmt.Errorf("query association candidates: %w", err)
	}
	defer rows.Close()

	var candidates []AssociationEvent
	for rows.Next() {
		var event AssociationEvent
		var timestamp string
		var project, summary sql.NullString
		if err := rows.Scan(&event.ID, &event.Source, &event.Type, &timestamp, &project, &summary, &event.Payload); err != nil {
			return nil, fmt.Errorf("scan association candidate: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			continue
		}
		event.Timestamp = parsed.UTC()
		if project.Valid {
			event.Project = project.String
		}
		if summary.Valid {
			event.Summary = summary.String
		}
		candidates = append(candidates, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query association candidates: %w", err)
	}
	return candidates, nil
}

func evaluateAssociationPair(left, right AssociationEvent) (AssociationCandidate, bool, error) {
	if left.Source == right.Source {
		return AssociationCandidate{}, false, nil
	}
	leftSignals := extractAssociationSignals(left)
	rightSignals := extractAssociationSignals(right)
	ids := canonicalAssociationEventIDs(left.ID, right.ID)
	patternKey := associationPatternKey(ids)
	candidate := AssociationCandidate{
		RelatedEventID: right.ID,
		SuggestionID:   stableSuggestionID("association", patternKey),
		PatternKey:     patternKey,
		EventIDs:       ids,
		TimeDistance:   absoluteDuration(left.Timestamp.Sub(right.Timestamp)),
	}

	if value := firstCommonValue(leftSignals.URLs, rightSignals.URLs); value != "" {
		candidate.Score += 100
		candidate.MatchedSignals = append(candidate.MatchedSignals, "url:"+value)
		candidate.Reasons = append(candidate.Reasons, "identical normalized URL "+value)
	}
	if value := firstCommonValue(leftSignals.IssueRefs, rightSignals.IssueRefs); value != "" {
		candidate.Score += 90
		candidate.MatchedSignals = append(candidate.MatchedSignals, "repository_issue:"+value)
		candidate.Reasons = append(candidate.Reasons, "same repository and issue or pull-request number "+value)
	}
	if value := matchingCommit(leftSignals.Commits, rightSignals.Commits); value != "" {
		candidate.Score += 85
		candidate.MatchedSignals = append(candidate.MatchedSignals, "commit:"+value)
		candidate.Reasons = append(candidate.Reasons, "matching commit SHA "+value)
	}
	if repository := firstCommonValue(leftSignals.Repositories, rightSignals.Repositories); repository != "" {
		if branch := firstCommonValue(leftSignals.Branches, rightSignals.Branches); branch != "" {
			candidate.Score += 65
			candidate.MatchedSignals = append(candidate.MatchedSignals, "repository_branch:"+repository+"@"+branch)
			candidate.Reasons = append(candidate.Reasons, "same repository and branch "+repository+"@"+branch)
		}
	}
	if overlap, shared, ok := associationTokenOverlap(leftSignals.TitleTokens, rightSignals.TitleTokens); ok {
		points := 30
		if overlap >= 0.80 {
			points = 50
		} else if overlap >= 0.60 {
			points = 40
		}
		candidate.Score += points
		value := fmt.Sprintf("%.2f:%s", overlap, strings.Join(shared, ","))
		candidate.MatchedSignals = append(candidate.MatchedSignals, "title_tokens:"+value)
		candidate.Reasons = append(candidate.Reasons, fmt.Sprintf("normalized title-token overlap %.2f (%s)", overlap, strings.Join(shared, ", ")))
	}
	if leftSignals.Project != "" && leftSignals.Project == rightSignals.Project {
		candidate.Score += 20
		candidate.MatchedSignals = append(candidate.MatchedSignals, "project:"+leftSignals.Project)
		candidate.Reasons = append(candidate.Reasons, "same project "+leftSignals.Project)
	}

	if candidate.Score > 0 {
		switch {
		case candidate.TimeDistance <= 15*time.Minute:
			candidate.Score += 10
			candidate.MatchedSignals = append(candidate.MatchedSignals, "time:within_15m")
			candidate.Reasons = append(candidate.Reasons, "timestamps within 15 minutes")
		case candidate.TimeDistance <= 2*time.Hour:
			candidate.Score += 5
			candidate.MatchedSignals = append(candidate.MatchedSignals, "time:within_2h")
			candidate.Reasons = append(candidate.Reasons, "timestamps within 2 hours")
		}
	}
	if candidate.Score > 100 {
		candidate.Score = 100
	}
	if candidate.Score < associationMinimumScore {
		return AssociationCandidate{}, false, nil
	}
	if candidate.Score >= 80 {
		candidate.Confidence = "high"
	} else {
		candidate.Confidence = "medium"
	}

	sources := canonicalAssociationSources(left, right, ids)
	evidence := associationEvidence{
		EventIDs:       append([]string(nil), ids...),
		Sources:        sources,
		Score:          candidate.Score,
		Confidence:     candidate.Confidence,
		MatchedSignals: append([]string(nil), candidate.MatchedSignals...),
		Reasons:        append([]string(nil), candidate.Reasons...),
		WindowBefore:   associationWindow.String(),
		WindowAfter:    associationWindow.String(),
		CandidateLimit: associationCandidateLimit,
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return AssociationCandidate{}, false, fmt.Errorf("encode association evidence: %w", err)
	}
	candidate.EvidenceJSON = string(evidenceJSON)
	return candidate, true, nil
}

func coalesceAssociationSuggestion(db *sql.DB, left, right AssociationEvent, candidate *AssociationCandidate) (string, error) {
	existing, existingErr := readSuggestionByPattern(db, "association", candidate.PatternKey)
	if existingErr != nil && !errors.Is(existingErr, sql.ErrNoRows) {
		return "", existingErr
	}
	suppressed, err := associationPatternSuppressed(db, candidate.PatternKey, time.Now().UTC())
	if err != nil {
		return "", err
	}
	if suppressed {
		if existingErr == nil {
			candidate.EvidenceJSON = existing.EvidenceJSON
			if existing.Status == "proposed" || existing.Status == "reviewed" {
				return "suppressed", nil
			}
			return existing.Status, nil
		}
		return "suppressed", nil
	}

	reason := strings.Join(candidate.Reasons, "; ")
	title := fmt.Sprintf("Associate %s with %s", candidate.EventIDs[0], candidate.EventIDs[1])
	derivedAt := left.Timestamp
	if right.Timestamp.After(derivedAt) {
		derivedAt = right.Timestamp
	}
	derivedTimestamp := derivedAt.UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`INSERT INTO suggestions
		(id, type, pattern_key, title, reason, confidence, lane, status, evidence_json, created_at, updated_at)
		VALUES (?, 'association', ?, ?, ?, ?, 'baseline', 'proposed', ?, ?, ?)
		ON CONFLICT(type, pattern_key) DO UPDATE SET
			title = excluded.title,
			reason = excluded.reason,
			confidence = excluded.confidence,
			lane = excluded.lane,
			evidence_json = excluded.evidence_json,
			updated_at = CASE
				WHEN (suggestions.title != excluded.title
					OR suggestions.reason != excluded.reason
					OR suggestions.confidence != excluded.confidence
					OR suggestions.lane != excluded.lane
					OR suggestions.evidence_json != excluded.evidence_json)
					AND suggestions.updated_at < excluded.updated_at
				THEN excluded.updated_at
				ELSE suggestions.updated_at
			END`,
		candidate.SuggestionID,
		candidate.PatternKey,
		title,
		reason,
		candidate.Confidence,
		candidate.EvidenceJSON,
		derivedTimestamp,
		derivedTimestamp,
	)
	if err != nil {
		return "", fmt.Errorf("store association suggestion: %w", err)
	}
	stored, err := readSuggestionByPattern(db, "association", candidate.PatternKey)
	if err != nil {
		return "", err
	}
	candidate.SuggestionID = stored.ID
	candidate.EvidenceJSON = stored.EvidenceJSON
	return stored.Status, nil
}

func associationPatternSuppressed(db *sql.DB, patternKey string, now time.Time) (bool, error) {
	var until sql.NullString
	err := db.QueryRow(`SELECT until_at FROM suggestion_suppressions WHERE type = 'association' AND pattern_key = ?`, patternKey).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read association suppression: %w", err)
	}
	if !until.Valid || strings.TrimSpace(until.String) == "" {
		return true, nil
	}
	parsed, err := time.Parse(time.RFC3339, until.String)
	if err != nil {
		return false, fmt.Errorf("parse association suppression until_at: %w", err)
	}
	return parsed.After(now), nil
}

func extractAssociationSignals(event AssociationEvent) associationSignals {
	signals := associationSignals{
		URLs:         map[string]bool{},
		Repositories: map[string]bool{},
		IssueRefs:    map[string]bool{},
		Commits:      map[string]bool{},
		Branches:     map[string]bool{},
		TitleTokens:  map[string]bool{},
		Project:      normalizeAssociationProject(event.Project),
	}
	var payload any
	if err := json.Unmarshal([]byte(event.Payload), &payload); err == nil {
		collectAssociationPayloadSignals(payload, "", &signals)
	}
	collectAssociationURLs(event.Summary, &signals)
	for token := range normalizeAssociationTokens(event.Summary) {
		signals.TitleTokens[token] = true
	}
	return signals
}

func collectAssociationPayloadSignals(value any, key string, signals *associationSignals) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		repository := ""
		var issueNumber int64
		for _, childKey := range keys {
			switch associationFieldKey(childKey) {
			case "repository":
				if text, ok := typed[childKey].(string); ok {
					repository = normalizeAssociationRepository(text)
				}
			case "number":
				switch number := typed[childKey].(type) {
				case float64:
					if number > 0 && number == float64(int64(number)) {
						issueNumber = int64(number)
					}
				case json.Number:
					if parsed, err := number.Int64(); err == nil && parsed > 0 {
						issueNumber = parsed
					}
				}
			}
		}
		if repository != "" && issueNumber > 0 {
			signals.IssueRefs[repository+"#"+strconv.FormatInt(issueNumber, 10)] = true
		}
		for _, childKey := range keys {
			collectAssociationPayloadSignals(typed[childKey], childKey, signals)
		}
	case []any:
		for _, child := range typed {
			collectAssociationPayloadSignals(child, key, signals)
		}
	case string:
		collectAssociationURLs(typed, signals)
		normalizedKey := associationFieldKey(key)
		switch normalizedKey {
		case "repository":
			if repository := normalizeAssociationRepository(typed); repository != "" {
				signals.Repositories[repository] = true
			}
		case "commit", "sha", "headsha":
			if commit := normalizeAssociationCommit(typed); commit != "" {
				signals.Commits[commit] = true
			}
		case "branch", "headref", "headrefname":
			if branch := normalizeAssociationBranch(typed); branch != "" {
				signals.Branches[branch] = true
			}
		case "title", "subject":
			for token := range normalizeAssociationTokens(typed) {
				signals.TitleTokens[token] = true
			}
		}
	}
}

func collectAssociationURLs(text string, signals *associationSignals) {
	for _, raw := range associationURLPattern.FindAllString(text, -1) {
		raw = strings.TrimRight(raw, `.,;:!?)]}`)
		normalized := normalizeAssociationURL(raw)
		if normalized == "" {
			continue
		}
		signals.URLs[normalized] = true
		parsed, err := url.Parse(normalized)
		if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
			continue
		}
		parts := splitAssociationURLPath(parsed.Path)
		if len(parts) < 2 {
			continue
		}
		repository := strings.ToLower(parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"))
		signals.Repositories[repository] = true
		if len(parts) >= 4 && (parts[2] == "issues" || parts[2] == "pull") {
			if number, err := strconv.ParseInt(parts[3], 10, 64); err == nil && number > 0 {
				signals.IssueRefs[repository+"#"+strconv.FormatInt(number, 10)] = true
			}
		}
		if len(parts) >= 4 && parts[2] == "commit" {
			if commit := normalizeAssociationCommit(parts[3]); commit != "" {
				signals.Commits[commit] = true
			}
		}
	}
}

func normalizeAssociationURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return ""
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	parsed.Scheme = scheme
	parsed.Host = hostname
	if port != "" {
		parsed.Host = net.JoinHostPort(hostname, port)
	}
	parsed.Fragment = ""
	if len(parsed.Path) > 1 {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
	}
	parsed.RawQuery = parsed.Query().Encode()
	return parsed.String()
}

func normalizeAssociationRepository(value string) string {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
			return ""
		}
		parts := splitAssociationURLPath(parsed.Path)
		if len(parts) < 2 {
			return ""
		}
		value = parts[0] + "/" + parts[1]
	}
	value = strings.Trim(value, "/")
	value = strings.TrimSuffix(value, ".git")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return ""
	}
	return strings.ToLower(parts[0] + "/" + parts[1])
}

func normalizeAssociationCommit(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !associationSHAPattern.MatchString(value) {
		return ""
	}
	return value
}

func normalizeAssociationBranch(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "refs/heads/")
	return value
}

func normalizeAssociationProject(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || associationGenericProjects[value] || strings.HasPrefix(value, "slack-list:") {
		return ""
	}
	return value
}

func normalizeAssociationTokens(value string) map[string]bool {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := map[string]bool{}
	for _, token := range fields {
		if utf8.RuneCountInString(token) < 3 || associationStopTokens[token] || associationNumericToken(token) {
			continue
		}
		tokens[token] = true
	}
	return tokens
}

func associationNumericToken(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func associationTokenOverlap(left, right map[string]bool) (float64, []string, bool) {
	if len(left) < 3 || len(right) < 3 {
		return 0, nil, false
	}
	var shared []string
	union := map[string]bool{}
	for token := range left {
		union[token] = true
		if right[token] {
			shared = append(shared, token)
		}
	}
	for token := range right {
		union[token] = true
	}
	if len(shared) < 2 {
		return 0, nil, false
	}
	sort.Strings(shared)
	overlap := float64(len(shared)) / float64(len(union))
	if overlap < 0.50 {
		return 0, nil, false
	}
	return overlap, shared, true
}

func firstCommonValue(left, right map[string]bool) string {
	var common []string
	for value := range left {
		if right[value] {
			common = append(common, value)
		}
	}
	sort.Strings(common)
	if len(common) == 0 {
		return ""
	}
	return common[0]
}

func matchingCommit(left, right map[string]bool) string {
	var matches []string
	for leftCommit := range left {
		for rightCommit := range right {
			if strings.HasPrefix(leftCommit, rightCommit) {
				matches = append(matches, rightCommit)
			} else if strings.HasPrefix(rightCommit, leftCommit) {
				matches = append(matches, leftCommit)
			}
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func canonicalAssociationEventIDs(left, right string) []string {
	ids := []string{left, right}
	sort.Strings(ids)
	return ids
}

func associationPatternKey(ids []string) string {
	encoded, _ := json.Marshal(ids)
	return "pair:" + string(encoded)
}

func canonicalAssociationSources(left, right AssociationEvent, ids []string) []string {
	byID := map[string]string{left.ID: left.Source, right.ID: right.Source}
	return []string{byID[ids[0]], byID[ids[1]]}
}

func associationFieldKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "_", "")
	key = strings.ReplaceAll(key, "-", "")
	return key
}

func splitAssociationURLPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func associationExplainMessage(result AssociationExplainResult) string {
	lines := []string{
		"Event: " + result.Event.ID,
		fmt.Sprintf("Candidates considered: %d (7-day window, limit %d)", result.CandidatesConsidered, associationCandidateLimit),
	}
	if len(result.Candidates) == 0 {
		lines = append(lines, fmt.Sprintf("No related events met the %d-point evidence threshold.", associationMinimumScore))
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "Related associations:")
	for _, candidate := range result.Candidates {
		lines = append(lines,
			"- "+candidate.RelatedEventID,
			"  suggestion: "+candidate.SuggestionID,
			fmt.Sprintf("  score: %d (%s)", candidate.Score, candidate.Confidence),
			"  state: "+candidate.Status,
			"  cited events: "+strings.Join(candidate.EventIDs, ", "),
			"  matched signals:",
		)
		for _, signal := range candidate.MatchedSignals {
			lines = append(lines, "  - "+signal)
		}
		lines = append(lines, "  reasons:")
		for _, reason := range candidate.Reasons {
			lines = append(lines, "  - "+reason)
		}
	}
	return strings.Join(lines, "\n")
}
