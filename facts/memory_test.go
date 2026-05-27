package facts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestMemoryInitCreatesStarterProjectMemoryAtSlugPath(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	result, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
		Project:   "Cupcake API",
	})
	if err != nil {
		t.Fatalf("memory init failed: %v", err)
	}

	expectedPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	if result.Path != expectedPath || !result.Created {
		t.Fatalf("expected created memory path %q, got %#v", expectedPath, result)
	}
	contents, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read project memory: %v", err)
	}
	for _, expected := range []string{"# Cupcake API", "## Context", "## Current priorities", "## Decisions", "## Constraints", "## Open questions"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("expected starter memory to include %q, got:\n%s", expected, contents)
		}
	}
}

func TestMemoryInitCreatesStarterPersonalMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	result, err := workgraph.InitPersonalMemory(workgraph.PersonalMemoryInitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("personal memory init failed: %v", err)
	}

	expectedPath := filepath.Join(memoryDir, "personal.md")
	if result.Path != expectedPath || !result.Created {
		t.Fatalf("expected created personal memory path %q, got %#v", expectedPath, result)
	}
	contents, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read personal memory: %v", err)
	}
	for _, expected := range []string{"# Personal memory", "## Priorities", "## Principles", "## Preferences", "## Working style", "## Constraints"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("expected starter personal memory to include %q, got:\n%s", expected, contents)
		}
	}
}

func TestMemoryInitCreatesStarterOrganizationMemoryAtSlugPath(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	result, err := workgraph.InitOrganizationMemory(workgraph.OrganizationMemoryInitConfig{
		HomeDir:      homeDir,
		MemoryDir:    memoryDir,
		Organization: "Cupcake Labs",
	})
	if err != nil {
		t.Fatalf("organization memory init failed: %v", err)
	}

	expectedPath := filepath.Join(memoryDir, "organizations", "cupcake-labs.md")
	if result.Path != expectedPath || !result.Created {
		t.Fatalf("expected created organization memory path %q, got %#v", expectedPath, result)
	}
	contents, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read organization memory: %v", err)
	}
	for _, expected := range []string{"# Cupcake Labs", "## Strategy", "## Planning notes", "## Operating principles", "## Current priorities", "## Constraints", "## Open questions"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("expected starter organization memory to include %q, got:\n%s", expected, contents)
		}
	}
}

func TestMemoryInitCreatesStarterTeamMemoryAtSlugPath(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	result, err := workgraph.InitTeamMemory(workgraph.TeamMemoryInitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
		Team:      "Platform Team",
	})
	if err != nil {
		t.Fatalf("team memory init failed: %v", err)
	}

	expectedPath := filepath.Join(memoryDir, "teams", "platform-team.md")
	if result.Path != expectedPath || !result.Created {
		t.Fatalf("expected created team memory path %q, got %#v", expectedPath, result)
	}
	contents, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read team memory: %v", err)
	}
	for _, expected := range []string{"# Platform Team", "## Strategy", "## Rituals", "## Ownership", "## Current goals", "## Constraints", "## Open questions"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("expected starter team memory to include %q, got:\n%s", expected, contents)
		}
	}
}

func TestMemoryInitPreservesExistingProjectMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	path := filepath.Join(memoryDir, "projects", "workgraph.md")
	if err := os.WriteFile(path, []byte("existing project memory"), 0o644); err != nil {
		t.Fatalf("write existing memory: %v", err)
	}
	result, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
		Project:   "WorkGraph",
	})
	if err != nil {
		t.Fatalf("memory init failed: %v", err)
	}
	if result.Created {
		t.Fatalf("expected existing project memory not to be recreated")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing memory: %v", err)
	}
	if string(contents) != "existing project memory" {
		t.Fatalf("expected existing memory preserved, got %q", contents)
	}
	if !strings.Contains(result.Message, path) {
		t.Fatalf("expected result message to report %q, got %q", path, result.Message)
	}
}

func TestMemoryInitPreservesExistingPersonalMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	path := filepath.Join(memoryDir, "personal.md")
	if err := os.WriteFile(path, []byte("existing personal memory"), 0o644); err != nil {
		t.Fatalf("write existing personal memory: %v", err)
	}
	result, err := workgraph.InitPersonalMemory(workgraph.PersonalMemoryInitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
	})
	if err != nil {
		t.Fatalf("personal memory init failed: %v", err)
	}
	if result.Created {
		t.Fatalf("expected existing personal memory not to be recreated")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing personal memory: %v", err)
	}
	if string(contents) != "existing personal memory" {
		t.Fatalf("expected existing personal memory preserved, got %q", contents)
	}
	if !strings.Contains(result.Message, path) {
		t.Fatalf("expected result message to report %q, got %q", path, result.Message)
	}
}

func TestMemoryInitPreservesExistingOrganizationMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	organizationsDir := filepath.Join(memoryDir, "organizations")
	if err := os.MkdirAll(organizationsDir, 0o755); err != nil {
		t.Fatalf("create organizations memory dir: %v", err)
	}
	path := filepath.Join(organizationsDir, "cupcake-labs.md")
	if err := os.WriteFile(path, []byte("existing organization memory"), 0o644); err != nil {
		t.Fatalf("write existing organization memory: %v", err)
	}
	result, err := workgraph.InitOrganizationMemory(workgraph.OrganizationMemoryInitConfig{
		HomeDir:      homeDir,
		MemoryDir:    memoryDir,
		Organization: "Cupcake Labs",
	})
	if err != nil {
		t.Fatalf("organization memory init failed: %v", err)
	}
	if result.Created {
		t.Fatalf("expected existing organization memory not to be recreated")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing organization memory: %v", err)
	}
	if string(contents) != "existing organization memory" {
		t.Fatalf("expected existing organization memory preserved, got %q", contents)
	}
	if !strings.Contains(result.Message, path) {
		t.Fatalf("expected result message to report %q, got %q", path, result.Message)
	}
}

func TestMemoryInitPreservesExistingTeamMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir}); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	teamsDir := filepath.Join(memoryDir, "teams")
	if err := os.MkdirAll(teamsDir, 0o755); err != nil {
		t.Fatalf("create teams memory dir: %v", err)
	}
	path := filepath.Join(teamsDir, "platform-team.md")
	if err := os.WriteFile(path, []byte("existing team memory"), 0o644); err != nil {
		t.Fatalf("write existing team memory: %v", err)
	}
	result, err := workgraph.InitTeamMemory(workgraph.TeamMemoryInitConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
		Team:      "Platform Team",
	})
	if err != nil {
		t.Fatalf("team memory init failed: %v", err)
	}
	if result.Created {
		t.Fatalf("expected existing team memory not to be recreated")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing team memory: %v", err)
	}
	if string(contents) != "existing team memory" {
		t.Fatalf("expected existing team memory preserved, got %q", contents)
	}
	if !strings.Contains(result.Message, path) {
		t.Fatalf("expected result message to report %q, got %q", path, result.Message)
	}
}

func TestMemoryInitRequiresWorkGraphInit(t *testing.T) {
	_, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{
		HomeDir:   filepath.Join(t.TempDir(), ".workgraph"),
		MemoryDir: filepath.Join(t.TempDir(), "workgraph-memory"),
		Project:   "workgraph",
	})
	if !errors.Is(err, workgraph.ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}
}

func TestMemorySuggestProjectEmitsDraftsWithEvidenceWithoutWritingMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}

	projectMemoryPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	insertEvent(t, initResult.DatabasePath, storedEvent{
		ID:        "event-auth-decision",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 27, 9, 30, 0, 0, time.UTC),
		Project:   "Cupcake API",
		Payload:   `{"path":"/tmp/cupcake/auth.go","operation":"modified"}`,
		Summary:   "Updated auth flow after deciding to require passkeys.",
	})

	result, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		MemoryDir:    memoryDir,
		Scope:        "project",
		Project:      "Cupcake API",
	})
	if err != nil {
		t.Fatalf("memory suggest failed: %v", err)
	}

	if result.MemoryPath != projectMemoryPath {
		t.Fatalf("expected memory path %q, got %q", projectMemoryPath, result.MemoryPath)
	}
	if len(result.Suggestions) != 1 {
		t.Fatalf("expected one draft suggestion, got %#v", result.Suggestions)
	}
	if result.Suggestions[0].EvidenceID != "event-auth-decision" {
		t.Fatalf("expected suggestion evidence id, got %#v", result.Suggestions[0])
	}
	for _, expected := range []string{"Draft memory update suggestions", "Status: draft suggestions only", "event-auth-decision", "Updated auth flow", "No memory files were changed"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("expected suggestion output to include %q, got:\n%s", expected, result.Message)
		}
	}
	if _, err := os.Stat(projectMemoryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected project memory not to be created, stat err: %v", err)
	}
}

