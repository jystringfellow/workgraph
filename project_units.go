package workgraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const projectUnitQuietGap = 2 * time.Hour

type ProjectUnitsConfig struct {
	HomeDir      string
	DatabasePath string
	Now          time.Time
	Location     *time.Location
	GitEmails    []string
}

type ProjectUnit struct {
	ID        string
	Kind      string
	Container string
	Start     time.Time
	End       time.Time
	EventIDs  []string
	Eligible  bool
}

type projectUnitEvent struct {
	ID        string
	Type      string
	Timestamp time.Time
	Project   string
	Payload   projectUnitPayload
}

type projectUnitPayload struct {
	ChannelID  string `json:"channel_id"`
	TS         string `json:"ts"`
	ThreadTS   string `json:"thread_ts"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	RepoPath   string `json:"repo_path"`
	ID         string `json:"id"`
}

func ListProjectUnits(config ProjectUnitsConfig) ([]ProjectUnit, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return nil, err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	location := config.Location
	if location == nil {
		location = time.Local
	}
	return projectUnits(db, now, location, normalizedEmailSet(config.GitEmails))
}

func projectUnits(db *sql.DB, now time.Time, location *time.Location, gitEmails map[string]bool) ([]ProjectUnit, error) {
	events, err := loadProjectUnitEvents(db, gitEmails)
	if err != nil {
		return nil, err
	}
	var units []ProjectUnit
	units = append(units, slackProjectUnits(events["slack"], now)...)
	units = append(units, keyedProjectUnits("github.pr", events["github"], func(event projectUnitEvent) string {
		return fmt.Sprintf("%s#%d", strings.ToLower(event.Payload.Repository), event.Payload.Number)
	})...)
	units = append(units, gitSessionProjectUnits(events["git"])...)
	units = append(units, keyedProjectUnits("notion.page", events["notion"], func(event projectUnitEvent) string {
		return event.Payload.ID + ":" + event.Timestamp.In(location).Format(time.DateOnly)
	})...)
	for _, event := range events["calendar"] {
		units = append(units, newProjectUnit(event.ID, "calendar.event", []projectUnitEvent{event}))
	}
	sort.Slice(units, func(i, j int) bool {
		if !units[i].Start.Equal(units[j].Start) {
			return units[i].Start.Before(units[j].Start)
		}
		return units[i].ID < units[j].ID
	})
	return units, nil
}

func loadProjectUnitEvents(db *sql.DB, gitEmails map[string]bool) (map[string][]projectUnitEvent, error) {
	rows, err := db.Query(`SELECT id, source, type, timestamp, COALESCE(project, ''), payload_json FROM events
		WHERE (type IN ('slack.message', 'slack.thread_reply', 'git.commit', 'notion.page_updated', 'calendar.event')
			OR type = 'github.pull_request' OR type GLOB 'github.pull_request.*')
			AND ` + notControlQueryEvent + `
		ORDER BY timestamp ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query unit events: %w", err)
	}
	defer rows.Close()
	events := map[string][]projectUnitEvent{}
	for rows.Next() {
		var event projectUnitEvent
		var source, timestamp, payload string
		if err := rows.Scan(&event.ID, &source, &event.Type, &timestamp, &event.Project, &payload); err != nil {
			return nil, fmt.Errorf("scan unit event: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil {
			continue
		}
		event.Timestamp = parsed.UTC()
		if json.Unmarshal([]byte(payload), &event.Payload) != nil || !projectUnitEventKeyed(source, event.Payload) {
			continue
		}
		if source == "git" && !gitCommitAuthoredByLocalIdentity(payload, gitEmails) {
			continue
		}
		events[source] = append(events[source], event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query unit events: %w", err)
	}
	for _, list := range events {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].Timestamp.Equal(list[j].Timestamp) {
				return list[i].Timestamp.Before(list[j].Timestamp)
			}
			return list[i].ID < list[j].ID
		})
	}
	return events, nil
}

func projectUnitEventKeyed(source string, payload projectUnitPayload) bool {
	switch source {
	case "slack":
		return payload.ChannelID != "" && payload.TS != ""
	case "github":
		return payload.Repository != "" && payload.Number > 0
	case "git":
		return payload.RepoPath != ""
	case "notion":
		return payload.ID != ""
	case "calendar":
		return true
	default:
		return false
	}
}

func slackProjectUnits(events []projectUnitEvent, now time.Time) []ProjectUnit {
	threads := map[string]bool{}
	for _, event := range events {
		if event.Payload.ThreadTS != "" {
			threads[event.Payload.ChannelID+":"+event.Payload.ThreadTS] = true
		}
	}
	var units []ProjectUnit
	threadMembers := map[string][]projectUnitEvent{}
	channelMessages := map[string][]projectUnitEvent{}
	for _, event := range events {
		threadTS := event.Payload.ThreadTS
		if threadTS == "" && threads[event.Payload.ChannelID+":"+event.Payload.TS] {
			threadTS = event.Payload.TS
		}
		if threadTS != "" {
			key := event.Payload.ChannelID + ":" + threadTS
			threadMembers[key] = append(threadMembers[key], event)
			continue
		}
		channelMessages[event.Payload.ChannelID] = append(channelMessages[event.Payload.ChannelID], event)
	}
	for key, members := range threadMembers {
		units = append(units, newProjectUnit("slack.thread:"+key, "slack.thread", members))
	}
	for channelID, messages := range channelMessages {
		for _, burst := range splitProjectUnitEvents(messages) {
			unit := newProjectUnit(fmt.Sprintf("slack.burst:%s:%s", channelID, burst[0].Payload.TS), "slack.burst", burst)
			unit.Eligible = now.Sub(unit.End) >= projectUnitQuietGap
			units = append(units, unit)
		}
	}
	return units
}

func gitSessionProjectUnits(events []projectUnitEvent) []ProjectUnit {
	byRepo := map[string][]projectUnitEvent{}
	for _, event := range events {
		byRepo[event.Payload.RepoPath] = append(byRepo[event.Payload.RepoPath], event)
	}
	var units []ProjectUnit
	for repoPath, commits := range byRepo {
		for _, session := range splitProjectUnitEvents(commits) {
			sha := strings.TrimPrefix(session[0].ID, "git.commit:")
			units = append(units, newProjectUnit(fmt.Sprintf("git.session:%s:%s", repoPath, sha), "git.session", session))
		}
	}
	return units
}

func keyedProjectUnits(kind string, events []projectUnitEvent, key func(projectUnitEvent) string) []ProjectUnit {
	groups := map[string][]projectUnitEvent{}
	for _, event := range events {
		groups[key(event)] = append(groups[key(event)], event)
	}
	units := make([]ProjectUnit, 0, len(groups))
	for groupKey, members := range groups {
		units = append(units, newProjectUnit(kind+":"+groupKey, kind, members))
	}
	return units
}

func splitProjectUnitEvents(events []projectUnitEvent) [][]projectUnitEvent {
	var groups [][]projectUnitEvent
	for index, event := range events {
		if index == 0 || event.Timestamp.Sub(events[index-1].Timestamp) >= projectUnitQuietGap {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], event)
	}
	return groups
}

func newProjectUnit(id, kind string, members []projectUnitEvent) ProjectUnit {
	unit := ProjectUnit{
		ID:        id,
		Kind:      kind,
		Container: members[0].Project,
		Start:     members[0].Timestamp,
		End:       members[len(members)-1].Timestamp,
		Eligible:  true,
	}
	for _, member := range members {
		unit.EventIDs = append(unit.EventIDs, member.ID)
	}
	return unit
}
