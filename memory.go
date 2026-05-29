package workgraph

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const projectMemoryDirName = "projects"
const organizationMemoryDirName = "organizations"
const teamMemoryDirName = "teams"
const personalMemoryFileName = "personal.md"

// MemoryDoc is user-owned context loaded from a local memory file.
type MemoryDoc struct {
	ID        string
	Path      string
	Kind      string
	Content   string
	UpdatedAt time.Time
}

// ProjectMemoryInitConfig controls creation of starter project memory.
type ProjectMemoryInitConfig struct {
	HomeDir   string
	MemoryDir string
	Project   string
}

// ProjectMemoryInitResult describes initialized project memory.
type ProjectMemoryInitResult struct {
	Path    string
	Created bool
	Message string
}

// PersonalMemoryInitConfig controls creation of starter personal memory.
type PersonalMemoryInitConfig struct {
	HomeDir   string
	MemoryDir string
}

// PersonalMemoryInitResult describes initialized personal memory.
type PersonalMemoryInitResult struct {
	Path    string
	Created bool
	Message string
}

// OrganizationMemoryInitConfig controls creation of starter organization memory.
type OrganizationMemoryInitConfig struct {
	HomeDir      string
	MemoryDir    string
	Organization string
}

// OrganizationMemoryInitResult describes initialized organization memory.
type OrganizationMemoryInitResult struct {
	Path    string
	Created bool
	Message string
}

// TeamMemoryInitConfig controls creation of starter team memory.
type TeamMemoryInitConfig struct {
	HomeDir   string
	MemoryDir string
	Team      string
}

// TeamMemoryInitResult describes initialized team memory.
type TeamMemoryInitResult struct {
	Path    string
	Created bool
	Message string
}

// MemorySuggestConfig controls draft memory update suggestions from evidence.
type MemorySuggestConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Scope        string
	Project      string
	MaxEvents    int
}

// MemorySuggestResult describes draft suggestions without mutating memory.
type MemorySuggestResult struct {
	Scope       string
	Project     string
	MemoryPath  string
	Suggestions []MemorySuggestion
	Message     string
}

// MemorySuggestion is a draft update backed by one captured event.
type MemorySuggestion struct {
	Draft      string
	EvidenceID string
	Evidence   string
}

// MemoryPromoteConfig controls explicit promotion of curated text into memory.
type MemoryPromoteConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Scope        string
	Project      string
	EvidenceID   string
	Text         string
}

// MemoryPromoteResult describes a successful explicit memory promotion.
type MemoryPromoteResult struct {
	Scope      string
	Project    string
	MemoryPath string
	EvidenceID string
	Message    string
}

// MemoryLinksConfig controls listing links between memory and evidence.
type MemoryLinksConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Scope        string
	Project      string
}

// MemoryLinksResult describes durable links for a memory document.
type MemoryLinksResult struct {
	Scope      string
	Project    string
	MemoryPath string
	Links      []MemoryLink
	Message    string
}

// MemoryLink connects one memory document to one evidence event.
type MemoryLink struct {
	ID            string
	MemoryDocPath string
	EventID       string
	Relation      string
	CreatedAt     time.Time
}

func projectMemoryDir(memoryDir string) string {
	return filepath.Join(memoryDir, projectMemoryDirName)
}

func organizationMemoryDir(memoryDir string) string {
	return filepath.Join(memoryDir, organizationMemoryDirName)
}

func teamMemoryDir(memoryDir string) string {
	return filepath.Join(memoryDir, teamMemoryDirName)
}

func personalMemoryPath(memoryDir string) string {
	return filepath.Join(memoryDir, personalMemoryFileName)
}

func organizationMemoryPath(memoryDir string, organization string) (string, bool) {
	slug := projectSlug(organization)
	if slug == "" {
		return "", false
	}

	return filepath.Join(organizationMemoryDir(memoryDir), slug+".md"), true
}

func teamMemoryPath(memoryDir string, team string) (string, bool) {
	slug := projectSlug(team)
	if slug == "" {
		return "", false
	}

	return filepath.Join(teamMemoryDir(memoryDir), slug+".md"), true
}

