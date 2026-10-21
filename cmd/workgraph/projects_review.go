package main

import (
	"bufio"
	"encoding/json"
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
	approve := flags.Int64("approve", 0, "Approve a proposed link or confirm an auto-applied link")
	reject := flags.Int64("reject", 0, "Assign no project to the unit of a proposed or applied link")
	assign := flags.String("assign", "", "Unit id to assign")
	project := flags.String("project", "", "With --assign, the catalog project slug")
	newProject := flags.String("new", "", "With --assign, create project memory with this slug and link to it")
	workstream := flags.String("workstream", "", "With --new, the workstream to declare")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	forms := 0
	for _, used := range []bool{*approve != 0, *reject != 0, *assign != ""} {
		if used {
			forms++
		}
	}
	if flags.NArg() != 0 || forms > 1 || (forms == 1 && (*auto || *jsonOutput)) ||
		(*assign != "") != (*project != "" || *newProject != "") || (*project != "" && *newProject != "") || (*workstream != "" && *newProject == "") {
		fmt.Fprintln(stderr, "usage: workgraph projects review [--auto [--since <duration>]] [--json] | --approve <link-id> | --reject <link-id> | --assign <unit-id> (--project <slug> | --new <slug> [--workstream <slug>])")
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
	if forms == 1 {
		link, err := applyProjectsReviewFlag(config, *approve, *reject, workgraph.ProjectUnitAssignment{UnitID: *assign, Project: *project, NewProject: *newProject, Workstream: *workstream})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, workgraph.ProjectLinkLine(link))
		return 0
	}
	pass, err := workgraph.LinkProjectUnits(workgraph.ProjectLinkPassConfig{HomeDir: *home, DatabasePath: *database, MemoryDir: *memory, Location: time.Local, GitEmails: config.GitEmails})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph projects review: %v\n", err)
		return 1
	}
	links, err := workgraph.ListProjectReview(config)
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
		return writeProjectsReviewJSON(links, unitsByID, stdout, stderr)
	}
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

func applyProjectsReviewFlag(config workgraph.ProjectReviewConfig, approve, reject int64, assignment workgraph.ProjectUnitAssignment) (workgraph.ProjectLink, error) {
	switch {
	case approve != 0:
		return workgraph.ApproveProjectReviewLink(config, approve)
	case reject != 0:
		return workgraph.RejectProjectLink(workgraph.ProjectLinkStoreConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath}, reject)
	default:
		return workgraph.AssignProjectUnit(config, assignment)
	}
}

func reviewProjectLinks(config workgraph.ProjectReviewConfig, links []workgraph.ProjectLink, units map[string]workgraph.ProjectUnit, scanner *bufio.Scanner, stdout, stderr io.Writer) int {
	exit := 0
	answering := true
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
		fmt.Fprintln(stdout, workgraph.ProjectLinkLine(decided))
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(stderr, "read review input: %v\n", err)
		return 1
	}
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

func writeProjectsReviewJSON(links []workgraph.ProjectLink, units map[string]workgraph.ProjectUnit, stdout, stderr io.Writer) int {
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
	if err := encoder.Encode(map[string]any{"items": items}); err != nil {
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
	flags.String("memory", "", "workgraph memory directory")
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
	fmt.Fprintf(stdout, "Undid %d auto-applied %s; each unit waits in workgraph projects review.\n", len(replacements), pluralWord(len(replacements), "link", "links"))
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
	flags.String("memory", "", "workgraph memory directory")
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
