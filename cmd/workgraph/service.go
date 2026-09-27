package main

import (
	"fmt"
	"io"

	workgraph "github.com/jystringfellow/workgraph"
)

func runService(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph service <install|status|uninstall> [--home path]")
		return 2
	}
	flags := flag.NewFlagSet("service "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected service arguments")
		return 2
	}
	message, err := workgraph.CaptureService(args[0], *home)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph service: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, message)
	return 0
}
