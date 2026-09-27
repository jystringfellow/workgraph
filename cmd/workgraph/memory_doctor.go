package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	workgraph "github.com/jystringfellow/workgraph"
)

func runMemoryDoctor(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	memory := flags.String("memory", "", "workgraph memory directory")
	database := flags.String("database", "", "workgraph SQLite database path")
	interactive := flags.Bool("interactive", false, "Ask before linking each suggested captured project")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: workgraph memory doctor [options] [project]")
		return 2
	}
	config := workgraph.MemoryDoctorConfig{HomeDir: *home, MemoryDir: *memory, DatabasePath: *database, Project: flags.Arg(0)}
	result, err := workgraph.DoctorMemory(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	if !*interactive {
		return 0
	}
	scanner := bufio.NewScanner(stdin)
	for _, candidate := range result.Candidates {
		fmt.Fprintf(stdout, "Link %q to captured project %q? [y/N] ", candidate.Title, candidate.Source)
		if !scanner.Scan() {
			break
		}
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if answer != "y" && answer != "yes" {
			continue
		}
		message, err := workgraph.UpdateMemoryProjectSources(workgraph.MemoryProjectSourcesConfig{HomeDir: *home, MemoryDir: *memory, DatabasePath: *database, Project: candidate.Project, Sources: []string{candidate.Source}})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory doctor: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, message)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "read confirmation: %v\n", err)
		return 1
	}
	return 0
}
