package main

import (
	stdflag "flag"
	"fmt"
	"io"
	"os"
	"strings"

	workgraph "github.com/jystringfellow/workgraph"
)

func runAI(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph ai <run|checkpoint|sessions|show|resume>")
		return 2
	}
	switch args[0] {
	case "run":
		return runAIRun(args[1:], stdin, stdout, stderr)
	case "checkpoint":
		return runAICheckpoint(args[1:], stdin, stdout, stderr)
	case "sessions":
		return runAISessions(args[1:], stdout, stderr)
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
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph ai sessions")
		return 2
	}
	result, err := workgraph.ListAISessions(workgraph.AISessionsConfig{HomeDir: *homeDir, DatabasePath: *databasePath})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph ai sessions: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
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
