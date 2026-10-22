package workgraph

import (
	"database/sql"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	workstreamSourcePrefix       = "workstream:"
	projectDescriptionLimit      = 200
	frontmatterDelimiter         = "---"
	utf8BOM                      = "\ufeff"
	sharedAcronymReasonPrefix    = "shared acronym "
	invalidFrontmatterDoctorNote = "Invalid frontmatter is treated as absent; the project is still listed."
)

var workstreamSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type ProjectCatalogEntry struct {
	Slug        string
	Title       string
	Workstream  string
	Aliases     []string
	Description string
	Path        string
}

type ProjectFrontmatterProblem struct {
	Slug    string
	Path    string
	Problem string
}

type ProjectCatalog struct {
	Projects []ProjectCatalogEntry
	Problems []ProjectFrontmatterProblem
}

type ContainerMappings struct {
	Projects    map[string][]string
	Workstreams map[string][]string
}

func LoadProjectCatalog(memoryDir string) (ProjectCatalog, error) {
	memoryDir, err := resolveMemoryDir(memoryDir)
	if err != nil {
		return ProjectCatalog{}, err
	}
	dir := projectMemoryDir(memoryDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return ProjectCatalog{}, nil
	}
	if err != nil {
		return ProjectCatalog{}, fmt.Errorf("read project memory: %w", err)
	}
	catalog := ProjectCatalog{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return ProjectCatalog{}, fmt.Errorf("read project memory: %w", err)
		}
		project, problem := parseProjectCatalogEntry(strings.TrimSuffix(entry.Name(), ".md"), path, string(content))
		catalog.Projects = append(catalog.Projects, project)
		if problem != "" {
			catalog.Problems = append(catalog.Problems, ProjectFrontmatterProblem{Slug: project.Slug, Path: path, Problem: problem})
		}
	}
	return catalog, nil
}

func (catalog ProjectCatalog) ForContainer(container string, mappings ContainerMappings) []ProjectCatalogEntry {
	workstreams := mappings.Workstreams[container]
	if len(workstreams) == 0 {
		return catalog.Projects
	}
	allowed := map[string]bool{}
	for _, workstream := range workstreams {
		allowed[workstream] = true
	}
	var narrowed []ProjectCatalogEntry
	for _, project := range catalog.Projects {
		if allowed[project.Workstream] {
			narrowed = append(narrowed, project)
		}
	}
	return narrowed
}

func (catalog ProjectCatalog) workstreamOf(slug string) string {
	for _, project := range catalog.Projects {
		if project.Slug == slug {
			return project.Workstream
		}
	}
	return ""
}

