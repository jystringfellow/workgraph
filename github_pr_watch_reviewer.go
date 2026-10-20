package workgraph

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

type githubPRTeamCache struct {
	Login       string   `json:"login"`
	RefreshedAt string   `json:"refreshed_at"`
	Teams       []string `json:"teams"`
	Warning     string   `json:"warning,omitempty"`
}

func githubPRTeamsWarning(entry connectorRuntimeEntry) string {
	if entry.GitHubPRTeams == nil {
		return ""
	}
	return entry.GitHubPRTeams.Warning
}

func refreshGitHubPRTeams(ctx context.Context, homeDir, gh string, params githubPRWatchParams, now time.Time, force bool) (*githubPRTeamCache, error) {
	if !params.hasRole(githubPRRoleReviewer) || params.ReviewRequests == "direct" {
		return nil, nil
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return nil, err
	}
	cache := state.entry(githubPRWatchConnectorID).GitHubPRTeams
	if cache != nil && cache.Login == params.IdentityLogin && !force {
		if refreshed, err := time.Parse(time.RFC3339Nano, cache.RefreshedAt); err == nil && now.Sub(refreshed) < time.Hour {
			return cache, nil
		}
	}
	if cache == nil || cache.Login != params.IdentityLogin {
		cache = &githubPRTeamCache{Login: params.IdentityLogin, Teams: []string{}}
	}
	teams, err := fetchGitHubPRTeams(ctx, gh, params.IdentityLogin)
	var retryAfter *ConnectorPollRetryAfter
	if err != nil {
		cache.Warning = "team membership unavailable; requests may be via team (unresolved): " + err.Error()
		errors.As(err, &retryAfter)
	} else {
		cache.Teams = teams
		cache.RefreshedAt = now.Format(time.RFC3339Nano)
		cache.Warning = ""
	}
	connectorPollStateMu.Lock()
	defer connectorPollStateMu.Unlock()
	state, err = readConnectorRuntimeFile(homeDir)
	if err != nil {
		return nil, err
	}
	entry := state.entry(githubPRWatchConnectorID)
	current, err := parseGitHubPRWatchParams(entry.BridgeParams)
	if err != nil {
		return nil, err
	}
	if len(entry.BridgeParams) == 0 || current.IdentityLogin != params.IdentityLogin {
		if retryAfter != nil {
			return cache, retryAfter
		}
		return cache, nil
	}
	entry.GitHubPRTeams = cache
	state.Connectors[githubPRWatchConnectorID] = entry
	if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
		return cache, err
	}
	if retryAfter != nil {
		return cache, retryAfter
	}
	return cache, nil
}

func fetchGitHubPRTeams(ctx context.Context, gh, login string) ([]string, error) {
	teams := []string{}
	var after any
	for {
		var data struct {
			Viewer *struct {
				Organizations struct {
					PageInfo githubPageInfo `json:"pageInfo"`
					Nodes    []struct {
						Login string `json:"login"`
						Teams struct {
							TotalCount int `json:"totalCount"`
							Nodes      []struct {
								Slug string `json:"slug"`
							} `json:"nodes"`
						} `json:"teams"`
					} `json:"nodes"`
				} `json:"organizations"`
			} `json:"viewer"`
		}
		errs, err := runGitHubGraphQL(ctx, gh, githubPRWatchTeamsQuery, map[string]any{"login": login, "after": after}, &data)
		if err != nil {
			return nil, err
		}
		if len(errs) > 0 {
			return nil, errs
		}
		if data.Viewer == nil {
			return nil, fmt.Errorf("team membership returned no viewer")
		}
		for _, org := range data.Viewer.Organizations.Nodes {
			if org.Teams.TotalCount > len(org.Teams.Nodes) {
				return nil, fmt.Errorf("team membership incomplete for %s", org.Login)
			}
			for _, team := range org.Teams.Nodes {
				teams = append(teams, org.Login+"/"+team.Slug)
			}
		}
		page := data.Viewer.Organizations.PageInfo
		if !page.HasNextPage {
			break
		}
		if page.EndCursor == "" || page.EndCursor == after {
			return nil, fmt.Errorf("team membership pagination did not advance")
		}
		after = page.EndCursor
	}
	sort.Strings(teams)
	return slices.Compact(teams), nil
}