func projectMemoryPath(memoryDir string, project string) (string, bool) {
	slug := projectSlug(project)
	if slug == "" {
		return "", false
	}

	return filepath.Join(projectMemoryDir(memoryDir), slug+".md"), true
}

func loadProjectMemory(memoryDir string, project string) (*MemoryDoc, string, error) {
	path, ok := projectMemoryPath(memoryDir, project)
	if !ok {
		return nil, "", nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, nil
		}
		return nil, path, fmt.Errorf("check project memory: %w", err)
	}
	if info.IsDir() {
		return nil, path, fmt.Errorf("project memory %q is a directory", path)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("read project memory: %w", err)
	}

	return &MemoryDoc{
		ID:        path,
		Path:      path,
		Kind:      "markdown",
		Content:   string(content),
		UpdatedAt: info.ModTime(),
	}, path, nil
}

// SuggestMemoryUpdates emits draft, evidence-backed memory suggestions without writing memory files.
func SuggestMemoryUpdates(config MemorySuggestConfig) (MemorySuggestResult, error) {
	scope := config.Scope
	if scope == "" {
		scope = "project"
	}
	if scope != "project" {
		return MemorySuggestResult{}, fmt.Errorf("memory suggest scope %q is not supported yet", scope)
	}
	if strings.TrimSpace(config.Project) == "" {
		return MemorySuggestResult{}, fmt.Errorf("project is required for project memory suggestions")
	}

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return MemorySuggestResult{}, err
	}
	memoryPath, ok := projectMemoryPath(memoryDir, config.Project)
	if !ok {
		return MemorySuggestResult{}, fmt.Errorf("project name %q cannot form a memory filename", config.Project)
	}

	dbPath, err := resumeDatabasePath(ResumeConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
	})
	if err != nil {
		return MemorySuggestResult{}, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return MemorySuggestResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return MemorySuggestResult{}, fmt.Errorf("open database: %w", err)
	}

	events, err := loadResumeEvents(db, time.Local)
	if err != nil {
		return MemorySuggestResult{}, err
	}
	projectEvents := resumeProjectEvents(events, config.Project)
	sort.Slice(projectEvents, func(i, j int) bool {
		if projectEvents[i].Timestamp.Equal(projectEvents[j].Timestamp) {
			return projectEvents[i].ID < projectEvents[j].ID
		}
		return projectEvents[i].Timestamp.After(projectEvents[j].Timestamp)
	})

	result := MemorySuggestResult{
		Scope:      scope,
		Project:    config.Project,
		MemoryPath: memoryPath,
	}
	limit := memorySuggestLimit(config.MaxEvents)
	for _, event := range projectEvents {
		if len(result.Suggestions) >= limit {
			break
		}
		result.Suggestions = append(result.Suggestions, MemorySuggestion{
			Draft:      "Consider whether this changes project context, priorities, decisions, constraints, or open questions.",
			EvidenceID: event.ID,
			Evidence:   memorySuggestionEvidence(event),
		})
	}
	result.Message = memorySuggestMessage(result)
	return result, nil
}

