package main

import (
	stdflag "flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func runAI(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph ai <run|checkpoint|sessions|archive|unarchive|show|resume>")
		return 2
	}
	switch args[0] {
	case "run":
		return runAIRun(args[1:], stdin, stdout, stderr)
	case "checkpoint":
		return runAICheckpoint(args[1:], stdin, stdout, stderr)
	case "sessions":
		return runAISessions(args[1:], stdout, stderr)
	case "archive":
		return runAIArchive(args[1:], stdout, stderr)
	case "unarchive":
		return runAIUnarchive(args[1:], stdout, stderr)
	case "show":
		return runAIShow(args[1:], stdout, stderr)
	case "resume":
		return runAIResume(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown ai command: %s\n", args[0])
		return 2
	}
}

func runAIRun(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("ai run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", os.Getenv("WORKGRAPH_AI_HOME"), "workgraph home directory")
	databasePath := flags.String("database", os.Getenv("WORKGRAPH_AI_DATABASE"), "workgraph SQLite database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	command := flags.Args()
	if len(command) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph ai run [--home <path>] [--database <path>] -- <agent-command>")
		return 2
	}
	result, err := workgraph.RunAISession(workgraph.AIRunConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Command:      command,
		Stdin:        stdin,
		Stdout:       stdout,
		Stderr:       stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph ai run: %v\n", err)
		return 1
	}
	return result.ExitCode
}

func runAICheckpoint(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	parseArgs, positional := reorderAICheckpointArgs(args)
	flags := flag.NewFlagSet("ai checkpoint", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", os.Getenv("WORKGRAPH_AI_HOME"), "workgraph home directory")
	databasePath := flags.String("database", os.Getenv("WORKGRAPH_AI_DATABASE"), "workgraph SQLite database path")
	fromStdin := flags.Bool("stdin", false, "read structured checkpoint JSON from standard input")
	if err := flags.Parse(parseArgs); err != nil {
		return 2
	}
	explicit := make(map[string]bool)
	flags.Visit(func(option *stdflag.Flag) {
		explicit[option.Name] = true
	})
	if flags.NArg() != 0 || len(positional) > 1 {
		fmt.Fprintln(stderr, "usage: workgraph ai checkpoint [session-id] --stdin")
		return 2
	}
	if !*fromStdin {
		fmt.Fprintln(stderr, "workgraph ai checkpoint: --stdin is required")
		return 2
	}
	sessionID := ""
	if len(positional) == 1 {
		sessionID = positional[0]
	}
	result, err := workgraph.CheckpointAISession(workgraph.AICheckpointConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath,
		HomeExplicit: explicit["home"], DatabaseExplicit: explicit["database"],
		SessionID: sessionID, Input: stdin,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph ai checkpoint: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "AI checkpoint recorded")
	fmt.Fprintln(stdout, "Session:", result.SessionID)
	fmt.Fprintln(stdout, "Event:", result.EventID)
	return 0
}

func reorderAICheckpointArgs(args []string) ([]string, []string) {
	flagArgs := make([]string, 0, len(args))
	var positional []string
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--home" || argument == "--database":
			flagArgs = append(flagArgs, argument)
			if index+1 < len(args) {
				index++
				flagArgs = append(flagArgs, args[index])
			}
		case argument == "--stdin" || argument == "-h" || argument == "--help" || strings.HasPrefix(argument, "--home=") || strings.HasPrefix(argument, "--database="):
			flagArgs = append(flagArgs, argument)
		case strings.HasPrefix(argument, "-"):
			flagArgs = append(flagArgs, argument)
		default:
			positional = append(positional, argument)
		}
	}
	return flagArgs, positional
}

