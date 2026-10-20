package workgraph

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type EventsTodayConfig struct {
	HomeDir      string
	DatabasePath string
	Type         string
	Actor        string
	Involvement  string
	Limit        int
}

type EventsTodayResult struct {
	Date        string
	Type        string
	Actor       string
	Involvement string
	Events      []TodayEvent
	Message     string
}

func EventsToday(config EventsTodayConfig) (EventsTodayResult, error) {
	today, err := Today(TodayConfig{
		HomeDir:            config.HomeDir,
		DatabasePath:       config.DatabasePath,
		Actor:              config.Actor,
		Involvement:        config.Involvement,
		IncludeAllEvidence: true,
	})
	if err != nil {
		return EventsTodayResult{}, err
	}
	eventType := strings.TrimSpace(config.Type)
	events := make([]TodayEvent, 0, len(today.Events))
	for _, event := range today.Events {
		if !eventTypeMatches(event.Type, eventType) {
			continue
		}
		events = append(events, event)
	}
	if config.Limit > 0 && len(events) > config.Limit {
		events = events[len(events)-config.Limit:]
	}
	result := EventsTodayResult{
		Date:        today.Date,
		Type:        eventType,
		Actor:       strings.TrimSpace(config.Actor),
		Involvement: strings.TrimSpace(config.Involvement),
		Events:      events,
	}
	result.Message = eventsTodayMessage(result, time.Now().Location())
	return result, nil
}

func eventTypeMatches(eventType string, filter string) bool {
	if filter == "" {
		return true
	}
	if prefix, wildcard := strings.CutSuffix(filter, ".*"); wildcard {
		return strings.HasPrefix(eventType, prefix+".")
	}
	return eventType == filter
}

func untrustedCommentLine(event TodayEvent) string {
	var payload struct {
		ActorKind string `json:"actor_kind"`
		Untrusted struct {
			Author string `json:"author"`
			Body   string `json:"body"`
		} `json:"untrusted"`
	}
	if event.Type != githubPREventReviewComment || json.Unmarshal([]byte(event.Payload), &payload) != nil || payload.Untrusted.Body == "" {
		return ""
	}
	firstLine, _, _ := strings.Cut(strings.TrimSpace(payload.Untrusted.Body), "\n")
	runes := []rune(strings.TrimSpace(firstLine))
	if len(runes) > todayEventLabelMaxRunes {
		runes = append(runes[:todayEventLabelMaxRunes-1], '…')
	}
	return fmt.Sprintf("  untrusted comment (%s, %s): %q", payload.Untrusted.Author, payload.ActorKind, string(runes))
}

func eventsTodayMessage(result EventsTodayResult, location *time.Location) string {
	lines := []string{
		"Events today",
		fmt.Sprintf("%s: %s", result.Date, pluralize(len(result.Events), "event")),
	}
	if result.Type != "" {
		lines = append(lines, "Type: "+result.Type)
	}
	if result.Actor != "" {
		lines = append(lines, "Actor: "+result.Actor)
	}
	if result.Involvement != "" {
		lines = append(lines, "Involvement: "+result.Involvement)
	}
	if len(result.Events) == 0 {
		lines = append(lines, "No matching events captured today.")
		return strings.Join(lines, "\n")
	}
	for _, event := range result.Events {
		lines = append(lines, fmt.Sprintf("- %s %s %s", event.Timestamp.In(location).Format("15:04"), event.Type, eventLabel(event)))
		lines = append(lines, "  id: "+event.ID)
		if event.Project != "" {
			lines = append(lines, "  project: "+event.Project)
		}
		if event.Actor != "" {
			lines = append(lines, "  actor: "+event.Actor)
		}
		switch {
		case event.Involvement == nil:
			lines = append(lines, "  involvement: unclassified")
		case len(event.Involvement) == 0:
			lines = append(lines, "  involvement: none")
		default:
			lines = append(lines, "  involvement: "+strings.Join(event.Involvement, ", "))
		}
		if event.Path != "" {
			lines = append(lines, "  path: "+event.Path)
		}
		if line := untrustedCommentLine(event); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
