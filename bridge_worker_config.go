package workgraph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type bridgeWorkerSettings struct {
	Model         string   `json:"model,omitempty"`
	ConfigDir     string   `json:"config_dir,omitempty"`
	ProviderTools []string `json:"provider_tools,omitempty"`
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

func configureBridgeWorkerSettings(homeDir string, client string, model string, clearModel bool, configDir string, clearConfigDir bool, clearProviderTools bool, addProviderTools []string) (bridgeWorkerSettings, error) {
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
	if clearProviderTools && len(settings.ProviderTools) > 0 {
		settings.ProviderTools = nil
		changed = true
	}
	for _, tool := range addProviderTools {
		if !slices.Contains(settings.ProviderTools, tool) {
			settings.ProviderTools = append(settings.ProviderTools, tool)
			changed = true
		}
	}
	slices.Sort(settings.ProviderTools)
	if err := validateStoredProviderTools(client, settings.ProviderTools); err != nil {
		return bridgeWorkerSettings{}, err
	}
	if changed {
		if settings.Model == "" && settings.ConfigDir == "" && len(settings.ProviderTools) == 0 {
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
	if err := validateStoredProviderTools(client, settings.ProviderTools); err != nil {
		return bridgeWorkerSettings{}, err
	}
	return settings, nil
}

func validateStoredProviderTools(client string, tools []string) error {
	if len(tools) == 0 {
		return nil
	}
	if !storesProviderTools(client) {
		return fmt.Errorf("bridge worker config stores provider tools for %s; only copilot and agy provider tools are supported", client)
	}
	form := "server(tool)"
	if client == "agy" {
		form = "mcp(server/tool)"
	}
	for _, tool := range tools {
		if _, err := validateProviderTools(client, []string{tool}); err != nil || strings.TrimSpace(tool) != tool {
			return fmt.Errorf("bridge worker config stores invalid %s provider tool %q; reinstall with --clear-provider-tools and re-approve exact %s names", providerToolsClientLabel(client), tool, form)
		}
	}
	return nil
}

func bridgeWorkerModelLabel(model string) string {
	if strings.TrimSpace(model) == "" {
		return "client default"
	}
	return model
}
