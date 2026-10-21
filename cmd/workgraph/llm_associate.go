package main

import (
	"fmt"
	"io"

	workgraph "github.com/jystringfellow/workgraph"
)

func runLLMAssociate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm associate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	memory := flags.String("memory", "", "workgraph memory directory")
	database := flags.String("database", "", "workgraph SQLite database path")
	dryRun := flags.Bool("dry-run", false, "Print the filtered payload without calling the model")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm associate [--dry-run] [options]")
		return 2
	}
	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{
		HomeDir:      *home,
		MemoryDir:    *memory,
		DatabasePath: *database,
		GitEmails:    workgraph.LocalGitEmails(workgraph.LocalGitIdentityConfig{HomeDir: *home}),
		DryRun:       *dryRun,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm associate: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMSources(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm sources", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	task := flags.String("for", "", "Task whose sources to show or change: associate")
	enable := flags.String("enable", "", "Source to enable: slack, slack.dm, github, git, notion, or calendar")
	disable := flags.String("disable", "", "Source to disable")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm sources --for associate [--enable <source>] [--disable <source>]")
		return 2
	}
	config := workgraph.LLMSourcesConfig{HomeDir: *home, Task: *task}
	if *enable != "" {
		config.Enable = []string{*enable}
	}
	if *disable != "" {
		config.Disable = []string{*disable}
	}
	result, err := workgraph.ConfigureLLMSources(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm sources: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}
