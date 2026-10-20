package facts

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
	_ "github.com/mattn/go-sqlite3"
)

func TestResumeShowsLastPersonalSessionWithoutGitHubSnapshotNoise(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	for _, event := range []storedEvent{
		{ID: "git:earlier", Source: "git", Type: "git.commit", Timestamp: now.Add(-48 * time.Hour), Project: "other", Summary: "Earlier personal work", Payload: `{"subject":"Earlier personal work","author_email":"me@example.test","commit":"1111111111111111"}`},
		{ID: "git:session-start", Source: "git", Type: "git.commit", Timestamp: now.Add(-16 * time.Hour), Project: "workgraph", Summary: "Start handoff work", Payload: `{"subject":"Start handoff work","author_email":"me@example.test","commit":"2222222222222222"}`},
		{ID: "git:session-end", Source: "git", Type: "git.commit", Timestamp: now.Add(-15*time.Hour - 40*time.Minute), Project: "workgraph", Summary: "Finish handoff work", Payload: `{"subject":"Finish handoff work","author_email":"me@example.test","commit":"3333333333333333"}`},
		{ID: "github:unrelated", Source: "github", Type: "github.issue", Timestamp: now.Add(-time.Hour), Project: "orca", Actor: "other-author", Summary: "Unrelated issue", Payload: `{"repository":"other/orca","number":9,"state":"open","actor":"other-author","title":"Unrelated issue"}`},
		{ID: "file:clone", Source: "file", Type: "file.created", Timestamp: now.Add(-30 * time.Minute), Project: "orca", Summary: "Cloned repository file", Payload: `{"path":"/tmp/orca/README.md"}`},
	} {
		insertEvent(t, initialized.DatabasePath, event)
	}

	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now, GitEmails: []string{"me@example.test"}, GitHubLogins: []string{"me"}})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	for _, expected := range []string{"Where you left off", "workgraph", "Start handoff work", "Finish handoff work", "Run: workgraph resume workgraph", "Resumable projects"} {
		if !strings.Contains(resume.Message, expected) {
			t.Errorf("expected %q in resume overview:\n%s", expected, resume.Message)
		}
	}
	if strings.Contains(resume.Message, "Unrelated issue") || strings.Contains(resume.Message, "Cloned repository file") || strings.Contains(resume.Message, "Where you left off\n- orca") {
		t.Fatalf("repository snapshots and clone creation must not become the handoff:\n%s", resume.Message)
	}
}

func TestResumeFallsBackToOlderPersonalWork(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "git:old", Source: "git", Type: "git.commit", Timestamp: now.Add(-9 * 24 * time.Hour), Project: "workgraph", Summary: "Old personal work", Payload: `{"subject":"Old personal work","author_email":"me@example.test","commit":"4444444444444444"}`})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now, GitEmails: []string{"me@example.test"}})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(resume.Message, "Old personal work") || !strings.Contains(resume.Message, "older than a week") {
		t.Fatalf("expected labeled fallback to last known work:\n%s", resume.Message)
	}
}

func TestResumeHandoffDoesNotInventPersonalWorkFromWeakEvidence(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	for _, event := range []storedEvent{
		{ID: "git:foreign", Source: "git", Type: "git.commit", Timestamp: now.Add(-time.Hour), Project: "orca", Summary: "Someone else's commit", Payload: `{"subject":"Someone else's commit","author_email":"other@example.test","commit":"5555555555555555"}`},
		{ID: "file:broad", Source: "file", Type: "file.modified", Timestamp: now.Add(-2 * time.Hour), Project: "Downloads", Payload: `{"path":"/tmp/Downloads/temp.txt"}`},
		{ID: "github:issue", Source: "github", Type: "github.issue", Timestamp: now.Add(-10 * time.Minute), Project: "openrig", Actor: "other-author", Payload: `{"repository":"other/openrig","number":8,"state":"open","actor":"other-author","title":"Unrelated issue"}`},
	} {
		insertEvent(t, initialized.DatabasePath, event)
	}
	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now, GitEmails: []string{"me@example.test"}, GitHubLogins: []string{"me"}})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(resume.Message, "No personal work session found in captured events.") || len(resume.LastWork) != 0 {
		t.Fatalf("weak evidence must not become a handoff:\n%s", resume.Message)
	}
}

