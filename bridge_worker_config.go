package workgraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type bridgeWorkerSettings struct {
	Model     string `json:"model,omitempty"`
	ConfigDir string `json:"config_dir,omitempty"`
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

func configureBridgeWorkerSettings(homeDir string, client string, model string, clearModel bool, configDir string, clearConfigDir bool) (bridgeWorkerSettings, error) {
	model = strings.TrimSpace(model)
	configDir = strings.TrimSpace(configDir)
	if model != "" && clearModel {
		return bridgeWorkerSettings{}, fmt.Errorf("--model and --clear-model cannot be used together")
	}
	if configDir != "" && clearConfigDir {
		return bridgeWorkerSettings{}, fmt.Errorf("--config-dir and --clear-config-dir cannot be used together")
	}
	config, err := readBridgeWorkerConfig(homeDir)
	if err != nil {
		return bridgeWorkerSettings{}, err
	}
	settings := config.Workers[client]
	changed := false
	switch {
	case clearModel:
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
	switch {
	case clearConfigDir:
		if settings.ConfigDir != "" {
			settings.ConfigDir = ""
			changed = true
		}
	case configDir != "":
		binding, err := normalizeSignedInClientBinding(client, configDir)
		if err != nil {
			return bridgeWorkerSettings{}, err
		}
		if settings.ConfigDir != binding.ConfigDir {
			settings.ConfigDir = binding.ConfigDir
			changed = true
		}
	}
	if changed {
		if settings.Model == "" && settings.ConfigDir == "" {
			delete(config.Workers, client)
		} else {
			config.Workers[client] = settings
		}
		path := bridgeWorkerConfigPath(homeDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return bridgeWorkerSettings{}, fmt.Errorf("create bridge worker config directory: %w", err)
		}
		if err := writeJSONFile(path, config); err != nil {
			return bridgeWorkerSettings{}, fmt.Errorf("write bridge worker config: %w", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return bridgeWorkerSettings{}, fmt.Errorf("secure bridge worker config: %w", err)
		}
	}
	return settings, nil
}

func bridgeWorkerSettingsFor(homeDir string, client string) (bridgeWorkerSettings, error) {
	config, err := readBridgeWorkerConfig(homeDir)
	if err != nil {
		return bridgeWorkerSettings{}, err
	}
	settings := config.Workers[client]
	settings.Model = strings.TrimSpace(settings.Model)
	settings.ConfigDir = strings.TrimSpace(settings.ConfigDir)
	return settings, nil
}

func bridgeWorkerModelLabel(model string) string {
	if strings.TrimSpace(model) == "" {
		return "client default"
	}
	return model
}
