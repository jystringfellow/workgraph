package workgraph

import (
	"database/sql"
	"fmt"
	"time"
)

const projectLinkBatchSize = 500

type ProjectRelinkConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Now          time.Time
	Location     *time.Location
	GitEmails    []string
	Containers   []string
}

type ProjectContainerRelink struct {
	Container string
	Project   string
	Relinked  int
	Released  int
	Resolved  int
}

type ProjectRelinkResult struct {
	RunID      string
	Containers []ProjectContainerRelink
	Checked    int
	Resolved   int
	Unfinished int
}

type projectRelinkChange struct {
	unit        ProjectUnit
	current     *ProjectLink
	replacement *ProjectLinkProposal
}

type projectRelinkState struct {
	units    []ProjectUnit
	current  map[string]ProjectLink
	undone   map[string]bool
	catalog  ProjectCatalog
	mappings ContainerMappings
	resolve  func(ProjectUnit) (ProjectLinkProposal, bool, error)
	close    func()
}

func RelinkProjectContainers(config ProjectRelinkConfig) (ProjectRelinkResult, error) {
	state, err := loadProjectRelinkState(config)
	if err != nil {
		return ProjectRelinkResult{}, err
	}
	defer state.close()
	containers := map[string]int{}
	result := ProjectRelinkResult{}
	for _, name := range config.Containers {
		if _, seen := containers[name]; seen {
			continue
		}
		containers[name] = len(result.Containers)
		result.Containers = append(result.Containers, ProjectContainerRelink{Container: name, Project: state.singleMappedProject(name)})
	}
	var changes []projectRelinkChange
	for _, unit := range state.units {
		if _, selected := containers[unit.Container]; !selected {
			continue
		}
		change, ok, err := state.containerChange(unit)
		if err != nil {
			return ProjectRelinkResult{}, err
		}
		if ok {
			changes = append(changes, change)
		}
	}
	applied, runID, unfinished, err := applyProjectRelinkChanges(ProjectLinkStoreConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: config.Now}, changes)
	result.RunID, result.Unfinished = runID, unfinished
	for _, change := range applied {
		counts := &result.Containers[containers[change.unit.Container]]
		switch {
		case change.current != nil && change.current.State == "proposed":
			counts.Resolved++
		case change.replacement == nil:
			counts.Released++
		default:
			counts.Relinked++
		}
	}
	return result, err
}

func RecheckProjectProposals(config ProjectRelinkConfig) (ProjectRelinkResult, error) {
	state, err := loadProjectRelinkState(config)
	if err != nil {
		return ProjectRelinkResult{}, err
	}
	defer state.close()
	result := ProjectRelinkResult{}
	var changes []projectRelinkChange
	for _, unit := range state.units {
		current, linked := state.current[unit.ID]
		if !linked || !state.proposalReplaceable(current) {
			continue
		}
		result.Checked++
		proposal, ok, err := state.resolve(unit)
		if err != nil {
			return ProjectRelinkResult{}, err
		}
		if ok {
			changes = append(changes, projectRelinkChange{unit: unit, current: &current, replacement: &proposal})
		}
	}
	applied, runID, unfinished, err := applyProjectRelinkChanges(ProjectLinkStoreConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: config.Now}, changes)
	result.RunID, result.Resolved, result.Unfinished = runID, len(applied), unfinished
	return result, err
}

func (relink ProjectContainerRelink) Line() string {
	resolved := fmt.Sprintf("%d pending %s resolved.", relink.Resolved, projectCountNoun(relink.Resolved, "proposal", "proposals"))
	if relink.Project != "" {
		line := fmt.Sprintf("Relinked %d %s from %s to %s; %s", relink.Relinked, projectCountNoun(relink.Relinked, "unit", "units"), relink.Container, relink.Project, resolved)
		if relink.Released > 0 {
			line += fmt.Sprintf(" %d %s for categorization.", relink.Released, projectCountNoun(relink.Released, "unit waits", "units wait"))
		}
		return line
	}
	line := fmt.Sprintf("Unlinked %d %s from %s, which maps to no single project; %s", relink.Released, projectCountNoun(relink.Released, "unit", "units"), relink.Container, resolved)
	if relink.Relinked > 0 {
		line += fmt.Sprintf(" %d %s relinked by other rules.", relink.Relinked, projectCountNoun(relink.Relinked, "unit", "units"))
	}
	return line
}

func (relink ProjectContainerRelink) Changed() bool {
	return relink.Relinked+relink.Released+relink.Resolved > 0
}

func (result ProjectRelinkResult) UnfinishedLine() string {
	line := fmt.Sprintf("Relinking stopped: %d %s not changed.", result.Unfinished, projectCountNoun(result.Unfinished, "unit was", "units were"))
	if result.RunID != "" {
		line += " Committed replacements are in run " + result.RunID + "; workgraph projects undo --run " + result.RunID + " reverses them."
	}
	return line
}

func (result ProjectRelinkResult) RecheckLine() string {
	line := fmt.Sprintf("Rechecked %d pending %s; %d resolved by deterministic rules", result.Checked, projectCountNoun(result.Checked, "proposal", "proposals"), result.Resolved)
	if result.Resolved > 0 {
		line += " (run " + result.RunID + ")"
	}
	return line + "."
}

