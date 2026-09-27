package main

import (
	"fmt"
	"io"

	workgraph "github.com/jystringfellow/workgraph"
)

func runMemorySourceLink(args []string, unlink bool, stdout, stderr io.Writer) int {
	command := "memory link"
	if unlink {
		command = "memory unlink"
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	memory := flags.String("memory", "", "workgraph memory directory")
	database := flags.String("database", "", "workgraph SQLite database path")
	sources := repeatedStringFlags{}
	flags.Var(&sources, "source", "Captured project identifier (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: workgraph %s --source <captured-project> <memory-project>\n", command)
		return 2
	}
	message, err := workgraph.UpdateMemoryProjectSources(workgraph.MemoryProjectSourcesConfig{HomeDir: *home, MemoryDir: *memory, DatabasePath: *database, Project: flags.Arg(0), Sources: sources, Unlink: unlink})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph %s: %v\n", command, err)
		return 1
	}
	fmt.Fprintln(stdout, message)
	return 0
}
