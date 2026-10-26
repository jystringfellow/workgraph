package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

const projectsReviewPrompt = "a approve, r reassign, n new project, x no project, s skip, q quit: "

type projectsReviewItem struct {
	LinkID     int64                      `json:"link_id"`
	UnitID     string                     `json:"unit_id"`
	UnitKind   string                     `json:"unit_kind"`
	Container  string                     `json:"container"`
	Start      string                     `json:"start,omitempty"`
	End        string                     `json:"end,omitempty"`
	State      string                     `json:"state"`
	Auto       bool                       `json:"auto"`
	Source     string                     `json:"source"`
	Rule       string                     `json:"rule,omitempty"`
	Profile    string                     `json:"profile,omitempty"`
	RunID      string                     `json:"run_id,omitempty"`
	Target     string                     `json:"target"`
	Project    string                     `json:"project,omitempty"`
	NewProject *workgraph.ProposedProject `json:"new_project,omitempty"`
	Kind       string                     `json:"kind,omitempty"`
	Summary    string                     `json:"summary"`
	Confidence float64                    `json:"confidence"`
	Evidence   []string                   `json:"evidence"`
	Decision   string                     `json:"decision,omitempty"`
}

func runProjectsReview(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("projects review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	memory := flags.String("memory", "", "workgraph memory directory")
	database := flags.String("database", "", "workgraph SQLite database path")
	auto := flags.Bool("auto", false, "List auto-applied links, newest first")
	since := flags.Duration("since", 24*time.Hour, "With --auto, how far back to list auto-applied links")
	jsonOutput := flags.Bool("json", false, "Print the review list as JSON without prompting")
	approve := projectLinkIDsFlag{}
	flags.Var(&approve, "approve", "Approve proposed links or confirm auto-applied links (repeatable, comma-separated)")
	reject := projectLinkIDsFlag{}
	flags.Var(&reject, "reject", "Assign no project to the units of proposed or applied links (repeatable, comma-separated)")
	assign := repeatedStringFlags{}
	flags.Var(&assign, "assign", "Unit id to assign (repeatable)")
	project := flags.String("project", "", "With --assign, the catalog project slug")
	newProject := flags.String("new", "", "With --assign, create project memory with this slug and link to it")
	workstream := flags.String("workstream", "", "With --new, the workstream to declare")
	approveAbove := flags.String("approve-above", "", "Approve every proposed LLM link at or above this confidence")
	target := flags.String("target", "", "With --approve-above, only links with this target: project or none")
	container := flags.String("container", "", "With --approve-above, only links of units in this container")
	yes := flags.Bool("yes", false, "With --approve-above, approve without asking")
	recheck := flags.Bool("recheck", false, "Replace proposed LLM links that deterministic rules now resolve")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	forms := 0
	for _, used := range []bool{len(approve) > 0, len(reject) > 0, len(assign) > 0, *approveAbove != "", *recheck} {
		if used {
			forms++
		}
	}
	if flags.NArg() != 0 || forms > 1 || (forms == 1 && (*auto || *jsonOutput)) ||
		(len(assign) > 0) != (*project != "" || *newProject != "") || (*project != "" && *newProject != "") || (*workstream != "" && *newProject == "") ||
		(*approveAbove == "" && (*target != "" || *container != "" || *yes)) {
		fmt.Fprintln(stderr, projectsReviewUsage)
		return 2
	}
	config := workgraph.ProjectReviewConfig{
		HomeDir:      *home,
		DatabasePath: *database,
		MemoryDir:    *memory,
		Location:     time.Local,
		GitEmails:    workgraph.LocalGitEmails(workgraph.LocalGitIdentityConfig{HomeDir: *home, DatabasePath: *database}),
		Auto:         *auto,
		Since:        *since,
	}
	switch {
	case *recheck:
		result, err := workgraph.RecheckProjectProposals(workgraph.ProjectRelinkConfig{HomeDir: *home, DatabasePath: *database, MemoryDir: *memory, Location: time.Local, GitEmails: config.GitEmails})
		if err != nil {
			if result.Unfinished > 0 {
				fmt.Fprintln(stdout, result.RecheckLine())
				fmt.Fprintln(stdout, result.UnfinishedLine())
			}
			fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.RecheckLine())
		return 0
	case *approveAbove != "":
		return approveProjectLinksAbove(config, *approveAbove, *target, *container, *yes, stdin, stdout, stderr)
	case len(approve) > 0:
		outcomes, err := workgraph.ApproveProjectReviewLinks(config, approve)
		return writeProjectReviewOutcomes(outcomes, err, len(approve) > 1, func(n int) string { return fmt.Sprintf("Approved %d %s", n, pluralWord(n, "link", "links")) }, stdout, stderr)
	case len(reject) > 0:
		outcomes, err := workgraph.RejectProjectLinks(workgraph.ProjectLinkStoreConfig{HomeDir: *home, DatabasePath: *database}, reject)
		return writeProjectReviewOutcomes(outcomes, err, len(reject) > 1, func(n int) string { return fmt.Sprintf("Rejected %d %s", n, pluralWord(n, "link", "links")) }, stdout, stderr)
	case len(assign) > 0:
		outcomes, err := workgraph.AssignProjectUnits(config, assign, workgraph.ProjectUnitAssignment{Project: *project, NewProject: *newProject, Workstream: *workstream})
		slug := firstNonEmptyString(*newProject, *project)
		return writeProjectReviewOutcomes(outcomes, err, len(assign) > 1, func(n int) string {
			return fmt.Sprintf("Assigned %d %s to %s", n, pluralWord(n, "unit", "units"), slug)
		}, stdout, stderr)
	}
	pass, err := workgraph.LinkProjectUnits(workgraph.ProjectLinkPassConfig{HomeDir: *home, DatabasePath: *database, MemoryDir: *memory, Location: time.Local, GitEmails: config.GitEmails})
	if workgraph.IsDatabaseReadOnly(err) {
		fmt.Fprintln(stderr, "workgraph projects review: warning: the database is read-only, so pending project links could not be refreshed")
	} else if err != nil {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	links, err := workgraph.ListProjectReview(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	accuracy, err := workgraph.ProjectLinkAccuracy(workgraph.ProjectLinkStoreConfig{HomeDir: *home, DatabasePath: *database})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	units, err := workgraph.ListProjectUnits(workgraph.ProjectUnitsConfig{HomeDir: *home, DatabasePath: *database, Location: time.Local, GitEmails: config.GitEmails})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	unitsByID := map[string]workgraph.ProjectUnit{}
	for _, unit := range units {
		unitsByID[unit.ID] = unit
	}
	if *jsonOutput {
		return writeProjectsReviewJSON(links, unitsByID, accuracy, stdout, stderr)
	}
	fmt.Fprintln(stdout, accuracy.Summary())
	if len(pass.Written) > 0 {
		fmt.Fprintf(stdout, "Linked %d %s by deterministic rules (run %s).\n", len(pass.Written), pluralWord(len(pass.Written), "unit", "units"), pass.RunID)
	}
	if len(links) == 0 {
		if *auto {
			fmt.Fprintf(stdout, "No auto-applied links in the last %s.\n", *since)
		} else {
			fmt.Fprintln(stdout, "Nothing to review.")
		}
		return 0
	}
	return reviewProjectLinks(config, links, unitsByID, bufio.NewScanner(stdin), stdout, stderr)
}

const projectsReviewUsage = "usage: workgraph projects review [--auto [--since <duration>]] [--json] | --approve <link-id>[,<link-id>...] | --reject <link-id>[,<link-id>...] | --assign <unit-id> [--assign <unit-id>...] (--project <slug> | --new <slug> [--workstream <slug>]) | --approve-above <confidence> [--target project|none] [--container <name>] [--yes] | --recheck"

type projectLinkIDsFlag []int64

func (ids *projectLinkIDsFlag) String() string {
	return fmt.Sprint([]int64(*ids))
}

func (ids *projectLinkIDsFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid link id %q", part)
		}
		*ids = append(*ids, id)
	}
	return nil
}

func approveProjectLinksAbove(config workgraph.ProjectReviewConfig, value, target, container string, confirmed bool, stdin io.Reader, stdout, stderr io.Writer) int {
	confidence, err := strconv.ParseFloat(value, 64)
	if err != nil || !workgraph.ProjectConfidenceValid(confidence) {
		fmt.Fprintf(stderr, "workgraph projects review: --approve-above takes a confidence from 0 to 1, got %q\n", value)
		return 2
	}
	if target != "" && target != "project" && target != "none" {
		fmt.Fprintf(stderr, "workgraph projects review: --target must be project or none, got %q\n", target)
		return 2
	}
	links, err := workgraph.ProjectLinksToApproveAbove(config, workgraph.ProjectApproveAboveFilter{Confidence: confidence, Target: target, Container: container})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	label := strconv.FormatFloat(confidence, 'f', -1, 64)
	if len(links) == 0 {
		fmt.Fprintf(stdout, "No proposed links at or above %s.\n", label)
		return 0
	}
	if !confirmed {
		fmt.Fprintf(stdout, "%d proposed %s at or above %s match.\n", len(links), pluralWord(len(links), "link", "links"), label)
		fmt.Fprint(stdout, "Approve them? [y/N] ")
		scanner := bufio.NewScanner(stdin)
		answer := ""
		if scanner.Scan() {
			answer = strings.ToLower(strings.TrimSpace(scanner.Text()))
		} else {
			fmt.Fprintln(stdout)
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(stderr, "read confirmation: %v\n", err)
			return 1
		}
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(stdout, "No links approved.")
			return 0
		}
	}
	outcomes, err := workgraph.ApproveProjectLinksAsListed(config, links)
	return writeProjectReviewOutcomes(outcomes, err, true, func(n int) string { return fmt.Sprintf("Approved %d %s", n, pluralWord(n, "link", "links")) }, stdout, stderr)
}