func TestMemoryPromoteProjectAppendsCuratedTextWithEvidence(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	projectMemoryPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	if err := os.WriteFile(projectMemoryPath, []byte("# Cupcake API\n\n## Context\nExisting context.\n"), 0o644); err != nil {
		t.Fatalf("write existing project memory: %v", err)
	}
	insertEvent(t, initResult.DatabasePath, storedEvent{
		ID:        "event-promote-decision",
		Type:      "github.issue",
		Timestamp: time.Date(2026, 5, 27, 11, 0, 0, 0, time.UTC),
		Project:   "Cupcake API",
		Payload:   `{"title":"Use passkeys for login","state":"closed"}`,
		Summary:   "Closed issue after deciding passkeys are required for login.",
	})

	result, err := workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		MemoryDir:    memoryDir,
		Scope:        "project",
		Project:      "Cupcake API",
		EvidenceID:   "event-promote-decision",
		Text:         "Decision: Cupcake API requires passkeys for login.",
	})
	if err != nil {
		t.Fatalf("memory promote failed: %v", err)
	}

	if result.MemoryPath != projectMemoryPath {
		t.Fatalf("expected memory path %q, got %q", projectMemoryPath, result.MemoryPath)
	}
	contents, err := os.ReadFile(projectMemoryPath)
	if err != nil {
		t.Fatalf("read promoted memory: %v", err)
	}
	for _, expected := range []string{
		"Existing context.",
		"## Promoted evidence",
		"Decision: Cupcake API requires passkeys for login.",
		"Evidence: event-promote-decision github.issue Closed issue after deciding passkeys are required for login.",
	} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("expected promoted memory to include %q, got:\n%s", expected, contents)
		}
	}
	for _, expected := range []string{"Promoted project memory", projectMemoryPath, "event-promote-decision"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("expected promote output to include %q, got:\n%s", expected, result.Message)
		}
	}

	links, err := workgraph.ListMemoryLinks(workgraph.MemoryLinksConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		MemoryDir:    memoryDir,
		Scope:        "project",
		Project:      "Cupcake API",
	})
	if err != nil {
		t.Fatalf("list memory links failed: %v", err)
	}
	if len(links.Links) != 1 {
		t.Fatalf("expected one memory link, got %#v", links.Links)
	}
	link := links.Links[0]
	if link.MemoryDocPath != projectMemoryPath || link.EventID != "event-promote-decision" || link.Relation != "supported_by" {
		t.Fatalf("expected project memory supported_by link, got %#v", link)
	}
}

func TestMemoryPromoteProjectCreatesMissingProjectMemoryWithEvidence(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	insertEvent(t, initResult.DatabasePath, storedEvent{
		ID:        "event-promote-new-memory",
		Type:      "file.modified",
		Timestamp: time.Date(2026, 5, 27, 11, 30, 0, 0, time.UTC),
		Project:   "Cupcake API",
		Payload:   `{"path":"/tmp/cupcake/routing.go","operation":"modified"}`,
		Summary:   "Added routing constraints for beta customers.",
	})

	_, err = workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		MemoryDir:    memoryDir,
		Scope:        "project",
		Project:      "Cupcake API",
		EvidenceID:   "event-promote-new-memory",
		Text:         "Constraint: beta customer routing must stay isolated.",
	})
	if err != nil {
		t.Fatalf("memory promote failed: %v", err)
	}

	projectMemoryPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	contents, err := os.ReadFile(projectMemoryPath)
	if err != nil {
		t.Fatalf("read created project memory: %v", err)
	}
	for _, expected := range []string{"# Cupcake API", "## Context", "Constraint: beta customer routing must stay isolated.", "Evidence: event-promote-new-memory"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("expected created promoted memory to include %q, got:\n%s", expected, contents)
		}
	}
}

