package workgraph

import (
	"fmt"
	"strings"
)

// ProviderToolRequirement describes one logical read-only provider operation.
type ProviderToolRequirement struct {
	Operation    string   `json:"operation"`
	Capabilities []string `json:"capabilities"`
	When         string   `json:"when,omitempty"`
	Detail       string   `json:"detail"`
}

// ConnectorToolRequirements groups provider operations for one connector.
type ConnectorToolRequirements struct {
	Connector string                    `json:"connector"`
	Tools     []ProviderToolRequirement `json:"tools"`
}

// ConnectorRequiredToolsResult is the shared CLI and MCP requirement view.
type ConnectorRequiredToolsResult struct {
	Connectors []ConnectorToolRequirements `json:"connectors"`
	Message    string                      `json:"-"`
}

// RequiredConnectorTools returns canonical bridged provider requirements.
func RequiredConnectorTools(connectorID string) (ConnectorRequiredToolsResult, error) {
	connectorID = strings.TrimSpace(connectorID)
	if connectorID != "" {
		normalizedID, err := normalizeConnectorID(connectorID)
		if err != nil {
			return ConnectorRequiredToolsResult{}, err
		}
		connectorID = normalizedID
		definition, _ := registeredConnector(connectorID)
		if !definition.Bridged {
			return ConnectorRequiredToolsResult{}, fmt.Errorf("connector %s does not support bridged capture", connectorID)
		}
		result := ConnectorRequiredToolsResult{Connectors: []ConnectorToolRequirements{toolRequirementsForDefinition(definition)}}
		result.Message = connectorRequiredToolsMessage(result)
		return result, nil
	}

	result := ConnectorRequiredToolsResult{}
	for _, definition := range connectorRegistry {
		if definition.Bridged {
			result.Connectors = append(result.Connectors, toolRequirementsForDefinition(definition))
		}
	}
	result.Message = connectorRequiredToolsMessage(result)
	return result, nil
}

func toolRequirementsForDefinition(definition connectorDefinition) ConnectorToolRequirements {
	tools := make([]ProviderToolRequirement, len(definition.RequiredTools))
	for index, requirement := range definition.RequiredTools {
		tools[index] = requirement
		tools[index].Capabilities = append([]string(nil), requirement.Capabilities...)
	}
	return ConnectorToolRequirements{Connector: definition.ID, Tools: tools}
}

func connectorRequiredToolsMessage(result ConnectorRequiredToolsResult) string {
	lines := []string{"Required provider tools"}
	for _, connector := range result.Connectors {
		lines = append(lines, connector.Connector)
		for _, requirement := range connector.Tools {
			line := fmt.Sprintf("  %s [%s]", requirement.Operation, strings.Join(requirement.Capabilities, ", "))
			if requirement.When != "" {
				line += " when " + requirement.When
			}
			if requirement.Detail != "" {
				line += ": " + requirement.Detail
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
