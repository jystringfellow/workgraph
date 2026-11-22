package workgraph

import (
	"errors"
	"fmt"
)

type ConnectorPolicy struct {
	settings managedSettingsFile
	present  bool
}

func LoadConnectorPolicy() (ConnectorPolicy, error) {
	settings, _, present, err := readManagedSettings()
	if err != nil {
		return ConnectorPolicy{}, err
	}
	return newConnectorPolicy(settings, present), nil
}

func CurrentConnectorPolicy() (ConnectorPolicy, error) {
	return LoadConnectorPolicy()
}

func newConnectorPolicy(settings managedSettingsFile, present bool) ConnectorPolicy {
	return ConnectorPolicy{
		settings: settings,
		present:  present,
	}
}

func (policy ConnectorPolicy) CanSetup(id string) (bool, string) {
	return policy.canUseConnector(id)
}

func (policy ConnectorPolicy) CanEnable(id string) (bool, string) {
	return policy.canUseConnector(id)
}

func (policy ConnectorPolicy) CanPoll(id string) (bool, string) {
	return policy.canUseConnector(id)
}

func (policy ConnectorPolicy) EnforceSetup(id string) error {
	allowed, reason := policy.CanSetup(id)
	if allowed {
		return nil
	}
	return errors.New(reason)
}

func (policy ConnectorPolicy) EnforceEnable(id string) error {
	allowed, reason := policy.CanEnable(id)
	if allowed {
		return nil
	}
	return errors.New(reason)
}

func (policy ConnectorPolicy) EnforcePoll(id string) error {
	allowed, reason := policy.CanPoll(id)
	if allowed {
		return nil
	}
	return errors.New(reason)
}

func (policy ConnectorPolicy) CanCaptureSlackDMs(includeDMs bool) (bool, string) {
	if !includeDMs || !policy.present {
		return true, ""
	}
	if managedBoolLockedFalse(policy.settings.Connectors.Slack.IncludeDMs) {
		return false, "Slack DM capture is disabled by managed settings"
	}
	return true, ""
}

func (policy ConnectorPolicy) EnforceSlackDMs(includeDMs bool) error {
	allowed, reason := policy.CanCaptureSlackDMs(includeDMs)
	if allowed {
		return nil
	}
	return errors.New(reason)
}

func (policy ConnectorPolicy) canUseConnector(id string) (bool, string) {
	normalized, err := normalizeConnectorID(id)
	if err != nil {
		return false, err.Error()
	}
	allowed, status := policy.allowedByManagedSettings(normalized)
	if !allowed {
		return false, fmt.Sprintf("connector %s %s by managed settings", normalized, status)
	}
	return true, ""
}

func (policy ConnectorPolicy) allowedByManagedSettings(normalizedID string) (bool, string) {
	if !policy.present {
		return true, ""
	}
	if stringAllowedFold(normalizedID, policy.settings.Connectors.DisabledIDs.Value) {
		return false, "is disabled"
	}
	if len(policy.settings.Connectors.AllowedIDs.Value) > 0 && !stringAllowedFold(normalizedID, policy.settings.Connectors.AllowedIDs.Value) {
		return false, "is not allowed"
	}
	return true, ""
}