func (poll *githubPRWatchPoll) reviewRequest(status githubPRReviewerStatus, inSearch bool) *githubPRReviewRequest {
	request := &githubPRReviewRequest{Teams: []string{}}
	if inSearch && poll.params.ReviewRequests == "direct" && status.ReviewRequests.TotalCount > len(status.ReviewRequests.Nodes) {
		request.Direct = true
	}
	ignored, eligible := false, false
	for _, node := range status.ReviewRequests.Nodes {
		reviewer := node.RequestedReviewer
		if reviewer == nil {
			continue
		}
		if reviewer.Typename == "User" && strings.EqualFold(reviewer.Login, poll.params.IdentityLogin) {
			request.Direct = true
		}
		if reviewer.Typename != "Team" || poll.params.ReviewRequests == "direct" {
			continue
		}
		team := reviewer.Organization.Login + "/" + reviewer.Slug
		if slices.ContainsFunc(poll.params.IgnoreTeams, func(value string) bool { return strings.EqualFold(value, team) }) {
			ignored = true
			continue
		}
		eligible = true
		if poll.teams != nil && slices.ContainsFunc(poll.teams.Teams, func(value string) bool { return strings.EqualFold(value, team) }) {
			request.Teams = append(request.Teams, team)
		}
	}
	sort.Strings(request.Teams)
	request.Teams = slices.Compact(request.Teams)
	if !request.Direct && len(request.Teams) == 0 && poll.params.ReviewRequests != "direct" {
		request.UnresolvedTeam = inSearch && (!ignored || eligible || status.ReviewRequests.TotalCount > len(status.ReviewRequests.Nodes))
	}
	if !request.Direct && len(request.Teams) == 0 && !request.UnresolvedTeam {
		return nil
	}
	return request
}

func (poll *githubPRWatchPoll) runReviewer() (githubPRWatchChanges, error) {
	previous, err := loadGitHubPRObservations(poll.db, githubPRRoleReviewer)
	if err != nil {
		return githubPRWatchChanges{}, err
	}
	var polled bool
	if err := poll.db.QueryRow(`SELECT reviewer_polled FROM github_pr_watch_state WHERE connector_id = ?`, githubPRWatchConnectorID).Scan(&polled); err != nil {
		return githubPRWatchChanges{}, err
	}
	nodes, err := poll.search(githubPRRoleReviewer)
	if err != nil {
		return githubPRWatchChanges{}, err
	}
	changes := githubPRWatchChanges{reviewerPolled: true}
	seen := map[string]bool{}
	for _, node := range nodes {
		seen[node.ID] = true
		request := poll.reviewRequest(node.githubPRReviewerStatus, true)
		if request == nil {
			if prior := previous[node.ID]; prior != nil {
				poll.removeReviewRequest(&changes, *prior, node.viewerReview())
			}
			continue
		}
		current := githubPRObservationFromStatus(node)
		current.Role, current.Ready = githubPRRoleReviewer, "unknown"
		current.ReviewRequest, current.ViewerReview = request, node.viewerReview()
		current.ObservedAt, current.VerifiedAt, current.Access = poll.observedAt, poll.observedAt, "ok"
		prior := previous[node.ID]
		if prior != nil {
			current.BaselineAt = prior.BaselineAt
			poll.reviewSubmitted(&changes, current, prior.ViewerReview)
		} else {
			baseline, stored, err := poll.storedObservedBaseline(node.ID, githubPRRoleReviewer)
			if err != nil {
				return githubPRWatchChanges{}, err
			}
			current.BaselineAt = baseline
			if !stored {
				current.BaselineAt = poll.observedAt
				event := poll.observedEvent(current, node, githubPRChecks{}, nil)
				event.Payload.Triage["needs_user"] = true
				event.Payload.Triage["request"] = request
				event.Payload.Untrusted = nil
				changes.events = append(changes.events, event)
			} else if current.ViewerReview != nil && githubTimeAfter(current.ViewerReview.SubmittedAt, baseline) {
				poll.reviewSubmitted(&changes, current, nil)
			}
			if polled {
				event := poll.event(current, githubPREventReviewRequested, poll.observedAt, current.AuthorLogin, poll.observedAt,
					fmt.Sprintf("PR #%d review requested", current.Number), map[string]any{"category": "review_request", "needs_user": true, "request": request})
				event.Payload.ActorKind = node.Author.kind()
				changes.events = append(changes.events, event)
			}
		}
		changes.upserts = append(changes.upserts, current)
	}
	leaving := []string{}
	for id := range previous {
		if !seen[id] {
			leaving = append(leaving, id)
		}
	}
	sort.Strings(leaving)
	followUps, err := poll.fetchReviewerFollowUps(leaving)
	if err != nil {
		return githubPRWatchChanges{}, err
	}
	for _, id := range leaving {
		observation := *previous[id]
		followUp := followUps[id]
		if followUp != nil {
			current := observation
			current.ViewerReview = followUp.viewerReview()
			if followUp.State == "OPEN" && followUp.ReviewRequests.TotalCount <= len(followUp.ReviewRequests.Nodes) && poll.reviewRequest(followUp.githubPRReviewerStatus, false) == nil {
				poll.removeReviewRequest(&changes, observation, followUp.viewerReview())
				continue
			}
			poll.reviewSubmitted(&changes, current, observation.ViewerReview)
			observation.ViewerReview = current.ViewerReview
		}
		poll.resolveLeaving(&changes, observation, nil, followUp)
	}
	return changes, nil
}

