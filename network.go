package workgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// NetworkDestinationsConfig controls local network destination reporting.
type NetworkDestinationsConfig struct {
	HomeDir string
	Format  string
}

// NetworkDestinationsResult describes externally reachable destinations from local config.
type NetworkDestinationsResult struct {
	HomeDir      string
	Destinations []NetworkDestination
	Message      string
}

// NetworkDestination describes one configured network destination without credentials.
type NetworkDestination struct {
	ID          string `json:"id"`
	Connector   string `json:"connector"`
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Configured  bool   `json:"configured"`
	Description string `json:"description"`
	label       string
}

// NetworkDestinations reports configured external destinations without contacting providers.
func NetworkDestinations(config NetworkDestinationsConfig) (NetworkDestinationsResult, error) {
	homeDir, err := resolveNetworkHomeDir(config.HomeDir)
	if err != nil {
		return NetworkDestinationsResult{}, err
	}

	destinations, err := collectNetworkDestinations(homeDir)
	if err != nil {
		return NetworkDestinationsResult{}, err
	}
	sort.Slice(destinations, func(i, j int) bool {
		return destinations[i].ID < destinations[j].ID
	})

	format := config.Format
	if format == "" {
		format = "text"
	}
	switch format {
	case "text":
		return NetworkDestinationsResult{
			HomeDir:      homeDir,
			Destinations: destinations,
			Message:      formatNetworkDestinationsText(destinations),
		}, nil
	case "json":
		message, err := formatNetworkDestinationsJSON(destinations)
		if err != nil {
			return NetworkDestinationsResult{}, err
		}
		return NetworkDestinationsResult{
			HomeDir:      homeDir,
			Destinations: destinations,
			Message:      message,
		}, nil
	default:
		return NetworkDestinationsResult{}, fmt.Errorf("unsupported network destinations format %q", config.Format)
	}
}

func resolveNetworkHomeDir(homeDir string) (string, error) {
	resolved, err := resolveHomeDir(homeDir)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve workgraph home: %w", err)
	}
	if _, err := os.Stat(filepath.Join(resolved, "workgraph.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return "", fmt.Errorf("check database: %w", err)
	}
	return resolved, nil
}

func collectNetworkDestinations(homeDir string) ([]NetworkDestination, error) {
	var destinations []NetworkDestination
	if err := appendSlackNetworkDestinations(homeDir, &destinations); err != nil {
		return nil, err
	}
	if err := appendCalendarNetworkDestinations(homeDir, &destinations); err != nil {
		return nil, err
	}
	if err := appendMailNetworkDestinations(homeDir, &destinations); err != nil {
		return nil, err
	}
	if err := appendNotionNetworkDestinations(homeDir, &destinations); err != nil {
		return nil, err
	}
	if err := appendAzureBoardsNetworkDestinations(homeDir, &destinations); err != nil {
		return nil, err
	}
	if err := appendLLMNetworkDestinations(homeDir, &destinations); err != nil {
		return nil, err
	}
	return destinations, nil
}

func appendSlackNetworkDestinations(homeDir string, destinations *[]NetworkDestination) error {
	config, err := readSlackConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if config.AccessToken == "" && config.APIBaseURL == "" {
		return nil
	}
	*destinations = append(*destinations, networkDestination("slack.api", "slack", "api", firstNonEmpty(config.APIBaseURL, "https://slack.com/api"), "Slack Web API base URL"))
	return nil
}

func appendCalendarNetworkDestinations(homeDir string, destinations *[]NetworkDestination) error {
	config, err := readOrEmptyCalendarConnectorConfig(homeDir)
	if err != nil {
		return err
	}
	if config.Google != nil && (config.Google.AccessToken != "" || config.Google.APIBaseURL != "" || config.Google.TokenURL != "") {
		*destinations = append(*destinations,
			networkDestination("calendar.google.api", "calendar.google", "api", firstNonEmpty(config.Google.APIBaseURL, "https://www.googleapis.com"), "Google Calendar API base URL"),
			networkDestination("calendar.google.token", "calendar.google", "token", resolveGoogleCalendarTokenURL(config.Google.TokenURL), "Google Calendar OAuth token endpoint"),
		)
	}
	if config.Microsoft != nil && (config.Microsoft.AccessToken != "" || config.Microsoft.APIBaseURL != "" || config.Microsoft.TokenURL != "") {
		*destinations = append(*destinations,
			networkDestination("calendar.microsoft.api", "calendar.microsoft", "api", firstNonEmpty(config.Microsoft.APIBaseURL, "https://graph.microsoft.com"), "Microsoft Graph Calendar API base URL"),
			networkDestination("calendar.microsoft.token", "calendar.microsoft", "token", resolveMicrosoftCalendarTokenURL(config.Microsoft.TokenURL), "Microsoft Calendar OAuth token endpoint"),
		)
	}
	return nil
}

