package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func installAntigravityPlugin(t *testing.T, homeDir, clientPath, installRoot string, extra ...string) ([]byte, error) {
	t.Helper()
	args := []string{"plugin", "install", "--home", homeDir, "--client", "agy",
		"--client-command", clientPath, "--install-root", installRoot, "--no-launchd"}
	return runworkgraph(t, repoRoot(t), append(args, extra...)...)
}

func antigravityDrainAllowRules(t *testing.T, arguments []string) []string {
	t.Helper()
	index := slices.Index(arguments, "--settings")
	if index < 0 || index+1 >= len(arguments) {
		t.Fatalf("Antigravity drain omitted --settings:\n%s", strings.Join(arguments, "\n"))
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(arguments[index+1]), &settings); err != nil {
		t.Fatalf("parse Antigravity drain settings: %v\n%s", err, arguments[index+1])
	}
	return settings.Permissions.Allow
}

func TestAntigravityPluginInstallPersistsOnlyExplicitProviderTools(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "agy")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Antigravity: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
	output, err := installAntigravityPlugin(t, homeDir, clientPath, installRoot,
		"--allow-provider-tool", "mcp(slack/slack_read_channel)", "--allow-provider-tool", "mcp(azure-devops/wit_query)",
		"--allow-provider-tool", "mcp(slack/slack_read_channel)")
	if err != nil || !strings.Contains(string(output), "Provider tools: 2 explicitly allowed") ||
		!strings.Contains(string(output), "Permissions: unattended workgraph MCP drain only, granted per drain invocation") {
		t.Fatalf("install Antigravity plugin with provider tools: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(filepath.Join(homeDir, "bridge", "workers.json"))
	if err != nil {
		t.Fatalf("read worker config: %v", err)
	}
	var config struct {
		Workers map[string]struct {
			ProviderTools []string `json:"provider_tools"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatalf("parse worker config: %v", err)
	}
	if got := strings.Join(config.Workers["agy"].ProviderTools, ","); got != "mcp(azure-devops/wit_query),mcp(slack/slack_read_channel)" {
		t.Fatalf("worker config stored provider tools %q:\n%s", got, contents)
	}
	if output, err := installAntigravityPlugin(t, homeDir, clientPath, installRoot, "--clear-provider-tools", "--allow-provider-tool", "mcp(slack/slack_list_user_channels)"); err != nil || !strings.Contains(string(output), "Provider tools: 1 explicitly allowed") {
		t.Fatalf("clear and replace Antigravity provider tools: %v\n%s", err, output)
	}
	doctor, err := runworkgraph(t, repoRoot(t), "plugin", "doctor", "--home", homeDir,
		"--client", "agy", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil || !strings.Contains(string(doctor), "Provider tools: 1 explicitly allowed") || !strings.Contains(string(doctor), "Permissions: granted per drain invocation") {
		t.Fatalf("doctor did not report Antigravity worker settings: %v\n%s", err, doctor)
	}
	for _, rejected := range []string{"mcp(slack/*)", "mcp(*)", "mcp(slack)", "mcp(workgraph/capture_ingest)", "mcp(workgraph_workgraph/capture_ingest)", "command(rm)", "write_file(*)", "slack(slack_read_channel)", "mcp__claude_ai_Slack__slack_read_channel"} {
		if output, err := installAntigravityPlugin(t, homeDir, clientPath, installRoot, "--allow-provider-tool", rejected); err == nil {
			t.Fatalf("expected Antigravity provider tool %q to be rejected:\n%s", rejected, output)
		}
	}
}

func TestAntigravityPluginInstallRejectsConfigDirBinding(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "agy")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Antigravity: %v", err)
	}
	output, err := installAntigravityPlugin(t, homeDir, clientPath, filepath.Join(fixtureDir, "installed"), "--config-dir", fixtureDir)
	if err == nil || !strings.Contains(string(output), "--config-dir is not supported for agy") {
		t.Fatalf("expected Antigravity config-dir binding to be rejected: %v\n%s", err, output)
	}
}

func TestAntigravityPluginReinstallReplacesTheInstalledPlugin(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	statePath := filepath.Join(fixtureDir, "installed-from")
	logPath := filepath.Join(fixtureDir, "agy.log")
	clientPath := filepath.Join(fixtureDir, "agy")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1 $2" in
  "plugin uninstall")
    if [ ! -f "` + statePath + `" ]; then echo 'plugin "workgraph" not found' >&2; exit 1; fi
    rm -f "` + statePath + `" ;;
  "plugin install")
    if [ -f "` + statePath + `" ]; then echo 'plugin "workgraph" already installed' >&2; exit 1; fi
    printf '%s' "$3" > "` + statePath + `" ;;
esac
exit 0
`
	if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake Antigravity: %v", err)
	}
	for _, installRoot := range []string{filepath.Join(fixtureDir, "first"), filepath.Join(fixtureDir, "second")} {
		if output, err := installAntigravityPlugin(t, homeDir, clientPath, installRoot); err != nil {
			t.Fatalf("install Antigravity plugin at %s: %v\n%s", installRoot, err, output)
		}
		installed, err := os.ReadFile(statePath)
		if want := filepath.Join(installRoot, "plugins", "workgraph"); err != nil || string(installed) != want {
			t.Fatalf("Antigravity installed plugin from %q, want %q: %v", installed, want, err)
		}
	}

	invalidLogPath := filepath.Join(fixtureDir, "agy-invalid.log")
	invalidPath := filepath.Join(fixtureDir, "agy-invalid")
	if err := os.WriteFile(invalidPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+invalidLogPath+"\"\ncase \"$2\" in validate) echo 'plugin.json is malformed' >&2; exit 1 ;; esac\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write invalid-package fake Antigravity: %v", err)
	}
	output, err := installAntigravityPlugin(t, homeDir, invalidPath, filepath.Join(fixtureDir, "invalid"))
	if err == nil || !strings.Contains(string(output), "plugin.json is malformed") {
		t.Fatalf("expected Antigravity package validation failure to surface: %v\n%s", err, output)
	}
	if invocations, _ := os.ReadFile(invalidLogPath); strings.Contains(string(invocations), "plugin uninstall") || strings.Contains(string(invocations), "plugin install") {
		t.Fatalf("an invalid package must not replace the installed plugin:\n%s", invocations)
	}

	failingPath := filepath.Join(fixtureDir, "agy-failing")
	if err := os.WriteFile(failingPath, []byte("#!/bin/sh\ncase \"$2\" in install) echo 'manifest is invalid' >&2; exit 1 ;; esac\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write failing fake Antigravity: %v", err)
	}
	output, err = installAntigravityPlugin(t, homeDir, failingPath, filepath.Join(fixtureDir, "third"))
	if err == nil || !strings.Contains(string(output), "manifest is invalid") {
		t.Fatalf("expected Antigravity plugin install failure to surface: %v\n%s", err, output)
	}
}

func TestAntigravityBridgeDrainAllowsOnlyWorkgraphDrainToolsAndExplicitProviderTools(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "drain.log")
	clientPath := filepath.Join(fixtureDir, "agy")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\ncase \"$1\" in plugin) exit 0 ;; esac\nprintf '%s\\n' \"$@\" > \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Antigravity: %v", err)
	}
	if output, err := installAntigravityPlugin(t, homeDir, clientPath, filepath.Join(fixtureDir, "installed"),
		"--model", "agy-bridge-pinned", "--allow-provider-tool", "mcp(slack/slack_list_user_channels)"); err != nil {
		t.Fatalf("install Antigravity plugin: %v\n%s", err, output)
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "agy", "--client-command", clientPath); err != nil {
		t.Fatalf("drain with Antigravity: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read Antigravity invocation: %v", err)
	}
	arguments := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	allow := antigravityDrainAllowRules(t, arguments)
	expected := []string{"mcp(slack/slack_list_user_channels)"}
	for _, tool := range []string{
		"capture_requests_list", "capture_requests_claim", "capture_request_renew", "capture_ingest",
		"capture_request_fail", "capture_watermark", "connector_status", "connector_required_tools",
		"bridge_worker_heartbeat", "llm_task_submit",
	} {
		expected = append(expected, "mcp(workgraph_workgraph/"+tool+")")
	}
	slices.Sort(expected)
	sortedAllow := slices.Sorted(slices.Values(allow))
	if !slices.Equal(sortedAllow, expected) {
		t.Fatalf("Antigravity drain allow rules = %q, want exactly %q", sortedAllow, expected)
	}
	for _, expected := range []string{"--model", "agy-bridge-pinned"} {
		if !slices.Contains(arguments, expected) {
			t.Fatalf("Antigravity drain invocation omitted %q:\n%s", expected, contents)
		}
	}
	for _, argument := range arguments {
		if argument == "--dangerously-skip-permissions" || argument == "--mode" || strings.HasPrefix(argument, "--mode=") {
			t.Fatalf("Antigravity drain granted broad permission %q:\n%s", argument, contents)
		}
	}
	prompt := arguments[len(arguments)-1]
	if arguments[len(arguments)-2] != "-p" || !strings.Contains(prompt, "List pending requests before claiming") || !strings.Contains(prompt, "Claim with worker name agy-drain-") {
		t.Fatalf("Antigravity drain prompt did not follow -p:\n%s", contents)
	}
}

func TestAntigravityBridgeDrainLeavesCaptureRequestsPendingWithoutProviderTools(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "agy.log")
	clientPath := filepath.Join(fixtureDir, "agy")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\ncase \"$1\" in plugin) exit 0 ;; esac\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Antigravity: %v", err)
	}
	if output, err := installAntigravityPlugin(t, homeDir, clientPath, filepath.Join(fixtureDir, "installed")); err != nil {
		t.Fatalf("install Antigravity plugin: %v\n%s", err, output)
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "agy", "--client-command", clientPath)
	if err != nil || !strings.Contains(string(output), "No Antigravity provider tools are explicitly allowed") {
		t.Fatalf("expected pending capability warning: %v\n%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("incapable drain launched Antigravity: %v", err)
	}
	var status string
	if err := openBridgedCaptureDatabase(t, homeDir).QueryRow(`SELECT status FROM capture_requests WHERE connector_id = 'slack'`).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("expected request to remain pending, got %q err=%v", status, err)
	}
}