func TestMemoryPromoteRejectsEvidenceFromAnotherProject(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	insertEvent(t, initResult.DatabasePath, storedEvent{
		ID:        "event-other-project",
		Type:      "github.issue",
		Timestamp: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
		Project:   "Other Project",
		Payload:   `{"title":"Wrong project","state":"open"}`,
		Summary:   "Belongs elsewhere.",
	})

	_, err = workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		MemoryDir:    memoryDir,
		Scope:        "project",
		Project:      "Cupcake API",
		EvidenceID:   "event-other-project",
		Text:         "This should not be promoted.",
	})
	if err == nil {
		t.Fatalf("expected promote to reject evidence from another project")
	}
	projectMemoryPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	if _, err := os.Stat(projectMemoryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected rejected promote not to create memory, stat err: %v", err)
	}
}

func TestMemoryInitCommandReportsStarterProjectMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	repoRoot := repoRoot(t)

	binary := filepath.Join(tempDir, "workgraph")
	build := exec.Command("go", "build", "-o", binary, "./cmd/workgraph")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph failed: %v\n%s", err, output)
	}
	init := exec.Command(binary, "init", "--home", homeDir, "--memory", memoryDir)
	if output, err := init.CombinedOutput(); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	cmd := exec.Command(binary, "memory", "init", "--home", homeDir, "--memory", memoryDir, "Cupcake API")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph memory init failed: %v\n%s", err, output)
	}

	expectedPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	for _, expected := range []string{"Project memory initialized", expectedPath, "Starter template"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected command output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestMemoryInitCommandReportsStarterPersonalMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	repoRoot := repoRoot(t)

	binary := filepath.Join(tempDir, "workgraph")
	build := exec.Command("go", "build", "-o", binary, "./cmd/workgraph")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph failed: %v\n%s", err, output)
	}
	init := exec.Command(binary, "init", "--home", homeDir, "--memory", memoryDir)
	if output, err := init.CombinedOutput(); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	cmd := exec.Command(binary, "memory", "init", "--home", homeDir, "--memory", memoryDir, "--scope", "personal")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph memory init --scope personal failed: %v\n%s", err, output)
	}

	expectedPath := filepath.Join(memoryDir, "personal.md")
	for _, expected := range []string{"Personal memory initialized", expectedPath, "Starter template"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected command output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestMemoryInitCommandReportsStarterOrganizationMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	repoRoot := repoRoot(t)

	binary := filepath.Join(tempDir, "workgraph")
	build := exec.Command("go", "build", "-o", binary, "./cmd/workgraph")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph failed: %v\n%s", err, output)
	}
	init := exec.Command(binary, "init", "--home", homeDir, "--memory", memoryDir)
	if output, err := init.CombinedOutput(); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	cmd := exec.Command(binary, "memory", "init", "--home", homeDir, "--memory", memoryDir, "--scope", "organization", "Cupcake Labs")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph memory init --scope organization failed: %v\n%s", err, output)
	}

	expectedPath := filepath.Join(memoryDir, "organizations", "cupcake-labs.md")
	for _, expected := range []string{"Organization memory initialized", expectedPath, "Starter template"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected command output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestMemoryInitCommandReportsStarterTeamMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	repoRoot := repoRoot(t)

	binary := filepath.Join(tempDir, "workgraph")
	build := exec.Command("go", "build", "-o", binary, "./cmd/workgraph")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph failed: %v\n%s", err, output)
	}
	init := exec.Command(binary, "init", "--home", homeDir, "--memory", memoryDir)
	if output, err := init.CombinedOutput(); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	cmd := exec.Command(binary, "memory", "init", "--home", homeDir, "--memory", memoryDir, "--scope", "team", "Platform Team")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph memory init --scope team failed: %v\n%s", err, output)
	}

	expectedPath := filepath.Join(memoryDir, "teams", "platform-team.md")
	for _, expected := range []string{"Team memory initialized", expectedPath, "Starter template"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected command output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestMemorySuggestCommandReportsDraftProjectSuggestions(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	repoRoot := repoRoot(t)

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	insertEvent(t, initResult.DatabasePath, storedEvent{
		ID:        "event-cli-suggest",
		Type:      "github.issue",
		Timestamp: time.Date(2026, 5, 27, 10, 15, 0, 0, time.UTC),
		Project:   "Cupcake API",
		Payload:   `{"title":"Document auth constraints","state":"open"}`,
		Summary:   "Opened issue to document auth constraints.",
	})

	binary := filepath.Join(tempDir, "workgraph")
	build := exec.Command("go", "build", "-o", binary, "./cmd/workgraph")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph failed: %v\n%s", err, output)
	}

	cmd := exec.Command(binary, "memory", "suggest", "--home", homeDir, "--memory", memoryDir, "--database", initResult.DatabasePath, "--scope", "project", "Cupcake API")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph memory suggest failed: %v\n%s", err, output)
	}

	expectedPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	for _, expected := range []string{"Draft memory update suggestions", "Status: draft suggestions only", expectedPath, "event-cli-suggest", "No memory files were changed"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected command output to include %q, got:\n%s", expected, output)
		}
	}
	if _, err := os.Stat(expectedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected project memory not to be created, stat err: %v", err)
	}
}