func appendMailNetworkDestinations(homeDir string, destinations *[]NetworkDestination) error {
	config, err := readOrEmptyMailConnectorConfig(homeDir)
	if err != nil {
		return err
	}
	if config.Google != nil && (config.Google.AccessToken != "" || config.Google.APIBaseURL != "" || config.Google.TokenURL != "") {
		*destinations = append(*destinations,
			networkDestination("mail.google.api", "mail.google", "api", firstNonEmpty(config.Google.APIBaseURL, "https://gmail.googleapis.com"), "Google Mail API base URL"),
			networkDestination("mail.google.token", "mail.google", "token", resolveGoogleMailTokenURL(config.Google.TokenURL), "Google Mail OAuth token endpoint"),
		)
	}
	if config.Microsoft != nil && (config.Microsoft.AccessToken != "" || config.Microsoft.APIBaseURL != "" || config.Microsoft.TokenURL != "") {
		*destinations = append(*destinations,
			networkDestination("mail.microsoft.api", "mail.microsoft", "api", firstNonEmpty(config.Microsoft.APIBaseURL, "https://graph.microsoft.com"), "Microsoft Graph Mail API base URL"),
			networkDestination("mail.microsoft.token", "mail.microsoft", "token", resolveMicrosoftMailTokenURL(config.Microsoft.TokenURL), "Microsoft Mail OAuth token endpoint"),
		)
	}
	return nil
}

func appendNotionNetworkDestinations(homeDir string, destinations *[]NetworkDestination) error {
	config, err := readNotionConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if config.AccessToken == "" && config.APIBaseURL == "" && config.TokenURL == "" {
		return nil
	}
	*destinations = append(*destinations,
		networkDestination("notion.api", "notion", "api", resolveNotionAPIBaseURL(config.APIBaseURL), "Notion API base URL"),
		networkDestination("notion.token", "notion", "token", resolveNotionTokenURL(config.TokenURL), "Notion OAuth token endpoint"),
	)
	return nil
}

func appendAzureBoardsNetworkDestinations(homeDir string, destinations *[]NetworkDestination) error {
	config, err := readAzureBoardsConnectorConfig(homeDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if config.AccessToken == "" && config.APIBaseURL == "" && config.TokenURL == "" {
		return nil
	}
	*destinations = append(*destinations,
		networkDestination("azure.boards.api", "azure.boards", "api", firstNonEmpty(config.APIBaseURL, "https://dev.azure.com"), "Azure DevOps Boards API base URL"),
		networkDestination("azure.boards.token", "azure.boards", "token", resolveAzureBoardsTokenURL(config.TokenURL), "Azure Boards OAuth token endpoint"),
	)
	return nil
}

func appendLLMNetworkDestinations(homeDir string, destinations *[]NetworkDestination) error {
	config, err := readOrEmptyLLMConnectorConfig(homeDir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(config.Profiles))
	for name := range config.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		profile := config.Profiles[name]
		destination := strings.TrimSpace(llmProfileDestination(profile))
		if destination == "" {
			continue
		}
		switch profile.Provider {
		case "openai-compatible":
			*destinations = append(*destinations, networkLLMDestination("llm.openai-compatible."+name, "llm.openai-compatible", name, destination, "OpenAI-compatible LLM profile destination"))
		case "bedrock":
			*destinations = append(*destinations, networkLLMDestination("llm.bedrock."+name, "llm.bedrock", name, destination, "Amazon Bedrock LLM profile destination"))
		default:
			*destinations = append(*destinations, networkLLMDestination("llm."+profile.Provider+"."+name, "llm."+profile.Provider, name, destination, "LLM profile destination"))
		}
	}
	return nil
}

func networkDestination(id, connector, kind, url, description string) NetworkDestination {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	return NetworkDestination{
		ID:          id,
		Connector:   connector,
		Kind:        kind,
		URL:         url,
		Configured:  url != "",
		Description: description,
		label:       connector + " " + kind,
	}
}

func networkLLMDestination(id, connector, profile, url, description string) NetworkDestination {
	return NetworkDestination{
		ID:          id,
		Connector:   connector,
		Kind:        "profile",
		URL:         strings.TrimSpace(url),
		Configured:  strings.TrimSpace(url) != "",
		Description: description,
		label:       connector + " profile " + profile,
	}
}

func formatNetworkDestinationsText(destinations []NetworkDestination) string {
	lines := []string{"Network destinations"}
	if len(destinations) == 0 {
		lines = append(lines, "No configured network destinations found.")
		return strings.Join(lines, "\n")
	}
	for _, destination := range destinations {
		lines = append(lines, "- "+destination.label+": "+destination.URL)
	}
	return strings.Join(lines, "\n")
}

type networkDestinationsJSONPayload struct {
	Destinations []NetworkDestination `json:"destinations"`
}

func formatNetworkDestinationsJSON(destinations []NetworkDestination) (string, error) {
	contents, err := json.MarshalIndent(networkDestinationsJSONPayload{Destinations: destinations}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode network destinations JSON: %w", err)
	}
	return string(contents), nil
}
