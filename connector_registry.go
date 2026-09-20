package workgraph

import "time"

type connectorDefinition struct {
	ID               string
	EventSource      string
	Direct           bool
	Bridged          bool
	CaptureSemantics string
	RequiredTools    []ProviderToolRequirement
	DefaultInterval  func() time.Duration
	ValidateBridge   func(bridgeParamValues) (bool, error)
}

var connectorRegistry = []connectorDefinition{
	{ID: "git", EventSource: "git", Direct: true, DefaultInterval: func() time.Duration { return gitPollInterval(0) }},
	{ID: "github", EventSource: "github", Direct: true, Bridged: true, CaptureSemantics: "bounded_events", RequiredTools: []ProviderToolRequirement{
		fetchIdentityProviderTool("github.search_pull_requests", "return repository, number, and updated revision for every matching pull request"),
		fetchIdentityProviderTool("github.search_issues", "return repository, number, and updated revision for every matching issue"),
	}, DefaultInterval: func() time.Duration { return githubPollInterval(0) }, ValidateBridge: validateGitHubBridgeParams},
	{ID: "slack", EventSource: "slack", Direct: true, Bridged: true, CaptureSemantics: "bounded_events", RequiredTools: []ProviderToolRequirement{
		fetchProviderToolWhen("slack.read_channel", "channels", "read every configured channel across the request window"),
		fetchProviderToolWhen("slack.search_messages", "participant_or_dms", "search only the approved participant or direct-message scope"),
		fetchIdentityProviderTool("slack.read_thread", "return detailed per-message timestamps and edit metadata for complete threads"),
	}, DefaultInterval: func() time.Duration { return slackPollInterval(0) }, ValidateBridge: validateSlackBridgeParams},
	{ID: "slack.lists", EventSource: "slack", Direct: true, Bridged: true, CaptureSemantics: "complete_snapshot", RequiredTools: []ProviderToolRequirement{
		fetchIdentityProviderTool("slack.read_file", "return complete List CSV used for row keys and content-hash revisions"),
	}, DefaultInterval: func() time.Duration { return slackListPollInterval(0) }, ValidateBridge: validateSlackListsBridgeParams},
	{ID: "calendar.google", EventSource: "calendar.google", Direct: true, Bridged: true, CaptureSemantics: "bounded_events", RequiredTools: []ProviderToolRequirement{
		fetchProviderTool("google.search_calendar_events", "search every configured calendar across the occurrence window"),
		fetchIdentityProviderTool("google.read_calendar_event", "return stable event id and revision or last-modified marker"),
	}, DefaultInterval: func() time.Duration { return calendarPollInterval(0) }, ValidateBridge: func(values bridgeParamValues) (bool, error) {
		return validateCalendarBridgeParams("calendar.google", values)
	}},
	{ID: "calendar.microsoft", EventSource: "calendar.microsoft", Direct: true, Bridged: true, CaptureSemantics: "bounded_events", RequiredTools: []ProviderToolRequirement{
		fetchProviderTool("microsoft.search_calendar_events", "search every configured calendar across the occurrence window"),
		fetchIdentityProviderTool("microsoft.read_resource", "return stable event id plus change key or last-modified revision"),
	}, DefaultInterval: func() time.Duration { return calendarPollInterval(0) }, ValidateBridge: func(values bridgeParamValues) (bool, error) {
		return validateCalendarBridgeParams("calendar.microsoft", values)
	}},
	{ID: "mail.google", EventSource: "mail.google", Direct: true, Bridged: true, CaptureSemantics: "bounded_events", RequiredTools: []ProviderToolRequirement{
		fetchProviderTool("google.search_mail", "search every configured mailbox across the request window"),
		fetchIdentityProviderTool("google.read_mail", "return stable message id, receipt time, and bounded preview"),
	}, DefaultInterval: func() time.Duration { return mailPollInterval(0) }, ValidateBridge: func(values bridgeParamValues) (bool, error) { return validateMailBridgeParams("mail.google", values) }},
	{ID: "mail.microsoft", EventSource: "mail.microsoft", Direct: true, Bridged: true, CaptureSemantics: "bounded_events", RequiredTools: []ProviderToolRequirement{
		fetchProviderTool("microsoft.search_mail", "search every configured folder across the padded request window"),
		fetchIdentityProviderTool("microsoft.read_resource", "return stable message id, receipt time, and bounded preview"),
	}, DefaultInterval: func() time.Duration { return mailPollInterval(0) }, ValidateBridge: func(values bridgeParamValues) (bool, error) {
		return validateMailBridgeParams("mail.microsoft", values)
	}},
	{ID: "notion", EventSource: "notion", Direct: true, DefaultInterval: func() time.Duration { return notionPollInterval(0) }},
	{ID: "notion.activity", EventSource: "notion", Bridged: true, CaptureSemantics: "bounded_events", RequiredTools: []ProviderToolRequirement{
		fetchIdentityProviderTool("notion.search", "return object id and last-edited time for participant-scoped results"),
	}, DefaultInterval: func() time.Duration { return 30 * time.Minute }, ValidateBridge: validateNotionActivityBridgeParams},
	{ID: "azure.boards", EventSource: "azure.boards", Direct: true, Bridged: true, CaptureSemantics: "bounded_events", RequiredTools: []ProviderToolRequirement{
		fetchProviderTool("azure.list_projects", "discover an accessible routing project without changing approved scope"),
		fetchProviderTool("azure.query_work_items", "query the approved project, area, or participant scope"),
		fetchIdentityProviderTool("azure.read_work_item", "return work item id and provider revision"),
	}, DefaultInterval: func() time.Duration { return azureBoardsPollInterval(0) }, ValidateBridge: validateAzureBoardsBridgeParams},
}

func fetchProviderTool(operation string, detail string) ProviderToolRequirement {
	return ProviderToolRequirement{Operation: operation, Capabilities: []string{"fetch"}, Detail: detail}
}

func fetchIdentityProviderTool(operation string, detail string) ProviderToolRequirement {
	return ProviderToolRequirement{Operation: operation, Capabilities: []string{"fetch", "identity"}, Detail: detail}
}

func fetchProviderToolWhen(operation string, when string, detail string) ProviderToolRequirement {
	requirement := fetchProviderTool(operation, detail)
	requirement.When = when
	return requirement
}

func registeredConnector(id string) (connectorDefinition, bool) {
	for _, definition := range connectorRegistry {
		if definition.ID == id {
			return definition, true
		}
	}
	return connectorDefinition{}, false
}

func (definition connectorDefinition) supportedModes() []string {
	modes := make([]string, 0, 2)
	if definition.Direct {
		modes = append(modes, "direct")
	}
	if definition.Bridged {
		modes = append(modes, "bridged")
	}
	return modes
}
