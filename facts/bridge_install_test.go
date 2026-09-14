package facts

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestBridgeInstallPackagesCodexAndClaudeCodeIdempotently(t *testing.T) {
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
			args := []string{"bridge", "install", "--home", homeDir, "--client", client,
				"--client-command", clientPath, "--install-root", installRoot, "--no-launchd"}
			for attempt := 0; attempt < 2; attempt++ {
				if output, err := runworkgraph(t, repoRoot(t), args...); err != nil {
					t.Fatalf("install %s attempt %d: %v\n%s", client, attempt+1, err, output)
				}
			}
			manifest := ".codex-plugin/plugin.json"
			if client == "claude-code" {
				manifest = ".claude-plugin/plugin.json"
			}
			if _, err := os.Stat(filepath.Join(installRoot, "plugins", "workgraph", manifest)); err != nil {
				t.Fatalf("installed %s manifest: %v", client, err)
			}
			for _, skill := range []string{"workgraph-bridge", "workgraph-memory", "workgraph-ai-checkpoint"} {
				if _, err := os.Stat(filepath.Join(installRoot, "plugins", "workgraph", "skills", skill, "SKILL.md")); err != nil {
					t.Fatalf("installed %s skill %s: %v", client, skill, err)
				}
			}
			logContents, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read client registration log: %v", err)
			}
			logText := string(logContents)
			for _, expected := range []string{"plugin marketplace add", "workgraph@workgraph"} {
				if !strings.Contains(logText, expected) {
					t.Fatalf("%s registration omitted %q:\n%s", client, expected, logText)
				}
			}
		})
	}
}

func TestBridgeDrainLaunchesClientOnlyForActiveDaemonWork(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	logPath := filepath.Join(fixtureDir, "drain.log")
	clientPath := filepath.Join(fixtureDir, "codex")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+logPath+"\"\n"), 0o700); err != nil {
		t.Fatalf("write fake Codex: %v", err)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath)
	if err != nil || !strings.Contains(string(output), "No active") {
		t.Fatalf("idle bridge drain: %v\n%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("idle drain launched Codex: %v", err)
	}
	connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
	if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
		t.Fatalf("emit request: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", "codex", "--client-command", clientPath); err != nil {
		t.Fatalf("active bridge drain: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read drain invocation: %v", err)
	}
	for _, expected := range []string{"exec", "--ephemeral", "workgraph-bridge", "capture requests"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("drain invocation omitted %q:\n%s", expected, contents)
		}
	}
}

func TestBridgeDoctorVerifiesInstalledPackageAndLocalMCPWithoutProvider(t *testing.T) {
	homeDir := initBridgedCaptureHome(t)
	fixtureDir := t.TempDir()
	clientPath := filepath.Join(fixtureDir, "claude")
	if err := os.WriteFile(clientPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake Claude: %v", err)
	}
	installRoot := filepath.Join(fixtureDir, "installed")
	if output, err := runworkgraph(t, repoRoot(t), "bridge", "install", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot, "--no-launchd"); err != nil {
		t.Fatalf("install bridge: %v\n%s", err, output)
	}
	output, err := runworkgraph(t, repoRoot(t), "bridge", "doctor", "--home", homeDir,
		"--client", "claude-code", "--client-command", clientPath, "--install-root", installRoot)
	if err != nil {
		t.Fatalf("doctor bridge: %v\n%s", err, output)
	}
	for _, expected := range []string{"Package: ready", "MCP: ready", "Round trip: ready", "Client: ready", "Worker: not installed"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("bridge doctor omitted %q:\n%s", expected, output)
		}
	}
}

func TestReferenceWorkersDrainFakeProviderRequestEndToEnd(t *testing.T) {
	for _, client := range []string{"codex", "claude-code"} {
		t.Run(client, func(t *testing.T) {
			homeDir := initBridgedCaptureHome(t)
			connectBridgedConnector(t, repoRoot(t), homeDir, "slack")
			if output, err := runworkgraph(t, repoRoot(t), "connectors", "poll", "--home", homeDir, "--once", "--connector", "slack"); err != nil {
				t.Fatalf("emit request: %v\n%s", err, output)
			}
			fixtureDir := t.TempDir()
			clientPath := filepath.Join(fixtureDir, "client")
			script := `#!/bin/sh
claim_file="${TMPDIR:-/tmp}/workgraph-reference-worker-$$.json"
claim_output="$($WORKGRAPH_EXECUTABLE capture requests --claim --home "$WORKGRAPH_HOME" --connector slack --max 1 --worker reference-fact --claim-file "$claim_file")" || exit 1
request_id="$(printf '%s\n' "$claim_output" | sed -n 's/^Request: //p')"
printf '%s\n' '{"type":"slack.message","timestamp":"2026-09-13T12:00:00Z","external_id":"C0DEMO123:reference-worker","summary":"Reference worker event","payload":{"channel":"C0DEMO123","text":"fake approved provider result"}}' | "$WORKGRAPH_EXECUTABLE" capture ingest --home "$WORKGRAPH_HOME" --request "$request_id" --claim-file "$claim_file" --json -
rm -f "$claim_file"
`
			if err := os.WriteFile(clientPath, []byte(script), 0o700); err != nil {
				t.Fatalf("write fake reference client: %v", err)
			}
			if output, err := runworkgraph(t, repoRoot(t), "bridge", "drain", "--home", homeDir, "--client", client, "--client-command", clientPath); err != nil {
				t.Fatalf("drain fake provider request: %v\n%s", err, output)
			}
			db, err := sql.Open("sqlite3", filepath.Join(homeDir, "workgraph.db"))
			if err != nil {
				t.Fatalf("open event store: %v", err)
			}
			defer db.Close()
			var events, completed int
			if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE source = 'slack' AND summary = 'Reference worker event'`).Scan(&events); err != nil {
				t.Fatalf("count reference events: %v", err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM capture_requests WHERE connector_id = 'slack' AND status = 'completed'`).Scan(&completed); err != nil {
				t.Fatalf("count completed requests: %v", err)
			}
			if events != 1 || completed != 1 {
				t.Fatalf("expected one event and completed request, got events=%d completed=%d", events, completed)
			}
		})
	}
}
