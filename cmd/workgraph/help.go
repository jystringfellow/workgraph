package main

import (
	stdflag "flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

var flag = struct {
	NewFlagSet      func(string, stdflag.ErrorHandling) *stdflag.FlagSet
	ContinueOnError stdflag.ErrorHandling
}{
	NewFlagSet:      newCommandFlagSet,
	ContinueOnError: stdflag.ContinueOnError,
}

type helpTopic struct {
	usage       string
	description string
}

var helpTopics = map[string]helpTopic{
	"ai":                      {"workgraph ai <subcommand>", "Capture and resume durable local CLI AI session context."},
	"ai archive":              {"workgraph ai archive <session-id> [<session-id>...] [options]", "Hide AI sessions from the default list without deleting their events."},
	"ai checkpoint":           {"workgraph ai checkpoint [session-id] --stdin [options]", "Append a validated structured checkpoint to an AI session."},
	"ai resume":               {"workgraph ai resume <session-id> [options]", "Launch a verified native codex, claude, copilot, or opencode session as a linked lifetime."},
	"ai run":                  {"workgraph ai run [options] -- <agent-command>", "Launch and record one CLI AI child-process lifetime; native adapters: codex, claude, copilot, opencode."},
	"ai sessions":             {"workgraph ai sessions [--status <status>] [--limit <count>] [--all | --archived] [options]", "List known local AI sessions and their derived status."},
	"ai show":                 {"workgraph ai show <session-id> [options]", "Render stored restart context for one AI session."},
	"ai unarchive":            {"workgraph ai unarchive <session-id> [<session-id>...] [options]", "Restore archived AI sessions to the default list."},
	"associations":            {"workgraph associations <subcommand>", "Inspect deterministic relationships between captured events."},
	"associations explain":    {"workgraph associations explain <event-id> [options]", "Explain the evidence and score for an event's associations."},
	"azure":                   {"workgraph azure <subcommand>", "Connect and capture from Azure services."},
	"azure boards":            {"workgraph azure boards <subcommand>", "Connect, capture, or disconnect Azure Boards."},
	"azure boards capture":    {"workgraph azure boards capture [options]", "Capture work items from a connected Azure Boards account."},
	"azure boards connect":    {"workgraph azure boards connect --organization <name> --project <name> --team <name> [options]", "Connect an Azure Boards account with OAuth."},
	"azure boards disconnect": {"workgraph azure boards disconnect [options]", "Remove the locally stored Azure Boards connection."},
	"calendar":                {"workgraph calendar <subcommand>", "Connect, capture, or disconnect calendar providers."},
	"calendar capture":        {"workgraph calendar capture [options]", "Capture normalized calendar events from a provider or JSON export."},
	"calendar connect":        {"workgraph calendar connect <google|microsoft> [options]", "Connect a calendar provider with OAuth."},
	"calendar disconnect":     {"workgraph calendar disconnect <google|microsoft> [options]", "Revoke and remove a calendar provider connection."},
	"connectors":              {"workgraph connectors <subcommand>", "Inspect and control configured connector polling."},
	"connectors disable":      {"workgraph connectors disable <connector> [options]", "Disable automatic polling for a connector."},
	"connectors doctor":       {"workgraph connectors doctor [options]", "Diagnose connector configuration and runtime state."},
	"connectors enable":       {"workgraph connectors enable <connector> [options]", "Enable automatic polling for a connector."},
	"connectors interval":     {"workgraph connectors interval <connector> <duration> [options]", "Set a connector's automatic polling interval."},
	"connectors list":         {"workgraph connectors list [options]", "List available connector types."},
	"connectors poll":         {"workgraph connectors poll --once [--connector <connector>] [options]", "Poll enabled connectors once and exit."},
	"connectors status":       {"workgraph connectors status [options]", "Show configured connectors and their runtime state."},
	"connectors upgrade":      {"workgraph connectors upgrade [options]", "Upgrade stored connector configuration."},
	"connectors validate":     {"workgraph connectors validate <connector> [options]", "Validate a connector's local setup."},
	"doctor":                  {"workgraph doctor [options]", "Diagnose local workgraph readiness without contacting providers."},
	"events":                  {"workgraph events <subcommand>", "Inspect captured source events."},
	"events today":            {"workgraph events today [--type <event-type>] [--limit <count>] [options]", "List detailed events captured during the current local day."},
	"git":                     {"workgraph git <subcommand>", "Connect and capture local Git activity."},
	"git capture":             {"workgraph git capture [--max-commits <count>] [options]", "Capture recent commits from configured watch roots."},
	"git connect":             {"workgraph git connect [options]", "Discover Git repositories from configured watch roots."},
	"github":                  {"workgraph github <subcommand>", "Connect and capture GitHub activity."},
	"github capture":          {"workgraph github capture [options]", "Capture normalized GitHub events."},
	"github connect":          {"workgraph github connect [options]", "Validate GitHub CLI authentication and save the connection."},
	"help":                    {"workgraph help [command [subcommand ...]]", "Show root or command-specific help."},
	"init":                    {"workgraph init [--force] [options]", "Initialize or refresh local workgraph state."},
	"llm":                     {"workgraph llm <subcommand>", "Configure and use optional LLM profiles."},
	"llm add":                 {"workgraph llm add <profile> --provider <provider> --model <model> [options]", "Add or replace a local LLM profile."},
	"llm doctor":              {"workgraph llm doctor [--profile <name>] [options]", "Inspect an LLM profile's local readiness."},
	"llm hosted":              {"workgraph llm hosted <subcommand>", "Inspect or change hosted LLM opt-in state."},
	"llm hosted disable":      {"workgraph llm hosted disable [options]", "Disable hosted LLM use."},
	"llm hosted enable":       {"workgraph llm hosted enable [options]", "Explicitly opt in to hosted LLM use."},
	"llm hosted status":       {"workgraph llm hosted status [options]", "Show hosted LLM opt-in state."},
	"llm list":                {"workgraph llm list [options]", "List configured LLM profiles."},
	"llm remove":              {"workgraph llm remove <profile> [options]", "Remove a local LLM profile."},
	"llm summarize":           {"workgraph llm summarize <target> [--dry-run] [--no-stream] [options]", "Summarize a supported target with the configured LLM."},
	"llm test":                {"workgraph llm test [--profile <name>] [options]", "Verify that an LLM profile can reach and use its provider."},
	"llm use":                 {"workgraph llm use <profile> [--for <task>] [options]", "Select an LLM profile as a default or for a task."},
	"mail":                    {"workgraph mail <subcommand>", "Connect, capture, or disconnect mail providers."},
	"mail capture":            {"workgraph mail capture [options]", "Capture normalized mail events from a connected provider."},
	"mail connect":            {"workgraph mail connect <google|microsoft> [options]", "Connect a mail provider with OAuth."},
	"mail disconnect":         {"workgraph mail disconnect <google|microsoft> [options]", "Revoke and remove a mail provider connection."},
	"memory":                  {"workgraph memory <subcommand>", "Manage user-owned durable memory files and evidence links."},
	"memory init":             {"workgraph memory init [--scope <scope>] [name] [options]", "Initialize project, personal, organization, or team memory."},
	"memory links":            {"workgraph memory links --scope project <project> [options]", "List captured-event links for project memory."},
	"memory promote":          {"workgraph memory promote --scope project --evidence <event-id> --text <text> <project> [options]", "Promote reviewed event evidence into project memory."},
	"memory suggest":          {"workgraph memory suggest --scope project <project> [options]", "Suggest project-memory updates from captured evidence."},
	"network":                 {"workgraph network <subcommand>", "Inspect configured outbound network destinations."},
	"network destinations":    {"workgraph network destinations [--format text|json] [options]", "List configured destinations without contacting them."},
	"notion":                  {"workgraph notion <subcommand>", "Connect, capture, and inspect Notion content."},
	"notion capture":          {"workgraph notion capture [options]", "Capture pages and databases from a connected Notion workspace."},
	"notion connect":          {"workgraph notion connect [options]", "Connect a Notion workspace with OAuth."},
	"notion connect-token":    {"workgraph notion connect-token --token <token> [options]", "Connect a Notion internal integration token."},
	"notion disconnect":       {"workgraph notion disconnect [options]", "Remove the locally stored Notion connection."},
	"notion index":            {"workgraph notion index <subcommand>", "Inspect the locally captured Notion object index."},
	"notion index list":       {"workgraph notion index list [--limit <count>] [options]", "List locally indexed Notion objects."},
	"notion index show":       {"workgraph notion index show <notion-id> [options]", "Show one locally indexed Notion object."},
	"resume":                  {"workgraph resume [project] [--all] [--debug-relevance] [options]", "Restore context for recent work or a specific project."},
	"review":                  {"workgraph review [--since week|7d|30d] [--format text|json] [options]", "Review local suggestion effectiveness over a time window."},
	"security":                {"workgraph security <subcommand>", "Inspect local security posture."},
	"security report":         {"workgraph security report [--format text|json] [options]", "Produce a secret-free local endpoint security report."},
	"settings":                {"workgraph settings <subcommand>", "Inspect and change local workgraph settings."},
	"settings add-watch":      {"workgraph settings add-watch [path] [options]", "Add a directory to the configured watch roots."},
	"settings doctor":         {"workgraph settings doctor [options]", "Diagnose local and managed settings."},
	"settings get":            {"workgraph settings get [--format text|json] [options]", "Show effective non-secret settings and provenance."},
	"slack":                   {"workgraph slack <subcommand>", "Connect, capture, and disconnect Slack workspaces."},
	"slack capture":           {"workgraph slack capture [options]", "Capture normalized Slack events."},
	"slack connect":           {"workgraph slack connect [options]", "Connect a Slack workspace with OAuth."},
	"slack disconnect":        {"workgraph slack disconnect [options]", "Remove the locally stored Slack connection."},
	"slack lists":             {"workgraph slack lists <subcommand>", "Capture Slack Lists."},
	"slack lists capture":     {"workgraph slack lists capture --list-id <id> [options]", "Capture items from one Slack List."},
	"start":                   {"workgraph start [--foreground] [--watch <path>] [options]", "Start background or foreground work capture."},
	"status":                  {"workgraph status [options]", "Show capture daemon and connector polling status."},
	"stop":                    {"workgraph stop [options]", "Stop the background capture daemon."},
	"suggestions":             {"workgraph suggestions <subcommand>", "Inspect and manage explicit workgraph suggestions."},
	"suggestions approve":     {"workgraph suggestions approve <id> [--note <text>] [options]", "Approve a pending suggestion."},
	"suggestions complete":    {"workgraph suggestions complete <id> [--note <text>] [options]", "Record completion of an approved suggestion."},
	"suggestions dismiss":     {"workgraph suggestions dismiss <id> --reason <code> [--note <text>] [options]", "Dismiss a suggestion with explicit feedback."},
	"suggestions list":        {"workgraph suggestions list [--status <status>] [--limit <count>] [options]", "List durable suggestions."},
	"suggestions show":        {"workgraph suggestions show <id> [options]", "Show a suggestion with its evidence and history."},
	"suggestions snooze":      {"workgraph suggestions snooze <id> --until <RFC3339> [options]", "Snooze a suggestion until a future instant."},
	"today":                   {"workgraph today [options]", "Show a compact overview of work captured today."},
	"version":                 {"workgraph version", "Show the installed workgraph version and build identity."},
}

var helpExamples = map[string][]string{
	"ai archive": {
		"workgraph ai archive <session-id> <session-id>",
		"workgraph ai archive --status ended --before 2026-08-01 --dry-run",
		"workgraph ai archive --all --yes",
	},
	"ai sessions": {
		"workgraph ai sessions --status interrupted",
		"workgraph ai sessions --archived",
		"workgraph ai sessions --limit 20",
	},
}

func isHelpArgument(argument string) bool {
	return argument == "help" || argument == "-h" || argument == "--help"
}

func runHelp(path []string, allowLeafPrefix bool, stdout io.Writer, stderr io.Writer) int {
	if len(path) == 0 {
		writeRootHelp(stdout)
		return 0
	}

	key := strings.Join(path, " ")
	topic, ok := helpTopics[key]
	if !ok && allowLeafPrefix {
		key, topic, ok = leafHelpPrefix(path)
	}
	if !ok {
		fmt.Fprintf(stderr, "unknown help topic: %s\n", strings.Join(path, " "))
		fmt.Fprintln(stderr, "Run 'workgraph help' to list available commands.")
		return 2
	}

	writeTopicHelp(stdout, key, topic)
	return 0
}

func leafHelpPrefix(path []string) (string, helpTopic, bool) {
	for size := len(path) - 1; size > 0; size-- {
		key := strings.Join(path[:size], " ")
		topic, ok := helpTopics[key]
		if ok && len(immediateSubcommands(key)) == 0 {
			return key, topic, true
		}
	}
	return "", helpTopic{}, false
}

func writeRootHelp(output io.Writer) {
	fmt.Fprintln(output, "workgraph — the open substrate for personal work intelligence")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  workgraph <command> [options]")
	fmt.Fprintln(output, "  workgraph help <command>")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Commands:")
	writeSubcommands(output, "")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Run 'workgraph help <command>' or 'workgraph <command> --help' for command details.")
}

func writeTopicHelp(output io.Writer, key string, topic helpTopic) {
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintf(output, "  %s\n", topic.usage)
	fmt.Fprintln(output)
	fmt.Fprintln(output, topic.description)

	if children := immediateSubcommands(key); len(children) > 0 {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Subcommands:")
		writeSubcommands(output, key)
		fmt.Fprintln(output)
		fmt.Fprintf(output, "Run 'workgraph help %s <subcommand>' for command details.\n", key)
		return
	}

	fmt.Fprintln(output)
	writeCommandOptions(output, key)
	if examples := helpExamples[key]; len(examples) > 0 {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Examples:")
		for _, example := range examples {
			fmt.Fprintln(output, "  "+example)
		}
	}
}

func writeSubcommands(output io.Writer, parent string) {
	for _, child := range immediateSubcommands(parent) {
		topic := helpTopics[child]
		name := strings.TrimPrefix(child, parent)
		name = strings.TrimSpace(name)
		fmt.Fprintf(output, "  %-16s %s\n", name, topic.description)
	}
}

func immediateSubcommands(parent string) []string {
	parentDepth := 0
	prefix := ""
	if parent != "" {
		parentDepth = len(strings.Fields(parent))
		prefix = parent + " "
	}

	var children []string
	for key := range helpTopics {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if len(strings.Fields(key)) == parentDepth+1 {
			children = append(children, key)
		}
	}
	sort.Strings(children)
	return children
}

func newCommandFlagSet(name string, handling stdflag.ErrorHandling) *stdflag.FlagSet {
	flags := stdflag.NewFlagSet(name, handling)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Options:")
		flags.VisitAll(func(option *stdflag.Flag) {
			value := " <value>"
			if boolean, ok := option.Value.(interface{ IsBoolFlag() bool }); ok && boolean.IsBoolFlag() {
				value = ""
			}
			fmt.Fprintf(flags.Output(), "  --%s%s\n      %s\n", option.Name, value, option.Usage)
		})
	}
	return flags
}

func writeCommandOptions(output io.Writer, key string) {
	args := helpOptionArgs(key)
	_ = runCommandForOptionHelp(args, io.Discard, output)
}

func helpOptionArgs(key string) []string {
	args := strings.Fields(key)
	placeholder := ""
	switch key {
	case "associations explain":
		placeholder = "event-id"
	case "calendar connect", "calendar disconnect", "mail connect", "mail disconnect":
		placeholder = "google"
	case "llm add", "llm remove", "llm use":
		placeholder = "profile"
	case "llm summarize":
		placeholder = "today"
	case "notion index show":
		placeholder = "notion-id"
	case "suggestions approve", "suggestions complete", "suggestions dismiss", "suggestions show", "suggestions snooze":
		placeholder = "suggestion-id"
	}
	if placeholder != "" {
		args = append(args, placeholder)
	}
	return append(args, "--help")
}

func runCommandForOptionHelp(args []string, stdout io.Writer, stderr io.Writer) int {
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
	default:
		return 2
	}
}