func writeProjectReviewOutcomes(outcomes []workgraph.ProjectReviewOutcome, err error, summarize bool, summary func(int) string, stdout, stderr io.Writer) int {
	if err != nil && len(outcomes) == 0 {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	decided, failed, skipped := 0, 0, 0
	for _, outcome := range outcomes {
		if errors.Is(outcome.Err, workgraph.ErrProjectLinkChanged) {
			fmt.Fprintf(stdout, "Skipped: %v\n", outcome.Err)
			skipped++
			continue
		}
		if outcome.Err != nil {
			fmt.Fprintf(stderr, "workgraph projects review: %v\n", outcome.Err)
			failed++
			continue
		}
		fmt.Fprintln(stdout, workgraph.ProjectLinkLine(outcome.Link))
		decided++
	}
	if summarize {
		line := summary(decided)
		if skipped > 0 {
			line += fmt.Sprintf("; %d skipped because %s changed after listing", skipped, pluralWord(skipped, "it", "they"))
		}
		if failed > 0 {
			line += fmt.Sprintf("; %d failed", failed)
		}
		fmt.Fprintln(stdout, line+".")
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func reviewProjectLinks(config workgraph.ProjectReviewConfig, links []workgraph.ProjectLink, units map[string]workgraph.ProjectUnit, scanner *bufio.Scanner, stdout, stderr io.Writer) int {
	exit := 0
	decidedCount := 0
	answering := true
	summarize := func() {
		if config.Auto {
			return
		}
		if left := len(links) - decidedCount; left > 0 {
			fmt.Fprintf(stdout, "%d left to decide (workgraph projects review).\n", left)
		} else {
			fmt.Fprintln(stdout, "Nothing left to decide.")
		}
	}
	read := func(prompt string) string {
		if !answering {
			return ""
		}
		fmt.Fprint(stdout, prompt)
		if !scanner.Scan() {
			answering = false
			fmt.Fprintln(stdout)
			return ""
		}
		return strings.TrimSpace(scanner.Text())
	}
	for _, link := range links {
		fmt.Fprintln(stdout, projectsReviewItemText(link, units))
		var decided workgraph.ProjectLink
		var err error
		switch key := strings.ToLower(read(projectsReviewPrompt)); key {
		case "", "s":
			continue
		case "q":
			summarize()
			return exit
		case "a":
			decided, err = workgraph.ApproveProjectReviewLink(config, link.ID)
		case "x":
			decided, err = workgraph.RejectProjectLink(workgraph.ProjectLinkStoreConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath}, link.ID)
		case "r":
			slug := read("Project slug: ")
			if slug == "" {
				continue
			}
			decided, err = workgraph.AssignProjectUnit(config, workgraph.ProjectUnitAssignment{UnitID: link.UnitID, Project: slug})
		case "n":
			slug := read("New project slug: ")
			if slug == "" {
				continue
			}
			decided, err = workgraph.AssignProjectUnit(config, workgraph.ProjectUnitAssignment{UnitID: link.UnitID, NewProject: slug, Workstream: read("Workstream (optional): ")})
		default:
			fmt.Fprintf(stdout, "Unknown choice %q; skipped.\n", key)
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
			exit = 1
			continue
		}
		decidedCount++
		fmt.Fprintln(stdout, workgraph.ProjectLinkLine(decided))
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "read review input: %v\n", err)
		return 1
	}
	summarize()
	return exit
}

func projectsReviewItemText(link workgraph.ProjectLink, units map[string]workgraph.ProjectUnit) string {
	lines := []string{fmt.Sprintf("Link %d: %s", link.ID, link.UnitID)}
	context := []string{link.Container}
	if unit, ok := units[link.UnitID]; ok {
		context = append(context, workgraph.ProjectUnitTimeRange(unit, time.Local))
	}
	if link.Kind != "" {
		context = append(context, "kind "+link.Kind)
	}
	lines = append(lines, "  "+strings.Join(context, ", "))
	state := "proposed"
	if link.State == "applied" {
		state = "auto-applied"
	}
	origin := workgraph.ProjectLinkOrigin(link)
	if link.Decision != "" {
		origin += ", " + link.Decision
	}
	lines = append(lines, fmt.Sprintf("  %s %s (%s), confidence %.2f", state, workgraph.ProjectLinkTarget(link), origin, link.Confidence))
	if link.Summary != "" {
		lines = append(lines, "  "+link.Summary)
	}
	lines = append(lines, "  evidence: "+strings.Join(link.Evidence, ", "))
	return strings.Join(lines, "\n")
}

func writeProjectsReviewJSON(links []workgraph.ProjectLink, units map[string]workgraph.ProjectUnit, accuracy workgraph.ProjectLinkAccuracyReport, stdout, stderr io.Writer) int {
	items := make([]projectsReviewItem, 0, len(links))
	for _, link := range links {
		item := projectsReviewItem{
			LinkID: link.ID, UnitID: link.UnitID, UnitKind: link.UnitKind, Container: link.Container,
			State: link.State, Auto: link.Auto, Source: link.Source, Rule: link.Rule, Profile: link.Profile, RunID: link.RunID,
			Target: link.Target, Project: link.Project, NewProject: link.NewProject, Kind: link.Kind, Summary: link.Summary,
			Confidence: link.Confidence, Evidence: link.Evidence, Decision: link.Decision,
		}
		if unit, ok := units[link.UnitID]; ok {
			item.Start, item.End = unit.Start.Format(time.RFC3339), unit.End.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"items": items, "auto_apply": accuracy.Metric()}); err != nil {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	return 0
}

func runProjectsUndo(args []string, stdout, stderr io.Writer) int {
	usage := "usage: workgraph projects undo <link-id> | --run <run-id> [options]"
	linkArg := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		linkArg, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("projects undo", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	memory := flags.String("memory", "", "workgraph memory directory")
	database := flags.String("database", "", "workgraph SQLite database path")
	runID := flags.String("run", "", "Undo every auto-applied link written by this run")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || (linkArg == "") == (*runID == "") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	store := workgraph.ProjectLinkStoreConfig{HomeDir: *home, DatabasePath: *database}
	var replacements []workgraph.ProjectLink
	if *runID != "" {
		undone, err := workgraph.UndoProjectLinkRun(store, *runID)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph projects undo: %v\n", err)
			return 1
		}
		replacements = undone
	} else {
		linkID, err := strconv.ParseInt(linkArg, 10, 64)
		if err != nil {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		replacement, err := workgraph.UndoProjectLink(store, linkID)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph projects undo: %v\n", err)
			return 1
		}
		replacements = []workgraph.ProjectLink{replacement}
	}
	if len(replacements) == 0 {
		fmt.Fprintln(stdout, "No auto-applied links to undo.")
		return 0
	}
	fmt.Fprintf(stdout, "Undid %d auto-applied %s; each unit waits in workgraph projects review%s\n", len(replacements), pluralWord(len(replacements), "link", "links"), workgraph.ProjectCommandOptions(*home, *memory, *database))
	for _, link := range replacements {
		fmt.Fprintf(stdout, "- %s: %s\n", link.UnitID, workgraph.ProjectLinkLine(link))
	}
	return 0
}

func runProjectsExplain(args []string, stdout, stderr io.Writer) int {
	usage := "usage: workgraph projects explain <unit-id> [options]"
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	unitID := args[0]
	flags := flag.NewFlagSet("projects explain", flag.ContinueOnError)
	flags.SetOutput(stderr)
	home := flags.String("home", "", "workgraph home directory")
	memory := flags.String("memory", "", "workgraph memory directory")
	database := flags.String("database", "", "workgraph SQLite database path")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	result, err := workgraph.ExplainProjectUnit(workgraph.ProjectReviewConfig{
		HomeDir:      *home,
		DatabasePath: *database,
		MemoryDir:    *memory,
		Location:     time.Local,
		GitEmails:    workgraph.LocalGitEmails(workgraph.LocalGitIdentityConfig{HomeDir: *home, DatabasePath: *database}),
	}, unitID)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph projects explain: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func pluralWord(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
