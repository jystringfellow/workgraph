package workgraph

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type ProjectsListConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
}

type ProjectsListResult struct {
	Catalog      ProjectCatalog
	Mappings     ContainerMappings
	AppliedUnits map[string]int
	Message      string
}

func ListProjects(config ProjectsListConfig) (ProjectsListResult, error) {
	catalog, err := LoadProjectCatalog(config.MemoryDir)
	if err != nil {
		return ProjectsListResult{}, err
	}
	mappings, err := loadContainerMappingsIfInitialized(config.HomeDir, config.DatabasePath)
	if err != nil {
		return ProjectsListResult{}, err
	}
	counts, err := ProjectAppliedUnitCounts(ProjectReviewConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return ProjectsListResult{}, err
	}
	result := ProjectsListResult{Catalog: catalog, Mappings: mappings, AppliedUnits: counts}
	result.Message = projectsListMessage(catalog, mappings, counts)
	return result, nil
}

func loadContainerMappingsIfInitialized(homeDir, databasePath string) (ContainerMappings, error) {
	path, err := resumeDatabasePath(ResumeConfig{HomeDir: homeDir, DatabasePath: databasePath})
	if errors.Is(err, ErrNotInitialized) {
		return ContainerMappings{}, nil
	}
	if err != nil {
		return ContainerMappings{}, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return ContainerMappings{}, err
	}
	defer db.Close()
	return LoadContainerMappings(db)
}

func projectsListMessage(catalog ProjectCatalog, mappings ContainerMappings, counts map[string]int) string {
	lines := []string{"Projects"}
	if len(catalog.Projects) == 0 {
		lines = append(lines, "No project memory documents found.", "Create one with: workgraph memory init <project>")
	}
	containers := map[string][]string{}
	for container, workstreams := range mappings.Workstreams {
		for _, workstream := range workstreams {
			containers[workstream] = append(containers[workstream], container)
		}
	}
	projectContainers := map[string][]string{}
	for container, projects := range mappings.Projects {
		for _, project := range projects {
			projectContainers[project] = append(projectContainers[project], container)
		}
	}
	for _, workstream := range projectCatalogWorkstreams(catalog, mappings) {
		lines = append(lines, "", "Workstream "+workstream)
		if mapped := containers[workstream]; len(mapped) > 0 {
			sort.Strings(mapped)
			lines = append(lines, "Containers: "+strings.Join(mapped, ", "))
		}
		count := 0
		for _, project := range catalog.Projects {
			if project.Workstream == workstream {
				lines = append(lines, projectsListLine(project, counts[project.Slug], projectContainers[project.Slug]))
				count++
			}
		}
		if count == 0 {
			lines = append(lines, "No projects declare this workstream.")
		}
	}
	var ungrouped []string
	for _, project := range catalog.Projects {
		if project.Workstream == "" {
			ungrouped = append(ungrouped, projectsListLine(project, counts[project.Slug], projectContainers[project.Slug]))
		}
	}
	if len(ungrouped) > 0 {
		lines = append(lines, "", "No workstream")
		lines = append(lines, ungrouped...)
	}
	if problems := len(catalog.Problems); problems > 0 {
		lines = append(lines, "", fmt.Sprintf("%s invalid frontmatter; run workgraph memory doctor for details.", pluralizeProjectFiles(problems)))
	}
	return strings.Join(lines, "\n")
}

func projectsListLine(project ProjectCatalogEntry, appliedUnits int, containers []string) string {
	line := fmt.Sprintf("- %s: %s", project.Slug, project.Title)
	if project.Description != "" {
		line += " — " + project.Description
	}
	units := "units"
	if appliedUnits == 1 {
		units = "unit"
	}
	line += fmt.Sprintf(" (%d %s linked in the last 7 days)", appliedUnits, units)
	if len(containers) == 0 {
		return line
	}
	sorted := append([]string{}, containers...)
	sort.Strings(sorted)
	return line + "\n  Containers: " + strings.Join(sorted, ", ")
}

func pluralizeProjectFiles(count int) string {
	if count == 1 {
		return "1 project file has"
	}
	return fmt.Sprintf("%d project files have", count)
}