func TestMemoryPromoteCommandAppendsProjectMemory(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	repoRoot := repoRoot(t)

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	insertEvent(t, initResult.DatabasePath, storedEvent{
		ID:        "event-cli-promote",
		Type:      "github.pull_request",
		Timestamp: time.Date(2026, 5, 27, 12, 30, 0, 0, time.UTC),
		Project:   "Cupcake API",
		Payload:   `{"title":"Add auth migration","state":"merged"}`,
		Summary:   "Merged auth migration.",
	})

	binary := filepath.Join(tempDir, "workgraph")
	build := exec.Command("go", "build", "-o", binary, "./cmd/workgraph")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph failed: %v\n%s", err, output)
	}

	cmd := exec.Command(binary,
		"memory", "promote",
		"--home", homeDir,
		"--memory", memoryDir,
		"--database", initResult.DatabasePath,
		"--scope", "project",
		"--evidence", "event-cli-promote",
		"--text", "Decision: auth migration has landed.",
		"Cupcake API",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph memory promote failed: %v\n%s", err, output)
	}

	expectedPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	for _, expected := range []string{"Promoted project memory", expectedPath, "event-cli-promote"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected command output to include %q, got:\n%s", expected, output)
		}
	}
	contents, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read promoted memory: %v", err)
	}
	for _, expected := range []string{"Decision: auth migration has landed.", "Evidence: event-cli-promote github.pull_request Merged auth migration."} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("expected promoted memory to include %q, got:\n%s", expected, contents)
		}
	}
}

func TestMemoryLinksCommandReportsProjectEvidenceLinks(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	memoryDir := filepath.Join(tempDir, "workgraph-memory")
	repoRoot := repoRoot(t)

	initResult, err := workgraph.Init(workgraph.InitConfig{HomeDir: homeDir, MemoryDir: memoryDir})
	if err != nil {
		t.Fatalf("init failed: %v", err)
	}
	insertEvent(t, initResult.DatabasePath, storedEvent{
		ID:        "event-cli-link",
		Type:      "github.issue",
		Timestamp: time.Date(2026, 5, 27, 13, 0, 0, 0, time.UTC),
		Project:   "Cupcake API",
		Payload:   `{"title":"Link memory evidence","state":"closed"}`,
		Summary:   "Closed issue after linking memory evidence.",
	})
	_, err = workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{
		HomeDir:      homeDir,
		DatabasePath: initResult.DatabasePath,
		MemoryDir:    memoryDir,
		Scope:        "project",
		Project:      "Cupcake API",
		EvidenceID:   "event-cli-link",
		Text:         "Decision: memory promotions keep evidence links.",
	})
	if err != nil {
		t.Fatalf("memory promote failed: %v", err)
	}

	binary := filepath.Join(tempDir, "workgraph")
	build := exec.Command("go", "build", "-o", binary, "./cmd/workgraph")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build workgraph failed: %v\n%s", err, output)
	}

	cmd := exec.Command(binary,
		"memory", "links",
		"--home", homeDir,
		"--memory", memoryDir,
		"--database", initResult.DatabasePath,
		"--scope", "project",
		"Cupcake API",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("workgraph memory links failed: %v\n%s", err, output)
	}

	expectedPath := filepath.Join(memoryDir, "projects", "cupcake-api.md")
	for _, expected := range []string{"Project memory links", expectedPath, "supported_by", "event-cli-link"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("expected links output to include %q, got:\n%s", expected, output)
		}
	}
}