// PromoteMemory appends user-curated memory text with an evidence link.
func PromoteMemory(config MemoryPromoteConfig) (MemoryPromoteResult, error) {
	scope := config.Scope
	if scope == "" {
		scope = "project"
	}
	if scope != "project" {
		return MemoryPromoteResult{}, fmt.Errorf("memory promote scope %q is not supported yet", scope)
	}
	if strings.TrimSpace(config.Project) == "" {
		return MemoryPromoteResult{}, fmt.Errorf("project is required for project memory promotion")
	}
	if strings.TrimSpace(config.EvidenceID) == "" {
		return MemoryPromoteResult{}, fmt.Errorf("evidence id is required for memory promotion")
	}
	text := strings.TrimSpace(config.Text)
	if text == "" {
		return MemoryPromoteResult{}, fmt.Errorf("memory text is required for promotion")
	}

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return MemoryPromoteResult{}, err
	}
	memoryPath, ok := projectMemoryPath(memoryDir, config.Project)
	if !ok {
		return MemoryPromoteResult{}, fmt.Errorf("project name %q cannot form a memory filename", config.Project)
	}

	event, err := memoryPromotionEvidence(config)
	if err != nil {
		return MemoryPromoteResult{}, err
	}
	if event.Project != config.Project {
		return MemoryPromoteResult{}, fmt.Errorf("evidence %q belongs to project %q, not %q", config.EvidenceID, event.Project, config.Project)
	}

	if err := os.MkdirAll(projectMemoryDir(memoryDir), 0o755); err != nil {
		return MemoryPromoteResult{}, fmt.Errorf("create project memory directory: %w", err)
	}
	if _, err := os.Stat(memoryPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := os.WriteFile(memoryPath, []byte(projectMemoryTemplate(config.Project)), 0o644); err != nil {
				return MemoryPromoteResult{}, fmt.Errorf("create project memory: %w", err)
			}
		} else {
			return MemoryPromoteResult{}, fmt.Errorf("check project memory: %w", err)
		}
	}

	entry := promotedMemoryEntry(text, event)
	file, err := os.OpenFile(memoryPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return MemoryPromoteResult{}, fmt.Errorf("open project memory: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(entry); err != nil {
		return MemoryPromoteResult{}, fmt.Errorf("append project memory: %w", err)
	}
	if err := storeMemoryLink(config, memoryPath, event.ID, "supported_by"); err != nil {
		return MemoryPromoteResult{}, err
	}

	result := MemoryPromoteResult{
		Scope:      scope,
		Project:    config.Project,
		MemoryPath: memoryPath,
		EvidenceID: config.EvidenceID,
	}
	result.Message = memoryPromoteMessage(result)
	return result, nil
}

// ListMemoryLinks returns durable links for project memory evidence.
func ListMemoryLinks(config MemoryLinksConfig) (MemoryLinksResult, error) {
	scope := config.Scope
	if scope == "" {
		scope = "project"
	}
	if scope != "project" {
		return MemoryLinksResult{}, fmt.Errorf("memory links scope %q is not supported yet", scope)
	}
	if strings.TrimSpace(config.Project) == "" {
		return MemoryLinksResult{}, fmt.Errorf("project is required for project memory links")
	}

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return MemoryLinksResult{}, err
	}
	memoryPath, ok := projectMemoryPath(memoryDir, config.Project)
	if !ok {
		return MemoryLinksResult{}, fmt.Errorf("project name %q cannot form a memory filename", config.Project)
	}

	dbPath, err := resumeDatabasePath(ResumeConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
	})
	if err != nil {
		return MemoryLinksResult{}, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return MemoryLinksResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return MemoryLinksResult{}, fmt.Errorf("open database: %w", err)
	}

	rows, err := db.Query(`SELECT id, memory_doc_path, event_id, relation, created_at
		FROM memory_links
		WHERE memory_doc_path = ?
		ORDER BY created_at, id`, memoryPath)
	if err != nil {
		return MemoryLinksResult{}, fmt.Errorf("query memory links: %w", err)
	}
	defer rows.Close()

	result := MemoryLinksResult{
		Scope:      scope,
		Project:    config.Project,
		MemoryPath: memoryPath,
	}
	for rows.Next() {
		var link MemoryLink
		var createdAt string
		if err := rows.Scan(&link.ID, &link.MemoryDocPath, &link.EventID, &link.Relation, &createdAt); err != nil {
			return MemoryLinksResult{}, fmt.Errorf("scan memory link: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return MemoryLinksResult{}, fmt.Errorf("parse memory link timestamp %q: %w", link.ID, err)
		}
		link.CreatedAt = parsed
		result.Links = append(result.Links, link)
	}
	if err := rows.Err(); err != nil {
		return MemoryLinksResult{}, fmt.Errorf("query memory links: %w", err)
	}

	result.Message = memoryLinksMessage(result)
	return result, nil
}