func runAISessions(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("ai sessions", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", os.Getenv("WORKGRAPH_AI_HOME"), "workgraph home directory")
	databasePath := flags.String("database", os.Getenv("WORKGRAPH_AI_DATABASE"), "workgraph SQLite database path")
	includeArchived := flags.Bool("all", false, "include archived AI sessions")
	archivedOnly := flags.Bool("archived", false, "show only archived AI sessions")
	status := flags.String("status", "", "filter by running, interrupted, ended, or unknown status")
	limit := flags.Int("limit", 0, "maximum number of matching sessions to show; zero is unlimited")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph ai sessions")
		return 2
	}
	result, err := workgraph.ListAISessions(workgraph.AISessionsConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath,
		IncludeArchived: *includeArchived, ArchivedOnly: *archivedOnly, Status: *status, Limit: *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph ai sessions: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runAIArchive(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("ai archive", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", os.Getenv("WORKGRAPH_AI_HOME"), "workgraph home directory")
	databasePath := flags.String("database", os.Getenv("WORKGRAPH_AI_DATABASE"), "workgraph SQLite database path")
	selectAll := flags.Bool("all", false, "select every unarchived AI session")
	status := flags.String("status", "", "select running, interrupted, ended, or unknown sessions")
	beforeText := flags.String("before", "", "select latest events strictly before YYYY-MM-DD or an RFC3339 timestamp")
	dryRun := flags.Bool("dry-run", false, "preview matching sessions without writing archive events")
	confirmed := flags.Bool("yes", false, "confirm and apply a selector-based archive batch")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	sessionIDs := flags.Args()
	if *dryRun && *confirmed {
		fmt.Fprintln(stderr, "workgraph ai archive: --dry-run and --yes cannot be combined")
		return 2
	}
	selectorPresent := *selectAll || *status != "" || *beforeText != ""
	if len(sessionIDs) > 0 {
		if selectorPresent || *dryRun || *confirmed {
			fmt.Fprintln(stderr, "workgraph ai archive: explicit session ids cannot be combined with selector, preview, or confirmation flags")
			return 2
		}
		result, err := workgraph.ArchiveAISessions(workgraph.AIArchiveBatchConfig{
			HomeDir: *homeDir, DatabasePath: *databasePath, SessionIDs: sessionIDs, Apply: true,
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph ai archive: %v\n", err)
			return 1
		}
		writeAIArchiveBatch(stdout, result, true, len(result.Matches) == 1)
		return 0
	}
	if !selectorPresent {
		fmt.Fprintln(stderr, "usage: workgraph ai archive <session-id> [<session-id>...] or workgraph ai archive (--all | --status <status> [--before <date>]) (--dry-run | --yes)")
		return 2
	}
	if *selectAll && (*status != "" || *beforeText != "") {
		fmt.Fprintln(stderr, "workgraph ai archive: --all cannot be combined with --status or --before")
		return 2
	}
	if *beforeText != "" && *status == "" {
		fmt.Fprintln(stderr, "workgraph ai archive: --before requires --status")
		return 2
	}
	var before *time.Time
	if *beforeText != "" {
		parsed, err := parseAIArchiveCutoff(*beforeText, time.Local)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph ai archive: %v\n", err)
			return 2
		}
		before = &parsed
	}
	config := workgraph.AIArchiveBatchConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath,
		Status: *status, Before: before, SelectAll: *selectAll, Apply: *confirmed,
	}
	result, err := workgraph.ArchiveAISessions(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph ai archive: %v\n", err)
		return 1
	}
	if len(result.Matches) == 0 {
		fmt.Fprintln(stdout, result.Message)
		return 0
	}
	if *dryRun {
		fmt.Fprintln(stdout, result.Message)
		return 0
	}
	if !*confirmed {
		fmt.Fprintf(stderr, "workgraph ai archive: matched %d sessions; rerun with --dry-run to preview or --yes to archive\n", len(result.Matches))
		return 2
	}
	writeAIArchiveBatch(stdout, result, true, false)
	return 0
}

func runAIUnarchive(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("ai unarchive", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", os.Getenv("WORKGRAPH_AI_HOME"), "workgraph home directory")
	databasePath := flags.String("database", os.Getenv("WORKGRAPH_AI_DATABASE"), "workgraph SQLite database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() == 0 {
		fmt.Fprintln(stderr, "usage: workgraph ai unarchive <session-id> [<session-id>...]")
		return 2
	}
	result, err := workgraph.UnarchiveAISessions(workgraph.AIArchiveBatchConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, SessionIDs: flags.Args(), Apply: true,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph ai unarchive: %v\n", err)
		return 1
	}
	writeAIArchiveBatch(stdout, result, false, len(result.Matches) == 1)
	return 0
}

func parseAIArchiveCutoff(value string, location *time.Location) (time.Time, error) {
	if parsed, err := time.ParseInLocation("2006-01-02", value, location); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	return time.Time{}, fmt.Errorf("invalid AI archive cutoff %q; use YYYY-MM-DD or RFC3339", value)
}

func writeAIArchiveBatch(output io.Writer, result workgraph.AIArchiveBatchResult, archived bool, singular bool) {
	changed := 0
	for _, transition := range result.Transitions {
		if transition.Changed {
			changed++
		}
	}
	state := "archived"
	verb := "Archived"
	if !archived {
		state = "unarchived"
		verb = "Unarchived"
	}
	if singular {
		transition := result.Transitions[0]
		if transition.Changed {
			fmt.Fprintln(output, "AI session "+state)
			fmt.Fprintln(output, "Session:", transition.SessionID)
			fmt.Fprintln(output, "Event:", transition.EventID)
			return
		}
		fmt.Fprintln(output, "AI session already "+state)
		fmt.Fprintln(output, "Session:", transition.SessionID)
		return
	}
	fmt.Fprintln(output, "AI sessions "+state)
	fmt.Fprintf(output, "Matched: %d\n", len(result.Matches))
	fmt.Fprintf(output, "%s: %d\n", verb, changed)
	fmt.Fprintf(output, "Already %s: %d\n", state, len(result.Matches)-changed)
	for _, transition := range result.Transitions {
		if !transition.Changed {
			continue
		}
		fmt.Fprintln(output, "Session:", transition.SessionID)
		fmt.Fprintln(output, "Event:", transition.EventID)
	}
}

func runAIShow(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("ai show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", os.Getenv("WORKGRAPH_AI_HOME"), "workgraph home directory")
	databasePath := flags.String("database", os.Getenv("WORKGRAPH_AI_DATABASE"), "workgraph SQLite database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph ai show <session-id>")
		return 2
	}
	result, err := workgraph.ShowAISession(workgraph.AIShowConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, SessionID: flags.Arg(0),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph ai show: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runAIResume(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("ai resume", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", os.Getenv("WORKGRAPH_AI_HOME"), "workgraph home directory")
	databasePath := flags.String("database", os.Getenv("WORKGRAPH_AI_DATABASE"), "workgraph SQLite database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph ai resume <session-id>")
		return 2
	}
	result, err := workgraph.ResumeAISession(workgraph.AIResumeConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, SessionID: flags.Arg(0),
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph ai resume: %v\n", err)
		return 1
	}
	return result.ExitCode
}

func runAINativeSessionHook(args []string, stdin io.Reader, stderr io.Writer) int {
	flags := flag.NewFlagSet("__ai-native-session", flag.ContinueOnError)
	flags.SetOutput(stderr)
	tool := flags.String("tool", "", "native AI tool identity")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *tool == "" {
		fmt.Fprintln(stderr, "workgraph AI native session hook: --tool is required")
		return 2
	}
	_, err := workgraph.BindAINativeSession(workgraph.AINativeSessionConfig{
		HomeDir: os.Getenv("WORKGRAPH_AI_HOME"), DatabasePath: os.Getenv("WORKGRAPH_AI_DATABASE"),
		SessionID: os.Getenv("WORKGRAPH_AI_SESSION_ID"), Tool: *tool, Input: stdin,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph AI native session hook: %v\n", err)
		return 1
	}
	return 0
}

func isAIRunPassthrough(args []string) bool {
	if len(args) < 3 || args[0] != "ai" || args[1] != "run" {
		return false
	}
	for _, argument := range args[2:] {
		if argument == "--" {
			return true
		}
	}
	return false
}
