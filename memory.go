package workgraph

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

const projectMemoryDirName = "projects"
const organizationMemoryDirName = "organizations"
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

func projectMemoryDir(memoryDir string) string {
	return filepath.Join(memoryDir, projectMemoryDirName)
}

func organizationMemoryDir(memoryDir string) string {
	return filepath.Join(memoryDir, organizationMemoryDirName)
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

// InitPersonalMemory creates starter Markdown for personal memory without overwriting.
func InitPersonalMemory(config PersonalMemoryInitConfig) (PersonalMemoryInitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return PersonalMemoryInitResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return PersonalMemoryInitResult{}, fmt.Errorf("resolve WorkGraph home: %w", err)
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
		return OrganizationMemoryInitResult{}, fmt.Errorf("resolve WorkGraph home: %w", err)
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

// InitProjectMemory creates starter Markdown for one project without overwriting.
func InitProjectMemory(config ProjectMemoryInitConfig) (ProjectMemoryInitResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return ProjectMemoryInitResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return ProjectMemoryInitResult{}, fmt.Errorf("resolve WorkGraph home: %w", err)
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