func TestResumeHandoffStopsAtProjectAndTimeGaps(t *testing.T) {
	for _, gap := range []struct {
		name    string
		project string
		age     time.Duration
	}{
		{name: "project change", project: "other", age: 70 * time.Minute},
		{name: "time gap", project: "workgraph", age: 91 * time.Minute},
	} {
		t.Run(gap.name, func(t *testing.T) {
			homeDir := filepath.Join(t.TempDir(), ".workgraph")
			initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
			if err != nil {
				t.Fatalf("init: %v", err)
			}
			now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
			insertEvent(t, initialized.DatabasePath, storedEvent{ID: "file:latest", Source: "file", Type: "file.modified", Timestamp: now.Add(-time.Hour), Project: "workgraph", Summary: "Latest work", Payload: `{"path":"/tmp/workgraph/latest.go"}`})
			insertEvent(t, initialized.DatabasePath, storedEvent{ID: "file:earlier", Source: "file", Type: "file.modified", Timestamp: now.Add(-gap.age), Project: gap.project, Summary: "Earlier work", Payload: `{"path":"/tmp/workgraph/earlier.go"}`})
			resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now})
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if len(resume.LastWork) != 1 || resume.LastWork[0].ID != "file:latest" {
				t.Fatalf("handoff crossed project or time gap: %#v", resume.LastWork)
			}
		})
	}
}

