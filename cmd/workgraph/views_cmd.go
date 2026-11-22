package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func runSuggestions(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions <scan|list|show|approve|dismiss|snooze|complete>")
		return 2
	}
	switch args[0] {
	case "scan":
		return runSuggestionsScan(args[1:], stdout, stderr)
	case "list":
		return runSuggestionsList(args[1:], stdout, stderr)
	case "show":
		return runSuggestionsShow(args[1:], stdout, stderr)
	case "approve":
		return runSuggestionsApprove(args[1:], stdout, stderr)
	case "dismiss":
		return runSuggestionsDismiss(args[1:], stdout, stderr)
	case "snooze":
		return runSuggestionsSnooze(args[1:], stdout, stderr)
	case "complete":
		return runSuggestionsComplete(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown suggestions command: %s\n", args[0])
		return 2
	}
}

func runSuggestionsSnooze(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions snooze <id> --until <RFC3339>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions snooze "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	until := flags.String("until", "", "Future RFC3339 instant when the suggestion should resurface")
	reason := flags.String("reason", "", "Optional stable snooze reason code")
	note := flags.String("note", "", "Optional snooze note")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*until) == "" {
		fmt.Fprintln(stderr, "usage: workgraph suggestions snooze <id> --until <RFC3339>")
		return 2
	}
	suggestion, err := workgraph.SnoozeSuggestion(workgraph.SuggestionSnoozeUpdate{
		HomeDir: *homeDir, DatabasePath: *databasePath, ID: id, UntilAt: *until, ReasonCode: *reason, FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions snooze: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion snoozed\nid: %s\nstatus: %s\nuntil: %s\n", suggestion.ID, suggestion.Status, *until)
	return 0
}

func runSuggestionsComplete(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions complete <id>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions complete "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	note := flags.String("note", "", "Optional completion note")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions complete <id>")
		return 2
	}
	suggestion, err := workgraph.CompleteSuggestion(workgraph.SuggestionStatusUpdate{
		HomeDir: *homeDir, DatabasePath: *databasePath, ID: id, FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions complete: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion completed\nid: %s\nstatus: %s\n", suggestion.ID, suggestion.Status)
	return 0
}

func runReview(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	since := flags.String("since", "week", "Review window: week, 7d, or 30d")
	format := flags.String("format", "text", "Output format: text or json")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph review [--since week|7d|30d] [--format text|json]")
		return 2
	}
	result, err := workgraph.EffectivenessReview(workgraph.EffectivenessReviewConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, Since: *since, Format: *format,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph review: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsShow(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions show <id>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions show "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions show <id>")
		return 2
	}

	result, err := workgraph.ShowSuggestion(workgraph.SuggestionShowConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions show: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("suggestions list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	status := flags.String("status", "", "Suggestion status to include")
	limit := flags.Int("limit", 25, "Maximum number of suggestions to show")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions list")
		return 2
	}

	result, err := workgraph.ListSuggestions(workgraph.SuggestionListConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Status:       *status,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsScan(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("suggestions scan", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scanType := flags.String("type", "", "Deterministic scanner to run: ignore")
	limit := flags.Int("limit", 20, "Maximum number of suggestions to record and show")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions scan [--type ignore] [--limit <count>]")
		return 2
	}

	result, err := workgraph.ScanSuggestions(workgraph.SuggestionScanConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Type:         *scanType,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions scan: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsApprove(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions approve <id>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions approve "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	note := flags.String("note", "", "Optional approval note")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions approve <id>")
		return 2
	}

	suggestion, err := workgraph.ApproveSuggestion(workgraph.SuggestionStatusUpdate{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
		FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions approve: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion approved\nid: %s\nstatus: %s\n", suggestion.ID, suggestion.Status)
	return 0
}

func runSuggestionsDismiss(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions dismiss <id> --reason <code>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions dismiss "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	reason := flags.String("reason", "", "Stable dismiss reason code")
	note := flags.String("note", "", "Optional dismiss note")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions dismiss <id> --reason <code>")
		return 2
	}

	suggestion, err := workgraph.DismissSuggestion(workgraph.SuggestionStatusUpdate{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
		ReasonCode:   *reason,
		FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions dismiss: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion dismissed\nid: %s\nstatus: %s\n", suggestion.ID, suggestion.Status)
	return 0
}

func runEvents(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph events <today|query>")
		return 2
	}
	switch args[0] {
	case "today":
		return runEventsToday(args[1:], stdout, stderr)
	case "query":
		return runEventsQuery(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown events command: %s\n", args[0])
		return 2
	}
}

func runAssociations(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph associations <explain>")
		return 2
	}
	if args[0] != "explain" {
		fmt.Fprintf(stderr, "unknown associations command: %s\n", args[0])
		return 2
	}
	return runAssociationsExplain(args[1:], stdout, stderr)
}

func runAssociationsExplain(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph associations explain <event-id>")
		return 2
	}
	eventID := args[0]
	flags := flag.NewFlagSet("associations explain "+eventID, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph associations explain <event-id>")
		return 2
	}
	result, err := workgraph.ExplainEventAssociations(workgraph.AssociationExplainConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, EventID: eventID,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph associations explain: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runEventsToday(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("events today", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventType := flags.String("type", "", "Event type to include")
	project := flags.String("project", "", "Exact event project to include")
	actor := flags.String("actor", "", "Exact event actor to include")
	involvement := flags.String("involvement", "", "Exact user involvement to include")
	query := flags.String("query", "", "Case-insensitive text substring to match")
	limit := flags.Int("limit", 0, "Maximum number of matching events to show")
	format := flags.String("format", "text", "Output format: text or json")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.EventsToday(workgraph.EventsTodayConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Type:         *eventType,
		Project:      *project,
		Actor:        *actor,
		Involvement:  *involvement,
		Query:        *query,
		Limit:        *limit,
		Format:       *format,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph events today: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runEventsQuery(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("events query", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	sinceRaw := flags.String("since", "", "Inclusive lower bound (RFC3339, YYYY-MM-DD, or relative like 24h or 7d)")
	untilRaw := flags.String("until", "", "Exclusive upper bound (RFC3339 or YYYY-MM-DD)")
	source := flags.String("source", "", "Exact event source to include")
	eventType := flags.String("type", "", "Event type or trailing wildcard prefix (e.g. git.*)")
	project := flags.String("project", "", "Exact event project to include")
	actor := flags.String("actor", "", "Exact event actor to include")
	involvement := flags.String("involvement", "", "Exact user involvement to include")
	query := flags.String("query", "", "Case-insensitive text substring to match")
	limit := flags.Int("limit", 0, "Maximum number of most recent matching events to show")
	format := flags.String("format", "text", "Output format: text or json")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph events query [--since <time>] [--until <time>] [--source <source>] [--type <type>] [--project <project>] [--actor <actor>] [--involvement <kind>] [--query <text>] [--limit <count>] [--format text|json]")
		return 2
	}

	now := time.Now()
	since, err := parseEventQueryTime(*sinceRaw, now, true)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph events query: invalid --since: %v\n", err)
		return 2
	}
	until, err := parseEventQueryTime(*untilRaw, now, false)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph events query: invalid --until: %v\n", err)
		return 2
	}

	result, err := workgraph.QueryEvents(workgraph.EventsQueryConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Since:        since,
		Until:        until,
		Source:       *source,
		Type:         *eventType,
		Project:      *project,
		Actor:        *actor,
		Involvement:  *involvement,
		Query:        *query,
		Limit:        *limit,
		Format:       *format,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph events query: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func parseEventQueryTime(raw string, now time.Time, allowRelative bool) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation("2006-01-02", trimmed, now.Location()); err == nil {
		return parsed, nil
	}
	if allowRelative {
		if strings.HasSuffix(trimmed, "d") && len(trimmed) > 1 {
			if days, err := strconv.Atoi(trimmed[:len(trimmed)-1]); err == nil && days > 0 {
				return now.Add(-time.Duration(days) * 24 * time.Hour), nil
			}
		}
		if d, err := time.ParseDuration(trimmed); err == nil && d > 0 {
			return now.Add(-d), nil
		}
		return time.Time{}, fmt.Errorf("%q (expected RFC3339, YYYY-MM-DD, or positive duration like 24h or 7d)", raw)
	}
	return time.Time{}, fmt.Errorf("%q (expected RFC3339 or YYYY-MM-DD)", raw)
}

func runMemory(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph memory <command>")
		return 2
	}

	switch args[0] {
	case "init":
		return runMemoryInit(args[1:], stdout, stderr)
	case "link":
		return runMemorySourceLink(args[1:], false, stdout, stderr)
	case "doctor":
		return runMemoryDoctor(args[1:], os.Stdin, stdout, stderr)
	case "unlink":
		return runMemorySourceLink(args[1:], true, stdout, stderr)
	case "links":
		return runMemoryLinks(args[1:], stdout, stderr)
	case "promote":
		return runMemoryPromote(args[1:], stdout, stderr)
	case "suggest":
		return runMemorySuggest(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown memory command: %s\n", args[0])
		return 2
	}
}

func runMemoryInit(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	scope := flags.String("scope", "project", "Memory scope: project, personal, organization, or team")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	switch *scope {
	case "personal":
		if flags.NArg() != 0 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope personal")
			return 2
		}
		result, err := workgraph.InitPersonalMemory(workgraph.PersonalMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "organization":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope organization <organization>")
			return 2
		}
		result, err := workgraph.InitOrganizationMemory(workgraph.OrganizationMemoryInitConfig{
			HomeDir:      *homeDir,
			MemoryDir:    *memoryDir,
			Organization: flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "team":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope team <team>")
			return 2
		}
		result, err := workgraph.InitTeamMemory(workgraph.TeamMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
			Team:      flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "project":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] [--scope project] <project>")
			return 2
		}
		result, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
			Project:   flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown memory scope: %s\n", *scope)
		return 2
	}
}

func runMemorySuggest(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory suggest", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory suggestion scope")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph memory suggest [--home path] [--memory path] [--database path] --scope project <project>")
		return 2
	}

	result, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory suggest: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMemoryLinks(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory links", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory link scope")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph memory links [--home path] [--memory path] [--database path] --scope project <project>")
		return 2
	}

	result, err := workgraph.ListMemoryLinks(workgraph.MemoryLinksConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory links: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMemoryPromote(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory promote", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory promotion scope")
	evidenceID := flags.String("evidence", "", "Event id supporting the promoted memory")
	text := flags.String("text", "", "Curated memory text to promote")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 || *evidenceID == "" || *text == "" {
		fmt.Fprintln(stderr, "usage: workgraph memory promote [--home path] [--memory path] [--database path] --scope project --evidence event-id --text text <project>")
		return 2
	}

	result, err := workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
		EvidenceID:   *evidenceID,
		Text:         *text,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory promote: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runToday(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("today", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	actor := flags.String("actor", "", "Exact event actor to include")
	involvement := flags.String("involvement", "", "Exact user involvement to include")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      *homeDir,
		MemoryDir:    *memoryDir,
		DatabasePath: *databasePath,
		Actor:        *actor,
		Involvement:  *involvement,
		GitEmails:    workgraph.LocalGitEmails(workgraph.LocalGitIdentityConfig{HomeDir: *homeDir, DatabasePath: *databasePath}),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph today: %v\n", err)
		return 1
	}

	for _, warning := range result.Warnings {
		fmt.Fprintf(stderr, "workgraph today: warning: %s\n", warning)
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runResume(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("resume", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	allProjects := flags.Bool("all", false, "Show all projects with captured events, including weak evidence")
	debugRelevance := flags.Bool("debug-relevance", false, "Explain why projects are shown or hidden in resume")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: workgraph resume [project]")
		return 2
	}

	project := ""
	if flags.NArg() == 1 {
		project = flags.Arg(0)
	}

	config := workgraph.ResumeConfig{
		HomeDir:        *homeDir,
		DatabasePath:   *databasePath,
		MemoryDir:      *memoryDir,
		Project:        project,
		AllProjects:    *allProjects,
		GitEmails:      workgraph.LocalGitEmails(workgraph.LocalGitIdentityConfig{HomeDir: *homeDir, DatabasePath: *databasePath}),
		GitHubLogins:   localGitHubLogins(),
		DebugRelevance: *debugRelevance,
	}
	result, err := workgraph.Resume(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph resume: %v\n", err)
		return 1
	}

	if _, err := fmt.Fprintln(stdout, result.Message); err != nil {
		fmt.Fprintf(stderr, "workgraph resume: write output: %v\n", err)
		return 1
	}
	if project == "" && !*allProjects && !*debugRelevance {
		if err := workgraph.RecordResumeCheckIn(config, result.AsOf); err != nil {
			fmt.Fprintf(stderr, "workgraph resume: %v\n", err)
			return 1
		}
	}
	return 0
}

func localGitHubLogins() []string {
	output, err := exec.Command("gh", "auth", "status", "--hostname", "github.com").CombinedOutput()
	if err != nil && len(output) == 0 {
		return nil
	}
	return githubLoginsFromAuthStatus(string(output))
}

func githubLoginsFromAuthStatus(output string) []string {
	seen := map[string]bool{}
	var logins []string
	for _, line := range strings.Split(output, "\n") {
		login := githubLoginFromAuthStatusLine(line)
		if login == "" || seen[login] {
			continue
		}
		seen[login] = true
		logins = append(logins, login)
	}
	return logins
}

func githubLoginFromAuthStatusLine(line string) string {
	const marker = " account "
	index := strings.Index(line, marker)
	if index == -1 {
		return ""
	}
	rest := strings.TrimSpace(line[index+len(marker):])
	if rest == "" {
		return ""
	}
	for i, char := range rest {
		if char == ' ' || char == '(' || char == '\t' {
			return strings.TrimSpace(rest[:i])
		}
	}
	return rest
}
