package facts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestAgentPluginInstallPackagesEveryCanonicalSkillForEveryClient(t *testing.T) {
	for _, client := range []string{"codex", "claude-code", "copilot", "agy"} {
		t.Run(client, func(t *testing.T) {
			homeDir := initBridgedCaptureHome(t)
			fixtureDir := t.TempDir()
			logPath := filepath.Join(fixtureDir, "client.log")
			clientPath := filepath.Join(fixtureDir, "client")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\n"
			if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
				t.Fatalf("write fake client: %v", err)
			}
			installRoot := filepath.Join(fixtureDir, "installed")
			unrelatedPath := filepath.Join(installRoot, "keep-me.txt")
			if err := os.MkdirAll(installRoot, 0o700); err != nil {
				t.Fatalf("create install root: %v", err)
			}
			if err := os.WriteFile(unrelatedPath, []byte("preserve\n"), 0o600); err != nil {
				t.Fatalf("write unrelated file: %v", err)
			}

			args := []string{"plugin", "install", "--home", homeDir, "--client", client,
				"--client-command", clientPath, "--install-root", installRoot, "--no-launchd"}
			for attempt := 0; attempt < 2; attempt++ {
				output, err := runworkgraph(t, repoRoot(t), args...)
				if err != nil {
					t.Fatalf("install %s attempt %d: %v\n%s", client, attempt+1, err, output)
				}
				for _, expected := range []string{"workgraph plugin installed", "Skills: 3", "MCP: workgraph", "Daemon: after an executable upgrade, run workgraph stop && workgraph start.", "Next: start a new"} {
					if !strings.Contains(string(output), expected) {
						t.Fatalf("install %s omitted %q:\n%s", client, expected, output)
					}
				}
			}

			pluginRoot := filepath.Join(installRoot, "plugins", "workgraph")
			manifestPath := filepath.Join(pluginRoot, ".codex-plugin", "plugin.json")
			if client == "claude-code" {
				manifestPath = filepath.Join(pluginRoot, ".claude-plugin", "plugin.json")
			}
			if client == "copilot" || client == "agy" {
				manifestPath = filepath.Join(pluginRoot, "plugin.json")
			}
			manifestBytes, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatalf("read installed %s manifest: %v", client, err)
			}
			var manifest struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}
			if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
				t.Fatalf("parse installed %s manifest: %v", client, err)
			}
			if manifest.Name != "workgraph" {
				t.Fatalf("installed %s plugin name = %q, want workgraph", client, manifest.Name)
			}
			versionMarker := "+codex."
			if client == "claude-code" {
				versionMarker = "+claude."
			}
			if client == "copilot" {
				versionMarker = "+copilot."
			}
			if client == "agy" {
				versionMarker = "+agy."
			}
			if !strings.HasPrefix(manifest.Version, "0.3.0"+versionMarker) {
				t.Fatalf("installed %s plugin version %q lacks 0.3.0 update cachebuster %q", client, manifest.Version, versionMarker)
			}

			canonicalFiles := []string{
				filepath.Join("workgraph-bridge", "SKILL.md"),
				filepath.Join("workgraph-bridge", "references", "event-contracts.md"),
				filepath.Join("workgraph-memory", "SKILL.md"),
				filepath.Join("workgraph-memory", "agents", "openai.yaml"),
				filepath.Join("workgraph-ai-checkpoint", "SKILL.md"),
				filepath.Join("workgraph-ai-checkpoint", "agents", "openai.yaml"),
			}
			for _, relative := range canonicalFiles {
				canonical, err := os.ReadFile(filepath.Join(repoRoot(t), ".agents", "skills", relative))
				if err != nil {
					t.Fatalf("read canonical %s: %v", relative, err)
				}
				installed, err := os.ReadFile(filepath.Join(pluginRoot, "skills", relative))
				if err != nil {
					t.Fatalf("read installed %s: %v", relative, err)
				}
				if string(installed) != string(canonical) {
					t.Fatalf("installed %s differs from canonical skill", relative)
				}
			}

			mcpFile := ".mcp.json"
			if client == "agy" {
				mcpFile = "mcp_config.json"
			}
			mcp, err := os.ReadFile(filepath.Join(pluginRoot, mcpFile))
			if err != nil {
				t.Fatalf("read installed MCP config: %v", err)
			}
			for _, expected := range []string{"workgraph", "bridge", "mcp", homeDir} {
				if !strings.Contains(string(mcp), expected) {
					t.Fatalf("installed MCP config omitted %q:\n%s", expected, mcp)
				}
			}
			if contents, err := os.ReadFile(unrelatedPath); err != nil || string(contents) != "preserve\n" {
				t.Fatalf("reinstall changed unrelated file: contents=%q error=%v", contents, err)
			}

			registration, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read %s registration log: %v", client, err)
			}
			if client == "agy" {
				for _, expected := range []string{"plugin validate " + pluginRoot + " --strict", "plugin uninstall workgraph", "plugin install " + pluginRoot} {
					if !strings.Contains(string(registration), expected) {
						t.Fatalf("Antigravity registration omitted %q:\n%s", expected, registration)
					}
				}
				if _, err := os.Stat(filepath.Join(pluginRoot, ".mcp.json")); !os.IsNotExist(err) {
					t.Fatalf("Antigravity package shipped a non-native .mcp.json: %v", err)
				}
				var mcpDocument struct {
					Servers map[string]struct {
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"mcpServers"`
				}
				if err := json.Unmarshal(mcp, &mcpDocument); err != nil {
					t.Fatalf("parse installed Antigravity MCP config: %v", err)
				}
				server, found := mcpDocument.Servers["workgraph"]
				if !found || len(mcpDocument.Servers) != 1 || server.Command == "" || strings.Join(server.Args, " ") != "bridge mcp --home "+homeDir {
					t.Fatalf("installed Antigravity MCP config is not a stdio workgraph server:\n%s", mcp)
				}
				return
			}
			if !strings.Contains(string(registration), "workgraph@workgraph") {
				t.Fatalf("%s did not install workgraph plugin:\n%s", client, registration)
			}
			if strings.Contains(string(registration), "workgraph-bridge@workgraph") {
				t.Fatalf("%s installed legacy bridge-only plugin:\n%s", client, registration)
			}
			if client == "claude-code" && !strings.Contains(string(registration), "plugin update --scope user workgraph@workgraph") {
				t.Fatalf("Claude Code reinstall omitted plugin update:\n%s", registration)
			}
			if client == "copilot" {
				for _, expected := range []string{"plugin marketplace add " + installRoot, "plugin install workgraph@workgraph"} {
					if !strings.Contains(string(registration), expected) {
						t.Fatalf("Copilot registration omitted %q:\n%s", expected, registration)
					}
				}
				var marketplace struct {
					Name    string `json:"name"`
					Plugins []struct {
						Name    string `json:"name"`
						Source  string `json:"source"`
						Version string `json:"version"`
					} `json:"plugins"`
				}
				marketplaceBytes, err := os.ReadFile(filepath.Join(installRoot, ".github", "plugin", "marketplace.json"))
				if err != nil {
					t.Fatalf("read installed Copilot marketplace: %v", err)
				}
				if err := json.Unmarshal(marketplaceBytes, &marketplace); err != nil {
					t.Fatalf("parse installed Copilot marketplace: %v", err)
				}
				if marketplace.Name != "workgraph" || len(marketplace.Plugins) != 1 || marketplace.Plugins[0].Name != "workgraph" ||
					marketplace.Plugins[0].Source != "./plugins/workgraph" || marketplace.Plugins[0].Version != manifest.Version {
					t.Fatalf("installed Copilot marketplace does not point at the stamped plugin:\n%s", marketplaceBytes)
				}
				var mcpDocument struct {
					Servers map[string]struct {
						Type    string   `json:"type"`
						Command string   `json:"command"`
						Args    []string `json:"args"`
					} `json:"mcpServers"`
				}
				if err := json.Unmarshal(mcp, &mcpDocument); err != nil {
					t.Fatalf("parse installed Copilot MCP config: %v", err)
				}
				server, found := mcpDocument.Servers["workgraph"]
				if !found || server.Type != "local" || server.Command == "" || strings.Join(server.Args, " ") != "bridge mcp --home "+homeDir {
					t.Fatalf("installed Copilot MCP config is not a local workgraph server:\n%s", mcp)
				}
			}
		})
	}
}

func TestAgentPluginDoctorChecksEverySkillAndBridgeAliasUsesSamePackage(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "codex")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
	installArgs := []string{"bridge", "install", "--home", homeDir, "--client", "codex",
		"--client-command", clientPath, "--install-root", installRoot, "--no-launchd"}
	if output, err := runworkgraph(t, repoRoot(t), installArgs...); err != nil {
		t.Fatalf("install through compatibility alias: %v\n%s", err, output)
	}
	manifest := filepath.Join(installRoot, "plugins", "workgraph", ".codex-plugin", "plugin.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("bridge alias did not install workgraph plugin: %v", err)
	}

	doctorArgs := []string{"plugin", "doctor", "--home", homeDir, "--client", "codex",
		"--client-command", clientPath, "--install-root", installRoot}
	output, err := runworkgraph(t, repoRoot(t), doctorArgs...)
	if err != nil {
		t.Fatalf("doctor workgraph plugin: %v\n%s", err, output)
	}
	for _, expected := range []string{"workgraph plugin doctor", "Package: ready", "Skills: 3/3 ready", "MCP: ready", "Round trip: ready"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("plugin doctor omitted %q:\n%s", expected, output)
		}
	}

	missingSkill := filepath.Join(installRoot, "plugins", "workgraph", "skills", "workgraph-memory", "SKILL.md")
	if err := os.Remove(missingSkill); err != nil {
		t.Fatalf("remove installed skill fixture: %v", err)
	}
	if output, err := runworkgraph(t, repoRoot(t), doctorArgs...); err == nil || !strings.Contains(string(output), "workgraph-memory") {
		t.Fatalf("doctor accepted missing memory skill: %v\n%s", err, output)
	}
}

func TestAgentPluginPackagesAndSetupAreDocumented(t *testing.T) {
	for _, relativePath := range []string{"README.md", filepath.Join("docs", "commands.md"), filepath.Join("docs", "connectors.md")} {
		contents, err := os.ReadFile(filepath.Join(repoRoot(t), relativePath))
		if err != nil {
			t.Fatalf("read %s: %v", relativePath, err)
		}
		for _, expected := range []string{
			"workgraph plugin install --client codex",
			"workgraph plugin install --client claude-code",
			"workgraph plugin install --client copilot",
			"workgraph plugin install --client agy",
			"workgraph plugin doctor",
			"workgraph-memory",
			"workgraph-ai-checkpoint",
		} {
			if !strings.Contains(string(contents), expected) {
				t.Fatalf("%s is missing %q", relativePath, expected)
			}
		}
	}
}

func TestBridgeMCPExposesReadOnlyWorkIntelligenceTools(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	memoryDir := filepath.Join(t.TempDir(), "workgraph-memory")
	projectsDir := filepath.Join(memoryDir, "projects")
	if err := os.MkdirAll(projectsDir, 0o700); err != nil {
		t.Fatalf("create project memory dir: %v", err)
	}
	memoryDoc := "---\nworkstream: core-platform\ndescription: Core workgraph substrate\n---\n# Workgraph Core\n\nShip read-only MCP intelligence tools.\n"
	if err := os.WriteFile(filepath.Join(projectsDir, "workgraph-core.md"), []byte(memoryDoc), 0o600); err != nil {
		t.Fatalf("write project memory: %v", err)
	}

	outsideSecret := filepath.Join(t.TempDir(), "outside-secret.md")
	if err := os.WriteFile(outsideSecret, []byte("# Secret Outside Root\n\nTOP-SECRET-OUTSIDE-MEMORY\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outsideSecret, filepath.Join(projectsDir, "escaped-link.md")); err != nil {
		t.Fatalf("create symlink outside memory root: %v", err)
	}

	db := openBridgedCaptureDatabase(t, homeDir)
	now := time.Now().UTC().Truncate(time.Second)
	stamp := now.Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, created_at, project, actor, summary, payload_json, involvement_json)
		VALUES ('git.commit:mcp-1', 'git', 'git.commit', ?, ?, 'workgraph-core', 'alice@example.com', 'Add intelligence tools', '{"commit":"abc1234","branch":"main","subject":"Add intelligence tools","author_email":"alice@example.com","raw_secret":"RAW-CAPTURED-PAYLOAD-TEXT"}', '["authored"]')`,
		stamp, stamp); err != nil {
		t.Fatalf("insert test event: %v", err)
	}

	expiredUntil := now.Add(-time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO suggestions (id, type, pattern_key, title, reason, confidence, lane, status, evidence_json, created_at, updated_at, resolved_at)
		VALUES ('sug-snoozed-1', 'ignore_name', '.cache', 'Ignore .cache', 'Repeated generated noise', 'high', 'baseline', 'snoozed', '{"event_ids":["git.commit:mcp-1"]}', ?, ?, ?)`,
		expiredUntil, expiredUntil, expiredUntil); err != nil {
		t.Fatalf("insert snoozed suggestion: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO suggestion_suppressions (id, type, pattern_key, reason, until_at, created_at)
		VALUES ('sup-snoozed-1', 'ignore_name', '.cache', 'later', ?, ?)`,
		expiredUntil, expiredUntil); err != nil {
		t.Fatalf("insert snooze suppression: %v", err)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"today_overview","arguments":{"actor":"alice@example.com","involvement":"authored"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"resume_project","arguments":{"project":"workgraph-core"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"resume_project","arguments":{"all":true}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"projects_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"resume_project","arguments":{"project":"workgraph-core","memory_dir":` + mustJSON(t, memoryDir) + `}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"projects_list","arguments":{"memory_dir":` + mustJSON(t, memoryDir) + `}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"resume_project","arguments":{"project":"escaped-link"}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"today_overview","arguments":{"actor":"alice@example.com","involvement":"authored","include_untrusted":true}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"resume_project","arguments":{"project":"workgraph-core","include_untrusted":true}}}`,
	}
	var output bytes.Buffer
	if err := workgraph.ServeBridgeMCP(workgraph.BridgeMCPConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
		Input:     strings.NewReader(strings.Join(requests, "\n") + "\n"),
		Output:    &output,
	}); err != nil {
		t.Fatalf("serve bridge MCP: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != len(requests) {
		t.Fatalf("expected %d MCP responses, got %d:\n%s", len(requests), len(lines), output.String())
	}

	for _, tool := range []string{"next_items", "today_overview", "resume_project", "projects_list"} {
		if !strings.Contains(lines[0], `"name":"`+tool+`"`) {
			t.Fatalf("tools/list omitted %q:\n%s", tool, lines[0])
		}
	}
	if strings.Contains(lines[0], `"memory_dir"`) {
		t.Fatalf("tools/list must not advertise caller-supplied memory_dir:\n%s", lines[0])
	}

	decodeResponse := func(line string) (bool, map[string]any) {
		t.Helper()
		var response struct {
			Result struct {
				IsError           bool           `json:"isError"`
				StructuredContent map[string]any `json:"structuredContent"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decode MCP response: %v\n%s", err, line)
		}
		return response.Result.IsError, response.Result.StructuredContent
	}
	decodeStructured := func(line string) map[string]any {
		t.Helper()
		isErr, content := decodeResponse(line)
		if isErr {
			t.Fatalf("MCP tool call returned error:\n%s", line)
		}
		return content
	}

	todayStructured := decodeStructured(lines[1])
	if todayEvents, ok := todayStructured["events"].([]any); !ok || len(todayEvents) != 1 {
		t.Fatalf("expected 1 today event, got %#v", todayStructured["events"])
	}
	if strings.Contains(lines[1], "RAW-CAPTURED-PAYLOAD-TEXT") {
		t.Fatalf("today_overview leaked raw captured payload by default:\n%s", lines[1])
	}
	if todayProjects, ok := todayStructured["projects"].([]any); !ok || len(todayProjects) != 1 {
		t.Fatalf("expected 1 today project count, got %#v", todayStructured["projects"])
	}
	if todaySessions, ok := todayStructured["sessions"].([]any); !ok || len(todaySessions) != 1 {
		t.Fatalf("expected 1 today session, got %#v", todayStructured["sessions"])
	}
	if !strings.Contains(todayStructured["message"].(string), "Add intelligence tools") {
		t.Fatalf("today_overview message omitted event summary:\n%s", todayStructured["message"])
	}

	var suggestionStatus string
	if err := db.QueryRow(`SELECT status FROM suggestions WHERE id = 'sug-snoozed-1'`).Scan(&suggestionStatus); err != nil {
		t.Fatalf("query snoozed suggestion status: %v", err)
	}
	if suggestionStatus != "snoozed" {
		t.Fatalf("expected today_overview to leave expired snoozed suggestion unchanged, got %q", suggestionStatus)
	}
	var suppressionCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM suggestion_suppressions WHERE id = 'sup-snoozed-1'`).Scan(&suppressionCount); err != nil {
		t.Fatalf("query snooze suppression: %v", err)
	}
	if suppressionCount != 1 {
		t.Fatalf("expected today_overview to leave suggestion_suppressions untouched, got count=%d", suppressionCount)
	}

	resumeStructured := decodeStructured(lines[2])
	if resumeStructured["project"] != "workgraph-core" {
		t.Fatalf("expected project workgraph-core, got %#v", resumeStructured["project"])
	}
	if strings.Contains(lines[2], "RAW-CAPTURED-PAYLOAD-TEXT") {
		t.Fatalf("resume_project leaked raw captured payload by default:\n%s", lines[2])
	}
	if !strings.Contains(resumeStructured["message"].(string), "Resume workgraph-core") {
		t.Fatalf("resume_project message omitted project heading:\n%s", resumeStructured["message"])
	}
	if resumeStructured["memory"] == nil {
		t.Fatalf("expected resume_project to include project memory, got %#v", resumeStructured)
	}

	resumeOverview := decodeStructured(lines[3])
	if resumable, ok := resumeOverview["projects"].([]any); !ok || len(resumable) != 1 {
		t.Fatalf("expected 1 resumable project in overview, got %#v", resumeOverview["projects"])
	}

	var checkInCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM resume_checkins`).Scan(&checkInCount); err != nil {
		t.Fatalf("query resume_checkins rows: %v", err)
	}
	if checkInCount != 0 {
		t.Fatalf("expected resume_project MCP calls not to record check-ins, row count=%d", checkInCount)
	}

	projectsStructured := decodeStructured(lines[4])
	if projects, ok := projectsStructured["projects"].([]any); !ok || len(projects) != 1 {
		t.Fatalf("expected 1 declared project (excluding symlink escape), got %#v", projectsStructured["projects"])
	}
	if strings.Contains(lines[4], "TOP-SECRET-OUTSIDE-MEMORY") || strings.Contains(lines[4], "escaped-link") {
		t.Fatalf("projects_list read symlink pointing outside configured memory root:\n%s", lines[4])
	}
	if workstreams, ok := projectsStructured["workstreams"].([]any); !ok || len(workstreams) != 1 || workstreams[0] != "core-platform" {
		t.Fatalf("expected workstream core-platform, got %#v", projectsStructured["workstreams"])
	}
	if !strings.Contains(projectsStructured["message"].(string), "workgraph-core: Workgraph Core") {
		t.Fatalf("projects_list message omitted declared project:\n%s", projectsStructured["message"])
	}

	if isErr, _ := decodeResponse(lines[5]); !isErr {
		t.Fatalf("expected resume_project to reject caller-supplied memory_dir:\n%s", lines[5])
	}
	if isErr, _ := decodeResponse(lines[6]); !isErr {
		t.Fatalf("expected projects_list to reject caller-supplied memory_dir:\n%s", lines[6])
	}
	if strings.Contains(lines[7], "TOP-SECRET-OUTSIDE-MEMORY") {
		t.Fatalf("resume_project leaked content through symlink outside configured memory root:\n%s", lines[7])
	}

	todayUntrusted := decodeStructured(lines[8])
	if !strings.Contains(lines[8], "RAW-CAPTURED-PAYLOAD-TEXT") || !strings.Contains(lines[8], `"trust":"untrusted"`) {
		t.Fatalf("expected include_untrusted=true on today_overview to include delimited untrusted payload:\n%s", lines[8])
	}
	if todayUntrusted["events"] == nil {
		t.Fatalf("expected events in today_overview with include_untrusted=true")
	}

	resumeUntrusted := decodeStructured(lines[9])
	if !strings.Contains(lines[9], "RAW-CAPTURED-PAYLOAD-TEXT") || !strings.Contains(lines[9], `"trust":"untrusted"`) {
		t.Fatalf("expected include_untrusted=true on resume_project to include delimited untrusted payload:\n%s", lines[9])
	}
	if resumeUntrusted["events"] == nil {
		t.Fatalf("expected events in resume_project with include_untrusted=true")
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(encoded)
}

func TestBridgeMCPRefusesSymlinkedProjectsDirectory(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	memoryDir := filepath.Join(t.TempDir(), "workgraph-memory")
	if err := os.MkdirAll(memoryDir, 0o700); err != nil {
		t.Fatalf("create memory dir: %v", err)
	}
	outsideDir := filepath.Join(t.TempDir(), "outside-projects")
	if err := os.MkdirAll(outsideDir, 0o700); err != nil {
		t.Fatalf("create outside dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "secret-proj.md"), []byte("# Secret Project\n\nOUTSIDE-PROJECTS-SYMLINK-SECRET\n"), 0o600); err != nil {
		t.Fatalf("write outside project file: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(memoryDir, "projects")); err != nil {
		t.Fatalf("symlink projects dir outside memory root: %v", err)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"resume_project","arguments":{"project":"secret-proj"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"projects_list","arguments":{}}}`,
	}
	var output bytes.Buffer
	if err := workgraph.ServeBridgeMCP(workgraph.BridgeMCPConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
		Input:     strings.NewReader(strings.Join(requests, "\n") + "\n"),
		Output:    &output,
	}); err != nil {
		t.Fatalf("serve bridge MCP: %v", err)
	}
	if strings.Contains(output.String(), "OUTSIDE-PROJECTS-SYMLINK-SECRET") || strings.Contains(output.String(), "secret-proj") && !strings.Contains(output.String(), `"isError":true`) {
		t.Fatalf("MCP read through symlinked projects directory outside memory root:\n%s", output.String())
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d:\n%s", len(lines), output.String())
	}
	for index, line := range lines {
		if strings.Contains(line, "OUTSIDE-PROJECTS-SYMLINK-SECRET") {
			t.Fatalf("response %d leaked secret content:\n%s", index+1, line)
		}
		if !strings.Contains(line, `"isError":true`) {
			t.Fatalf("expected response %d to reject symlinked projects dir with isError=true:\n%s", index+1, line)
		}
	}
}

func TestBridgeMCPReadOnlyToolsDoNotMigrateOlderSchema(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	memoryDir := filepath.Join(t.TempDir(), "workgraph-memory")
	if err := os.MkdirAll(filepath.Join(memoryDir, "projects"), 0o700); err != nil {
		t.Fatalf("create memory dir: %v", err)
	}
	db := openBridgedCaptureDatabase(t, homeDir)
	legacyID := "feedfacecafebeef0123456789abcdef"
	now := time.Now().UTC().Truncate(time.Second)
	stamp := now.Format(time.RFC3339Nano)
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, actor, summary, created_at)
		VALUES (?, 'github', 'github.pull_request', ?, '{"repo":"acme/widget","number":99,"state":"open","title":"Legacy PR","author":"alice","head":"feature/legacy"}', 'widget', 'alice', 'Legacy PR', ?)`,
		legacyID, stamp, stamp); err != nil {
		t.Fatalf("insert legacy github row: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 4`); err != nil {
		t.Fatalf("set user_version=4: %v", err)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"today_overview","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"resume_project","arguments":{"project":"widget"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"resume_project","arguments":{"all":true}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"projects_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"next_items","arguments":{}}}`,
	}
	var output bytes.Buffer
	if err := workgraph.ServeBridgeMCP(workgraph.BridgeMCPConfig{
		HomeDir:   homeDir,
		MemoryDir: memoryDir,
		Input:     strings.NewReader(strings.Join(requests, "\n") + "\n"),
		Output:    &output,
	}); err != nil {
		t.Fatalf("serve bridge MCP: %v", err)
	}

	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 4 {
		t.Fatalf("expected read-only MCP calls to leave user_version at 4, got %d", version)
	}
	var legacyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id = ?`, legacyID).Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy row: %v", err)
	}
	if legacyCount != 1 {
		t.Fatalf("expected read-only MCP calls to leave legacy row %q untouched, got count=%d", legacyID, legacyCount)
	}
}
