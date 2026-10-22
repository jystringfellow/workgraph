package workgraph

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	slackKindIM      = "im"
	slackKindMPIM    = "mpim"
	slackKindChannel = "channel"
)

var (
	slackConversationIDPattern = regexp.MustCompile(`^[CDG][A-Z0-9]{8,}$`)
	slackMPDMSuffixPattern     = regexp.MustCompile(`-\d+$`)
	slackDMLabelPrefixes       = []struct{ prefix, kind string }{
		{"group direct message", slackKindMPIM},
		{"group dm", slackKindMPIM},
		{"direct message", slackKindIM},
		{"dm", slackKindIM},
	}
)

func slackPayloadConversationKind(payload string) string {
	var parsed struct {
		ConversationType string `json:"conversation_type"`
		ChannelType      string `json:"channel_type"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return ""
	}
	for _, value := range []string{parsed.ConversationType, parsed.ChannelType} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "im", "dm":
			return slackKindIM
		case "mpim", "group_dm":
			return slackKindMPIM
		case "channel", "public_channel", "private_channel":
			return slackKindChannel
		}
	}
	return ""
}

func parseSlackDMLabel(name string) (string, string, bool) {
	lower := strings.ToLower(name)
	switch lower {
	case "im":
		return slackKindIM, "", true
	case "mpim":
		return slackKindMPIM, "", true
	}
	if strings.HasPrefix(lower, "mpdm-") {
		handles := strings.Split(slackMPDMSuffixPattern.ReplaceAllString(name[len("mpdm-"):], ""), "--")
		return slackKindMPIM, strings.Join(handles, ", "), true
	}
	for _, label := range slackDMLabelPrefixes {
		if !strings.HasPrefix(lower, label.prefix) {
			continue
		}
		rest := strings.TrimSpace(name[len(label.prefix):])
		switch {
		case rest == "":
		case strings.HasPrefix(rest, ":"):
			rest = rest[1:]
		case strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")"):
			rest = rest[1 : len(rest)-1]
		case strings.HasPrefix(strings.ToLower(rest), "with "):
			rest = rest[len("with "):]
		default:
			return "", "", false
		}
		return label.kind, strings.TrimSpace(rest), true
	}
	return "", "", false
}

func canonicalSlackContainer(name, channelID, kind string) string {
	name = slackContainerName(name)
	if name == channelID {
		return ""
	}
	if name == "" || kind == slackKindChannel {
		return name
	}
	labelKind, names, ok := parseSlackDMLabel(name)
	if !ok {
		return name
	}
	if names == "" || slackConversationIDPattern.MatchString(names) {
		if channelID == "" {
			return name
		}
		return ""
	}
	if kind == "" {
		kind = labelKind
	}
	if kind == slackKindMPIM {
		return "Group DM: " + names
	}
	return "DM: " + names
}

func slackConversationFallbackContainer(payload, label string) string {
	channelID, channelName := slackResumeChannelIdentity(payload)
	if channelID == "" {
		return ""
	}
	kind := slackPayloadConversationKind(payload)
	if kind == "" {
		for _, candidate := range []string{channelName, slackContainerName(label)} {
			if labelKind, _, ok := parseSlackDMLabel(candidate); ok {
				kind = labelKind
				break
			}
		}
	}
	if kind == "" && strings.HasPrefix(channelID, "D") {
		kind = slackKindIM
	}
	return slackChannelIDContainer(channelID, kind)
}

func slackChannelIDContainer(channelID, kind string) string {
	switch kind {
	case slackKindIM:
		return "DM (" + channelID + ")"
	case slackKindMPIM:
		return "Group DM (" + channelID + ")"
	default:
		return ""
	}
}

func slackIDOnlyContainer(project, channelID string) bool {
	project = strings.TrimSpace(project)
	return project == channelID || project == slackChannelIDContainer(channelID, slackKindIM) || project == slackChannelIDContainer(channelID, slackKindMPIM)
}

func storedSlackContainer(project, payload string) string {
	channelID, _ := slackResumeChannelIdentity(payload)
	if container := canonicalSlackContainer(project, channelID, slackPayloadConversationKind(payload)); container != "" {
		return container
	}
	return slackPayloadContainer(payload)
}

func slackDMRepairCandidate(project string) bool {
	name := slackContainerName(project)
	if slackConversationIDPattern.MatchString(name) {
		return true
	}
	_, _, ok := parseSlackDMLabel(name)
	return ok
}

func slackRawChannelIDProject(project, payload string) bool {
	return slackConversationIDPattern.MatchString(slackContainerName(project)) && slackConversationFallbackContainer(payload, "") == ""
}
