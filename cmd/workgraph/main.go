package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	workgraph "github.com/jystringfellow/workgraph"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph <command>")
		return 2
	}

	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "run":
		return runCapture(args[1:], stdout, stderr)
	case "today":
		return runToday(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

func runInit(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "WorkGraph home directory")
	memoryDir := flags.String("memory", "", "WorkGraph memory directory")
	databasePath := flags.String("database", "", "WorkGraph SQLite database path")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph init: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "WorkGraph home directory")
	databasePath := flags.String("database", "", "WorkGraph SQLite database path")
	watchDirs := watchDirFlags{}
	flags.Var(&watchDirs, "watch", "Directory to watch for local work activity")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		WatchDirs:    watchDirs,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph run: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, capture.Status.Message)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
				fmt.Fprintf(stdout, "%s %s\n", event.Type, event.Path)
			}
		}
	}()

	if err := capture.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "workgraph run: %v\n", err)
		return 1
	}
	<-eventDone

	return 0
}

func runToday(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("today", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "WorkGraph home directory")
	databasePath := flags.String("database", "", "WorkGraph SQLite database path")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph today: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

type watchDirFlags []string

func (flags *watchDirFlags) String() string {
	return fmt.Sprint([]string(*flags))
}

func (flags *watchDirFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
}
