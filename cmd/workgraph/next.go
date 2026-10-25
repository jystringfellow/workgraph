package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func runNext(args []string, stdout, stderr io.Writer) int {
	action := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = args[0]
		args = args[1:]
		if action != "explain" && action != "done" && action != "snooze" {
			fmt.Fprintln(stderr, "unknown next command:", action)
			return 2
		}
	}
	name := "next"
	if action != "" {
		name += " " + action
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	database := flags.String("database", "", "workgraph SQLite database path")
	config := workgraph.NextConfig{}
	var jsonOutput, watch bool
	var duration time.Duration
	if action == "" {
		flags.StringVar(&config.Project, "project", "", "Exact project to include")
		flags.StringVar(&config.Reason, "reason", "", "Exact reason to include")
		flags.BoolVar(&jsonOutput, "json", false, "Print versioned JSON items")
		flags.BoolVar(&config.ShowIDs, "ids", false, "Show item ids in text output")
		flags.BoolVar(&config.TeamReviews, "team-reviews", false, "List every team-only review request in text output")
		flags.BoolVar(&config.Stale, "stale", false, "List pull requests with no activity in 30+ days in text output")
		flags.BoolVar(&config.IncludeUntrusted, "include-untrusted", false, "Include delimited untrusted content in JSON")
		flags.BoolVar(&watch, "watch", false, "Stream item changes as JSON Lines")
	}
	if action == "snooze" {
		flags.DurationVar(&duration, "for", 0, "How long to hide the item (for example 2h)")
	}
	id := ""
	if action != "" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id = args[0]
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if action != "" && id == "" && flags.NArg() == 1 {
		id = flags.Arg(0)
	} else if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected next arguments")
		return 2
	}
	if (action != "" && id == "") || (action == "snooze" && duration <= 0) || (watch && !jsonOutput) || (config.IncludeUntrusted && !jsonOutput) || (config.TeamReviews && jsonOutput) || (config.Stale && jsonOutput) {
		fmt.Fprintln(stderr, "usage: workgraph next [--ids] [--team-reviews] [--stale] | next --json [--watch] [--include-untrusted] | explain <id> | done <id> | snooze <id> --for <duration>")
		return 2
	}
	config.HomeDir = *home
	config.DatabasePath = *database
	var err error
	switch action {
	case "explain":
		var message string
		message, err = workgraph.ExplainNext(config, id)
		if err == nil {
			_, err = fmt.Fprintln(stdout, message)
		}
	case "done":
		err = workgraph.DoneNext(config, id)
		if err == nil {
			_, err = fmt.Fprintln(stdout, "Done:", id)
		}
	case "snooze":
		err = workgraph.SnoozeNext(config, id, duration)
		if err == nil {
			_, err = fmt.Fprintln(stdout, "Snoozed:", id)
		}
	default:
		if watch {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			err = workgraph.WatchNext(ctx, config, stdout)
			if err == context.Canceled {
				return 0
			}
		} else {
			var result workgraph.NextResult
			result, err = workgraph.Next(config)
			if err == nil {
				if jsonOutput {
					err = json.NewEncoder(stdout).Encode(result.Items)
				} else {
					_, err = fmt.Fprintln(stdout, result.Message)
				}
			}
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph %s: %v\n", name, err)
		return 1
	}
	return 0
}
