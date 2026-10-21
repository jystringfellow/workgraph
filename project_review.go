package workgraph

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	projectReviewAutoWindow = 24 * time.Hour
	projectListCountWindow  = 7 * 24 * time.Hour
)

var projectShellSafePattern = regexp.MustCompile(`^[A-Za-z0-9_.:/#@=+-]+$`)

type ProjectReviewConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Now          time.Time
	Location     *time.Location
	GitEmails    []string
	Auto         bool
	Since        time.Duration
}

type ProjectUnitAssignment struct {
	UnitID     string
	Project    string
	NewProject string
	Workstream string
}

type ProjectExplainResult struct {
	Unit    *ProjectUnit
	Links   []ProjectLink
	Message string
}

func ListProjectReview(config ProjectReviewConfig) ([]ProjectLink, error) {
	links, err := ReadProjectLinks(config.store(), ProjectLinksQuery{})
	if err != nil {
		return nil, err
	}
	now := config.now()
	if config.Auto {
		since := config.Since
		if since <= 0 {
			since = projectReviewAutoWindow
		}
		var auto []ProjectLink
		for _, link := range links {
			if projectLinkUndoable(link) && !link.CreatedAt.Before(now.Add(-since)) {
				auto = append(auto, link)
			}
		}
		sort.SliceStable(auto, func(i, j int) bool { return auto[i].ID > auto[j].ID })
		return auto, nil
	}
	accuracy, err := ProjectLinkAccuracy(ProjectLinkStoreConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: now})
	if err != nil {
		return nil, err
	}
	return projectReviewDefaultLinks(links, accuracy), nil
}

func projectReviewDefaultLinks(links []ProjectLink, accuracy ProjectLinkAccuracyReport) []ProjectLink {
	var proposed, undecided []ProjectLink
	for _, link := range links {
		switch {
		case link.State == "proposed":
			proposed = append(proposed, link)
		case link.State == "applied" && link.Auto && link.Decision == "":
			undecided = append(undecided, link)
		}
	}
	if accuracy.Status == "insufficient_data" {
		proposed = append(proposed, undecided...)
	}
	return proposed
}

func projectLinkUndoable(link ProjectLink) bool {
	return link.State == "applied" && link.Auto && (link.Decision == "" || link.Decision == "confirmed")
}

func ApproveProjectReviewLink(config ProjectReviewConfig, linkID int64) (ProjectLink, error) {
	links, err := ReadProjectLinks(config.store(), ProjectLinksQuery{})
	if err != nil {
		return ProjectLink{}, err
	}
	for _, link := range links {
		if link.ID != linkID || link.State != "proposed" || link.Target != "new_project" {
			continue
		}
		catalog, err := LoadProjectCatalog(config.MemoryDir)
		if err != nil {
			return ProjectLink{}, err
		}
		if !catalogHasProject(catalog, link.NewProject.Slug) {
			if err := createProjectMemory(config, link.NewProject.Slug, link.NewProject.Workstream); err != nil {
				return ProjectLink{}, err
			}
		}
	}
	var approved ProjectLink
	err = withProjectLinksTx(config.store(), func(tx *sql.Tx, now time.Time) error {
		var err error
		approved, err = approveNewProjectLinkTx(tx, now, linkID)
		return err
	})
	return approved, err
}

func AssignProjectUnit(config ProjectReviewConfig, assignment ProjectUnitAssignment) (ProjectLink, error) {
	project := assignment.Project
	if assignment.NewProject != "" {
		if err := createProjectMemory(config, assignment.NewProject, assignment.Workstream); err != nil {
			return ProjectLink{}, err
		}
		project = assignment.NewProject
	} else {
		catalog, err := LoadProjectCatalog(config.MemoryDir)
		if err != nil {
			return ProjectLink{}, err
		}
		if !catalogHasProject(catalog, project) {
			return ProjectLink{}, fmt.Errorf("project %q has no project memory; use --new to create it", project)
		}
	}
	link := ProjectLinkAssignment{UnitID: assignment.UnitID, Target: "project", Project: project}
	unit, err := findProjectUnit(config, assignment.UnitID)
	if err != nil {
		return ProjectLink{}, err
	}
	if unit != nil {
		link.UnitKind, link.Container, link.Evidence = unit.Kind, unit.Container, unit.EventIDs
	}
	return AssignProjectLink(config.store(), link)
}

func createProjectMemory(config ProjectReviewConfig, slug, workstream string) error {
	if !validWorkstreamSlug(slug) {
		return fmt.Errorf("project slug %q must be lowercase kebab-case", slug)
	}
	if workstream != "" && !validWorkstreamSlug(workstream) {
		return fmt.Errorf("workstream %q must be lowercase kebab-case", workstream)
	}
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return err
	}
	if err := requireMemoryInitHome(homeDir); err != nil {
		return err
	}
	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return err
	}
	path, _ := projectMemoryPath(memoryDir, slug)
	if err := os.MkdirAll(projectMemoryDir(memoryDir), 0o755); err != nil {
		return fmt.Errorf("create project memory directory: %w", err)
	}
	content := projectMemoryTemplate(slug)
	if workstream != "" {
		content = frontmatterDelimiter + "\nworkstream: " + workstream + "\n" + frontmatterDelimiter + "\n" + content
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("project %q already exists; use --project %s", slug, slug)
	}
	if err != nil {
		return fmt.Errorf("create project memory: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("write project memory: %w", err)
	}
	return nil
}