// InitPersonalMemory creates starter Markdown for personal memory without overwriting.
func InitPersonalMemory(config PersonalMemoryInitConfig) (PersonalMemoryInitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return PersonalMemoryInitResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return PersonalMemoryInitResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if err := requireMemoryInitHome(homeDir); err != nil {
		return PersonalMemoryInitResult{}, err
	}

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return PersonalMemoryInitResult{}, err
	}
	if err := os.MkdirAll(memoryDir, 0o755); err != nil {
		return PersonalMemoryInitResult{}, fmt.Errorf("create memory repo: %w", err)
	}

	path := personalMemoryPath(memoryDir)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			result := PersonalMemoryInitResult{Path: path}
			result.Message = personalMemoryInitMessage(result)
			return result, nil
		}
		return PersonalMemoryInitResult{}, fmt.Errorf("create personal memory: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(personalMemoryTemplate()); err != nil {
		return PersonalMemoryInitResult{}, fmt.Errorf("write personal memory: %w", err)
	}

	result := PersonalMemoryInitResult{Path: path, Created: true}
	result.Message = personalMemoryInitMessage(result)
	return result, nil
}

// InitOrganizationMemory creates starter Markdown for one organization without overwriting.
func InitOrganizationMemory(config OrganizationMemoryInitConfig) (OrganizationMemoryInitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return OrganizationMemoryInitResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return OrganizationMemoryInitResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if err := requireMemoryInitHome(homeDir); err != nil {
		return OrganizationMemoryInitResult{}, err
	}

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return OrganizationMemoryInitResult{}, err
	}
	path, ok := organizationMemoryPath(memoryDir, config.Organization)
	if !ok {
		return OrganizationMemoryInitResult{}, fmt.Errorf("organization name %q cannot form a memory filename", config.Organization)
	}
	if err := os.MkdirAll(organizationMemoryDir(memoryDir), 0o755); err != nil {
		return OrganizationMemoryInitResult{}, fmt.Errorf("create organization memory directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			result := OrganizationMemoryInitResult{Path: path}
			result.Message = organizationMemoryInitMessage(result)
			return result, nil
		}
		return OrganizationMemoryInitResult{}, fmt.Errorf("create organization memory: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(organizationMemoryTemplate(config.Organization)); err != nil {
		return OrganizationMemoryInitResult{}, fmt.Errorf("write organization memory: %w", err)
	}

	result := OrganizationMemoryInitResult{Path: path, Created: true}
	result.Message = organizationMemoryInitMessage(result)
	return result, nil
}

// InitTeamMemory creates starter Markdown for one team without overwriting.
func InitTeamMemory(config TeamMemoryInitConfig) (TeamMemoryInitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return TeamMemoryInitResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return TeamMemoryInitResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if err := requireMemoryInitHome(homeDir); err != nil {
		return TeamMemoryInitResult{}, err
	}

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return TeamMemoryInitResult{}, err
	}
	path, ok := teamMemoryPath(memoryDir, config.Team)
	if !ok {
		return TeamMemoryInitResult{}, fmt.Errorf("team name %q cannot form a memory filename", config.Team)
	}
	if err := os.MkdirAll(teamMemoryDir(memoryDir), 0o755); err != nil {
		return TeamMemoryInitResult{}, fmt.Errorf("create team memory directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			result := TeamMemoryInitResult{Path: path}
			result.Message = teamMemoryInitMessage(result)
			return result, nil
		}
		return TeamMemoryInitResult{}, fmt.Errorf("create team memory: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(teamMemoryTemplate(config.Team)); err != nil {
		return TeamMemoryInitResult{}, fmt.Errorf("write team memory: %w", err)
	}

	result := TeamMemoryInitResult{Path: path, Created: true}
	result.Message = teamMemoryInitMessage(result)
	return result, nil
}