func LoadContainerMappings(db *sql.DB) (ContainerMappings, error) {
	mappings := ContainerMappings{Projects: map[string][]string{}, Workstreams: map[string][]string{}}
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'memory_project_sources')`).Scan(&exists); err != nil {
		return ContainerMappings{}, err
	}
	if !exists {
		return mappings, nil
	}
	rows, err := db.Query(`SELECT memory_doc_path, captured_project FROM memory_project_sources ORDER BY captured_project, memory_doc_path`)
	if err != nil {
		return ContainerMappings{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, container string
		if err := rows.Scan(&key, &container); err != nil {
			return ContainerMappings{}, err
		}
		if workstream, ok := strings.CutPrefix(key, workstreamSourcePrefix); ok {
			mappings.Workstreams[container] = append(mappings.Workstreams[container], workstream)
			continue
		}
		mappings.Projects[container] = append(mappings.Projects[container], strings.TrimSuffix(filepath.Base(key), ".md"))
	}
	return mappings, rows.Err()
}

func validWorkstreamSlug(slug string) bool {
	return workstreamSlugPattern.MatchString(slug)
}

func parseProjectCatalogEntry(slug, path, content string) (ProjectCatalogEntry, string) {
	entry := ProjectCatalogEntry{Slug: slug, Path: path}
	content = strings.TrimPrefix(content, utf8BOM)
	body := content
	frontmatter, rest, hasFrontmatter := splitFrontmatter(content)
	problem := ""
	if hasFrontmatter {
		body = rest
		fields, err := parseProjectFrontmatter(frontmatter)
		if err != nil {
			problem = err.Error()
		} else {
			entry.Workstream, entry.Aliases, entry.Description = fields.workstream, fields.aliases, fields.description
		}
	} else if firstLine(content) == frontmatterDelimiter {
		problem = "frontmatter is not closed"
	}
	entry.Title = memoryProjectTitle(body, slug)
	if entry.Description == "" {
		entry.Description = truncateRunes(firstBodyParagraph(body), projectDescriptionLimit)
	}
	return entry, problem
}

func projectMemoryBody(content string) string {
	content = strings.TrimPrefix(content, utf8BOM)
	if _, rest, ok := splitFrontmatter(content); ok {
		return rest
	}
	return content
}

func splitFrontmatter(content string) (string, string, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		return "", content, false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == frontmatterDelimiter {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", content, false
}

func firstLine(content string) string {
	line, _, _ := strings.Cut(content, "\n")
	return strings.TrimSpace(line)
}

type projectFrontmatter struct {
	workstream  string
	aliases     []string
	description string
}

func parseProjectFrontmatter(frontmatter string) (projectFrontmatter, error) {
	fields := projectFrontmatter{}
	listKey := ""
	for _, raw := range strings.Split(frontmatter, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if item, ok := strings.CutPrefix(line, "- "); ok && listKey != "" {
			if listKey == "aliases" {
				fields.aliases = append(fields.aliases, unquoteFrontmatter(item))
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return projectFrontmatter{}, fmt.Errorf("frontmatter line %q is not key: value", line)
		}
		listKey = ""
		switch key {
		case "workstream":
			fields.workstream = unquoteFrontmatter(value)
			if !validWorkstreamSlug(fields.workstream) {
				return projectFrontmatter{}, fmt.Errorf("workstream %q is not a lowercase kebab-case slug", fields.workstream)
			}
		case "aliases":
			if value == "" {
				listKey = key
				continue
			}
			aliases, err := parseFrontmatterFlowList(value)
			if err != nil {
				return projectFrontmatter{}, err
			}
			fields.aliases = aliases
		case "description":
			fields.description = catalogDisplayText(unquoteFrontmatter(value))
			if len([]rune(fields.description)) > projectDescriptionLimit {
				return projectFrontmatter{}, fmt.Errorf("description is longer than %d characters", projectDescriptionLimit)
			}
		default:
			if value == "" {
				listKey = key
			}
		}
	}
	return fields, nil
}

func parseFrontmatterFlowList(value string) ([]string, error) {
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("aliases must be a [a, b] list or a block list")
	}
	var items []string
	var current strings.Builder
	quote := rune(0)
	add := func() {
		if item := unquoteFrontmatter(current.String()); item != "" {
			items = append(items, item)
		}
		current.Reset()
	}
	for _, r := range strings.TrimSuffix(strings.TrimPrefix(value, "["), "]") {
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '"' || r == '\'') && strings.TrimSpace(current.String()) == "":
			quote = r
		case quote == 0 && r == ',':
			add()
			continue
		}
		current.WriteRune(r)
	}
	if quote != 0 {
		return nil, fmt.Errorf("aliases list has an unclosed quote")
	}
	add()
	return items, nil
}

func unquoteFrontmatter(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}

func firstBodyParagraph(body string) string {
	var paragraph []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			if len(paragraph) > 0 {
				break
			}
			continue
		}
		paragraph = append(paragraph, line)
	}
	return markdownPlainText(strings.Join(paragraph, " "))
}

func markdownPlainText(markdown string) string {
	return catalogDisplayText(html.UnescapeString(markdown))
}

func catalogDisplayText(value string) string {
	value = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, nextANSISequence.ReplaceAllString(value, ""))
	return strings.Join(strings.Fields(value), " ")
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func projectCatalogWorkstreams(catalog ProjectCatalog, mappings ContainerMappings) []string {
	seen := map[string]bool{}
	for _, project := range catalog.Projects {
		if project.Workstream != "" {
			seen[project.Workstream] = true
		}
	}
	for _, workstreams := range mappings.Workstreams {
		for _, workstream := range workstreams {
			seen[workstream] = true
		}
	}
	var workstreams []string
	for workstream := range seen {
		workstreams = append(workstreams, workstream)
	}
	sort.Strings(workstreams)
	return workstreams
}
