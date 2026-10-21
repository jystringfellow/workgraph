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
	Catalog  ProjectCatalog
	Mappings ContainerMappings
	Message  string
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
	result := ProjectsListResult{Catalog: catalog, Mappings: mappings}
	result.Message = projectsListMessage(catalog, mappings)
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

func projectsListMessage(catalog ProjectCatalog, mappings ContainerMappings) string {
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
	for _, workstream := range projectCatalogWorkstreams(catalog, mappings) {
		lines = append(lines, "", "Workstream "+workstream)
		if mapped := containers[workstream]; len(mapped) > 0 {
			sort.Strings(mapped)
			lines = append(lines, "Containers: "+strings.Join(mapped, ", "))
		}
		count := 0
		for _, project := range catalog.Projects {
			if project.Workstream == workstream {
				lines = append(lines, projectsListLine(project))
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
			ungrouped = append(ungrouped, projectsListLine(project))
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

func projectsListLine(project ProjectCatalogEntry) string {
	line := fmt.Sprintf("- %s: %s", project.Slug, project.Title)
	if project.Description != "" {
		line += " — " + project.Description
	}
	return line
}

func pluralizeProjectFiles(count int) string {
	if count == 1 {
		return "1 project file has"
	}
	return fmt.Sprintf("%d project files have", count)
}