// InitProjectMemory creates starter Markdown for one project without overwriting.
func InitProjectMemory(config ProjectMemoryInitConfig) (ProjectMemoryInitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return ProjectMemoryInitResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return ProjectMemoryInitResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}
	if err := requireMemoryInitHome(homeDir); err != nil {
		return ProjectMemoryInitResult{}, err
	}

	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return ProjectMemoryInitResult{}, err
	}
	path, ok := projectMemoryPath(memoryDir, config.Project)
	if !ok {
		return ProjectMemoryInitResult{}, fmt.Errorf("project name %q cannot form a memory filename", config.Project)
	}
	if err := os.MkdirAll(projectMemoryDir(memoryDir), 0o755); err != nil {
		return ProjectMemoryInitResult{}, fmt.Errorf("create project memory directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			result := ProjectMemoryInitResult{Path: path}
			result.Message = projectMemoryInitMessage(result)
			return result, nil
		}
		return ProjectMemoryInitResult{}, fmt.Errorf("create project memory: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(projectMemoryTemplate(config.Project)); err != nil {
		return ProjectMemoryInitResult{}, fmt.Errorf("write project memory: %w", err)
	}

	result := ProjectMemoryInitResult{Path: path, Created: true}
	result.Message = projectMemoryInitMessage(result)
	return result, nil
}

func projectSlug(project string) string {
	var slug strings.Builder
	needsHyphen := false
	for _, r := range strings.TrimSpace(project) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if needsHyphen && slug.Len() > 0 {
				slug.WriteByte('-')
			}
			slug.WriteRune(unicode.ToLower(r))
			needsHyphen = false
			continue
		}
		needsHyphen = slug.Len() > 0
	}
	return slug.String()
}

func requireMemoryInitHome(homeDir string) error {
	configPath := filepath.Join(homeDir, "config.json")
	if _, err := os.Stat(configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return fmt.Errorf("check config: %w", err)
	}
	return nil
}

func projectMemoryTemplate(project string) string {
	return strings.Join([]string{
		"# " + strings.TrimSpace(project),
		"",
		"## Context",
		"",
		"## Current priorities",
		"",
		"## Decisions",
		"",
		"## Constraints",
		"",
		"## Open questions",
		"",
	}, "\n")
}

func personalMemoryTemplate() string {
	return strings.Join([]string{
		"# Personal memory",
		"",
		"## Priorities",
		"",
		"## Principles",
		"",
		"## Preferences",
		"",
		"## Working style",
		"",
		"## Constraints",
		"",
	}, "\n")
}

func memorySuggestLimit(configured int) int {
	if configured > 0 {
		return configured
	}
	return 5
}

func memorySuggestionEvidence(event ResumeEvent) string {
	label := event.Summary
	if label == "" {
		label = resumeEventLabel(event)
	}
	return fmt.Sprintf("%s %s", event.Type, label)
}

func memorySuggestMessage(result MemorySuggestResult) string {
	lines := []string{
		"Draft memory update suggestions",
		"Scope: " + result.Scope,
		"Project: " + result.Project,
		"Project memory: " + result.MemoryPath,
		"Status: draft suggestions only; No memory files were changed.",
	}
	if len(result.Suggestions) == 0 {
		lines = append(lines, "", "No suggestions found from recent captured evidence.")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", "Suggestions")
	for _, suggestion := range result.Suggestions {
		lines = append(lines,
			"- "+suggestion.Draft,
			"  Evidence: "+suggestion.EvidenceID+" "+suggestion.Evidence,
		)
	}
	return strings.Join(lines, "\n")
}

func memoryPromotionEvidence(config MemoryPromoteConfig) (ResumeEvent, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
	})
	if err != nil {
		return ResumeEvent{}, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return ResumeEvent{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return ResumeEvent{}, fmt.Errorf("open database: %w", err)
	}

	events, err := loadResumeEvents(db, time.Local)
	if err != nil {
		return ResumeEvent{}, err
	}
	for _, event := range events {
		if event.ID == config.EvidenceID {
			return event, nil
		}
	}
	return ResumeEvent{}, fmt.Errorf("evidence event %q was not found", config.EvidenceID)
}

func promotedMemoryEntry(text string, event ResumeEvent) string {
	return strings.Join([]string{
		"",
		"## Promoted evidence",
		"",
		"- " + text,
		"  Evidence: " + event.ID + " " + memorySuggestionEvidence(event),
		"",
	}, "\n")
}