func findProjectUnit(config ProjectReviewConfig, unitID string) (*ProjectUnit, error) {
	units, err := ListProjectUnits(ProjectUnitsConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: config.Now, Location: config.Location, GitEmails: config.GitEmails})
	if err != nil {
		return nil, err
	}
	for _, unit := range units {
		if unit.ID == unitID {
			return &unit, nil
		}
	}
	return nil, nil
}

func ExplainProjectUnit(config ProjectReviewConfig, unitID string) (ProjectExplainResult, error) {
	if strings.TrimSpace(unitID) == "" {
		return ProjectExplainResult{}, errors.New("unit id is required")
	}
	unit, err := findProjectUnit(config, unitID)
	if err != nil {
		return ProjectExplainResult{}, err
	}
	links, err := ReadProjectLinks(config.store(), ProjectLinksQuery{UnitID: unitID})
	if err != nil {
		return ProjectExplainResult{}, err
	}
	result := ProjectExplainResult{Unit: unit, Links: links}
	result.Message = projectExplainMessage(unitID, unit, links, config)
	return result, nil
}

func projectExplainMessage(unitID string, unit *ProjectUnit, links []ProjectLink, config ProjectReviewConfig) string {
	lines := []string{"Unit " + unitID}
	if unit == nil {
		lines = append(lines, "This unit no longer exists; the links below belong to an earlier grouping.")
	} else {
		lines = append(lines,
			"Kind: "+unit.Kind,
			"Container: "+unit.Container,
			"Time: "+ProjectUnitTimeRange(*unit, config.location()),
			"Members:")
		for _, id := range unit.EventIDs {
			lines = append(lines, "- "+id)
		}
	}
	lines = append(lines, "", "Links:")
	if len(links) == 0 {
		lines = append(lines, "No links for this unit.")
	}
	var current *ProjectLink
	for index, link := range links {
		lines = append(lines, "- "+ProjectLinkLine(link))
		if projectLinkCurrent(link) {
			current = &links[index]
		}
	}
	unitArg := projectShellArg(unitID)
	options := ProjectCommandOptions(config.HomeDir, config.MemoryDir, config.DatabasePath)
	lines = append(lines, "", "Change it:",
		"  workgraph projects review --assign "+unitArg+" --project <slug>"+options,
		"  workgraph projects review --assign "+unitArg+" --new <slug> [--workstream <slug>]"+options)
	if current != nil {
		lines = append(lines, fmt.Sprintf("  workgraph projects review --reject %d%s", current.ID, options))
		if projectLinkUndoable(*current) {
			lines = append(lines, fmt.Sprintf("  workgraph projects undo %d%s", current.ID, options))
		}
	}
	return strings.Join(lines, "\n")
}

func ProjectLinkLine(link ProjectLink) string {
	parts := []string{fmt.Sprintf("Link %d: %s %s", link.ID, link.State, ProjectLinkTarget(link)), ProjectLinkOrigin(link)}
	if link.Auto {
		parts = append(parts, "auto")
	}
	if link.Decision != "" {
		parts = append(parts, "decision "+link.Decision)
	}
	return strings.Join(parts, ", ")
}

func ProjectLinkOrigin(link ProjectLink) string {
	origin := "source " + link.Source
	if link.Rule != "" {
		origin += ", rule " + link.Rule
	}
	if link.Profile != "" {
		origin += ", profile " + link.Profile
	}
	if link.RunID != "" {
		origin += ", run " + link.RunID
	}
	return origin
}

func ProjectLinkTarget(link ProjectLink) string {
	switch link.Target {
	case "project":
		return "project " + link.Project
	case "new_project":
		if link.NewProject != nil {
			return "new project " + link.NewProject.Slug
		}
		return "new project"
	default:
		return "no project"
	}
}

func ProjectUnitTimeRange(unit ProjectUnit, location *time.Location) string {
	const layout = "2006-01-02 15:04"
	return unit.Start.In(location).Format(layout) + " to " + unit.End.In(location).Format(layout)
}

func ProjectAppliedUnitCounts(config ProjectReviewConfig) (map[string]int, error) {
	links, err := ReadProjectLinks(config.store(), ProjectLinksQuery{})
	if errors.Is(err, ErrNotInitialized) {
		return map[string]int{}, nil
	}
	if err != nil {
		return nil, err
	}
	since := config.now().Add(-projectListCountWindow)
	counts := map[string]int{}
	for _, link := range links {
		if link.State != "applied" || link.Target != "project" {
			continue
		}
		if !link.CreatedAt.Before(since) || (link.Decision == "approved" && !link.DecidedAt.Before(since)) {
			counts[link.Project]++
		}
	}
	return counts, nil
}

func ProjectCommandOptions(homeDir, memoryDir, databasePath string) string {
	var options strings.Builder
	for _, option := range [][2]string{{"--home", homeDir}, {"--memory", memoryDir}, {"--database", databasePath}} {
		if option[1] != "" {
			options.WriteString(" " + option[0] + " " + projectShellArg(option[1]))
		}
	}
	return options.String()
}

func projectShellArg(value string) string {
	if projectShellSafePattern.MatchString(value) {
		return value
	}
	return memoryShellQuote(value)
}

func (config ProjectReviewConfig) store() ProjectLinkStoreConfig {
	return ProjectLinkStoreConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: config.Now}
}

func (config ProjectReviewConfig) now() time.Time {
	if config.Now.IsZero() {
		return time.Now().UTC()
	}
	return config.Now.UTC()
}

func (config ProjectReviewConfig) location() *time.Location {
	if config.Location == nil {
		return time.Local
	}
	return config.Location
}
