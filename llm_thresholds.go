package workgraph

import (
	"errors"
	"fmt"
	"strings"
)

const (
	defaultAutoApplyConfidence = 0.95
	minimumAutoApplyConfidence = 0.5
	defaultVerifyFloor         = 0.6
	llmTaskAssociateVerify     = "associate-verify"
)

type LLMSetProfileConfig struct {
	HomeDir             string
	Name                string
	AutoApplyConfidence *float64
	VerifyFloor         *float64
}

func SetLLMProfile(config LLMSetProfileConfig) (LLMResult, error) {
	homeDir, err := resolveLLMHomeDir(config.HomeDir)
	if err != nil {
		return LLMResult{}, err
	}
	name := strings.TrimSpace(config.Name)
	if name == "" {
		return LLMResult{}, errors.New("llm profile name is required")
	}
	if config.AutoApplyConfidence == nil && config.VerifyFloor == nil {
		return LLMResult{}, errors.New("llm set needs --auto-apply-confidence or --verify-floor")
	}
	stored, err := readLLMConnectorConfig(homeDir)
	if err != nil {
		return LLMResult{}, err
	}
	profile, ok := stored.Profiles[name]
	if !ok {
		return LLMResult{}, fmt.Errorf("llm profile %q is not configured", name)
	}
	if config.AutoApplyConfidence != nil {
		profile.AutoApplyConfidence = config.AutoApplyConfidence
	}
	if config.VerifyFloor != nil {
		profile.VerifyFloor = config.VerifyFloor
	}
	if err := validateLLMProfileThresholds(profile); err != nil {
		return LLMResult{}, err
	}
	stored.Profiles[name] = profile
	configPath := llmConfigPath(homeDir)
	if err := writeLLMConnectorConfig(configPath, stored); err != nil {
		return LLMResult{}, err
	}
	lines := append([]string{"LLM profile updated: " + name}, profile.thresholdLines()...)
	lines = append(lines, "Config: "+configPath)
	return LLMResult{ConfigPath: configPath, Message: strings.Join(lines, "\n")}, nil
}

func validateLLMProfileThresholds(profile llmProfile) error {
	if value := profile.AutoApplyConfidence; value != nil && (!ProjectConfidenceValid(*value) || *value < minimumAutoApplyConfidence) {
		return fmt.Errorf("auto-apply confidence %v must be from %.1f to 1.0", *value, minimumAutoApplyConfidence)
	}
	if value := profile.VerifyFloor; value != nil && !ProjectConfidenceValid(*value) {
		return fmt.Errorf("verify floor %v must be from 0 to 1", *value)
	}
	return nil
}

func (profile llmProfile) autoApplyThreshold() float64 {
	if profile.AutoApplyConfidence == nil {
		return defaultAutoApplyConfidence
	}
	return *profile.AutoApplyConfidence
}

func (profile llmProfile) verifyFloor() float64 {
	if profile.VerifyFloor == nil {
		return defaultVerifyFloor
	}
	return *profile.VerifyFloor
}

func (profile llmProfile) thresholdLabel() string {
	return fmt.Sprintf("auto-apply %.2f, verify-floor %.2f", profile.autoApplyThreshold(), profile.verifyFloor())
}

func (profile llmProfile) thresholdLines() []string {
	return []string{
		fmt.Sprintf("Auto-apply confidence: %.2f", profile.autoApplyThreshold()),
		fmt.Sprintf("Verify floor: %.2f", profile.verifyFloor()),
	}
}

type autoApplyThresholds map[string]float64

func (thresholds autoApplyThresholds) of(profile string) float64 {
	if threshold, ok := thresholds[profile]; ok {
		return threshold
	}
	return defaultAutoApplyConfidence
}

func thresholdsFromConfig(config llmConnectorConfig) autoApplyThresholds {
	thresholds := autoApplyThresholds{}
	for name, profile := range config.Profiles {
		thresholds[name] = profile.autoApplyThreshold()
	}
	return thresholds
}

func loadAutoApplyThresholds(homeDir string) autoApplyThresholds {
	homeDir, err := resolveLLMHomeDir(homeDir)
	if err != nil {
		return autoApplyThresholds{}
	}
	stored, err := readOrEmptyLLMConnectorConfig(homeDir)
	if err != nil {
		return autoApplyThresholds{}
	}
	return thresholdsFromConfig(stored)
}