func memoryPromoteMessage(result MemoryPromoteResult) string {
	return strings.Join([]string{
		"Promoted project memory",
		"Path: " + result.MemoryPath,
		"Evidence: " + result.EvidenceID,
	}, "\n")
}

func storeMemoryLink(config MemoryPromoteConfig, memoryPath string, eventID string, relation string) error {
	dbPath, err := resumeDatabasePath(ResumeConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
	})
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := memoryLinkID(memoryPath, eventID, relation)
	_, err = db.Exec(`INSERT OR IGNORE INTO memory_links
		(id, memory_doc_path, event_id, relation, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		id,
		memoryPath,
		eventID,
		relation,
		now,
	)
	if err != nil {
		return fmt.Errorf("store memory link: %w", err)
	}
	return nil
}

func memoryLinkID(memoryPath string, eventID string, relation string) string {
	return relation + ":" + eventID + ":" + memoryPath
}

func memoryLinksMessage(result MemoryLinksResult) string {
	lines := []string{
		"Project memory links",
		"Project: " + result.Project,
		"Path: " + result.MemoryPath,
	}
	if len(result.Links) == 0 {
		lines = append(lines, "", "No memory links found.")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "", "Links")
	for _, link := range result.Links {
		lines = append(lines, fmt.Sprintf("- %s %s", link.Relation, link.EventID))
	}
	return strings.Join(lines, "\n")
}

func organizationMemoryTemplate(organization string) string {
	return strings.Join([]string{
		"# " + strings.TrimSpace(organization),
		"",
		"## Strategy",
		"",
		"## Planning notes",
		"",
		"## Operating principles",
		"",
		"## Current priorities",
		"",
		"## Constraints",
		"",
		"## Open questions",
		"",
	}, "\n")
}

func teamMemoryTemplate(team string) string {
	return strings.Join([]string{
		"# " + strings.TrimSpace(team),
		"",
		"## Strategy",
		"",
		"## Rituals",
		"",
		"## Ownership",
		"",
		"## Current goals",
		"",
		"## Constraints",
		"",
		"## Open questions",
		"",
	}, "\n")
}

func projectMemoryInitMessage(result ProjectMemoryInitResult) string {
	heading := "Project memory already exists"
	hint := "Add or edit context, priorities, decisions, constraints, and open questions."
	if result.Created {
		heading = "Project memory initialized"
		hint = "Starter template added for context, priorities, decisions, constraints, and open questions."
	}
	return strings.Join([]string{
		heading,
		"Path: " + result.Path,
		hint,
	}, "\n")
}

func teamMemoryInitMessage(result TeamMemoryInitResult) string {
	heading := "Team memory already exists"
	hint := "Add or edit strategy, rituals, ownership, current goals, constraints, and open questions."
	if result.Created {
		heading = "Team memory initialized"
		hint = "Starter template added for strategy, rituals, ownership, current goals, constraints, and open questions."
	}
	return strings.Join([]string{
		heading,
		"Path: " + result.Path,
		hint,
	}, "\n")
}

func organizationMemoryInitMessage(result OrganizationMemoryInitResult) string {
	heading := "Organization memory already exists"
	hint := "Add or edit strategy, planning notes, operating principles, current priorities, constraints, and open questions."
	if result.Created {
		heading = "Organization memory initialized"
		hint = "Starter template added for strategy, planning notes, operating principles, current priorities, constraints, and open questions."
	}
	return strings.Join([]string{
		heading,
		"Path: " + result.Path,
		hint,
	}, "\n")
}

func personalMemoryInitMessage(result PersonalMemoryInitResult) string {
	heading := "Personal memory already exists"
	hint := "Add or edit priorities, principles, preferences, working style, and constraints."
	if result.Created {
		heading = "Personal memory initialized"
		hint = "Starter template added for priorities, principles, preferences, working style, and constraints."
	}
	return strings.Join([]string{
		heading,
		"Path: " + result.Path,
		hint,
	}, "\n")
}