func projectCountNoun(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func loadProjectRelinkState(config ProjectRelinkConfig) (projectRelinkState, error) {
	catalog, err := LoadProjectCatalog(config.MemoryDir)
	if err != nil {
		return projectRelinkState{}, err
	}
	units, err := ListProjectUnits(ProjectUnitsConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: config.Now, Location: config.Location, GitEmails: config.GitEmails})
	if err != nil {
		return projectRelinkState{}, err
	}
	links, err := ReadProjectLinks(ProjectLinkStoreConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath}, ProjectLinksQuery{})
	if err != nil {
		return projectRelinkState{}, err
	}
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return projectRelinkState{}, err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return projectRelinkState{}, fmt.Errorf("open database: %w", err)
	}
	state, err := newProjectRelinkState(db, catalog, units, links)
	if err != nil {
		db.Close()
		return projectRelinkState{}, err
	}
	state.close = func() { db.Close() }
	return state, nil
}

func newProjectRelinkState(db *sql.DB, catalog ProjectCatalog, units []ProjectUnit, links []ProjectLink) (projectRelinkState, error) {
	mappings, err := LoadContainerMappings(db)
	if err != nil {
		return projectRelinkState{}, err
	}
	index, err := projectMemoryIdentifierIndex(catalog)
	if err != nil {
		return projectRelinkState{}, err
	}
	if err := seedProjectIdentifierIndex(db, index, units, links, nil); err != nil {
		return projectRelinkState{}, err
	}
	state := projectRelinkState{units: units, current: map[string]ProjectLink{}, undone: map[string]bool{}, catalog: catalog, mappings: mappings}
	for _, link := range links {
		if projectLinkCurrent(link) {
			state.current[link.UnitID] = link
		}
		if link.Decision == "undone" {
			state.undone[link.UnitID] = true
		}
	}
	state.resolve = func(unit ProjectUnit) (ProjectLinkProposal, bool, error) {
		return deterministicProjectLink(db, unit, index, catalog, mappings)
	}
	return state, nil
}

func (state projectRelinkState) singleMappedProject(container string) string {
	mapped := map[string]bool{}
	for _, slug := range state.mappings.Projects[container] {
		if catalogHasProject(state.catalog, slug) {
			mapped[slug] = true
		}
	}
	if len(mapped) != 1 {
		return ""
	}
	for slug := range mapped {
		return slug
	}
	return ""
}

func (state projectRelinkState) containerChange(unit ProjectUnit) (projectRelinkChange, bool, error) {
	current, linked := state.current[unit.ID]
	switch {
	case !linked && unit.Eligible:
	case linked && projectContainerMappingLinkMovable(current):
	case linked && state.proposalReplaceable(current):
	default:
		return projectRelinkChange{}, false, nil
	}
	proposal, resolved, err := state.resolve(unit)
	if err != nil {
		return projectRelinkChange{}, false, err
	}
	change := projectRelinkChange{unit: unit}
	if linked {
		change.current = &current
	}
	if resolved {
		change.replacement = &proposal
	}
	switch {
	case !linked || current.State == "proposed":
		return change, resolved, nil
	case resolved && proposal.Project == current.Project:
		return projectRelinkChange{}, false, nil
	default:
		return change, true, nil
	}
}

func projectContainerMappingLinkMovable(link ProjectLink) bool {
	return link.State == "applied" && link.Auto && link.Source == "deterministic" && link.Rule == "container_mapping" && link.Decision == ""
}

func (state projectRelinkState) proposalReplaceable(link ProjectLink) bool {
	return link.State == "proposed" && link.Source == "llm" && !state.undone[link.UnitID]
}

func applyProjectRelinkChanges(store ProjectLinkStoreConfig, changes []projectRelinkChange) ([]projectRelinkChange, string, int, error) {
	if len(changes) == 0 {
		return nil, "", 0, nil
	}
	id, err := newEventID()
	if err != nil {
		return nil, "", len(changes), fmt.Errorf("create run id: %w", err)
	}
	runID := "run_" + id
	var applied []projectRelinkChange
	wrote := false
	committedRunID := func() string {
		if wrote {
			return runID
		}
		return ""
	}
	for start := 0; start < len(changes); start += projectLinkBatchSize {
		batch := changes[start:min(start+projectLinkBatchSize, len(changes))]
		var batchApplied []projectRelinkChange
		err := withProjectLinksTx(store, func(tx *sql.Tx, now time.Time) error {
			batchApplied = nil
			var proposals []ProjectLinkProposal
			for _, change := range batch {
				ok, err := supersedeProjectLinkTx(tx, now, change)
				if err != nil {
					return err
				}
				if !ok {
					continue
				}
				batchApplied = append(batchApplied, change)
				if change.replacement != nil {
					proposals = append(proposals, *change.replacement)
				}
			}
			if len(proposals) == 0 {
				return nil
			}
			_, err := recordProjectLinkRunTx(tx, now, ProjectLinkRun{Source: "deterministic", RunID: runID, Links: proposals})
			return err
		})
		if err != nil {
			return applied, committedRunID(), len(changes) - start, err
		}
		for _, change := range batchApplied {
			wrote = wrote || change.replacement != nil
		}
		applied = append(applied, batchApplied...)
	}
	return applied, committedRunID(), 0, nil
}

func supersedeProjectLinkTx(tx *sql.Tx, now time.Time, change projectRelinkChange) (bool, error) {
	current, err := currentProjectLinkTx(tx, change.unit.ID)
	if err != nil {
		return false, err
	}
	if change.current == nil {
		return current == nil, nil
	}
	if current == nil || current.ID != change.current.ID || current.State != change.current.State || current.Decision != "" {
		return false, nil
	}
	current.State, current.DecidedAt = "rejected", now
	return true, decideProjectLink(tx, *current)
}

func projectLinkSuperseded(link ProjectLink) bool {
	return link.State == "rejected" && link.Decision == ""
}
