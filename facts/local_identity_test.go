package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestTodayAndResumeUsePerRepositoryGitIdentityWhenGlobalEmailIsEmpty(t *testing.T) {
	gh := writeFakeGHGraphQL(t, t.TempDir())
	t.Setenv("PATH", filepath.Dir(gh.Path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	userHome, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	workRepo := filepath.Join(userHome, "Code", "work-service")
	personalRepo := filepath.Join(userHome, "Code", "personal", "side-project")
	for _, repo := range []string{workRepo, personalRepo} {
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatalf("create repository: %v", err)
		}
		runGit(t, repo, "init", "-b", "main")
	}
	runGit(t, workRepo, "config", "user.email", "Me@Work.Test ")
	personalConfig := filepath.Join(userHome, ".gitconfig-personal")
	if err := os.WriteFile(personalConfig, []byte("[user]\n\temail = me@personal.test\n"), 0o644); err != nil {
		t.Fatalf("write personal git config: %v", err)
	}
	globalConfig := filepath.Join(userHome, ".gitconfig")
	include := fmt.Sprintf("[includeIf \"gitdir:%s/\"]\n\tpath = %s\n", filepath.Join(userHome, "Code", "personal"), personalConfig)
	if err := os.WriteFile(globalConfig, []byte(include), 0o644); err != nil {
		t.Fatalf("write global git config: %v", err)
	}
	t.Setenv("HOME", userHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(userHome, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Now()
	commit := func(id, repo, email, subject string, at time.Time) {
		insertEvent(t, initialized.DatabasePath, storedEvent{
			ID:        id,
			Source:    "git",
			Type:      "git.commit",
			Timestamp: at,
			Project:   filepath.Base(repo),
			Actor:     email,
			Summary:   subject,
			Payload:   fmt.Sprintf(`{"repo_path":%q,"commit":"%s","branch":"main","subject":%q,"author_email":%q}`, repo, strings.Repeat(id[len(id)-1:], 16), subject, email),
		})
	}
	commit("git:work-own-1", workRepo, "me@work.test", "Fix owned work bug", now.Add(-10*time.Minute))
	commit("git:personal-own-2", personalRepo, "ME@personal.test", "Polish personal side project", now.Add(-20*time.Minute))
	commit("git:work-foreign-3", workRepo, "smartling@example.test", "Smartling translation job", now.Add(-5*time.Minute))
	commit("git:work-dependabot-4", workRepo, "dependabot@example.test", "Bump dependency", now.Add(-4*time.Minute))
	commit("git:work-noemail-5", workRepo, "", "Commit without author email", now.Add(-3*time.Minute))

	today, err := runworkgraph(t, repoRoot(t), "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("today: %v\n%s", err, today)
	}
	for _, expected := range []string{"Fix owned work bug", "Polish personal side project"} {
		if !strings.Contains(string(today), expected) {
			t.Errorf("today omitted own commit %q:\n%s", expected, today)
		}
	}
	for _, foreign := range []string{"Smartling translation job", "Bump dependency", "Commit without author email"} {
		if strings.Contains(string(today), foreign) {
			t.Errorf("today showed commit not authored by local identity %q:\n%s", foreign, today)
		}
	}
	if !strings.Contains(string(today), "2 events") {
		t.Errorf("today counts should include only own commits:\n%s", today)
	}

	events, err := runworkgraph(t, repoRoot(t), "events", "today", "--home", homeDir)
	if err != nil {
		t.Fatalf("events today: %v\n%s", err, events)
	}
	for _, expected := range []string{"Fix owned work bug", "Smartling translation job", "Bump dependency"} {
		if !strings.Contains(string(events), expected) {
			t.Errorf("events today should keep every stored commit, missing %q:\n%s", expected, events)
		}
	}

	actor, err := runworkgraph(t, repoRoot(t), "today", "--home", homeDir, "--actor", "smartling@example.test")
	if err != nil {
		t.Fatalf("today --actor: %v\n%s", err, actor)
	}
	if !strings.Contains(string(actor), "Smartling translation job") || strings.Contains(string(actor), "Fix owned work bug") {
		t.Errorf("explicit actor filter should select exactly that actor:\n%s", actor)
	}

	resume, err := runworkgraph(t, repoRoot(t), "resume", "--home", homeDir)
	if err != nil {
		t.Fatalf("resume: %v\n%s", err, resume)
	}
	if strings.Contains(string(resume), "No personal work session found") || !strings.Contains(string(resume), "Fix owned work bug") {
		t.Fatalf("resume should find the personal session from per-repository identity:\n%s", resume)
	}
	if strings.Contains(string(resume), "Smartling translation job") {
		t.Fatalf("resume treated a foreign commit as personal work:\n%s", resume)
	}
}

func TestTodayAndResumeIncludeCommitsWhenLocalIdentityIsUnknown(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Date(2026, 10, 21, 15, 0, 0, 0, time.Local)
	insertEvent(t, initialized.DatabasePath, storedEvent{
		ID:        "git:unknown-identity",
		Source:    "git",
		Type:      "git.commit",
		Timestamp: now.Add(-10 * time.Minute),
		Project:   "workgraph",
		Summary:   "Commit with unverifiable author",
		Payload:   `{"repo_path":"/tmp/Code/workgraph","commit":"3333333333333333","branch":"main","subject":"Commit with unverifiable author","author_email":"someone@example.test"}`,
	})

	today, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now})
	if err != nil {
		t.Fatalf("today: %v", err)
	}
	if !strings.Contains(today.Message, "Commit with unverifiable author") {
		t.Fatalf("today hid a commit although no local identity is known:\n%s", today.Message)
	}

	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(resume.LastWork) != 1 || strings.Contains(resume.Message, "No personal work session found") {
		t.Fatalf("resume claimed no personal work although identity is unknown:\n%s", resume.Message)
	}
	if !strings.Contains(resume.Message, "Git identity unknown") {
		t.Fatalf("resume should say the git identity is unknown:\n%s", resume.Message)
	}

	known, err := workgraph.Today(workgraph.TodayConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now, GitEmails: []string{"me@example.test"}})
	if err != nil {
		t.Fatalf("today with identity: %v", err)
	}
	if strings.Contains(known.Message, "Commit with unverifiable author") {
		t.Fatalf("today showed a foreign commit when identity is known:\n%s", known.Message)
	}
}

