package workgraph

import (
	"database/sql"
	"fmt"
	"time"
)

type ProjectReviewOutcome struct {
	Ref  string
	Link ProjectLink
	Err  error
}

type ProjectApproveAboveFilter struct {
	Confidence float64
	Target     string
	Container  string
}

func ApproveProjectReviewLinks(config ProjectReviewConfig, linkIDs []int64) ([]ProjectReviewOutcome, error) {
	outcomes := projectLinkIDOutcomes(linkIDs)
	links, err := ReadProjectLinks(config.store(), ProjectLinksQuery{})
	if err != nil {
		return nil, err
	}
	byID := map[int64]ProjectLink{}
	for _, link := range links {
		byID[link.ID] = link
	}
	var catalog *ProjectCatalog
	for index, linkID := range linkIDs {
		link, found := byID[linkID]
		if !found || link.State != "proposed" || link.Target != "new_project" {
			continue
		}
		if catalog == nil {
			loaded, err := LoadProjectCatalog(config.MemoryDir)
			if err != nil {
				return nil, err
			}
			catalog = &loaded
		}
		if catalogHasProject(*catalog, link.NewProject.Slug) {
			continue
		}
		if err := createProjectMemory(config, link.NewProject.Slug, link.NewProject.Workstream); err != nil {
			outcomes[index].Err = err
			continue
		}
		catalog.Projects = append(catalog.Projects, ProjectCatalogEntry{Slug: link.NewProject.Slug})
	}
	err = decideProjectReviewBatches(config.store(), outcomes, func(tx *sql.Tx, now time.Time, index int) (ProjectLink, error) {
		return approveNewProjectLinkTx(tx, now, linkIDs[index])
	})
	return outcomes, err
}

func RejectProjectLinks(config ProjectLinkStoreConfig, linkIDs []int64) ([]ProjectReviewOutcome, error) {
	outcomes := projectLinkIDOutcomes(linkIDs)
	err := decideProjectReviewBatches(config, outcomes, func(tx *sql.Tx, now time.Time, index int) (ProjectLink, error) {
		link, err := readProjectLinkTx(tx, linkIDs[index])
		if err != nil {
			return ProjectLink{}, err
		}
		if !projectLinkCurrent(link) {
			return ProjectLink{}, fmt.Errorf("project link %d is not proposed or applied", link.ID)
		}
		return assignProjectLinkTx(tx, now, ProjectLinkAssignment{UnitID: link.UnitID, Target: "none"})
	})
	return outcomes, err
}

func AssignProjectUnits(config ProjectReviewConfig, unitIDs []string, assignment ProjectUnitAssignment) ([]ProjectReviewOutcome, error) {
	project := assignment.Project
	if assignment.NewProject != "" {
		if err := createProjectMemory(config, assignment.NewProject, assignment.Workstream); err != nil {
			return nil, err
		}
		project = assignment.NewProject
	} else {
		catalog, err := LoadProjectCatalog(config.MemoryDir)
		if err != nil {
			return nil, err
		}
		if !catalogHasProject(catalog, project) {
			return nil, fmt.Errorf("project %q has no project memory; use --new to create it", project)
		}
	}
	units, err := ListProjectUnits(ProjectUnitsConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: config.Now, Location: config.Location, GitEmails: config.GitEmails})
	if err != nil {
		return nil, err
	}
	unitsByID := map[string]ProjectUnit{}
	for _, unit := range units {
		unitsByID[unit.ID] = unit
	}
	outcomes := make([]ProjectReviewOutcome, len(unitIDs))
	for index, unitID := range unitIDs {
		outcomes[index].Ref = unitID
	}
	err = decideProjectReviewBatches(config.store(), outcomes, func(tx *sql.Tx, now time.Time, index int) (ProjectLink, error) {
		link := ProjectLinkAssignment{UnitID: unitIDs[index], Target: "project", Project: project}
		if unit, found := unitsByID[unitIDs[index]]; found {
			link.UnitKind, link.Container, link.Evidence = unit.Kind, unit.Container, unit.EventIDs
		}
		return assignProjectLinkTx(tx, now, link)
	})
	return outcomes, err
}

func ProjectLinksToApproveAbove(config ProjectReviewConfig, filter ProjectApproveAboveFilter) ([]ProjectLink, error) {
	if !ProjectConfidenceValid(filter.Confidence) {
		return nil, fmt.Errorf("confidence %v is outside [0, 1]", filter.Confidence)
	}
	if filter.Target != "" && filter.Target != "project" && filter.Target != "none" {
		return nil, fmt.Errorf("target %q must be project or none", filter.Target)
	}
	links, err := ReadProjectLinks(config.store(), ProjectLinksQuery{})
	if err != nil {
		return nil, err
	}
	undone := map[string]bool{}
	for _, link := range links {
		if link.Decision == "undone" {
			undone[link.UnitID] = true
		}
	}
	var matched []ProjectLink
	for _, link := range links {
		switch {
		case link.State != "proposed" || link.Source != "llm" || undone[link.UnitID]:
		case link.Target != "project" && link.Target != "none":
		case filter.Target != "" && link.Target != filter.Target:
		case filter.Container != "" && link.Container != filter.Container:
		case link.Confidence < filter.Confidence:
		default:
			matched = append(matched, link)
		}
	}
	return matched, nil
}

func ProjectConfidenceValid(confidence float64) bool {
	return confidence >= 0 && confidence <= 1
}

func projectLinkIDOutcomes(linkIDs []int64) []ProjectReviewOutcome {
	outcomes := make([]ProjectReviewOutcome, len(linkIDs))
	for index, linkID := range linkIDs {
		outcomes[index].Ref = fmt.Sprint(linkID)
	}
	return outcomes
}

func decideProjectReviewBatches(store ProjectLinkStoreConfig, outcomes []ProjectReviewOutcome, decide func(*sql.Tx, time.Time, int) (ProjectLink, error)) error {
	for start := 0; start < len(outcomes); start += projectLinkBatchSize {
		var pending []int
		for index := start; index < min(start+projectLinkBatchSize, len(outcomes)); index++ {
			if outcomes[index].Err == nil {
				pending = append(pending, index)
			}
		}
		if len(pending) == 0 {
			continue
		}
		err := withProjectLinksTx(store, func(tx *sql.Tx, now time.Time) error {
			for _, index := range pending {
				outcomes[index].Link, outcomes[index].Err = ProjectLink{}, nil
				if _, err := tx.Exec(`SAVEPOINT project_review_item`); err != nil {
					return err
				}
				link, err := decide(tx, now, index)
				if err != nil {
					if _, rollbackErr := tx.Exec(`ROLLBACK TO project_review_item`); rollbackErr != nil {
						return rollbackErr
					}
				}
				if _, releaseErr := tx.Exec(`RELEASE project_review_item`); releaseErr != nil {
					return releaseErr
				}
				outcomes[index].Link, outcomes[index].Err = link, err
			}
			return nil
		})
		if err != nil {
			for _, index := range pending {
				outcomes[index].Link, outcomes[index].Err = ProjectLink{}, fmt.Errorf("%s not decided: its batch was rolled back", outcomes[index].Ref)
			}
			for index := start + projectLinkBatchSize; index < len(outcomes); index++ {
				if outcomes[index].Err == nil {
					outcomes[index].Err = fmt.Errorf("%s not decided: an earlier batch failed", outcomes[index].Ref)
				}
			}
			return err
		}
	}
	return nil
}
