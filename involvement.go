package workgraph

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

var allowedInvolvement = map[string]bool{
	"assigned":             true,
	"attendee":             true,
	"authored":             true,
	"created":              true,
	"edited":               true,
	"explicitly_watched":   true,
	"involved":             true,
	"mentioned":            true,
	"organizer":            true,
	"recipient":            true,
	"review_requested":     true,
	"thread_participation": true,
}

func validInvolvement(value string) bool {
	return allowedInvolvement[strings.TrimSpace(value)]
}

func normalizeInvolvement(values []string) ([]string, error) {
	unique := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validInvolvement(value) {
			return nil, fmt.Errorf("unknown involvement %q", value)
		}
		unique[value] = true
	}
	normalized := make([]string, 0, len(unique))
	for value := range unique {
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func involvementJSON(values []string) (string, error) {
	normalized, err := normalizeInvolvement(values)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode involvement: %w", err)
	}
	return string(encoded), nil
}
