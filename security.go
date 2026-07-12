package workgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SecurityReportConfig controls read-only endpoint security reporting.
type SecurityReportConfig struct {
	HomeDir string
	Format  string
}

// SecurityReportResult contains a secret-free endpoint security report.
type SecurityReportResult struct {
	HomeDir string
	Message string
}

type securityReportPayload struct {
	Version           int                       `json:"version"`
	Status            string                    `json:"status"`
	Platform          string                    `json:"platform"`
	Home              securityPathState         `json:"home"`
	Database          securityDatabaseState     `json:"database"`
	LocalFiles        []securityLocalFileState  `json:"local_files"`
	ManagedSettings   securityManagedState      `json:"managed_settings"`
	CredentialStorage securityCredentialStorage `json:"credential_storage"`
	Network           securityNetworkState      `json:"network"`
	Findings          []securityFinding         `json:"findings"`
}

type securityPathState struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Mode     string `json:"mode,omitempty"`
	UserOnly bool   `json:"user_only"`
}

type securityDatabaseState struct {
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Mode       string `json:"mode,omitempty"`
	UserOnly   bool   `json:"user_only"`
	Encryption string `json:"encryption"`
}

type securityLocalFileState struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Mode     string `json:"mode,omitempty"`
	UserOnly bool   `json:"user_only"`
}

type securityManagedState struct {
	Path   string `json:"path"`
	Active bool   `json:"active"`
}

type securityCredentialStorage struct {
	ConnectorSecrets  string `json:"connector_secrets"`
	OSCredentialStore bool   `json:"os_credential_store"`
}

type securityNetworkState struct {
	ConfiguredDestinationCount int `json:"configured_destination_count"`
}

type securityFinding struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Remediation string `json:"remediation"`
}

// SecurityReport inspects local controls without contacting providers or modifying state.
func SecurityReport(config SecurityReportConfig) (SecurityReportResult, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return SecurityReportResult{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return SecurityReportResult{}, fmt.Errorf("resolve workgraph home: %w", err)
	}

	report, err := collectSecurityReport(homeDir)
	if err != nil {
		return SecurityReportResult{}, err
	}
	format := config.Format
	if format == "" {
		format = "text"
	}
	var message string
	switch format {
	case "text":
		message = formatSecurityReportText(report)
	case "json":
		contents, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return SecurityReportResult{}, fmt.Errorf("encode security report: %w", err)
		}
		message = string(contents)
	default:
		return SecurityReportResult{}, fmt.Errorf("unsupported security report format %q", config.Format)
	}
	return SecurityReportResult{HomeDir: homeDir, Message: message}, nil
}

