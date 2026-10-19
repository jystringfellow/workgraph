package facts

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestTodayExcludesGitHubSnapshotsFromPersonalActivity(t *testing.T) {
	clock := time.Now()
	now := time.Date(clock.Year(), clock.Month(), clock.Day(), 12, 0, 0, 0, clock.Location())
	longTitle := strings.Repeat("Inspect the complete snapshot evidence. ", 6) + "DETAIL-TAIL"
	for _, snapshot := range []struct {
		name        string
		event       storedEvent
		involvement []string
		label       string
	}{
		{
			name: "legacy_issue",
			event: storedEvent{
				ID: "github:legacy-issue", Source: "github", Type: "github.issue", Timestamp: now,
				Project: "snapshot-project", Actor: "connected-user", Summary: "Legacy issue summary",
				Payload: `{"repository":"acme/widget","number":12,"url":"https://github.com/acme/widget/issues/12","state":"open","actor":"connected-user","title":"Legacy issue updated by someone else"}`,
			},
			label: "Legacy issue updated by someone else (#12 open)",
		},
		{
			name: "legacy_pull_request",
			event: storedEvent{
				ID: "github:legacy-pr", Source: "github", Type: "github.pull_request", Timestamp: now,
				Project: "snapshot-project", Actor: "connected-user", Summary: "Legacy pull request summary",
				Payload: `{"repository":"acme/widget","number":42,"url":"https://github.com/acme/widget/pull/42","state":"closed","actor":"connected-user","title":"Legacy pull request updated by someone else","branch":"feature/widget","commit":"abcdef1234567890"}`,
			},
			label: "Legacy pull request updated by someone else (#42 closed)",
		},
		{
			name: "participant_issue",
			event: storedEvent{
				ID: "github:participant-issue", Source: "github", Type: "github.issue", Timestamp: now,
				Project: "snapshot-project", Actor: "other-author", Summary: "Participant issue summary",
				Involvement: `["involved"]`,
				Payload:     `{"repository":"acme/widget","number":13,"url":"https://github.com/acme/widget/issues/13","state":"open","actor":"other-author","title":"Participant issue with unknown updater","matched_scopes":["involves"]}`,
			},
			involvement: []string{"involved"},
			label:       "Participant issue with unknown updater (#13 open)",
		},
		{
			name: "participant_pull_request",
			event: storedEvent{
				ID: "github:participant-pr", Source: "github", Type: "github.pull_request", Timestamp: now,
				Project: "snapshot-project", Actor: "other-author", Summary: "Participant pull request summary\n\twith stored whitespace",
				Involvement: `["involved","review_requested"]`,
				Payload:     fmt.Sprintf(`{"repository":"acme/widget","number":43,"url":"https://github.com/acme/widget/pull/43","state":"open","actor":"other-author","title":%q,"matched_scopes":["involves","review_requested"]}`, longTitle),
			},
			involvement: []string{"involved", "review_requested"},
			label:       longTitle + " (#43 open)",
		},
	} {
		for _, withGit := range []bool{false, true} {
			name := snapshot.name
			if withGit {
				name += "_with_personal_git"
			}
			t.Run(name, func(t *testing.T) {
				homeDir, dbPath := initAssociationStore(t)
				insertEvent(t, dbPath, snapshot.event)
				if withGit {
					insertEvent(t, dbPath, storedEvent{
						ID: "git:personal", Source: "git", Type: "git.commit", Timestamp: now.Add(time.Minute),
						Project: "personal-project", Actor: "local-developer", Summary: "Implement personal work",
						Payload: `{"repo_path":"/tmp/personal-project","subject":"Implement personal work","branch":"main","commit":"0123456789abcdef"}`,
					})
				}
				before := snapshotEventRows(t, dbPath)
				today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now})
				if err != nil {
					t.Fatalf("today failed: %v", err)
				}
				if withGit {
					if len(today.Events) != 1 || today.Events[0].ID != "git:personal" {
						t.Errorf("expected only personal git activity, got %#v", today.Events)
					}
					if len(today.Sessions) != 1 || len(today.Sessions[0].Events) != 1 || today.Sessions[0].Events[0].ID != "git:personal" {
						t.Errorf("expected only a personal git session, got %#v", today.Sessions)
					}
					for _, expected := range []string{now.Format("2006-01-02") + ": 1 event", "- personal-project: 1 event", "git.commit Implement personal work (main 0123456)", "Details: workgraph events today"} {
						if !strings.Contains(today.Message, expected) {
							t.Errorf("expected %q in personal activity output:\n%s", expected, today.Message)
						}
					}
				} else {
					if len(today.Events) != 0 || len(today.Sessions) != 0 {
						t.Errorf("GitHub work snapshot must not count as personal activity or create sessions: events=%d sessions=%d", len(today.Events), len(today.Sessions))
					}
					for _, expected := range []string{now.Format("2006-01-02") + ": 0 events", "No activity has been captured today."} {
						if !strings.Contains(today.Message, expected) {
							t.Errorf("expected snapshot-only empty state %q:\n%s", expected, today.Message)
						}
					}
					if strings.Contains(today.Message, "Projects") || strings.Contains(today.Message, "Sessions") {
						t.Errorf("snapshot-only day must not create Projects or Sessions:\n%s", today.Message)
					}
				}
				if strings.Contains(today.Message, "snapshot-project") || strings.Contains(today.Message, snapshot.event.Type) {
					t.Errorf("snapshot must not appear as a project or session event:\n%s", today.Message)
				}
				if len(today.Associations) != 0 || strings.Contains(today.Message, "Associations") {
					t.Errorf("unrelated snapshots and git work must not create association context:\n%s", today.Message)
				}

				details, err := workgraph.EventsToday(workgraph.EventsTodayConfig{HomeDir: homeDir, DatabasePath: dbPath})
				if err != nil {
					t.Fatalf("events today failed: %v", err)
				}
				wantCount := 1
				if withGit {
					wantCount++
				}
				if len(details.Events) != wantCount {
					t.Fatalf("expected all %d stored events in detailed evidence, got %#v", wantCount, details.Events)
				}
				var found bool
				for _, event := range details.Events {
					if event.ID != snapshot.event.ID {
						continue
					}
					found = true
					if event.Source != snapshot.event.Source || event.Type != snapshot.event.Type || !event.Timestamp.Equal(snapshot.event.Timestamp) || event.Project != snapshot.event.Project || event.Actor != snapshot.event.Actor || event.Summary != snapshot.event.Summary || event.Payload != snapshot.event.Payload || !reflect.DeepEqual(event.Involvement, snapshot.involvement) {
						t.Errorf("detailed evidence must preserve the complete snapshot metadata and raw payload: %#v", event)
					}
				}
				if !found {
					t.Errorf("snapshot %s must remain inspectable", snapshot.event.ID)
				}
				involvementLabel := "unclassified"
				if snapshot.involvement != nil {
					involvementLabel = strings.Join(snapshot.involvement, ", ")
				}
				for _, expected := range []string{snapshot.event.Type + " " + snapshot.label, "id: " + snapshot.event.ID, "project: snapshot-project", "actor: " + snapshot.event.Actor, "involvement: " + involvementLabel} {
					if !strings.Contains(details.Message, expected) {
						t.Errorf("expected complete detailed evidence %q:\n%s", expected, details.Message)
					}
				}
				filtered, err := workgraph.EventsToday(workgraph.EventsTodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Actor: snapshot.event.Actor, Type: snapshot.event.Type})
				if err != nil || len(filtered.Events) != 1 || filtered.Events[0].ID != snapshot.event.ID {
					t.Errorf("exact actor/type inspection must retain snapshots: events=%#v err=%v", filtered.Events, err)
				}
				if snapshot.involvement != nil {
					filtered, err := workgraph.EventsToday(workgraph.EventsTodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Involvement: "involved"})
					if err != nil || len(filtered.Events) != 1 || filtered.Events[0].ID != snapshot.event.ID {
						t.Errorf("exact involvement inspection must retain snapshots: events=%#v err=%v", filtered.Events, err)
					}
					personal, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: dbPath, Now: now, Involvement: "involved"})
					if err != nil || len(personal.Events) != 1 || personal.Events[0].ID != snapshot.event.ID {
						t.Errorf("explicit today involvement inspection must retain matching snapshots: events=%#v err=%v", personal.Events, err)
					}
				}
				for _, filter := range []workgraph.EventsTodayConfig{
					{HomeDir: homeDir, DatabasePath: dbPath, Actor: snapshot.event.Actor + "-different"},
					{HomeDir: homeDir, DatabasePath: dbPath, Involvement: "authored"},
				} {
					filtered, err := workgraph.EventsToday(filter)
					if err != nil || len(filtered.Events) != 0 {
						t.Errorf("inspection must use exact stored metadata without inferring authorship: events=%#v err=%v", filtered.Events, err)
					}
				}
				if after := snapshotEventRows(t, dbPath); after != before {
					t.Error("today and detailed inspection must not rewrite or delete stored events")
				}
			})
		}
	}
}
