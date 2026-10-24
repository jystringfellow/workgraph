package workgraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	projectWorkItemPattern     = regexp.MustCompile(`(?i)\bAB#(\d+)\b`)
	projectIssueRefPattern     = regexp.MustCompile(`\b([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#(\d+)\b`)
	projectDefaultBranches     = map[string]bool{"main": true, "master": true, "develop": true, "trunk": true}
	projectNotionURLDomains    = []string{"notion.so", "notion.site"}
	projectNotionPageIDPattern = regexp.MustCompile(`(?i)(?:^|-)([0-9a-f]{32}|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
)

type ProjectLinkPassConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Now          time.Time
	Location     *time.Location
	GitEmails    []string
}

type projectIdentifierIndex struct {
	keys    map[string]map[string]bool
	commits map[string]map[string]bool
}

func LinkProjectUnits(config ProjectLinkPassConfig) (ProjectLinkRunResult, error) {
	catalog, err := LoadProjectCatalog(config.MemoryDir)
	if err != nil {
		return ProjectLinkRunResult{}, err
	}
	store := ProjectLinkStoreConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: config.Now}
	units, err := ListProjectUnits(ProjectUnitsConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, Now: config.Now, Location: config.Location, GitEmails: config.GitEmails})
	if err != nil {
		return ProjectLinkRunResult{}, err
	}
	links, err := ReadProjectLinks(store, ProjectLinksQuery{})
	if err != nil {
		return ProjectLinkRunResult{}, err
	}
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return ProjectLinkRunResult{}, err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return ProjectLinkRunResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	mappings, err := LoadContainerMappings(db)
	if err != nil {
		return ProjectLinkRunResult{}, err
	}
	index, err := projectMemoryIdentifierIndex(catalog)
	if err != nil {
		return ProjectLinkRunResult{}, err
	}
	if err := seedProjectIdentifierIndex(db, index, units, links, nil); err != nil {
		return ProjectLinkRunResult{}, err
	}
	linked := map[string]bool{}
	for _, link := range links {
		if projectLinkCurrent(link) {
			linked[link.UnitID] = true
		}
	}
	var proposals []ProjectLinkProposal
	for _, unit := range units {
		if linked[unit.ID] || !unit.Eligible {
			continue
		}
		proposal, ok, err := deterministicProjectLink(db, unit, index, catalog, mappings)
		if err != nil {
			return ProjectLinkRunResult{}, err
		}
		if ok {
			proposals = append(proposals, proposal)
		}
	}
	if len(proposals) == 0 {
		return ProjectLinkRunResult{}, nil
	}
	return RecordProjectLinkRun(store, ProjectLinkRun{Source: "deterministic", Links: proposals})
}

func seedProjectIdentifierIndex(db *sql.DB, index projectIdentifierIndex, units []ProjectUnit, links []ProjectLink, permitted func(AssociationEvent) bool) error {
	unitsByID := map[string]ProjectUnit{}
	for _, unit := range units {
		unitsByID[unit.ID] = unit
	}
	for _, link := range links {
		if !projectLinkCurrent(link) || link.State != "applied" || link.Target != "project" || !projectLinkUserApproved(link) {
			continue
		}
		members := link.Evidence
		if unit, ok := unitsByID[link.UnitID]; ok {
			members = unit.EventIDs
		}
		identifiers, err := projectPermittedEventIdentifiers(db, members, permitted)
		if err != nil {
			return err
		}
		for _, cited := range identifiers {
			index.add(cited.key, link.Project)
		}
	}
	return nil
}

func (index projectIdentifierIndex) citedProjects(identifiers []projectCitedIdentifier) map[string]bool {
	projects := map[string]bool{}
	for _, cited := range identifiers {
		for project := range index.lookup(cited.key) {
			projects[project] = true
		}
	}
	return projects
}

func deterministicProjectLink(db *sql.DB, unit ProjectUnit, index projectIdentifierIndex, catalog ProjectCatalog, mappings ContainerMappings) (ProjectLinkProposal, bool, error) {
	proposal := ProjectLinkProposal{UnitID: unit.ID, UnitKind: unit.Kind, Container: unit.Container, Target: "project", Apply: true}
	identifiers, err := projectEventIdentifiers(db, unit.EventIDs)
	if err != nil {
		return ProjectLinkProposal{}, false, err
	}
	projects := map[string]bool{}
	var evidence, matched []string
	for _, cited := range identifiers {
		found := index.lookup(cited.key)
		for project := range found {
			projects[project] = true
		}
		if len(found) > 0 {
			evidence = appendUnique(evidence, cited.eventID)
			matched = appendUnique(matched, cited.key)
		}
	}
	switch len(projects) {
	case 0:
	case 1:
		for project := range projects {
			proposal.Project = project
		}
		proposal.Rule, proposal.Evidence = "shared_identifier", evidence
		proposal.Summary = "Cites " + strings.Join(matched, ", ")
		return proposal, true, nil
	default:
		return ProjectLinkProposal{}, false, nil
	}
	mapped := map[string]bool{}
	for _, slug := range mappings.Projects[unit.Container] {
		if catalogHasProject(catalog, slug) {
			mapped[slug] = true
		}
	}
	if len(mapped) != 1 {
		return ProjectLinkProposal{}, false, nil
	}
	for project := range mapped {
		proposal.Project = project
	}
	proposal.Rule, proposal.Evidence = "container_mapping", unit.EventIDs
	proposal.Summary = "Container " + unit.Container + " is mapped to " + proposal.Project
	return proposal, true, nil
}

func projectLinkUserApproved(link ProjectLink) bool {
	return link.Source == "user" || link.Decision == "approved" || link.Decision == "confirmed"
}

func catalogHasProject(catalog ProjectCatalog, slug string) bool {
	for _, project := range catalog.Projects {
		if project.Slug == slug {
			return true
		}
	}
	return false
}

func projectMemoryIdentifierIndex(catalog ProjectCatalog) (projectIdentifierIndex, error) {
	index := projectIdentifierIndex{keys: map[string]map[string]bool{}, commits: map[string]map[string]bool{}}
	for _, project := range catalog.Projects {
		content, err := os.ReadFile(project.Path)
		if err != nil {
			return projectIdentifierIndex{}, fmt.Errorf("read project memory: %w", err)
		}
		for _, key := range projectTextIdentifiers(string(content)) {
			index.add(key, project.Slug)
		}
	}
	return index, nil
}

func (index projectIdentifierIndex) add(key, project string) {
	target := index.keys
	if commit, ok := strings.CutPrefix(key, "commit:"); ok {
		target, key = index.commits, commit
	}
	if target[key] == nil {
		target[key] = map[string]bool{}
	}
	target[key][project] = true
}

func (index projectIdentifierIndex) lookup(key string) map[string]bool {
	commit, ok := strings.CutPrefix(key, "commit:")
	if !ok {
		return index.keys[key]
	}
	found := map[string]bool{}
	for known, projects := range index.commits {
		if strings.HasPrefix(known, commit) || strings.HasPrefix(commit, known) {
			for project := range projects {
				found[project] = true
			}
		}
	}
	return found
}

type projectCitedIdentifier struct {
	eventID string
	key     string
}

func projectEventIdentifiers(db *sql.DB, eventIDs []string) ([]projectCitedIdentifier, error) {
	return projectPermittedEventIdentifiers(db, eventIDs, nil)
}

func projectPermittedEventIdentifiers(db *sql.DB, eventIDs []string, permitted func(AssociationEvent) bool) ([]projectCitedIdentifier, error) {
	var cited []projectCitedIdentifier
	for _, eventID := range eventIDs {
		event, err := readAssociationEvent(db, eventID)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read unit event %s: %w", eventID, err)
		}
		if permitted != nil && !permitted(event) {
			continue
		}
		for _, key := range projectEventIdentifierKeys(event) {
			cited = append(cited, projectCitedIdentifier{eventID: eventID, key: key})
		}
	}
	return cited, nil
}

func projectEventIdentifierKeys(event AssociationEvent) []string {
	signals := extractAssociationSignals(event)
	keys := map[string]bool{}
	for ref := range signals.IssueRefs {
		keys["issue:"+ref] = true
	}
	for commit := range signals.Commits {
		keys["commit:"+commit] = true
	}
	for raw := range signals.URLs {
		if key := projectNotionURLKey(raw); key != "" {
			keys[key] = true
		}
	}
	var payload any
	if json.Unmarshal([]byte(event.Payload), &payload) == nil {
		texts := []string{event.Summary}
		collectProjectTrustedStrings(payload, &texts)
		for _, key := range projectTextIdentifiers(strings.Join(texts, "\n")) {
			keys[key] = true
		}
		collectProjectBranchKeys(payload, keys)
		if fields, ok := payload.(map[string]any); ok && event.Source == "notion" {
			if id, _ := fields["id"].(string); id != "" {
				if pageID := projectNotionPageID(id); pageID != "" {
					keys["notion:"+pageID] = true
				}
			}
		}
	}
	return sortedKeys(keys)
}

func collectProjectBranchKeys(value any, keys map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		branch, repository := "", ""
		for key, child := range typed {
			text, _ := child.(string)
			switch associationFieldKey(key) {
			case "branch", "headref", "headrefname":
				branch = normalizeAssociationBranch(text)
			case "headrepository":
				if headRepository := normalizeAssociationRepository(text); headRepository != "" {
					repository = headRepository
				}
			case "repository":
				if repository == "" {
					repository = normalizeAssociationRepository(text)
				}
			case "repopath":
				if repository == "" {
					repository = text
				}
			}
		}
		if branch != "" && repository != "" && !projectDefaultBranches[branch] {
			keys["branch:"+repository+"@"+branch] = true
		}
		for key, child := range typed {
			if key != "untrusted" {
				collectProjectBranchKeys(child, keys)
			}
		}
	case []any:
		for _, child := range typed {
			collectProjectBranchKeys(child, keys)
		}
	}
}

func projectTextIdentifiers(text string) []string {
	keys := map[string]bool{}
	signals := associationSignals{URLs: map[string]bool{}, Repositories: map[string]bool{}, IssueRefs: map[string]bool{}, Commits: map[string]bool{}}
	collectAssociationURLs(text, &signals)
	for ref := range signals.IssueRefs {
		keys["issue:"+ref] = true
	}
	for commit := range signals.Commits {
		keys["commit:"+commit] = true
	}
	for raw := range signals.URLs {
		if key := projectNotionURLKey(raw); key != "" {
			keys[key] = true
		}
	}
	withoutURLs := associationURLPattern.ReplaceAllString(text, " ")
	for _, match := range projectIssueRefPattern.FindAllStringSubmatch(withoutURLs, -1) {
		if repository := normalizeAssociationRepository(match[1]); repository != "" {
			if number, err := strconv.ParseInt(match[2], 10, 64); err == nil && number > 0 {
				keys["issue:"+repository+"#"+strconv.FormatInt(number, 10)] = true
			}
		}
	}
	for _, match := range projectWorkItemPattern.FindAllStringSubmatch(text, -1) {
		keys["workitem:AB#"+match[1]] = true
	}
	return sortedKeys(keys)
}

func projectNotionURLKey(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := parsed.Hostname()
	for _, domain := range projectNotionURLDomains {
		if host != domain && !strings.HasSuffix(host, "."+domain) {
			continue
		}
		parts := splitAssociationURLPath(parsed.Path)
		if len(parts) == 0 {
			return ""
		}
		if pageID := projectNotionPageID(parts[len(parts)-1]); pageID != "" {
			return "notion:" + pageID
		}
	}
	return ""
}

func projectNotionPageID(value string) string {
	match := projectNotionPageIDPattern.FindStringSubmatch(value)
	if match == nil {
		return ""
	}
	return strings.ToLower(strings.ReplaceAll(match[1], "-", ""))
}

func collectProjectTrustedStrings(value any, texts *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key != "untrusted" {
				collectProjectTrustedStrings(child, texts)
			}
		}
	case []any:
		for _, child := range typed {
			collectProjectTrustedStrings(child, texts)
		}
	case string:
		*texts = append(*texts, typed)
	}
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