func (poll *githubPRWatchPoll) fetchReviewerFollowUps(ids []string) (map[string]*githubPRFollowUpNode, error) {
	result := map[string]*githubPRFollowUpNode{}
	if len(ids) == 0 {
		return result, nil
	}
	var data struct {
		Nodes []*githubPRFollowUpNode `json:"nodes"`
	}
	errs, err := poll.graphQL(githubPRWatchReviewerFollowUpQuery, map[string]any{"ids": ids, "login": poll.params.IdentityLogin}, &data)
	if err != nil {
		return nil, err
	}
	for _, issue := range errs {
		if issue.Type != "FORBIDDEN" && issue.Type != "NOT_FOUND" {
			return nil, errs
		}
	}
	for _, node := range data.Nodes {
		if node != nil && node.ID != "" {
			result[node.ID] = node
		}
	}
	return result, nil
}

func (poll *githubPRWatchPoll) reviewSubmitted(changes *githubPRWatchChanges, current githubPRObservation, previous *githubPRViewerReview) bool {
	review := current.ViewerReview
	if review == nil || (previous != nil && previous.ID == review.ID) {
		return false
	}
	changes.events = append(changes.events, poll.event(current, githubPREventReviewSubmitted, review.ID, poll.params.IdentityLogin, review.SubmittedAt,
		fmt.Sprintf("PR #%d review submitted", current.Number), map[string]any{"category": "review_request", "needs_user": false}))
	return true
}

func (poll *githubPRWatchPoll) removeReviewRequest(changes *githubPRWatchChanges, observation githubPRObservation, review *githubPRViewerReview) {
	current := observation
	current.ViewerReview = review
	reason := "withdrawn"
	if poll.reviewSubmitted(changes, current, observation.ViewerReview) {
		reason = "reviewed"
	}
	changes.events = append(changes.events, poll.event(current, githubPREventReviewRequestRemoved, poll.observedAt, "", poll.observedAt,
		fmt.Sprintf("PR #%d review request removed (%s)", current.Number, reason), map[string]any{"category": "review_request", "needs_user": false, "reason": reason}))
	changes.deletes = append(changes.deletes, observation)
}
