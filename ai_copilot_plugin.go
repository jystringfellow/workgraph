package workgraph

import (
	"bytes"
	"encoding/json"
	"errors"
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

func ensureAICopilotSessionPlugin(homeDir string) (string, error) {
	adapters := filepath.Join(homeDir, "ai-adapters")
	if err := ensureAIPrivateAdapterDirectory(adapters, "AI"); err != nil {
		return "", err
	}
	parent := filepath.Join(adapters, "copilot")
	if err := ensureAIPrivateAdapterDirectory(parent, "Copilot"); err != nil {
		return "", err
	}
	files, err := aiCopilotSessionPluginFiles()
	if err != nil {
		return "", err
	}
	target := filepath.Join(parent, aiCopilotSessionPluginName)
	if aiPluginDirectoryMatches(target, files) {
		return target, nil
	}

	staging, err := os.MkdirTemp(parent, ".workgraph-session-*")
	if err != nil {
		return "", fmt.Errorf("create Copilot session plugin: %w", err)
	}
	defer os.RemoveAll(staging)
	for relative, contents := range files {
		path := filepath.Join(staging, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", fmt.Errorf("create Copilot session plugin directory: %w", err)
		}
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			return "", fmt.Errorf("write Copilot session plugin file: %w", err)
		}
	}

	retired, err := os.MkdirTemp(parent, ".workgraph-session-retired-*")
	if err != nil {
		return "", fmt.Errorf("prepare Copilot session plugin replacement: %w", err)
	}
	defer os.RemoveAll(retired)
	retiredPlugin := filepath.Join(retired, aiCopilotSessionPluginName)
	if err := os.Rename(target, retiredPlugin); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("retire Copilot session plugin: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		if aiPluginDirectoryMatches(target, files) {
			return target, nil
		}
		return "", fmt.Errorf("install Copilot session plugin: %w", err)
	}
	return target, nil
}

func aiPluginDirectoryMatches(root string, expected map[string][]byte) bool {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return false
	}
	seen := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("unexpected entry")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		want, ok := expected[relative]
		if !ok {
			return errors.New("unexpected file")
		}
		contents, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(contents, want) {
			return errors.New("changed file")
		}
		seen++
		return nil
	})
	return err == nil && seen == len(expected)
}