func collectSecurityReport(homeDir string) (securityReportPayload, error) {
	home := inspectSecurityPath(homeDir, true)
	databasePath := filepath.Join(homeDir, "workgraph.db")
	databaseFile := inspectSecurityPath(databasePath, false)
	localFiles := inspectSecurityLocalFiles(homeDir)
	_, managedPath, managedPresent, err := readManagedSettings()
	if err != nil {
		return securityReportPayload{}, err
	}
	destinations, err := collectNetworkDestinations(homeDir)
	if err != nil {
		return securityReportPayload{}, fmt.Errorf("inspect configured network destinations: %w", err)
	}

	findings := []securityFinding{
		{
			Code:        "sqlite_not_encrypted",
			Severity:    "high",
			Description: "The SQLite event store is protected by local file permissions but is not encrypted by workgraph.",
			Remediation: "Use organization-required full-disk encryption until workgraph SQLite encryption and OS-backed keys are implemented.",
		},
		{
			Code:        "connector_secrets_file_backed",
			Severity:    "high",
			Description: "Connector access and refresh tokens are stored in user-only local files rather than an operating-system credential store.",
			Remediation: "Restrict endpoint access and use organization-required disk encryption until OS credential-store integration is implemented.",
		},
	}
	if runtime.GOOS == "windows" {
		findings = append(findings, securityFinding{
			Code:        "windows_acl_not_verified",
			Severity:    "high",
			Description: "Windows ACL hardening for workgraph local credentials has not been implemented and verified in Windows CI.",
			Remediation: "Do not approve Windows deployment until credential ACL hardening is implemented and verified.",
		})
	}
	if runtime.GOOS != "windows" {
		for _, state := range append([]securityLocalFileState{{ID: "database", Category: "captured_data", Path: databasePath, Exists: databaseFile.Exists, Mode: databaseFile.Mode, UserOnly: databaseFile.UserOnly}}, localFiles...) {
			if state.Exists && !state.UserOnly {
				findings = append(findings, securityFinding{
					Code:        "local_file_permissions_too_broad",
					Severity:    "high",
					Description: fmt.Sprintf("Local %s file permissions are broader than the current user: %s.", state.ID, state.Path),
					Remediation: "Run workgraph init and rewrite the affected connector configuration to repair supported POSIX permissions.",
				})
			}
		}
		if home.Exists && !home.UserOnly {
			findings = append(findings, securityFinding{
				Code:        "home_permissions_too_broad",
				Severity:    "high",
				Description: "The workgraph home directory permits access beyond the current local user.",
				Remediation: "Run workgraph init to repair supported POSIX permissions to 0700.",
			})
		}
	}

	return securityReportPayload{
		Version:  1,
		Status:   "attention_required",
		Platform: runtime.GOOS,
		Home:     home,
		Database: securityDatabaseState{
			Path:       databasePath,
			Exists:     databaseFile.Exists,
			Mode:       databaseFile.Mode,
			UserOnly:   databaseFile.UserOnly,
			Encryption: "not_enabled",
		},
		LocalFiles: localFiles,
		ManagedSettings: securityManagedState{
			Path:   managedPath,
			Active: managedPresent,
		},
		CredentialStorage: securityCredentialStorage{
			ConnectorSecrets:  "local_files",
			OSCredentialStore: false,
		},
		Network:  securityNetworkState{ConfiguredDestinationCount: len(destinations)},
		Findings: findings,
	}, nil
}

func inspectSecurityLocalFiles(homeDir string) []securityLocalFileState {
	files := []struct {
		id       string
		category string
		name     string
	}{
		{"settings", "configuration", "settings.json"},
		{"slack_credentials", "connector_credentials", "slack.json"},
		{"calendar_credentials", "connector_credentials", "calendar.json"},
		{"mail_credentials", "connector_credentials", "mail.json"},
		{"notion_credentials", "connector_credentials", "notion.json"},
		{"azure_boards_credentials", "connector_credentials", "azure-boards.json"},
		{"llm_configuration", "credential_adjacent", "llm.json"},
		{"connector_runtime", "credential_adjacent", "connectors.json"},
		{"daemon_state", "runtime", "daemon.json"},
		{"daemon_pid", "runtime", "daemon.pid"},
		{"daemon_log", "runtime", "daemon.log"},
	}
	result := make([]securityLocalFileState, 0, len(files))
	for _, file := range files {
		path := filepath.Join(homeDir, file.name)
		state := inspectSecurityPath(path, false)
		result = append(result, securityLocalFileState{
			ID:       file.id,
			Category: file.category,
			Path:     path,
			Exists:   state.Exists,
			Mode:     state.Mode,
			UserOnly: state.UserOnly,
		})
	}
	return result
}

func inspectSecurityPath(path string, directory bool) securityPathState {
	state := securityPathState{Path: path}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state
		}
		return state
	}
	state.Exists = true
	state.Mode = fmt.Sprintf("%04o", info.Mode().Perm())
	if runtime.GOOS == "windows" {
		return state
	}
	expected := os.FileMode(0o600)
	if directory {
		expected = 0o700
	}
	state.UserOnly = info.Mode().Perm()&0o077 == 0 && info.Mode().Perm()&expected == expected
	return state
}

func formatSecurityReportText(report securityReportPayload) string {
	lines := []string{
		"workgraph security report",
		"Status: attention required",
		"Platform: " + report.Platform,
		"Home: " + report.Home.Path,
		"SQLite encryption: not enabled",
		"Connector credential storage: local files",
		fmt.Sprintf("Managed settings: %t (%s)", report.ManagedSettings.Active, report.ManagedSettings.Path),
		fmt.Sprintf("Configured network destinations: %d", report.Network.ConfiguredDestinationCount),
		"Findings:",
	}
	for _, finding := range report.Findings {
		lines = append(lines, fmt.Sprintf("- [%s] %s: %s", finding.Severity, finding.Code, finding.Description))
	}
	return strings.Join(lines, "\n")
}
