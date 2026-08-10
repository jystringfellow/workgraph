package facts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var publicCommandPaths = []string{
	"ai",
	"ai archive",
	"ai checkpoint",
	"ai resume",
	"ai run",
	"ai sessions",
	"ai show",
	"ai unarchive",
	"associations",
	"associations explain",
	"azure",
	"azure boards",
	"azure boards capture",
	"azure boards connect",
	"azure boards disconnect",
	"calendar",
	"calendar capture",
	"calendar connect",
	"calendar disconnect",
	"connectors",
	"connectors disable",
	"connectors doctor",
	"connectors enable",
	"connectors interval",
	"connectors list",
	"connectors poll",
	"connectors status",
	"connectors upgrade",
	"connectors validate",
	"doctor",
	"events",
	"events today",
	"git",
	"git capture",
	"git connect",
	"github",
	"github capture",
	"github connect",
	"help",
	"init",
	"llm",
	"llm add",
	"llm doctor",
	"llm hosted",
	"llm hosted disable",
	"llm hosted enable",
	"llm hosted status",
	"llm list",
	"llm remove",
	"llm summarize",
	"llm test",
	"llm use",
	"mail",
	"mail capture",
	"mail connect",
	"mail disconnect",
	"memory",
	"memory init",
	"memory links",
	"memory promote",
	"memory suggest",
	"network",
	"network destinations",
	"notion",
	"notion capture",
	"notion connect",
	"notion connect-token",
	"notion disconnect",
	"notion index",
	"notion index list",
	"notion index show",
	"resume",
	"review",
	"security",
	"security report",
	"settings",
	"settings add-watch",
	"settings doctor",
	"settings get",
	"slack",
	"slack capture",
	"slack connect",
	"slack disconnect",
	"slack lists",
	"slack lists capture",
	"start",
	"status",
	"stop",
	"suggestions",
	"suggestions approve",
	"suggestions complete",
	"suggestions dismiss",
	"suggestions list",
	"suggestions show",
	"suggestions snooze",
	"today",
}

func TestRootHelpListsPublicCommands(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		output := runWorkgraphCommand(t, nil, args...)
		for _, expected := range []string{
			"workgraph",
			"Usage:",
			"Commands:",
			"help",
			"init",
			"connectors",
			"today",
			"resume",
			"workgraph help <command>",
		} {
			if !strings.Contains(output, expected) {
				t.Fatalf("workgraph %s: expected %q in help output, got:\n%s", strings.Join(args, " "), expected, output)
			}
		}
		if strings.Contains(output, "__capture-worker") || strings.Contains(output, "__capture-supervisor") {
			t.Fatalf("workgraph %s exposed internal commands:\n%s", strings.Join(args, " "), output)
		}
	}
}

func TestEveryPublicCommandHasHelp(t *testing.T) {
	for _, commandPath := range publicCommandPaths {
		commandPath := commandPath
		t.Run(strings.ReplaceAll(commandPath, " ", "_"), func(t *testing.T) {
			path := strings.Fields(commandPath)
			forms := [][]string{
				append([]string{"help"}, path...),
				append(append([]string{}, path...), "help"),
				append(append([]string{}, path...), "-h"),
				append(append([]string{}, path...), "--help"),
			}
			var firstOutput string
			for _, args := range forms {
				output := runWorkgraphCommand(t, nil, args...)
				for _, expected := range []string{"Usage:", "workgraph " + commandPath} {
					if !strings.Contains(output, expected) {
						t.Fatalf("workgraph %s: expected %q in help output, got:\n%s", strings.Join(args, " "), expected, output)
					}
				}
				if firstOutput == "" {
					firstOutput = output
				} else if output != firstOutput {
					t.Fatalf("workgraph %s: expected equivalent help output, got:\n%s\nwant:\n%s", strings.Join(args, " "), output, firstOutput)
				}
			}
			if !strings.Contains(firstOutput, "Subcommands:") && commandPath != "help" && !strings.Contains(firstOutput, "Options:") {
				t.Fatalf("workgraph help %s did not list leaf-command options:\n%s", commandPath, firstOutput)
			}
		})
	}
}