func TestResumeProjectListShowsTenRecentPersonalProjectsUnlessAllIsRequested(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	now := time.Date(2026, 10, 21, 15, 0, 0, 0, time.Local)
	for i := 1; i <= 12; i++ {
		project := fmt.Sprintf("recent-%02d", i)
		insertEvent(t, initialized.DatabasePath, storedEvent{
			ID:        "file:" + project,
			Type:      "file.modified",
			Timestamp: now.Add(-time.Duration(i) * time.Hour),
			Project:   project,
			Payload:   fmt.Sprintf(`{"path":"/tmp/%s/main.go"}`, project),
		})
	}
	insertEvent(t, initialized.DatabasePath, storedEvent{
		ID:        "file:dormant",
		Type:      "file.modified",
		Timestamp: now.AddDate(0, 0, -20),
		Project:   "dormant",
		Payload:   `{"path":"/tmp/dormant/main.go"}`,
	})

	resume, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now, GitEmails: []string{"me@example.test"}})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	list := resume.Message[strings.Index(resume.Message, "Resumable projects"):]
	for i := 1; i <= 10; i++ {
		if !strings.Contains(list, fmt.Sprintf("- recent-%02d:", i)) {
			t.Errorf("default list missing recent-%02d:\n%s", i, list)
		}
	}
	for _, hidden := range []string{"- recent-11:", "- recent-12:", "- dormant:"} {
		if strings.Contains(list, hidden) {
			t.Errorf("default list should cap at ten projects active in the last 14 days, showed %q:\n%s", hidden, list)
		}
	}
	if strings.Index(list, "- recent-01:") > strings.Index(list, "- recent-10:") {
		t.Errorf("expected most recent project first:\n%s", list)
	}
	if !strings.Contains(list, "3 more: workgraph resume --all") {
		t.Errorf("expected pointer to the full list:\n%s", list)
	}
	if len(resume.Projects) != 10 {
		t.Errorf("expected ten listed projects, got %d", len(resume.Projects))
	}

	all, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: homeDir, DatabasePath: initialized.DatabasePath, Now: now, AllProjects: true})
	if err != nil {
		t.Fatalf("resume --all: %v", err)
	}
	for _, expected := range []string{"- recent-11:", "- recent-12:", "- dormant:"} {
		if !strings.Contains(all.Message, expected) {
			t.Errorf("resume --all should show the full list, missing %q:\n%s", expected, all.Message)
		}
	}
	if strings.Contains(all.Message, "more: workgraph resume --all") {
		t.Errorf("resume --all should not point to itself:\n%s", all.Message)
	}
}
