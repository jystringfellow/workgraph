package workgraph

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const aiCopilotSessionPluginName = "workgraph-session"

func aiCopilotSessionPluginFiles() (map[string][]byte, error) {
	manifest, err := json.MarshalIndent(map[string]any{
		"name":        aiCopilotSessionPluginName,
		"description": "Session-only workgraph AI checkpoint skill for wrapped GitHub Copilot CLI sessions.",
		"author":      map[string]string{"name": "workgraph"},
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Copilot session plugin manifest: %w", err)
	}
	files := map[string][]byte{"plugin.json": append(manifest, '\n')}
	skillRoot := ".agents/skills/workgraph-ai-checkpoint"
	err = fs.WalkDir(agentPluginAssets, skillRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(skillRoot, path)
		if err != nil {
			return err
		}
		contents, err := agentPluginAssets.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.Join("skills", "workgraph-ai-checkpoint", relative)] = contents
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read embedded AI checkpoint skill: %w", err)
	}
	return files, nil
}

func createAICopilotSessionPlugin(homeDir string) (string, func(), error) {
	adapters := filepath.Join(homeDir, "ai-adapters")
	if err := ensureAIPrivateAdapterDirectory(adapters, "AI"); err != nil {
		return "", nil, err
	}
	parent := filepath.Join(adapters, "copilot")
	if err := ensureAIPrivateAdapterDirectory(parent, "Copilot"); err != nil {
		return "", nil, err
	}
	files, err := aiCopilotSessionPluginFiles()
	if err != nil {
		return "", nil, err
	}
	pluginDir, err := os.MkdirTemp(parent, aiCopilotSessionPluginName+"-*")
	if err != nil {
		return "", nil, fmt.Errorf("create Copilot session plugin: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(pluginDir) }
	for relative, contents := range files {
		path := filepath.Join(pluginDir, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("create Copilot session plugin directory: %w", err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("write Copilot session plugin file: %w", err)
		}
	}
	return pluginDir, cleanup, nil
}