func TestAIHelpNamesVerifiedNativeAdapters(t *testing.T) {
	for _, path := range []string{"ai run", "ai resume"} {
		output := runWorkgraphCommand(t, nil, append([]string{"help"}, strings.Fields(path)...)...)
		for _, tool := range []string{"codex", "claude", "copilot", "opencode"} {
			if !strings.Contains(output, tool) {
				t.Fatalf("help for %q does not name verified adapter %q:\n%s", path, tool, output)
			}
		}
	}
	resumeOutput := runWorkgraphCommand(t, nil, "help", "ai", "resume")
	if !strings.Contains(resumeOutput, "Launch a verified native") {
		t.Fatalf("AI resume help did not make process launch explicit:\n%s", resumeOutput)
	}
}

func TestAIArchiveAndSessionsHelpShowLifecycleExamples(t *testing.T) {
	archiveOutput := runWorkgraphCommand(t, nil, "help", "ai", "archive")
	for _, expected := range []string{
		"Examples:",
		"workgraph ai archive <session-id> <session-id>",
		"workgraph ai archive --status ended --before 2026-08-01 --dry-run",
		"workgraph ai archive --all --yes",
	} {
		if !strings.Contains(archiveOutput, expected) {
			t.Fatalf("archive help missing %q:\n%s", expected, archiveOutput)
		}
	}
	sessionsOutput := runWorkgraphCommand(t, nil, "help", "ai", "sessions")
	for _, expected := range []string{
		"Examples:",
		"workgraph ai sessions --status interrupted",
		"workgraph ai sessions --archived",
		"workgraph ai sessions --limit 20",
	} {
		if !strings.Contains(sessionsOutput, expected) {
			t.Fatalf("sessions help missing %q:\n%s", expected, sessionsOutput)
		}
	}
}

func TestLeafHelpListsAvailableOptions(t *testing.T) {
	for _, test := range []struct {
		commandPath string
		options     []string
	}{
		{"today", []string{"Options:", "--home", "--database"}},
		{"ai sessions", []string{"Options:", "--all", "--archived", "--status", "--limit"}},
		{"llm add", []string{"Options:", "--provider", "--model", "--api-key-env"}},
		{"calendar connect", []string{"Options:", "--client-id", "--no-browser", "--calendar-id"}},
		{"suggestions dismiss", []string{"Options:", "--reason", "--note"}},
	} {
		output := runWorkgraphCommand(t, nil, append([]string{"help"}, strings.Fields(test.commandPath)...)...)
		for _, expected := range test.options {
			if !strings.Contains(output, expected) {
				t.Fatalf("workgraph help %s: expected option %q, got:\n%s", test.commandPath, expected, output)
			}
		}
	}
}

func TestCommandGroupHelpListsImmediateSubcommands(t *testing.T) {
	output := runWorkgraphCommand(t, nil, "connectors", "help")
	for _, expected := range []string{"Subcommands:", "list", "status", "poll", "validate", "enable", "disable"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected connectors help to include %q, got:\n%s", expected, output)
		}
	}
}

func TestUnknownHelpPathIsActionable(t *testing.T) {
	output, err := runWorkgraphCommandAllowError(nil, "help", "does-not-exist")
	if err == nil {
		t.Fatalf("expected unknown help path to fail, got:\n%s", output)
	}
	for _, expected := range []string{"unknown help topic: does-not-exist", "workgraph help"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected unknown help output to include %q, got:\n%s", expected, output)
		}
	}
}

func TestHelpDoesNotRunCommandsOrExposeEnvironmentDefaults(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	output := runWorkgraphCommand(t, []string{
		"WORKGRAPH_SLACK_TOKEN=help-test-secret",
		"WORKGRAPH_SLACK_CLIENT_SECRET=help-client-secret",
	}, "init", "--home", homeDir, "help")
	if _, err := os.Stat(homeDir); !os.IsNotExist(err) {
		t.Fatalf("help ran init and changed local state: %v", err)
	}
	if strings.Contains(output, "help-test-secret") || strings.Contains(output, "help-client-secret") {
		t.Fatalf("help exposed an environment-backed flag default:\n%s", output)
	}

	output = runWorkgraphCommand(t, []string{
		"WORKGRAPH_SLACK_TOKEN=help-test-secret",
		"WORKGRAPH_SLACK_CLIENT_SECRET=help-client-secret",
	}, "help", "slack", "connect")
	if strings.Contains(output, "help-test-secret") || strings.Contains(output, "help-client-secret") {
		t.Fatalf("help exposed an environment-backed flag default:\n%s", output)
	}
}
