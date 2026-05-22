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

func projectMemoryDir(memoryDir string) string {
	return filepath.Join(memoryDir, projectMemoryDirName)
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
