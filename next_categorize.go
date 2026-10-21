package workgraph

import (
	"fmt"
	"time"
)

const (
	nextReasonCategorize    = "categorize"
	nextGroupCategorize     = "categorize"
	nextCategorizeTextLimit = 3
)

type nextCategorizeState struct {
	AutoAppliedToday int
	Paused           bool
	PauseReason      string
}

func loadNextCategorizeItems(db projectLinkQueryer, now time.Time, includeUntrusted bool) ([]NextItem, nextCategorizeState, error) {
	present, err := projectLinksTablePresent(db)
	if err != nil || !present {
		return nil, nextCategorizeState{}, err
	}
	links, err := queryProjectLinks(db, `WHERE state IN ('proposed', 'applied') ORDER BY id`)
	if err != nil {
		return nil, nextCategorizeState{}, err
	}
	accuracy, err := projectLinkAccuracy(db, now.UTC())
	if err != nil {
		return nil, nextCategorizeState{}, err
	}
	state := nextCategorizeState{Paused: accuracy.Paused, PauseReason: accuracy.PauseReason}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	for _, link := range links {
		if projectLinkUndoable(link) && !link.CreatedAt.Before(midnight) {
			state.AutoAppliedToday++
		}
	}
	var items []NextItem
	for _, link := range projectReviewDefaultLinks(links, accuracy) {
		items = append(items, nextCategorizeItem(link, includeUntrusted))
	}
	return items, state, nil
}

func nextCategorizeItem(link ProjectLink, includeUntrusted bool) NextItem {
	item := NextItem{
		Schema:   nextItemSchemaV1,
		ID:       fmt.Sprintf("projects:categorize:%d", link.ID),
		Source:   "projects",
		Reason:   nextReasonCategorize,
		Group:    nextGroupCategorize,
		Project:  link.Container,
		Since:    link.CreatedAt,
		OpenedBy: fmt.Sprintf("project_link:%d", link.ID),
		Evidence: link.Evidence,
		Subject:  NextItemSubject{Kind: "unit", ID: link.UnitID},
		Detail: map[string]any{
			"link_id": link.ID, "unit_kind": link.UnitKind, "state": link.State, "auto": link.Auto,
			"source": link.Source, "target": link.Target, "project": link.Project, "proposed_project": link.NewProject,
			"kind": link.Kind, "confidence": link.Confidence, "rule": link.Rule, "profile": link.Profile, "run_id": link.RunID,
		},
		Triage:    map[string]any{},
		Freshness: NextItemFreshness{AsOf: link.CreatedAt, Access: "ok"},
	}
	if includeUntrusted && link.Summary != "" {
		item.Untrusted = map[string]any{"trust": "untrusted", "summary": link.Summary}
	}
	return item
}

func nextCategorizeLinkID(item NextItem) int64 {
	id, _ := item.Detail["link_id"].(int64)
	return id
}

func nextCategorizeItems(items []NextItem) []NextItem {
	var categorize []NextItem
	for _, item := range items {
		if item.Reason == nextReasonCategorize {
			categorize = append(categorize, item)
		}
	}
	return categorize
}

func nextCategorizeLines(items []NextItem, state nextCategorizeState, showIDs bool) []string {
	categorize := nextCategorizeItems(items)
	if len(categorize) == 0 && !state.Paused {
		return nil
	}
	lines := []string{fmt.Sprintf("Categorize: %d awaiting a decision (workgraph projects review), %d auto-applied today (workgraph projects review --auto)", len(categorize), state.AutoAppliedToday)}
	if state.Paused {
		lines = append(lines, "Auto-apply paused: "+state.PauseReason)
	}
	if len(categorize) > nextCategorizeTextLimit {
		categorize = categorize[:nextCategorizeTextLimit]
	}
	for _, item := range categorize {
		lines = append(lines, "  "+nextCategorizeLine(item, showIDs))
	}
	return lines
}

func nextCategorizeLine(item NextItem, showIDs bool) string {
	state := "proposed"
	if item.Detail["state"] == "applied" {
		state = "auto-applied"
	}
	link := ProjectLink{Target: fmt.Sprint(item.Detail["target"]), Project: fmt.Sprint(item.Detail["project"])}
	link.NewProject, _ = item.Detail["proposed_project"].(*ProposedProject)
	confidence, _ := item.Detail["confidence"].(float64)
	line := fmt.Sprintf("%s in %s: %s %s (confidence %.2f)", item.Subject.ID, item.Project, state, ProjectLinkTarget(link), confidence)
	if showIDs {
		line += " [" + item.ID + "]"
	}
	return nextDisplayText(line)
}

func nextNonCategorizeItems(items []NextItem) []NextItem {
	var other []NextItem
	for _, item := range items {
		if item.Reason != nextReasonCategorize {
			other = append(other, item)
		}
	}
	return other
}
