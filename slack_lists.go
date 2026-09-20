package workgraph

import (
	"fmt"
	"strings"
)

type SlackListStateOptions struct {
	Column     string   `json:"column"`
	DoneValues []string `json:"done_values"`
}

type SlackListOptions struct {
	State            *SlackListStateOptions `json:"state,omitempty"`
	InterestColumns  []string               `json:"interest_columns,omitempty"`
	RowKeyCandidates [][]string             `json:"row_key_candidates,omitempty"`
}

func defaultSlackListRowKeyCandidates() [][]string {
	return [][]string{{"Related Message"}, {"Title", "Cycle"}, {"Title"}}
}

func normalizeSlackListOptions(options SlackListOptions) (SlackListOptions, error) {
	if options.State != nil {
		state := *options.State
		state.Column = strings.TrimSpace(state.Column)
		if state.Column == "" || len(state.DoneValues) == 0 {
			return SlackListOptions{}, fmt.Errorf("state requires a non-empty column and done_values array")
		}
		state.DoneValues = normalizeSlackListColumns(state.DoneValues)
		if len(state.DoneValues) == 0 {
			return SlackListOptions{}, fmt.Errorf("state requires a non-empty column and done_values array")
		}
		options.State = &state
	}
	options.InterestColumns = normalizeSlackListColumns(options.InterestColumns)
	if len(options.RowKeyCandidates) > 0 {
		candidates, err := normalizeSlackListRowKeyCandidates(options.RowKeyCandidates)
		if err != nil {
			return SlackListOptions{}, err
		}
		options.RowKeyCandidates = candidates
	}
	return options, nil
}

func normalizeSlackListOptionsMap(options map[string]SlackListOptions, listIDs []string) (map[string]SlackListOptions, error) {
	if len(options) == 0 {
		return nil, nil
	}
	allowed := make(map[string]string, len(listIDs))
	for _, listID := range listIDs {
		trimmed := strings.TrimSpace(listID)
		if trimmed != "" {
			allowed[strings.ToLower(trimmed)] = trimmed
		}
	}
	normalized := make(map[string]SlackListOptions, len(options))
	for listID, option := range options {
		canonicalID, found := allowed[strings.ToLower(strings.TrimSpace(listID))]
		if !found {
			return nil, fmt.Errorf("list_options contains unconfigured Slack List %q", strings.TrimSpace(listID))
		}
		parsed, err := normalizeSlackListOptions(option)
		if err != nil {
			return nil, fmt.Errorf("list_options %q: %w", canonicalID, err)
		}
		normalized[canonicalID] = parsed
	}
	return normalized, nil
}

func normalizeSlackListColumns(columns []string) []string {
	normalized := make([]string, 0, len(columns))
	seen := map[string]bool{}
	for _, column := range columns {
		column = strings.TrimSpace(column)
		key := strings.ToLower(column)
		if column == "" || seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, column)
	}
	return normalized
}

func normalizeSlackListRowKeyCandidates(candidates [][]string) ([][]string, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("row_key_candidates must be a non-empty array of non-empty string arrays")
	}
	normalized := make([][]string, len(candidates))
	for index, candidate := range candidates {
		normalized[index] = normalizeSlackListColumns(candidate)
		if len(normalized[index]) == 0 || len(normalized[index]) != len(candidate) {
			return nil, fmt.Errorf("row_key_candidates must be a non-empty array of non-empty string arrays")
		}
	}
	return normalized, nil
}

func slackListOptionsFor(options map[string]SlackListOptions, listID string) SlackListOptions {
	option, _ := findSlackListOptions(options, listID)
	return option
}

func findSlackListOptions(options map[string]SlackListOptions, listID string) (SlackListOptions, bool) {
	for configuredID, option := range options {
		if strings.EqualFold(strings.TrimSpace(configuredID), strings.TrimSpace(listID)) {
			return option, true
		}
	}
	return SlackListOptions{}, false
}

func interpretSlackListFields(fields map[string]any, options SlackListOptions, legacyDoneColumn string) (*bool, map[string]any, error) {
	var done *bool
	state := options.State
	legacy := false
	if state == nil && strings.TrimSpace(legacyDoneColumn) != "" {
		state = &SlackListStateOptions{Column: strings.TrimSpace(legacyDoneColumn)}
		legacy = true
	}
	if state != nil {
		value, found := slackListSnapshotField(fields, state.Column)
		if found && snapshotFieldText(value) != "" {
			interpreted := false
			if legacy {
				var err error
				interpreted, err = normalizeSlackListDone(value)
				if err != nil {
					return nil, nil, fmt.Errorf("state column %q: %w", state.Column, err)
				}
			} else {
				actual := strings.TrimSpace(snapshotFieldText(value))
				for _, candidate := range state.DoneValues {
					if strings.EqualFold(actual, strings.TrimSpace(candidate)) {
						interpreted = true
						break
					}
				}
			}
			done = &interpreted
		}
	}

	interest := map[string]any{}
	for _, column := range options.InterestColumns {
		if value, found := slackListSnapshotField(fields, column); found {
			interest[column] = value
		}
	}
	if len(interest) == 0 {
		interest = nil
	}
	return done, interest, nil
}
