package workgraph

import (
	"fmt"
	"regexp"
)

type llmOutboundFilterResult struct {
	Messages        []openAICompatibleMessage
	RedactionCount  int
	ManagedPatterns int
}

type llmRedactionPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

func filterHostedLLMMessages(messages []openAICompatibleMessage) (llmOutboundFilterResult, error) {
	patterns, managedCount, err := llmOutboundRedactionPatterns()
	if err != nil {
		return llmOutboundFilterResult{}, err
	}
	filtered := make([]openAICompatibleMessage, 0, len(messages))
	total := 0
	for _, message := range messages {
		content := message.Content
		for _, pattern := range patterns {
			matches := pattern.Pattern.FindAllStringIndex(content, -1)
			if len(matches) == 0 {
				continue
			}
			total += len(matches)
			content = pattern.Pattern.ReplaceAllString(content, "[REDACTED:"+pattern.Name+"]")
		}
		message.Content = content
		filtered = append(filtered, message)
	}
	return llmOutboundFilterResult{
		Messages:        filtered,
		RedactionCount:  total,
		ManagedPatterns: managedCount,
	}, nil
}

func llmOutboundRedactionPatterns() ([]llmRedactionPattern, int, error) {
	patterns := []llmRedactionPattern{
		mustLLMRedactionPattern("github-token", `\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9_]{20,}\b`),
		mustLLMRedactionPattern("slack-token", `\bxox(?:[abprs]|c)-[A-Za-z0-9-]{10,}\b`),
		mustLLMRedactionPattern("notion-token", `\b(?:secret|ntn)_[A-Za-z0-9_]{20,}\b`),
		mustLLMRedactionPattern("aws-access-key", `\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
		mustLLMRedactionPattern("bearer-token", `(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{20,}`),
		mustLLMRedactionPattern("private-key", `-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	}
	managed, _, present, err := readManagedSettings()
	if err != nil {
		return nil, 0, err
	}
	if !present {
		return patterns, 0, nil
	}
	managedPatterns := 0
	for _, rawPattern := range managed.LLM.OutboundFilter.SensitivePatterns.Value {
		if rawPattern == "" {
			continue
		}
		compiled, err := regexp.Compile(rawPattern)
		if err != nil {
			return nil, 0, fmt.Errorf("compile managed outbound LLM sensitive pattern: %w", err)
		}
		patterns = append(patterns, llmRedactionPattern{Name: "managed-pattern", Pattern: compiled})
		managedPatterns++
	}
	return patterns, managedPatterns, nil
}

func mustLLMRedactionPattern(name string, pattern string) llmRedactionPattern {
	compiled := regexp.MustCompile(pattern)
	return llmRedactionPattern{Name: name, Pattern: compiled}
}
