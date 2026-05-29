package workgraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigWatchConfig controls updates to config watch roots.
type ConfigWatchConfig struct {
	HomeDir string
	Path    string
}

// ConfigWatchResult describes a watch root added to the config.
type ConfigWatchResult struct {
	ConfigPath string
	AddedPath  string
	WatchDirs  []string
	Message    string
}

// AddWatchDir prepends a resolved watch directory to workgraph config.
func AddWatchDir(config ConfigWatchConfig) (ConfigWatchResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return ConfigWatchResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return ConfigWatchResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}

	configPath := filepath.Join(homeDir, "config.json")
	localConfig, err := readConfig(configPath)
	if err != nil {
		return ConfigWatchResult{}, err
	}

	watchDir := config.Path
	if watchDir == "" {
		watchDir = "."
	}
	watchDir, err = filepath.Abs(watchDir)
	if err != nil {
		return ConfigWatchResult{}, fmt.Errorf("resolve watch directory: %w", err)
	}

	info, err := os.Stat(watchDir)
	if err != nil {
		return ConfigWatchResult{}, fmt.Errorf("watch directory %q: %w", watchDir, err)
	}
	if !info.IsDir() {
		return ConfigWatchResult{}, fmt.Errorf("watch path %q is not a directory", watchDir)
	}

	localConfig.WatchDirs = prependUniquePath(watchDir, localConfig.WatchDirs)
	localConfig.ConservativeWatchDirs = removePath(watchDir, localConfig.ConservativeWatchDirs)
	if err := writeConfig(configPath, localConfig); err != nil {
		return ConfigWatchResult{}, err
	}

	result := ConfigWatchResult{
		ConfigPath: configPath,
		AddedPath:  watchDir,
		WatchDirs:  append([]string(nil), localConfig.WatchDirs...),
	}
	result.Message = strings.Join([]string{
		"workgraph config updated",
		"Config: " + result.ConfigPath,
		"Added watch directory: " + result.AddedPath,
	}, "\n")

	return result, nil
}

func prependUniquePath(path string, paths []string) []string {
	result := []string{path}
	for _, existing := range paths {
		if existing == path {
			continue
		}
		result = append(result, existing)
	}
	return result
}

func removePath(path string, paths []string) []string {
	result := []string{}
	for _, existing := range paths {
		if existing == path {
			continue
		}
		result = append(result, existing)
	}
	return result
}

func writeConfig(configPath string, config configFile) error {
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	contents = append(contents, '\n')

	if err := os.WriteFile(configPath, contents, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
