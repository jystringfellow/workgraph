package workgraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type bridgeWorkerSettings struct {
	Model string `json:"model,omitempty"`
}

type bridgeWorkerConfigFile struct {
	Workers map[string]bridgeWorkerSettings `json:"workers,omitempty"`
}

func bridgeWorkerConfigPath(homeDir string) string {
	return filepath.Join(homeDir, "bridge", "workers.json")
}

func readBridgeWorkerConfig(homeDir string) (bridgeWorkerConfigFile, error) {
	path := bridgeWorkerConfigPath(homeDir)
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return bridgeWorkerConfigFile{Workers: map[string]bridgeWorkerSettings{}}, nil
		}
		return bridgeWorkerConfigFile{}, fmt.Errorf("read bridge worker config: %w", err)
	}
	var config bridgeWorkerConfigFile
	if err := json.Unmarshal(contents, &config); err != nil {
		return bridgeWorkerConfigFile{}, fmt.Errorf("parse bridge worker config: %w", err)
	}
	if config.Workers == nil {
		config.Workers = map[string]bridgeWorkerSettings{}
	}
	return config, nil
}

func configureBridgeWorkerModel(homeDir string, client string, model string, clear bool) (string, error) {
	model = strings.TrimSpace(model)
	if model != "" && clear {
		return "", fmt.Errorf("--model and --clear-model cannot be used together")
	}
	config, err := readBridgeWorkerConfig(homeDir)
	if err != nil {
		return "", err
	}
	settings := config.Workers[client]
	changed := false
	switch {
	case clear:
		if settings.Model != "" {
			settings.Model = ""
			changed = true
		}
	case model != "":
		if settings.Model != model {
			settings.Model = model
			changed = true
		}
	}
	if changed {
		if settings.Model == "" {
			delete(config.Workers, client)
		} else {
			config.Workers[client] = settings
		}
		path := bridgeWorkerConfigPath(homeDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", fmt.Errorf("create bridge worker config directory: %w", err)
		}
		if err := writeJSONFile(path, config); err != nil {
			return "", fmt.Errorf("write bridge worker config: %w", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("secure bridge worker config: %w", err)
		}
	}
	return settings.Model, nil
}

func bridgeWorkerModel(homeDir string, client string) (string, error) {
	config, err := readBridgeWorkerConfig(homeDir)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(config.Workers[client].Model), nil
}

func bridgeWorkerModelLabel(model string) string {
	if strings.TrimSpace(model) == "" {
		return "client default"
	}
	return model
}
