package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	identity := currentBuildIdentity()
	workgraph.SetProcessBuildIdentity(workgraph.BuildIdentity{
		Version: identity.Version,
		Commit:  identity.Commit,
		Built:   identity.BuildDate,
	})
	if len(args) > 0 && args[0] == "help" {
		if len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
			return runHelp([]string{"help"}, false, stdout, stderr)
		}
		return runHelp(args[1:], false, stdout, stderr)
	}
	if len(args) == 1 && isHelpArgument(args[0]) {
		return runHelp(nil, false, stdout, stderr)
	}
	if len(args) > 1 && isHelpArgument(args[len(args)-1]) && !isAIRunPassthrough(args) {
		return runHelp(args[:len(args)-1], true, stdout, stderr)
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph <command>")
		return 2
	}

	switch args[0] {
	case "ai":
		return runAI(args[1:], os.Stdin, stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "settings":
		return runSettings(args[1:], stdout, stderr)
	case "network":
		return runNetwork(args[1:], stdout, stderr)
	case "security":
		return runSecurity(args[1:], stdout, stderr)
	case "connectors":
		return runConnectors(args[1:], stdout, stderr)
	case "capture":
		return runCapture(args[1:], os.Stdin, stdout, stderr)
	case "bridge":
		return runBridge(args[1:], os.Stdin, stdout, stderr)
	case "plugin":
		return runPlugin(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "git":
		return runGit(args[1:], stdout, stderr)
	case "github":
		return runGitHub(args[1:], stdout, stderr)
	case "calendar":
		return runCalendar(args[1:], stdout, stderr)
	case "mail":
		return runMail(args[1:], stdout, stderr)
	case "azure":
		return runAzure(args[1:], stdout, stderr)
	case "llm":
		return runLLM(args[1:], stdout, stderr)
	case "events":
		return runEvents(args[1:], stdout, stderr)
	case "associations":
		return runAssociations(args[1:], stdout, stderr)
	case "suggestions":
		return runSuggestions(args[1:], stdout, stderr)
	case "review":
		return runReview(args[1:], stdout, stderr)
	case "notion":
		return runNotion(args[1:], stdout, stderr)
	case "memory":
		return runMemory(args[1:], stdout, stderr)
	case "start":
		return runCaptureStart(args[1:], stdout, stderr)
	case "status":
		return runCaptureStatus(args[1:], stdout, stderr)
	case "stop":
		return runCaptureStop(args[1:], stdout, stderr)
	case "today":
		return runToday(args[1:], stdout, stderr)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "resume":
		return runResume(args[1:], stdout, stderr)
	case "slack":
		return runSlack(args[1:], stdout, stderr)
	case "__capture-worker":
		return runCaptureWorker(args[1:], stderr)
	case "__capture-supervisor":
		return runCaptureSupervisor(args[1:], stderr)
	case "__ai-native-session":
		return runAINativeSessionHook(args[1:], os.Stdin, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

func runPlugin(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph plugin <install|doctor>")
		return 2
	}
	switch args[0] {
	case "install":
		return runPluginInstall(args[1:], stdout, stderr)
	case "doctor":
		return runPluginDoctor(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown plugin command: %s\n", args[0])
		return 2
	}
}

func runPluginInstall(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "client to install into: codex or claude-code")
	clientCommand := flags.String("client-command", "", "client executable override")
	installRoot := flags.String("install-root", "", "package installation root override")
	noLaunchd := flags.Bool("no-launchd", false, "skip installing the macOS bridge drain worker")
	model := flags.String("model", "", "pin the unattended bridge worker to this client model")
	clearModel := flags.Bool("clear-model", false, "clear the persisted bridge worker model and use the client default")
	var providerTools repeatedStringFlags
	flags.Var(&providerTools, "allow-provider-tool", "exact provider MCP tool name to allow for the unattended Claude worker; repeatable")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph plugin install --client <codex|claude-code>")
		return 2
	}
	result, err := workgraph.InstallPlugin(workgraph.PluginInstallConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand,
		InstallRoot: *installRoot, SkipLaunchd: *noLaunchd, ProviderTools: providerTools,
		Model: *model, ClearModel: *clearModel,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph plugin install: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runPluginDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("plugin doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "client to diagnose: codex or claude-code")
	clientCommand := flags.String("client-command", "", "client executable override")
	installRoot := flags.String("install-root", "", "package installation root override")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph plugin doctor --client <codex|claude-code>")
		return 2
	}
	message, err := workgraph.DoctorPlugin(workgraph.PluginDoctorConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand, InstallRoot: *installRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph plugin doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, message)
	return 0
}

func runBridge(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph bridge <mcp|install|drain|doctor>")
		return 2
	}
	if args[0] == "install" {
		return runBridgeInstall(args[1:], stdout, stderr)
	}
	if args[0] == "drain" {
		return runBridgeDrain(args[1:], stdout, stderr)
	}
	if args[0] == "doctor" {
		return runBridgeDoctor(args[1:], stdout, stderr)
	}
	if args[0] != "mcp" {
		fmt.Fprintf(stderr, "unknown bridge command: %s\n", args[0])
		return 2
	}
	flags := flag.NewFlagSet("bridge mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph bridge mcp [--home <path>] [--database <path>]")
		return 2
	}
	if err := workgraph.ServeBridgeMCP(workgraph.BridgeMCPConfig{HomeDir: *homeDir, DatabasePath: *databasePath, Input: stdin, Output: stdout}); err != nil {
		fmt.Fprintf(stderr, "workgraph bridge mcp: %v\n", err)
		return 1
	}
	return 0
}

func runBridgeDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("bridge doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "reference client: codex or claude-code")
	clientCommand := flags.String("client-command", "", "client executable override")
	installRoot := flags.String("install-root", "", "package installation root override")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph bridge doctor --client <codex|claude-code>")
		return 2
	}
	message, err := workgraph.DoctorBridge(workgraph.BridgeDoctorConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand, InstallRoot: *installRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph bridge doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, message)
	return 0
}

func runBridgeDrain(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("bridge drain", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "reference client: codex or claude-code")
	clientCommand := flags.String("client-command", "", "client executable override")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph bridge drain --client <codex|claude-code>")
		return 2
	}
	message, err := workgraph.DrainBridge(*homeDir, *client, *clientCommand)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph bridge drain: %v\n", err)
		return 1
	}
	if message != "" {
		fmt.Fprintln(stdout, message)
	}
	return 0
}

