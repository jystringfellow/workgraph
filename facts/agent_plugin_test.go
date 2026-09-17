package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPluginInstallPackagesEveryCanonicalSkillForBothClients(t *testing.T) {
	for _, client := range []string{"codex", "claude-code"} {
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
			if !strings.Contains(manifest.Version, versionMarker) {
				t.Fatalf("installed %s plugin version %q lacks update cachebuster %q", client, manifest.Version, versionMarker)
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

			mcp, err := os.ReadFile(filepath.Join(pluginRoot, ".mcp.json"))
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
			if !strings.Contains(string(registration), "workgraph@workgraph") {
				t.Fatalf("%s did not install workgraph plugin:\n%s", client, registration)
			}
			if strings.Contains(string(registration), "workgraph-bridge@workgraph") {
				t.Fatalf("%s installed legacy bridge-only plugin:\n%s", client, registration)
			}
			if client == "claude-code" && !strings.Contains(string(registration), "plugin update --scope user workgraph@workgraph") {
				t.Fatalf("Claude Code reinstall omitted plugin update:\n%s", registration)
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
