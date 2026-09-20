package workgraph

import (
	"fmt"
	"strings"
	"time"
)

// EventsTodayConfig controls read-only inspection of today's captured events.
type EventsTodayConfig struct {
	HomeDir      string
	DatabasePath string
	Type         string
	Actor        string
	Involvement  string
	Limit        int
}

// EventsTodayResult describes events selected for inspection.
type EventsTodayResult struct {
	Date        string
	Type        string
	Actor       string
	Involvement string
	Events      []TodayEvent
	Message     string
}

// EventsToday returns today's captured events with optional type filtering.
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
		if eventType != "" && event.Type != eventType {
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
	}
	return strings.Join(lines, "\n")
}