func runBridgeInstall(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("bridge install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "reference client: codex or claude-code")
	clientCommand := flags.String("client-command", "", "client executable override")
	installRoot := flags.String("install-root", "", "package installation root override")
	noLaunchd := flags.Bool("no-launchd", false, "skip installing the macOS drain worker")
	model := flags.String("model", "", "pin the unattended bridge worker to this client model")
	clearModel := flags.Bool("clear-model", false, "clear the persisted bridge worker model and use the client default")
	var providerTools repeatedStringFlags
	flags.Var(&providerTools, "allow-provider-tool", "exact provider MCP tool name to allow for the unattended Claude worker; repeatable")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph bridge install --client <codex|claude-code>")
		return 2
	}
	result, err := workgraph.InstallBridge(workgraph.BridgeInstallConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand,
		InstallRoot: *installRoot, SkipLaunchd: *noLaunchd, ProviderTools: providerTools,
		Model: *model, ClearModel: *clearModel,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph bridge install: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSecurity(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph security <report>")
		return 2
	}
	if args[0] != "report" {
		fmt.Fprintf(stderr, "unknown security command: %s\n", args[0])
		return 2
	}
	flags := flag.NewFlagSet("security report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph security report [--format text|json]")
		return 2
	}
	result, err := workgraph.SecurityReport(workgraph.SecurityReportConfig{HomeDir: *homeDir, Format: *format})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph security report: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNetwork(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph network <destinations>")
		return 2
	}
	switch args[0] {
	case "destinations":
		return runNetworkDestinations(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown network command: %s\n", args[0])
		return 2
	}
}

func runNetworkDestinations(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("network destinations", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	format := flags.String("format", "text", "output format: text or json")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph network destinations [--format text|json]")
		return 2
	}

	result, err := workgraph.NetworkDestinations(workgraph.NetworkDestinationsConfig{
		HomeDir: *homeDir,
		Format:  *format,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph network destinations: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestions(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions <scan|list|show|approve|dismiss|snooze|complete>")
		return 2
	}
	switch args[0] {
	case "scan":
		return runSuggestionsScan(args[1:], stdout, stderr)
	case "list":
		return runSuggestionsList(args[1:], stdout, stderr)
	case "show":
		return runSuggestionsShow(args[1:], stdout, stderr)
	case "approve":
		return runSuggestionsApprove(args[1:], stdout, stderr)
	case "dismiss":
		return runSuggestionsDismiss(args[1:], stdout, stderr)
	case "snooze":
		return runSuggestionsSnooze(args[1:], stdout, stderr)
	case "complete":
		return runSuggestionsComplete(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown suggestions command: %s\n", args[0])
		return 2
	}
}

func runSuggestionsSnooze(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions snooze <id> --until <RFC3339>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions snooze "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	until := flags.String("until", "", "Future RFC3339 instant when the suggestion should resurface")
	reason := flags.String("reason", "", "Optional stable snooze reason code")
	note := flags.String("note", "", "Optional snooze note")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*until) == "" {
		fmt.Fprintln(stderr, "usage: workgraph suggestions snooze <id> --until <RFC3339>")
		return 2
	}
	suggestion, err := workgraph.SnoozeSuggestion(workgraph.SuggestionSnoozeUpdate{
		HomeDir: *homeDir, DatabasePath: *databasePath, ID: id, UntilAt: *until, ReasonCode: *reason, FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions snooze: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion snoozed\nid: %s\nstatus: %s\nuntil: %s\n", suggestion.ID, suggestion.Status, *until)
	return 0
}

func runSuggestionsComplete(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions complete <id>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions complete "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	note := flags.String("note", "", "Optional completion note")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions complete <id>")
		return 2
	}
	suggestion, err := workgraph.CompleteSuggestion(workgraph.SuggestionStatusUpdate{
		HomeDir: *homeDir, DatabasePath: *databasePath, ID: id, FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions complete: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion completed\nid: %s\nstatus: %s\n", suggestion.ID, suggestion.Status)
	return 0
}

func runReview(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	since := flags.String("since", "week", "Review window: week, 7d, or 30d")
	format := flags.String("format", "text", "Output format: text or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph review [--since week|7d|30d] [--format text|json]")
		return 2
	}
	result, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, Since: *since, Format: *format,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph review: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsShow(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions show <id>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions show "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions show <id>")
		return 2
	}

	result, err := workgraph.ShowSuggestion(workgraph.SuggestionShowConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions show: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("suggestions list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	status := flags.String("status", "", "Suggestion status to include")
	limit := flags.Int("limit", 25, "Maximum number of suggestions to show")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions list")
		return 2
	}

	result, err := workgraph.ListSuggestions(workgraph.SuggestionListConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Status:       *status,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsScan(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("suggestions scan", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scanType := flags.String("type", "", "Deterministic scanner to run: ignore")
	limit := flags.Int("limit", 20, "Maximum number of suggestions to record and show")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions scan [--type ignore] [--limit <count>]")
		return 2
	}

	result, err := workgraph.ScanSuggestions(workgraph.SuggestionScanConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Type:         *scanType,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions scan: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsApprove(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions approve <id>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions approve "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	note := flags.String("note", "", "Optional approval note")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions approve <id>")
		return 2
	}

	suggestion, err := workgraph.ApproveSuggestion(workgraph.SuggestionStatusUpdate{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
		FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions approve: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion approved\nid: %s\nstatus: %s\n", suggestion.ID, suggestion.Status)
	return 0
}

func runSuggestionsDismiss(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions dismiss <id> --reason <code>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions dismiss "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	reason := flags.String("reason", "", "Stable dismiss reason code")
	note := flags.String("note", "", "Optional dismiss note")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions dismiss <id> --reason <code>")
		return 2
	}

	suggestion, err := workgraph.DismissSuggestion(workgraph.SuggestionStatusUpdate{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
		ReasonCode:   *reason,
		FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions dismiss: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion dismissed\nid: %s\nstatus: %s\n", suggestion.ID, suggestion.Status)
	return 0
}

func runDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph doctor")
		return 2
	}

	result, err := workgraph.Doctor(workgraph.DoctorConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runEvents(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph events <today>")
		return 2
	}
	switch args[0] {
	case "today":
		return runEventsToday(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown events command: %s\n", args[0])
		return 2
	}
}

func runAssociations(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph associations <explain>")
		return 2
	}
	if args[0] != "explain" {
		fmt.Fprintf(stderr, "unknown associations command: %s\n", args[0])
		return 2
	}
	return runAssociationsExplain(args[1:], stdout, stderr)
}

func runAssociationsExplain(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph associations explain <event-id>")
		return 2
	}
	eventID := args[0]
	flags := flag.NewFlagSet("associations explain "+eventID, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph associations explain <event-id>")
		return 2
	}
	result, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, EventID: eventID,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph associations explain: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runEventsToday(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("events today", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventType := flags.String("type", "", "Event type to include")
	actor := flags.String("actor", "", "Exact event actor to include")
	involvement := flags.String("involvement", "", "Exact user involvement to include")
	limit := flags.Int("limit", 0, "Maximum number of matching events to show")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.EventsToday(workgraph.EventsTodayConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Type:         *eventType,
		Actor:        *actor,
		Involvement:  *involvement,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph events today: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLM(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm <command>")
		return 2
	}

	switch args[0] {
	case "add":
		return runLLMAdd(args[1:], stdout, stderr)
	case "connect":
		return runLLMConnect(args[1:], stdout, stderr)
	case "list":
		return runLLMList(args[1:], stdout, stderr)
	case "remove":
		return runLLMRemove(args[1:], stdout, stderr)
	case "use":
		return runLLMUse(args[1:], stdout, stderr)
	case "hosted":
		return runLLMHosted(args[1:], stdout, stderr)
	case "test":
		return runLLMTest(args[1:], stdout, stderr)
	case "doctor":
		return runLLMDoctor(args[1:], stdout, stderr)
	case "summarize":
		return runLLMSummarize(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown llm command: %s\n", args[0])
		return 2
	}
}

func runLLMAdd(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm add <profile>")
		return 2
	}
	profile := args[0]

	flags := flag.NewFlagSet("llm add "+profile, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	provider := flags.String("provider", "", "LLM provider")
	client := flags.String("client", "", "Signed-in AI client: codex or claude-code")
	baseURL := flags.String("base-url", "", "OpenAI-compatible base URL")
	model := flags.String("model", "", "LLM model")
	apiKeyEnv := flags.String("api-key-env", "", "Environment variable containing the API key")
	awsProfile := flags.String("aws-profile", "", "AWS profile for Bedrock")
	region := flags.String("region", "", "Cloud provider region")
	modelARN := flags.String("model-arn", "", "Bedrock model or inference profile ARN")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:    *homeDir,
		Name:       profile,
		Provider:   *provider,
		Client:     *client,
		BaseURL:    *baseURL,
		Model:      *model,
		APIKeyEnv:  *apiKeyEnv,
		AWSProfile: *awsProfile,
		Region:     *region,
		ModelARN:   *modelARN,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm add: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm connect <codex|claude-code> [options]")
		return 2
	}
	client := args[0]
	flags := flag.NewFlagSet("llm connect "+client, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	name := flags.String("name", "", "Profile name; defaults to the client id")
	task := flags.String("for", "", "Task to route to this profile")
	model := flags.String("model", "", "Optional client model; defaults to the client's configured model")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm connect <codex|claude-code> [options]")
		return 2
	}
	result, err := workgraph.ConnectLLMClient(workgraph.LLMConnectClientConfig{
		HomeDir: *homeDir,
		Client:  client,
		Name:    *name,
		Task:    *task,
		Model:   *model,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm connect: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.ListLLMProfiles(workgraph.LLMListConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMRemove(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm remove <profile>")
		return 2
	}
	profile := args[0]

	flags := flag.NewFlagSet("llm remove "+profile, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.RemoveLLMProfile(workgraph.LLMRemoveProfileConfig{
		HomeDir: *homeDir,
		Name:    profile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm remove: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMUse(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm use <profile>")
		return 2
	}
	profile := args[0]

	flags := flag.NewFlagSet("llm use "+profile, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	task := flags.String("for", "", "Task to route to this profile")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{
		HomeDir: *homeDir,
		Name:    profile,
		Task:    *task,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm use: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMHosted(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm hosted <status|enable|disable>")
		return 2
	}
	switch args[0] {
	case "status":
		return runLLMHostedStatus(args[1:], stdout, stderr)
	case "enable":
		return runLLMHostedEnable(args[1:], stdout, stderr)
	case "disable":
		return runLLMHostedDisable(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown llm hosted command: %s\n", args[0])
		return 2
	}
}

func runLLMHostedStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm hosted status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm hosted status")
		return 2
	}
	result, err := workgraph.HostedLLMStatus(workgraph.LLMHostedConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm hosted status: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMHostedEnable(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm hosted enable", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm hosted enable")
		return 2
	}
	result, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm hosted enable: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMHostedDisable(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm hosted disable", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm hosted disable")
		return 2
	}
	result, err := workgraph.DisableHostedLLM(workgraph.LLMHostedConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm hosted disable: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMTest(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm test", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	profile := flags.String("profile", "", "LLM profile to test")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: *homeDir,
		Profile: *profile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm test: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	profile := flags.String("profile", "", "LLM profile to inspect")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm doctor [--profile name]")
		return 2
	}

	result, err := workgraph.DoctorLLMProfiles(workgraph.LLMDoctorConfig{
		HomeDir: *homeDir,
		Profile: *profile,
	})
	fmt.Fprintln(stdout, result.Message)
	if err != nil {
		return 1
	}
	return 0
}

func runLLMSummarize(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm summarize <target>")
		return 2
	}
	target := args[0]

	flags := flag.NewFlagSet("llm summarize "+target, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	dryRun := flags.Bool("dry-run", false, "Preview prompt and context without calling the provider")
	noStream := flags.Bool("no-stream", false, "Print the summary after the provider call completes")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if target != "today" {
		fmt.Fprintf(stderr, "unsupported llm summarize target: %s\n", target)
		return 2
	}

	summarizeConfig := workgraph.LLMSummarizeTodayConfig{
		HomeDir: *homeDir,
		DryRun:  *dryRun,
	}
	if !*dryRun && !*noStream {
		summarizeConfig.Stream = func(chunk string) error {
			_, err := fmt.Fprint(stdout, chunk)
			return err
		}
	}
	result, err := workgraph.SummarizeTodayWithLLM(summarizeConfig)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm summarize today: %v\n", err)
		return 1
	}
	if result.Message != "" {
		fmt.Fprintln(stdout, result.Message)
	}
	return 0
}

func runConnectors(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors <connect|mode|list|required-tools|status|doctor|upgrade|poll|validate|enable|disable|interval>")
		return 2
	}

	switch args[0] {
	case "connect":
		return runConnectorsConnect(args[1:], stdout, stderr)
	case "mode":
		return runConnectorsMode(args[1:], stdout, stderr)
	case "list":
		return runConnectorsList(args[1:], stdout, stderr)
	case "required-tools":
		return runConnectorsRequiredTools(args[1:], stdout, stderr)
	case "status":
		return runConnectorsStatus(args[1:], stdout, stderr)
	case "doctor":
		return runConnectorsDoctor(args[1:], stdout, stderr)
	case "upgrade":
		return runConnectorsUpgrade(args[1:], stdout, stderr)
	case "poll":
		return runConnectorsPoll(args[1:], stdout, stderr)
	case "validate":
		return runConnectorsValidate(args[1:], stdout, stderr)
	case "enable":
		return runConnectorsEnable(args[1:], stdout, stderr)
	case "disable":
		return runConnectorsDisable(args[1:], stdout, stderr)
	case "interval":
		return runConnectorsInterval(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown connectors command: %s\n", args[0])
		return 2
	}
}

func runConnectorsRequiredTools(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors required-tools", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: workgraph connectors required-tools [connector]")
		return 2
	}
	connectorID := ""
	if flags.NArg() == 1 {
		connectorID = flags.Arg(0)
	}
	result, err := workgraph.RequiredConnectorTools(connectorID)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors required-tools: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors connect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	mode := flags.String("mode", "direct", "capture mode: direct or bridged")
	paramsJSON := flags.String("params-json", "", "non-secret bridged connector scope as a JSON object")
	connectorArg := ""
	connectorFirst := false
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		connectorArg = args[0]
		connectorFirst = true
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if connectorArg == "" && flags.NArg() == 1 {
		connectorArg = flags.Arg(0)
	}
	if connectorArg == "" || flags.NArg() > 1 || (connectorFirst && flags.NArg() != 0) {
		fmt.Fprintln(stderr, "usage: workgraph connectors connect [--mode direct|bridged] <connector>")
		return 2
	}
	if strings.ToLower(strings.TrimSpace(*mode)) != "bridged" {
		fmt.Fprintln(stderr, "workgraph connectors connect: generic setup currently requires --mode bridged; use the provider-specific connect command for direct capture")
		return 1
	}
	result, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir:      *homeDir,
		ID:           connectorArg,
		BridgeParams: json.RawMessage(*paramsJSON),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors connect: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsMode(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors mode", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: workgraph connectors mode <connector> <direct|bridged>")
		return 2
	}
	result, err := workgraph.SetConnectorMode(workgraph.ConnectorModeConfig{
		HomeDir: *homeDir,
		ID:      flags.Arg(0),
		Mode:    flags.Arg(1),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors mode: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCapture(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph capture <requests|ingest|watermark>")
		return 2
	}
	switch args[0] {
	case "requests":
		return runCaptureRequests(args[1:], stdin, stdout, stderr)
	case "ingest":
		return runCaptureIngest(args[1:], stdin, stdout, stderr)
	case "watermark":
		return runCaptureWatermark(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown capture command: %s\n", args[0])
		return 2
	}

}

func runCaptureRequests(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("capture requests", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	claim := flags.Bool("claim", false, "claim available capture requests")
	list := flags.Bool("list", false, "list capture requests")
	cancelID := flags.String("cancel", "", "cancel one pending or claimed request id")
	cancelReason := flags.String("reason", "", "reason recorded for request cancellation")
	renewID := flags.String("renew", "", "renew a claimed request id")
	failID := flags.String("fail", "", "report failure for a claimed request id")
	errorJSON := flags.String("error-json", "", "JSON failure details; - reads stdin")
	connector := flags.String("connector", "", "limit claims to one connector")
	maxClaims := flags.Int("max", 1, "maximum requests to claim")
	worker := flags.String("worker", "", "bridge worker identity")
	claimFile := flags.String("claim-file", "", "0600 file for the returned claim capability")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph capture requests --list | --cancel <id> [--reason <text>] | --claim [--connector X] [--max N] --worker <name> --claim-file <path>")
		return 2
	}
	if strings.TrimSpace(*cancelID) != "" {
		if *claim || *list || strings.TrimSpace(*renewID) != "" || strings.TrimSpace(*failID) != "" || strings.TrimSpace(*claimFile) != "" || strings.TrimSpace(*worker) != "" || strings.TrimSpace(*connector) != "" || *maxClaims != 1 {
			fmt.Fprintln(stderr, "usage: workgraph capture requests --cancel <id> [--reason <text>]")
			return 2
		}
		if err := workgraph.CancelCaptureRequest(workgraph.CaptureRequestCancelConfig{
			HomeDir: *homeDir, DatabasePath: *databasePath, RequestID: *cancelID, Reason: *cancelReason,
		}); err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests cancel: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Capture request cancelled\nRequest: %s\n", strings.TrimSpace(*cancelID))
		return 0
	}
	if strings.TrimSpace(*cancelReason) != "" {
		fmt.Fprintln(stderr, "workgraph capture requests: --reason requires --cancel")
		return 2
	}
	capabilityOperation := strings.TrimSpace(*renewID) != "" || strings.TrimSpace(*failID) != ""
	if capabilityOperation {
		if *claim || *list || (strings.TrimSpace(*renewID) != "" && strings.TrimSpace(*failID) != "") || strings.TrimSpace(*claimFile) == "" {
			fmt.Fprintln(stderr, "usage: workgraph capture requests (--renew <id> | --fail <id>) --claim-file <path>")
			return 2
		}
		requestID := strings.TrimSpace(*renewID)
		if requestID == "" {
			requestID = strings.TrimSpace(*failID)
		}
		claimRequestID, claimToken, err := readCaptureClaimFile(*claimFile)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests: %v\n", err)
			return 1
		}
		if claimRequestID != requestID {
			fmt.Fprintln(stderr, "workgraph capture requests: claim file does not match request")
			return 1
		}
		config := workgraph.CaptureRequestCapabilityConfig{
			HomeDir: *homeDir, DatabasePath: *databasePath, RequestID: requestID, ClaimToken: claimToken,
		}
		if strings.TrimSpace(*renewID) != "" {
			request, err := workgraph.RenewCaptureRequest(config)
			if err != nil {
				fmt.Fprintf(stderr, "workgraph capture requests renew: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "Capture request renewed\nRequest: %s\nLease expires: %s\n", request.ID, request.LeaseExpiresAt)
			return 0
		}
		if *errorJSON != "" {
			if *errorJSON != "-" {
				fmt.Fprintln(stderr, "workgraph capture requests: --error-json currently supports stdin (-) only")
				return 2
			}
			var details struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(stdin).Decode(&details); err != nil {
				fmt.Fprintf(stderr, "workgraph capture requests: parse failure JSON: %v\n", err)
				return 1
			}
			config.Error = details.Error
		}
		if err := workgraph.FailCaptureRequest(config); err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests fail: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Capture request returned for retry\nRequest: %s\n", requestID)
		return 0
	}
	if !*claim {
		if strings.TrimSpace(*worker) != "" || strings.TrimSpace(*claimFile) != "" {
			fmt.Fprintln(stderr, "workgraph capture requests: --worker and --claim-file require --claim")
			return 2
		}
		requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{
			HomeDir: *homeDir, DatabasePath: *databasePath, ConnectorID: *connector,
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Capture requests")
		if len(requests) == 0 {
			fmt.Fprintln(stdout, "No capture requests.")
		}
		for _, request := range requests {
			line := fmt.Sprintf("- %s: %s, connector %s, %s, %s to %s, attempts %d", request.ID, request.Status, request.ConnectorID, request.CaptureSemantics, request.Since, request.Until, request.Attempts)
			if request.ClaimedBy != "" {
				line += ", worker " + request.ClaimedBy + ", lease expires " + request.LeaseExpiresAt
			}
			fmt.Fprintln(stdout, line)
		}
		return 0
	}
	if *list {
		fmt.Fprintln(stderr, "workgraph capture requests: choose either --list or --claim")
		return 2
	}
	if strings.TrimSpace(*worker) == "" || strings.TrimSpace(*claimFile) == "" {
		fmt.Fprintln(stderr, "usage: workgraph capture requests --claim [--connector X] [--max N] --worker <name> --claim-file <path>")
		return 2
	}
	if *maxClaims != 1 {
		fmt.Fprintln(stderr, "workgraph capture requests: CLI claim files currently require --max 1")
		return 2
	}
	claimed, err := workgraph.ClaimCaptureRequests(workgraph.CaptureRequestClaimConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ConnectorID:  *connector,
		Worker:       *worker,
		Max:          *maxClaims,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture requests: %v\n", err)
		return 1
	}
	if len(claimed) == 0 {
		fmt.Fprintln(stdout, "No capture requests available.")
		return 0
	}
	claimContents, err := json.MarshalIndent(struct {
		RequestID  string `json:"request_id"`
		ClaimToken string `json:"claim_token"`
	}{RequestID: claimed[0].Request.ID, ClaimToken: claimed[0].ClaimToken}, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture requests: encode claim file: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*claimFile, append(claimContents, '\n'), 0o600); err != nil {
		fmt.Fprintf(stderr, "workgraph capture requests: write claim file: %v\n", err)
		return 1
	}
	if err := os.Chmod(*claimFile, 0o600); err != nil {
		fmt.Fprintf(stderr, "workgraph capture requests: secure claim file: %v\n", err)
		return 1
	}
	request := claimed[0].Request
	fmt.Fprintf(stdout, "Capture request claimed\nRequest: %s\nConnector: %s\nSource: %s\nCapture semantics: %s\nSince: %s\nUntil: %s\nParams: %s\nLease expires: %s\nClaim file: %s\n",
		request.ID, request.ConnectorID, request.Source, request.CaptureSemantics, request.Since, request.Until, request.Params, request.LeaseExpiresAt, *claimFile)
	return 0
}

func runCaptureWatermark(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("capture watermark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	connector := flags.String("connector", "", "connector id")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*connector) == "" {
		fmt.Fprintln(stderr, "usage: workgraph capture watermark --connector <connector>")
		return 2
	}
	watermark, err := workgraph.CaptureWatermark(workgraph.CaptureRequestListConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, ConnectorID: *connector,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture watermark: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, watermark)
	return 0
}

func runCaptureIngest(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("capture ingest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	source := flags.String("source", "", "connector source id")
	requestID := flags.String("request", "", "claimed capture request id")
	claimFile := flags.String("claim-file", "", "file containing the claim capability")
	jsonInput := flags.String("json", "-", "JSON input path; - reads stdin")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	manual := strings.TrimSpace(*source) != ""
	scheduled := strings.TrimSpace(*requestID) != "" && strings.TrimSpace(*claimFile) != ""
	if flags.NArg() != 0 || manual == scheduled {
		fmt.Fprintln(stderr, "usage: workgraph capture ingest (--source <connector> | --request <id> --claim-file <path>) [--json -]")
		return 2
	}
	if *jsonInput != "-" {
		fmt.Fprintln(stderr, "workgraph capture ingest: --json currently supports stdin (-) only")
		return 1
	}
	claimToken := ""
	if scheduled {
		claimRequestID, token, err := readCaptureClaimFile(*claimFile)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph capture ingest: %v\n", err)
			return 1
		}
		if claimRequestID != *requestID {
			fmt.Fprintln(stderr, "workgraph capture ingest: claim file does not match request")
			return 1
		}
		claimToken = token
	}
	result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Source:       *source,
		RequestID:    *requestID,
		ClaimToken:   claimToken,
		Input:        stdin,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture ingest: %v\n", err)
		return 1
	}
	if result.WeakDedupe > 0 {
		fmt.Fprintf(stderr, "workgraph capture ingest warning: %d event(s) omitted external_id; weak dedupe was used\n", result.WeakDedupe)
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func readCaptureClaimFile(path string) (string, string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read claim file: %w", err)
	}
	var claim struct {
		RequestID  string `json:"request_id"`
		ClaimToken string `json:"claim_token"`
	}
	if err := json.Unmarshal(contents, &claim); err != nil {
		return "", "", fmt.Errorf("parse claim file: %w", err)
	}
	if strings.TrimSpace(claim.RequestID) == "" || strings.TrimSpace(claim.ClaimToken) == "" {
		return "", "", fmt.Errorf("claim file is missing request_id or claim_token")
	}
	return claim.RequestID, claim.ClaimToken, nil
}

func runConnectorsList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	result, err := workgraph.ListConnectors(workgraph.ConnectorListConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	result, err := workgraph.StatusConnectors(workgraph.ConnectorListConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors status: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors doctor")
		return 2
	}
	result, err := workgraph.DoctorConnectors(workgraph.ConnectorListConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsUpgrade(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors upgrade", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors upgrade")
		return 2
	}
	result, err := workgraph.UpgradeConnectors(workgraph.ConnectorListConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors upgrade: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsValidate(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	gh := flags.String("gh", "", "gh-compatible executable for GitHub auth validation")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph connectors validate <connector>")
		return 2
	}
	result, err := workgraph.ValidateConnector(workgraph.ConnectorValidateConfig{
		HomeDir:       *homeDir,
		ID:            flags.Arg(0),
		GitHubCommand: *gh,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors validate: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsPoll(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors poll", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	connector := flags.String("connector", "", "Connector id to poll")
	once := flags.Bool("once", false, "Poll once and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors poll --once [--connector <connector>]")
		return 2
	}
	result, err := workgraph.PollConnectors(workgraph.ConnectorPollConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           *connector,
		Once:         *once,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors poll: %v\n", err)
		if result.Message != "" {
			fmt.Fprintln(stdout, result.Message)
		}
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsEnable(args []string, stdout io.Writer, stderr io.Writer) int {
	return runConnectorsSetEnabled("enable", args, stdout, stderr, true)
}

func runConnectorsDisable(args []string, stdout io.Writer, stderr io.Writer) int {
	return runConnectorsSetEnabled("disable", args, stdout, stderr, false)
}

func runConnectorsSetEnabled(command string, args []string, stdout io.Writer, stderr io.Writer, enabled bool) int {
	flags := flag.NewFlagSet("connectors "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: workgraph connectors %s <connector>\n", command)
		return 2
	}
	result, err := workgraph.SetConnectorEnabled(workgraph.ConnectorUpdateConfig{
		HomeDir: *homeDir,
		ID:      flags.Arg(0),
		Enabled: enabled,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors %s: %v\n", command, err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsInterval(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors interval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: workgraph connectors interval <connector> <duration>")
		return 2
	}
	interval, err := time.ParseDuration(flags.Arg(1))
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors interval: parse duration: %v\n", err)
		return 1
	}
	result, err := workgraph.SetConnectorInterval(workgraph.ConnectorUpdateConfig{
		HomeDir:  *homeDir,
		ID:       flags.Arg(0),
		Interval: interval,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors interval: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotion(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph notion <command>")
		return 2
	}

	switch args[0] {
	case "capture":
		return runNotionCapture(args[1:], stdout, stderr)
	case "connect":
		return runNotionConnect(args[1:], stdout, stderr)
	case "connect-token":
		return runNotionConnectToken(args[1:], stdout, stderr)
	case "disconnect":
		return runNotionDisconnect(args[1:], stdout, stderr)
	case "index":
		return runNotionIndex(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown notion command: %s\n", args[0])
		return 2
	}
}

func runNotionIndex(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph notion index <list|show>")
		return 2
	}
	switch args[0] {
	case "list":
		return runNotionIndexList(args[1:], stdout, stderr)
	case "show":
		return runNotionIndexShow(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown notion index command: %s\n", args[0])
		return 2
	}
}

func runNotionIndexList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion index list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	limit := flags.Int("limit", 25, "Maximum number of indexed Notion objects to show")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	result, err := workgraph.ListNotionIndex(workgraph.NotionIndexListConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion index list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionIndexShow(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph notion index show <notion-id>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("notion index show", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	result, err := workgraph.ShowNotionIndex(workgraph.NotionIndexShowConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion index show: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	token := flags.String("token", os.Getenv("WORKGRAPH_NOTION_TOKEN"), "Notion access token")
	notionAPIBaseURL := flags.String("notion-api-base", "", "Notion API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureNotion(workgraph.NotionCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Token:        *token,
		APIBaseURL:   *notionAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", "", "Notion OAuth client id")
	redirectURI := flags.String("redirect-uri", "", "Notion OAuth redirect URI")
	code := flags.String("code", "", "Notion OAuth code")
	state := flags.String("state", "", "Notion OAuth state")
	expectedState := flags.String("expected-state", "", "Expected Notion OAuth state")
	noBrowser := flags.Bool("no-browser", false, "Print the authorization URL instead of opening a browser")
	authBaseURL := flags.String("notion-auth-base", "", "Notion authorization URL")
	tokenURL := flags.String("notion-token-url", "", "Notion token relay URL")
	notionAPIBaseURL := flags.String("notion-api-base", "", "Notion API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	config := workgraph.NotionConnectConfig{
		HomeDir:       *homeDir,
		ClientID:      *clientID,
		RedirectURI:   *redirectURI,
		Code:          *code,
		State:         *state,
		ExpectedState: *expectedState,
		AuthBaseURL:   *authBaseURL,
		TokenURL:      *tokenURL,
		APIBaseURL:    *notionAPIBaseURL,
	}
	var result workgraph.NotionConnectResult
	var err error
	if *code == "" && !*noBrowser {
		result, err = workgraph.ConnectNotionWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectNotion(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionConnectToken(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion connect-token", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	token := flags.String("token", "", "Notion internal integration token")
	notionAPIBaseURL := flags.String("notion-api-base", "", "Notion API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph notion connect-token --token <token>")
		return 2
	}

	result, err := workgraph.ConnectNotionWithToken(workgraph.NotionConnectTokenConfig{
		HomeDir:    *homeDir,
		Token:      *token,
		APIBaseURL: *notionAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion connect-token: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion disconnect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectNotion(workgraph.NotionDisconnectConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMail(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph mail <command>")
		return 2
	}

	switch args[0] {
	case "capture":
		return runMailCapture(args[1:], stdout, stderr)
	case "connect":
		return runMailConnect(args[1:], stdout, stderr)
	case "disconnect":
		return runMailDisconnect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown mail command: %s\n", args[0])
		return 2
	}
}

func runMailCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("mail capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	provider := flags.String("provider", "", "Mail provider to capture from")
	mailboxID := flags.String("mailbox-id", "", "Provider mailbox id")
	token := flags.String("token", os.Getenv("WORKGRAPH_MAIL_TOKEN"), "Mail provider access token")
	clientID := flags.String("client-id", "", "Mail provider OAuth client id")
	tokenURL := flags.String("mail-token-url", "", "Mail provider token URL")
	mailAPIBaseURL := flags.String("mail-api-base", "", "Mail API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureMailMessages(workgraph.MailCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Provider:     *provider,
		MailboxID:    *mailboxID,
		Token:        *token,
		ClientID:     *clientID,
		TokenURL:     *tokenURL,
		APIBaseURL:   *mailAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph mail capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMailConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph mail connect <provider>")
		return 2
	}
	provider := args[0]

	flags := flag.NewFlagSet("mail connect "+provider, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", "", "Mail provider OAuth client id")
	redirectURI := flags.String("redirect-uri", "", "Mail provider OAuth redirect URI")
	code := flags.String("code", "", "Mail provider OAuth code")
	codeVerifier := flags.String("code-verifier", "", "Mail provider OAuth PKCE code verifier")
	state := flags.String("state", "", "Mail provider OAuth state")
	expectedState := flags.String("expected-state", "", "Expected Mail provider OAuth state")
	noBrowser := flags.Bool("no-browser", false, "Print the authorization URL instead of opening a browser")
	authBaseURL := flags.String("mail-auth-base", "", "Mail provider authorization URL")
	tokenURL := flags.String("mail-token-url", "", "Mail provider token URL")
	mailAPIBaseURL := flags.String("mail-api-base", "", "Mail API base URL")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	config := workgraph.MailConnectConfig{
		HomeDir:       *homeDir,
		Provider:      provider,
		ClientID:      *clientID,
		RedirectURI:   *redirectURI,
		Code:          *code,
		CodeVerifier:  *codeVerifier,
		State:         *state,
		ExpectedState: *expectedState,
		AuthBaseURL:   *authBaseURL,
		TokenURL:      *tokenURL,
		APIBaseURL:    *mailAPIBaseURL,
	}
	var result workgraph.MailConnectResult
	var err error
	if *code == "" && !*noBrowser {
		result, err = workgraph.ConnectMailWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectMail(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph mail connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMailDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph mail disconnect <provider>")
		return 2
	}
	provider := args[0]

	flags := flag.NewFlagSet("mail disconnect "+provider, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	revokeURL := flags.String("mail-revoke-url", "", "Mail provider token revoke URL")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectMail(workgraph.MailDisconnectConfig{
		HomeDir:   *homeDir,
		Provider:  provider,
		RevokeURL: *revokeURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph mail disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runInit(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	force := flags.Bool("force", false, "Refresh init-owned defaults such as settings.json")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Force:        *force,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph init: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCalendar(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph calendar <command>")
		return 2
	}

	switch args[0] {
	case "capture":
		return runCalendarCapture(args[1:], stdout, stderr)
	case "connect":
		return runCalendarConnect(args[1:], stdout, stderr)
	case "disconnect":
		return runCalendarDisconnect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown calendar command: %s\n", args[0])
		return 2
	}
}

func runCalendarConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph calendar connect <provider>")
		return 2
	}
	provider := args[0]

	flags := flag.NewFlagSet("calendar connect "+provider, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", "", "Calendar provider OAuth client id")
	redirectURI := flags.String("redirect-uri", "", "Calendar provider OAuth redirect URI")
	code := flags.String("code", "", "Calendar provider OAuth code")
	codeVerifier := flags.String("code-verifier", "", "Calendar provider OAuth PKCE code verifier")
	state := flags.String("state", "", "Calendar provider OAuth state")
	expectedState := flags.String("expected-state", "", "Expected Calendar provider OAuth state")
	noBrowser := flags.Bool("no-browser", false, "Print the authorization URL instead of opening a browser")
	calendarIDs := watchDirFlags{}
	flags.Var(&calendarIDs, "calendar-id", "Provider calendar id to collect after connecting")
	authBaseURL := flags.String("calendar-auth-base", "", "Calendar provider authorization URL")
	tokenURL := flags.String("calendar-token-url", "", "Calendar provider token URL")
	calendarAPIBaseURL := flags.String("calendar-api-base", "", "Calendar API base URL")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	config := workgraph.CalendarConnectConfig{
		HomeDir:       *homeDir,
		Provider:      provider,
		ClientID:      *clientID,
		RedirectURI:   *redirectURI,
		Code:          *code,
		CodeVerifier:  *codeVerifier,
		State:         *state,
		ExpectedState: *expectedState,
		CalendarIDs:   calendarIDs,
		AuthBaseURL:   *authBaseURL,
		TokenURL:      *tokenURL,
		APIBaseURL:    *calendarAPIBaseURL,
	}
	var result workgraph.CalendarConnectResult
	var err error
	if *code == "" && !*noBrowser {
		result, err = workgraph.ConnectCalendarWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectCalendar(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph calendar connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCalendarDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph calendar disconnect <provider>")
		return 2
	}
	provider := args[0]

	flags := flag.NewFlagSet("calendar disconnect "+provider, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	revokeURL := flags.String("calendar-revoke-url", "", "Calendar provider token revoke URL")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectCalendar(workgraph.CalendarDisconnectConfig{
		HomeDir:   *homeDir,
		Provider:  provider,
		RevokeURL: *revokeURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph calendar disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCalendarCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("calendar capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventsFile := flags.String("events-file", "", "Calendar event export JSON file")
	provider := flags.String("provider", "", "Calendar provider to capture from")
	calendarID := flags.String("calendar-id", "", "Provider calendar id")
	token := flags.String("token", os.Getenv("WORKGRAPH_CALENDAR_TOKEN"), "Calendar provider access token")
	clientID := flags.String("client-id", os.Getenv("WORKGRAPH_GOOGLE_CLIENT_ID"), "Calendar provider OAuth client id")
	tokenURL := flags.String("calendar-token-url", "", "Calendar provider token URL")
	calendarAPIBaseURL := flags.String("calendar-api-base", "", "Calendar API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureCalendarEvents(workgraph.CalendarCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		EventsFile:   *eventsFile,
		Provider:     *provider,
		CalendarID:   *calendarID,
		Token:        *token,
		ClientID:     *clientID,
		TokenURL:     *tokenURL,
		APIBaseURL:   *calendarAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph calendar capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlack(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph slack <command>")
		return 2
	}

	switch args[0] {
	case "capture":
		return runSlackCapture(args[1:], stdout, stderr)
	case "connect":
		return runSlackConnect(args[1:], stdout, stderr)
	case "disconnect":
		return runSlackDisconnect(args[1:], stdout, stderr)
	case "lists":
		return runSlackLists(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown slack command: %s\n", args[0])
		return 2
	}
}

func runAzure(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph azure <boards>")
		return 2
	}
	switch args[0] {
	case "boards":
		return runAzureBoards(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown azure command: %s\n", args[0])
		return 2
	}
}

func runAzureBoards(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph azure boards <connect|capture|disconnect>")
		return 2
	}
	switch args[0] {
	case "connect":
		return runAzureBoardsConnect(args[1:], stdout, stderr)
	case "capture":
		return runAzureBoardsCapture(args[1:], stdout, stderr)
	case "disconnect":
		return runAzureBoardsDisconnect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown azure boards command: %s\n", args[0])
		return 2
	}
}

func runAzureBoardsConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("azure boards connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", os.Getenv("WORKGRAPH_AZURE_DEVOPS_CLIENT_ID"), "Azure DevOps OAuth client id")
	redirectURI := flags.String("redirect-uri", workgraph.DefaultAzureBoardsRedirectURI, "Azure Boards OAuth redirect URI")
	code := flags.String("code", "", "Azure Boards OAuth code returned to the redirect URI")
	codeVerifier := flags.String("code-verifier", "", "Azure Boards OAuth PKCE verifier")
	state := flags.String("state", "", "Azure Boards OAuth state")
	expectedState := flags.String("expected-state", "", "Expected Azure Boards OAuth state")
	organization := flags.String("organization", "", "Azure DevOps organization name")
	project := flags.String("project", "", "Azure DevOps project name")
	team := flags.String("team", "", "Azure DevOps team name")
	wiql := flags.String("wiql", "", "Custom Azure Boards WIQL query")
	authBaseURL := flags.String("azure-auth-base", "", "Azure Boards OAuth authorization base URL")
	tokenURL := flags.String("azure-token-url", "", "Azure Boards OAuth token URL")
	apiBaseURL := flags.String("azure-api-base", "", "Azure DevOps API base URL")
	noBrowser := flags.Bool("no-browser", false, "Print the OAuth URL instead of opening a browser")
	areaPaths := watchDirFlags{}
	flags.Var(&areaPaths, "area-path", "Azure Boards area path to include")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	config := workgraph.AzureBoardsConnectConfig{
		HomeDir:       *homeDir,
		ClientID:      *clientID,
		RedirectURI:   *redirectURI,
		Code:          *code,
		CodeVerifier:  *codeVerifier,
		State:         *state,
		ExpectedState: *expectedState,
		Organization:  *organization,
		Project:       *project,
		Team:          *team,
		AreaPaths:     areaPaths,
		WIQL:          *wiql,
		AuthBaseURL:   *authBaseURL,
		TokenURL:      *tokenURL,
		APIBaseURL:    *apiBaseURL,
	}
	var result workgraph.AzureBoardsResult
	var err error
	if *code == "" && !*noBrowser {
		result, err = workgraph.ConnectAzureBoardsWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectAzureBoards(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph azure boards connect: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runAzureBoardsCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("azure boards capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	token := flags.String("token", os.Getenv("WORKGRAPH_AZURE_DEVOPS_TOKEN"), "Azure DevOps access token")
	organization := flags.String("organization", "", "Azure DevOps organization name")
	project := flags.String("project", "", "Azure DevOps project name")
	team := flags.String("team", "", "Azure DevOps team name")
	wiql := flags.String("wiql", "", "Custom Azure Boards WIQL query")
	apiBaseURL := flags.String("azure-api-base", "", "Azure DevOps API base URL")
	areaPaths := watchDirFlags{}
	flags.Var(&areaPaths, "area-path", "Azure Boards area path to include")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureAzureBoards(workgraph.AzureBoardsCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Token:        *token,
		Organization: *organization,
		Project:      *project,
		Team:         *team,
		AreaPaths:    areaPaths,
		WIQL:         *wiql,
		APIBaseURL:   *apiBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph azure boards capture: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runAzureBoardsDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("azure boards disconnect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectAzureBoards(workgraph.AzureBoardsCaptureConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph azure boards disconnect: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlackDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack disconnect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectSlack(workgraph.SlackDisconnectConfig{
		HomeDir:    *homeDir,
		APIBaseURL: *slackAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph slack disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlackCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventsFile := flags.String("events-file", "", "Slack event export JSON file")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureSlackEvents(workgraph.SlackCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		EventsFile:   *eventsFile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph slack capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlackLists(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph slack lists <capture>")
		return 2
	}
	switch args[0] {
	case "capture":
		return runSlackListsCapture(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown slack lists command: %s\n", args[0])
		return 2
	}
}

func runSlackListsCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack lists capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	token := flags.String("token", os.Getenv("WORKGRAPH_SLACK_TOKEN"), "Slack API token")
	listID := flags.String("list-id", "", "Slack List id to capture")
	optionsJSON := flags.String("options-json", "", "non-secret interpretation options for this Slack List")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	var options workgraph.SlackListOptions
	if strings.TrimSpace(*optionsJSON) != "" {
		decoder := json.NewDecoder(strings.NewReader(*optionsJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&options); err != nil {
			fmt.Fprintf(stderr, "workgraph slack lists capture: options-json must be a Slack List options object: %v\n", err)
			return 1
		}
	}
	result, err := workgraph.CaptureSlackList(workgraph.SlackListCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Token:        *token,
		ListID:       *listID,
		Options:      options,
		APIBaseURL:   *slackAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph slack lists capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlackConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", os.Getenv("WORKGRAPH_SLACK_CLIENT_ID"), "Slack app client id")
	clientSecret := flags.String("client-secret", os.Getenv("WORKGRAPH_SLACK_CLIENT_SECRET"), "Slack app client secret")
	redirectURI := flags.String("redirect-uri", workgraph.DefaultSlackRedirectURI, "Slack OAuth redirect URI")
	localCallbackURI := flags.String("local-callback-uri", workgraph.DefaultSlackLocalCallbackURI, "Local Slack OAuth callback URI")
	code := flags.String("code", "", "Slack OAuth code returned to the redirect URI")
	state := flags.String("state", "", "Slack OAuth state returned to the redirect URI")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")
	channels := watchDirFlags{}
	flags.Var(&channels, "channel", "Slack channel id to collect after connecting")
	listIDs := watchDirFlags{}
	flags.Var(&listIDs, "list", "Slack List id to collect after connecting")
	listOptionsJSON := flags.String("list-options-json", "", "non-secret per-List interpretation options keyed by List id")
	includeDMs := flags.Bool("include-dms", false, "Opt into collecting Slack direct and group direct messages")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	var listOptions map[string]workgraph.SlackListOptions
	if strings.TrimSpace(*listOptionsJSON) != "" {
		decoder := json.NewDecoder(strings.NewReader(*listOptionsJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&listOptions); err != nil {
			fmt.Fprintf(stderr, "workgraph slack connect: list-options-json must be an object keyed by List id: %v\n", err)
			return 1
		}
	}
	config := workgraph.SlackConnectConfig{
		HomeDir:          *homeDir,
		ClientID:         *clientID,
		ClientSecret:     *clientSecret,
		RedirectURI:      *redirectURI,
		LocalCallbackURI: *localCallbackURI,
		Code:             *code,
		State:            *state,
		ExpectedState:    *state,
		Channels:         channels,
		ListIDs:          listIDs,
		ListOptions:      listOptions,
		IncludeDMs:       *includeDMs,
		APIBaseURL:       *slackAPIBaseURL,
	}
	var result workgraph.SlackConnectResult
	var err error
	if *code == "" {
		result, err = workgraph.ConnectSlackWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectSlack(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph slack connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGitHub(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph github <command>")
		return 2
	}

	switch args[0] {
	case "connect":
		return runGitHubConnect(args[1:], stdout, stderr)
	case "capture":
		return runGitHubCapture(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown github command: %s\n", args[0])
		return 2
	}
}

func runGitHubConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("github connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	gh := flags.String("gh", "", "gh-compatible executable for GitHub auth validation")
	paramsJSON := flags.String("params-json", "", "non-secret GitHub capture scope as a JSON object; defaults to participant scope for @me")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph github connect [--params-json '<scope-json>']")
		return 2
	}

	result, err := workgraph.ConnectGitHub(workgraph.ConnectorConnectConfig{
		HomeDir:       *homeDir,
		GitHubCommand: *gh,
		ParamsJSON:    json.RawMessage(*paramsJSON),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph github connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGitHubCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("github capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventsFile := flags.String("events-file", "", "GitHub event export JSON file")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureGitHubEvents(workgraph.GitHubCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		EventsFile:   *eventsFile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph github capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGit(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph git <command>")
		return 2
	}

	switch args[0] {
	case "connect":
		return runGitConnect(args[1:], stdout, stderr)
	case "capture":
		return runGitCapture(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown git command: %s\n", args[0])
		return 2
	}
}

func runGitConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("git connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph git connect")
		return 2
	}

	result, err := workgraph.ConnectGit(workgraph.ConnectorConnectConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph git connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMemory(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph memory <command>")
		return 2
	}

	switch args[0] {
	case "init":
		return runMemoryInit(args[1:], stdout, stderr)
	case "links":
		return runMemoryLinks(args[1:], stdout, stderr)
	case "promote":
		return runMemoryPromote(args[1:], stdout, stderr)
	case "suggest":
		return runMemorySuggest(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown memory command: %s\n", args[0])
		return 2
	}
}

func runMemoryInit(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	scope := flags.String("scope", "project", "Memory scope: project, personal, organization, or team")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	switch *scope {
	case "personal":
		if flags.NArg() != 0 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope personal")
			return 2
		}
		result, err := workgraph.InitPersonalMemory(workgraph.PersonalMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "organization":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope organization <organization>")
			return 2
		}
		result, err := workgraph.InitOrganizationMemory(workgraph.OrganizationMemoryInitConfig{
			HomeDir:      *homeDir,
			MemoryDir:    *memoryDir,
			Organization: flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "team":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope team <team>")
			return 2
		}
		result, err := workgraph.InitTeamMemory(workgraph.TeamMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
			Team:      flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "project":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] [--scope project] <project>")
			return 2
		}
		result, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
			Project:   flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown memory scope: %s\n", *scope)
		return 2
	}
}

func runMemorySuggest(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory suggest", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory suggestion scope")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph memory suggest [--home path] [--memory path] [--database path] --scope project <project>")
		return 2
	}

	result, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory suggest: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMemoryLinks(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory links", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory link scope")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph memory links [--home path] [--memory path] [--database path] --scope project <project>")
		return 2
	}

	result, err := workgraph.ListMemoryLinks(workgraph.MemoryLinksConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory links: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMemoryPromote(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory promote", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory promotion scope")
	evidenceID := flags.String("evidence", "", "Event id supporting the promoted memory")
	text := flags.String("text", "", "Curated memory text to promote")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 || *evidenceID == "" || *text == "" {
		fmt.Fprintln(stderr, "usage: workgraph memory promote [--home path] [--memory path] [--database path] --scope project --evidence event-id --text text <project>")
		return 2
	}

	result, err := workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
		EvidenceID:   *evidenceID,
		Text:         *text,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory promote: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGitCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("git capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	maxCommits := flags.Int("max-commits", 50, "Maximum recent commits to read per repository")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MaxCommits:   *maxCommits,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph git capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSettings(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph settings <command>")
		return 2
	}

	switch args[0] {
	case "add-ignore-name":
		return runSettingsIgnoreName(args[1:], false, stdout, stderr)
	case "add-ignore-path":
		return runSettingsIgnorePath(args[1:], false, stdout, stderr)
	case "add-watch":
		return runSettingsAddWatch(args[1:], stdout, stderr)
	case "get":
		return runSettingsGet(args[1:], stdout, stderr)
	case "doctor":
		return runSettingsDoctor(args[1:], stdout, stderr)
	case "remove-ignore-name":
		return runSettingsIgnoreName(args[1:], true, stdout, stderr)
	case "remove-ignore-path":
		return runSettingsIgnorePath(args[1:], true, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown settings command: %s\n", args[0])
		return 2
	}
}

func runSettingsGet(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("settings get", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	format := flags.String("format", "text", "output format: text or json")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph settings get [--format text|json]")
		return 2
	}

	result, err := workgraph.GetSettings(workgraph.SettingsGetConfig{
		HomeDir: *homeDir,
		Format:  *format,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings get: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSettingsDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("settings doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph settings doctor")
		return 2
	}

	result, err := workgraph.DoctorSettings(workgraph.SettingsDoctorConfig{
		HomeDir: *homeDir,
	})
	fmt.Fprintln(stdout, result.Message)
	if err != nil {
		return 1
	}
	return 0
}

func runSettingsAddWatch(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("settings add-watch", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	path := "."
	if flags.NArg() > 0 {
		path = flags.Arg(0)
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: workgraph settings add-watch [path]")
		return 2
	}

	result, err := workgraph.AddWatchDir(workgraph.SettingsWatchConfig{
		HomeDir: *homeDir,
		Path:    path,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings add-watch: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSettingsIgnorePath(args []string, remove bool, stdout io.Writer, stderr io.Writer) int {
	command := "add-ignore-path"
	if remove {
		command = "remove-ignore-path"
	}
	flags := flag.NewFlagSet("settings "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: workgraph settings %s <path>\n", command)
		return 2
	}

	config := workgraph.SettingsIgnoreConfig{HomeDir: *homeDir, Path: flags.Arg(0)}
	var result workgraph.SettingsIgnoreResult
	var err error
	if remove {
		result, err = workgraph.RemoveIgnorePath(config)
	} else {
		result, err = workgraph.AddIgnorePath(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings %s: %v\n", command, err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSettingsIgnoreName(args []string, remove bool, stdout io.Writer, stderr io.Writer) int {
	command := "add-ignore-name"
	if remove {
		command = "remove-ignore-name"
	}
	flags := flag.NewFlagSet("settings "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: workgraph settings %s <name>\n", command)
		return 2
	}

	config := workgraph.SettingsIgnoreConfig{HomeDir: *homeDir, Name: flags.Arg(0)}
	var result workgraph.SettingsIgnoreResult
	var err error
	if remove {
		result, err = workgraph.RemoveIgnoreName(config)
	} else {
		result, err = workgraph.AddIgnoreName(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings %s: %v\n", command, err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCaptureStart(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	foreground := flags.Bool("foreground", false, "Run capture attached to the current terminal")
	watchDirs := watchDirFlags{}
	flags.Var(&watchDirs, "watch", "Directory to watch for local work activity")
	slackToken := flags.String("slack-token", os.Getenv("WORKGRAPH_SLACK_TOKEN"), "Slack API token for read-only message collection")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")
	slackChannels := watchDirFlags{}
	flags.Var(&slackChannels, "slack-channel", "Slack channel id to collect while running")
	slackListIDs := watchDirFlags{}
	flags.Var(&slackListIDs, "slack-list", "Slack List id to collect while running")
	slackIncludeDMs := flags.Bool("slack-include-dms", false, "Opt into collecting Slack direct and group direct messages")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	if !*foreground {
		status, err := workgraph.StartDaemon(workgraph.DaemonConfig{
			HomeDir:         *homeDir,
			DatabasePath:    *databasePath,
			WatchDirs:       watchDirs,
			SlackToken:      *slackToken,
			SlackChannels:   slackChannels,
			SlackListIDs:    slackListIDs,
			SlackIncludeDMs: *slackIncludeDMs,
			SlackAPIBaseURL: *slackAPIBaseURL,
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph start: %v\n", err)
			return 1
		}

		fmt.Fprintln(stdout, status.Message)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:         *homeDir,
		DatabasePath:    *databasePath,
		WatchDirs:       watchDirs,
		SlackToken:      *slackToken,
		SlackChannels:   slackChannels,
		SlackListIDs:    slackListIDs,
		SlackIncludeDMs: *slackIncludeDMs,
		SlackAPIBaseURL: *slackAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph start: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, capture.Status.Message)

	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-capture.Events:
				if !ok {
					return
				}
				fmt.Fprintln(stdout, formatCapturedEvent(event))
			}
		}
	}()

	if err := capture.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "workgraph start: %v\n", err)
		return 1
	}
	<-eventDone

	return 0
}

func formatCapturedEvent(event workgraph.CapturedEvent) string {
	if event.Type == "git.commit" {
		if event.Project != "" && event.Summary != "" {
			return fmt.Sprintf("%s %s %s", event.Type, event.Project, event.Summary)
		}
		if event.Summary != "" {
			return fmt.Sprintf("%s %s", event.Type, event.Summary)
		}
	}
	return fmt.Sprintf("%s %s", event.Type, event.Path)
}

func runCaptureStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	config, ok := parseCaptureControlConfig("status", args, stderr)
	if !ok {
		return 2
	}

	status, err := workgraph.DaemonStatusForConfig(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph status: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, status.Message)
	return 0
}

func runCaptureStop(args []string, stdout io.Writer, stderr io.Writer) int {
	config, ok := parseCaptureControlConfig("stop", args, stderr)
	if !ok {
		return 2
	}

	status, err := workgraph.StopDaemon(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph stop: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, status.Message)
	return 0
}

func runToday(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("today", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	actor := flags.String("actor", "", "Exact event actor to include")
	involvement := flags.String("involvement", "", "Exact user involvement to include")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Actor:        *actor,
		Involvement:  *involvement,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph today: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runResume(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("resume", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	allProjects := flags.Bool("all", false, "Show all projects with captured events, including weak evidence")
	debugRelevance := flags.Bool("debug-relevance", false, "Explain why projects are shown or hidden in resume")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: workgraph resume [project]")
		return 2
	}

	project := ""
	if flags.NArg() == 1 {
		project = flags.Arg(0)
	}

	result, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:        *homeDir,
		DatabasePath:   *databasePath,
		MemoryDir:      *memoryDir,
		Project:        project,
		AllProjects:    *allProjects,
		GitEmails:      localGitEmails(),
		GitHubLogins:   localGitHubLogins(),
		DebugRelevance: *debugRelevance,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph resume: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func localGitEmails() []string {
	output, err := exec.Command("git", "config", "--global", "--get-all", "user.email").Output()
	if err != nil {
		return nil
	}
	var emails []string
	for _, line := range strings.Split(string(output), "\n") {
		email := strings.TrimSpace(line)
		if email != "" {
			emails = append(emails, email)
		}
	}
	return emails
}

func localGitHubLogins() []string {
	output, err := exec.Command("gh", "auth", "status", "--hostname", "github.com").CombinedOutput()
	if err != nil && len(output) == 0 {
		return nil
	}
	return githubLoginsFromAuthStatus(string(output))
}

func githubLoginsFromAuthStatus(output string) []string {
	seen := map[string]bool{}
	var logins []string
	for _, line := range strings.Split(output, "\n") {
		login := githubLoginFromAuthStatusLine(line)
		if login == "" || seen[login] {
			continue
		}
		seen[login] = true
		logins = append(logins, login)
	}
	return logins
}

func githubLoginFromAuthStatusLine(line string) string {
	const marker = " account "
	index := strings.Index(line, marker)
	if index == -1 {
		return ""
	}
	rest := strings.TrimSpace(line[index+len(marker):])
	if rest == "" {
		return ""
	}
	for i, char := range rest {
		if char == ' ' || char == '(' || char == '\t' {
			return strings.TrimSpace(rest[:i])
		}
	}
	return rest
}

func runCaptureWorker(args []string, stderr io.Writer) int {
	config, ok := parseCaptureControlConfig("__capture-worker", args, stderr)
	if !ok {
		return 2
	}

	if err := workgraph.RunDaemon(config); err != nil {
		fmt.Fprintf(stderr, "workgraph capture worker: %v\n", err)
		return 1
	}
	return 0
}

func runCaptureSupervisor(args []string, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture supervisor: find executable: %v\n", err)
		return 1
	}
	command := exec.Command(executable, append([]string{"__capture-worker"}, args...)...)
	command.Stdin = os.Stdin
	command.Stdout = stderr
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			if status, ok := exitError.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGTERM {
				return 0
			}
		}
		fmt.Fprintf(stderr, "workgraph capture supervisor: %v\n", err)
		return 1
	}
	return 0
}

func parseCaptureControlConfig(command string, args []string, stderr io.Writer) (workgraph.DaemonConfig, bool) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	watchDirs := watchDirFlags{}
	flags.Var(&watchDirs, "watch", "Directory to watch for local work activity")
	slackToken := flags.String("slack-token", os.Getenv("WORKGRAPH_SLACK_TOKEN"), "Slack API token for read-only message collection")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")
	slackChannels := watchDirFlags{}
	flags.Var(&slackChannels, "slack-channel", "Slack channel id to collect while running")
	slackListIDs := watchDirFlags{}
	flags.Var(&slackListIDs, "slack-list", "Slack List id to collect while running")
	slackIncludeDMs := flags.Bool("slack-include-dms", false, "Opt into collecting Slack direct and group direct messages")

	if err := flags.Parse(args); err != nil {
		return workgraph.DaemonConfig{}, false
	}

	return workgraph.DaemonConfig{
		HomeDir:         *homeDir,
		DatabasePath:    *databasePath,
		WatchDirs:       watchDirs,
		SlackToken:      *slackToken,
		SlackChannels:   slackChannels,
		SlackListIDs:    slackListIDs,
		SlackIncludeDMs: *slackIncludeDMs,
		SlackAPIBaseURL: *slackAPIBaseURL,
	}, true
}

type watchDirFlags []string

type repeatedStringFlags []string

func (flags *repeatedStringFlags) String() string {
	return fmt.Sprint([]string(*flags))
}

func (flags *repeatedStringFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
}

func (flags *watchDirFlags) String() string {
	return fmt.Sprint([]string(*flags))
}

func (flags *watchDirFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
}
