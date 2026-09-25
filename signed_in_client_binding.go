package workgraph

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type signedInClientBinding struct {
	ConfigDir string `json:"config_dir,omitempty"`
}

func normalizeSignedInClientBinding(client string, configDir string) (signedInClientBinding, error) {
	if _, err := normalizeAIClient(client); err != nil {
		return signedInClientBinding{}, err
	}
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return signedInClientBinding{}, nil
	}
	resolved, err := filepath.Abs(configDir)
	if err != nil {
		return signedInClientBinding{}, fmt.Errorf("resolve client config directory: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return signedInClientBinding{}, fmt.Errorf("check client config directory: %w", err)
	}
	if !info.IsDir() {
		return signedInClientBinding{}, fmt.Errorf("client config directory is not a directory: %s", resolved)
	}
	return signedInClientBinding{ConfigDir: resolved}, nil
}

func signedInClientConfigEnvironmentName(client string) string {
	if client == "claude-code" {
		return "CLAUDE_CONFIG_DIR"
	}
	return "CODEX_HOME"
}

func applySignedInClientBinding(environment []string, client string, binding signedInClientBinding) []string {
	if binding.ConfigDir == "" {
		return environment
	}
	name := signedInClientConfigEnvironmentName(client)
	prefix := name + "="
	bound := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			bound = append(bound, entry)
		}
	}
	return append(bound, prefix+binding.ConfigDir)
}

func signedInClientBindingLabel(binding signedInClientBinding) string {
	if binding.ConfigDir == "" {
		return "ambient default"
	}
	return binding.ConfigDir
}

func verifySignedInClientBinding(binding signedInClientBinding) error {
	if binding.ConfigDir == "" {
		return nil
	}
	info, err := os.Stat(binding.ConfigDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}
