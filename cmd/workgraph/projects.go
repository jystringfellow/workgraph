package main

import (
	"fmt"
	"io"

	workgraph "github.com/jystringfellow/workgraph"
)

func runProjects(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph projects <command>")
		return 2
	}
	switch args[0] {
	case "list":
		return runProjectsList(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown projects command: %s\n", args[0])
		return 2
	}
}

func runProjectsList(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("projects list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	memory := flags.String("memory", "", "workgraph memory directory")
	database := flags.String("database", "", "workgraph SQLite database path")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph projects list [options]")
		return 2
	}
	result, err := workgraph.ListProjects(workgraph.ProjectsListConfig{HomeDir: *home, MemoryDir: *memory, DatabasePath: *database})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph projects list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}