func TestResumeCommandRecordsCheckInAndShowsNewlyCapturedPersonalWork(t *testing.T) {
	gh := writeFakeGHGraphQL(t, t.TempDir())
	t.Setenv("PATH", filepath.Dir(gh.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Now()
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "file:before", Source: "file", Type: "file.modified", Timestamp: now.Add(-time.Hour), Project: "workgraph", Summary: "Earlier file work", Payload: `{"path":"/tmp/workgraph/earlier.go"}`})
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE resume_checkins`); err != nil {
		t.Fatalf("simulate older database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	first, err := runworkgraph(t, repoRoot(t), "resume", "--home", homeDir)
	if err != nil {
		t.Fatalf("first resume: %v\n%s", err, first)
	}
	if strings.Contains(string(first), "Personal work since last check-in") {
		t.Fatalf("first check-in must not claim a previous interval:\n%s", first)
	}
	previous := resumeOverviewCheckIn(t, initialized.DatabasePath)
	if previous.IsZero() {
		t.Fatal("plain resume did not record a check-in")
	}

	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "file:late-capture", Source: "file", Type: "file.modified", Timestamp: now.Add(-2 * time.Hour), CreatedAt: time.Now(), Project: "workgraph", Summary: "Late captured file work", Payload: `{"path":"/tmp/workgraph/late.go"}`})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "slack:relevant", Source: "slack", Type: "slack.message", Timestamp: now.Add(-2 * time.Hour), CreatedAt: time.Now(), Project: "workgraph", Summary: "Review request from Lila", Involvement: `["mentioned"]`, Payload: `{"text":"Review request from Lila"}`})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "github:ambient", Source: "github", Type: "github.issue", Timestamp: now.Add(-2 * time.Hour), CreatedAt: time.Now(), Project: "orca", Summary: "Ambient repository issue", Payload: `{"title":"Ambient repository issue","number":7,"state":"open"}`})
	second, err := runworkgraph(t, repoRoot(t), "resume", "--home", homeDir)
	if err != nil {
		t.Fatalf("second resume: %v\n%s", err, second)
	}
	for _, expected := range []string{"Personal work since last check-in", "Late captured file work", "Previous check-in", "Relevant updates since last check-in", "Review request from Lila"} {
		if !strings.Contains(string(second), expected) {
			t.Errorf("expected %q after previous check-in:\n%s", expected, second)
		}
	}
	if strings.Contains(string(second), "Ambient repository issue") {
		t.Fatalf("unclassified GitHub snapshot re-entered the handoff:\n%s", second)
	}
	if !resumeOverviewCheckIn(t, initialized.DatabasePath).After(previous) {
		t.Fatal("successful second resume did not advance check-in")
	}
	third, err := runworkgraph(t, repoRoot(t), "resume", "--home", homeDir)
	if err != nil {
		t.Fatalf("third resume: %v\n%s", err, third)
	}
	if !strings.Contains(string(third), "No new personal work captured.") {
		t.Fatalf("third resume should not repeat the previous interval:\n%s", third)
	}
	if !strings.Contains(string(third), "No new relevant updates captured.") {
		t.Fatalf("third resume should not repeat relevant updates:\n%s", third)
	}
}

func TestResumeInspectionsDoNotAdvanceOverviewCheckIn(t *testing.T) {
	gh := writeFakeGHGraphQL(t, t.TempDir())
	t.Setenv("PATH", filepath.Dir(gh.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Now()
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "file:work", Source: "file", Type: "file.modified", Timestamp: now.Add(-time.Hour), Project: "workgraph", Payload: `{"path":"/tmp/workgraph/work.go"}`})
	if output, err := runworkgraph(t, repoRoot(t), "resume", "--home", homeDir); err != nil {
		t.Fatalf("initial resume: %v\n%s", err, output)
	}
	previous := resumeOverviewCheckIn(t, initialized.DatabasePath)
	for _, args := range [][]string{
		{"resume", "--home", homeDir, "workgraph"},
		{"resume", "--all", "--home", homeDir},
		{"resume", "--debug-relevance", "--home", homeDir},
	} {
		if output, err := runworkgraph(t, repoRoot(t), args...); err != nil {
			t.Fatalf("inspection %v: %v\n%s", args, err, output)
		}
		if got := resumeOverviewCheckIn(t, initialized.DatabasePath); !got.Equal(previous) {
			t.Fatalf("inspection %v advanced check-in from %v to %v", args, previous, got)
		}
	}
	if _, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now}); err != nil {
		t.Fatalf("query API: %v", err)
	}
	if got := resumeOverviewCheckIn(t, initialized.DatabasePath); !got.Equal(previous) {
		t.Fatalf("query API advanced check-in from %v to %v", previous, got)
	}
}

func TestResumeCheckInAdvancesWithinSameSecond(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	config := workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath}
	first := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	second := first.Add(100 * time.Millisecond)
	if err := workgraph.RecordResumeCheckIn(config, first); err != nil {
		t.Fatalf("first check-in: %v", err)
	}
	if err := workgraph.RecordResumeCheckIn(config, second); err != nil {
		t.Fatalf("second check-in: %v", err)
	}
	if got := resumeOverviewCheckIn(t, initialized.DatabasePath); !got.Equal(second) {
		t.Fatalf("same-second check-in stayed at %v, want %v", got, second)
	}
	if err := workgraph.RecordResumeCheckIn(config, first); err != nil {
		t.Fatalf("older check-in: %v", err)
	}
	if got := resumeOverviewCheckIn(t, initialized.DatabasePath); !got.Equal(second) {
		t.Fatalf("older check-in moved checkpoint backward to %v", got)
	}
}

func TestResumeReadsLegacyDatabaseWithoutInvolvementColumn(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "file:legacy", Source: "file", Type: "file.modified", Timestamp: now.Add(-time.Hour), Project: "workgraph", Summary: "Legacy local work", Payload: `{"path":"/tmp/workgraph/legacy.go"}`})
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE events DROP COLUMN involvement_json`); err != nil {
		t.Fatalf("simulate legacy event schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now})
	if err != nil {
		t.Fatalf("legacy resume: %v", err)
	}
	if !strings.Contains(resume.Message, "Legacy local work") || !strings.Contains(resume.Message, "Resumable projects") {
		t.Fatalf("legacy event was lost from resume:\n%s", resume.Message)
	}
}

func resumeOverviewCheckIn(t *testing.T, dbPath string) time.Time {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`SELECT checked_at FROM resume_checkins WHERE scope = 'overview'`).Scan(&raw); err != nil {
		t.Fatalf("read overview check-in: %v", err)
	}
	checkedAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("parse overview check-in %q: %v", raw, err)
	}
	return checkedAt
}
