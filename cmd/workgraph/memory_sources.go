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
	workstream := flags.String("workstream", "", "Map the containers to this workstream instead of a project")
	sources := repeatedStringFlags{}
	flags.Var(&sources, "source", "Container where activity was captured (repeatable)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *workstream == "" && flags.NArg() != 1 || *workstream != "" && flags.NArg() != 0 {
		fmt.Fprintf(stderr, "usage: workgraph %s --source <container> <memory-project>\n       workgraph %s --workstream <slug> --source <container>\n", command, command)
		return 2
	}
	message, err := workgraph.UpdateMemoryProjectSources(workgraph.MemoryProjectSourcesConfig{HomeDir: *home, MemoryDir: *memory, DatabasePath: *database, Project: flags.Arg(0), Workstream: *workstream, Sources: sources, Unlink: unlink, GitEmails: workgraph.LocalGitEmails(workgraph.LocalGitIdentityConfig{HomeDir: *home, DatabasePath: *database})})
	if message != "" {
		fmt.Fprintln(stdout, message)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph %s: %v\n", command, err)
		return 1
	}
	return 0
}
