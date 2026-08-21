package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultSuggestionScanLimit          = 20
	maximumSuggestionScanLimit          = 100
	defaultSuggestionScanMaxDirectories = 10_000
	suggestionScanDirectoryThreshold    = 16
	suggestionScanEventThreshold        = 8
	suggestionScanDistinctPathThreshold = 3
	suggestionScanEventLimit            = 2_000
	suggestionScanEvidencePathLimit     = 20
	suggestionScanEvidenceEventLimit    = 50
)

var suggestionScanEventWindow = 24 * time.Hour

// SuggestionScanConfig controls an explicit deterministic suggestion scan.
type SuggestionScanConfig struct {
	HomeDir        string
	DatabasePath   string
	Type           string
	Limit          int
	MaxDirectories int
	Now            time.Time
}

// SuggestionScanResult describes suggestions recorded by one bounded scan.
type SuggestionScanResult struct {
	Type                 string
	WatchRoots           int
	DirectoriesInspected int
	Truncated            bool
	Suggestions          []Suggestion
	Message              string
}

type suggestionScanCandidate struct {
	Path           string
	Name           string
	DirectoryCount int
	EventIDs       []string
	SamplePaths    []string
	eventIDSet     map[string]bool
	pathSet        map[string]bool
}

type suggestionScanProposal struct {
	Type         string
	PatternKey   string
	Title        string
	Reason       string
	Score        int
	EvidenceJSON string
}

type suggestionScanEvidence struct {
	EventIDs       []string `json:"event_ids,omitempty"`
	Paths          []string `json:"paths,omitempty"`
	Sources        []string `json:"sources"`
	Score          int      `json:"score"`
	MatchedSignals []string `json:"matched_signals"`
	Reasons        []string `json:"reasons"`
}

// ScanSuggestions runs bounded local suggestion producers and stores proposals.
func ScanSuggestions(config SuggestionScanConfig) (SuggestionScanResult, error) {
	scanType := strings.TrimSpace(config.Type)
	if scanType == "" {
		scanType = "ignore"
	}
	if scanType != "ignore" {
		return SuggestionScanResult{}, fmt.Errorf("unsupported suggestion scan type %q", config.Type)
	}

	limit := config.Limit
	if limit == 0 {
		limit = defaultSuggestionScanLimit
	}
	if limit < 1 || limit > maximumSuggestionScanLimit {
		return SuggestionScanResult{}, fmt.Errorf("suggestion scan limit must be between 1 and %d", maximumSuggestionScanLimit)
	}
	maxDirectories := config.MaxDirectories
	if maxDirectories == 0 {
		maxDirectories = defaultSuggestionScanMaxDirectories
	}
	if maxDirectories < 1 {
		return SuggestionScanResult{}, errors.New("suggestion scan directory bound must be positive")
	}

	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return SuggestionScanResult{}, err
	}
	now := config.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	candidates := map[string]*suggestionScanCandidate{}
	inspected, truncated, err := scanSuggestionDirectories(status, maxDirectories, candidates)
	if err != nil {
		return SuggestionScanResult{}, err
	}

	db, err := openSuggestionDatabase(status.HomeDir, status.DatabasePath)
	if err != nil {
		return SuggestionScanResult{}, err
	}
	defer db.Close()
	if err := scanSuggestionEvents(db, status, now, candidates); err != nil {
		return SuggestionScanResult{}, err
	}

	proposals, err := buildSuggestionScanProposals(candidates, defaultMaxWatchEntries())
	if err != nil {
		return SuggestionScanResult{}, err
	}
	sort.Slice(proposals, func(i, j int) bool {
		if proposals[i].Score != proposals[j].Score {
			return proposals[i].Score > proposals[j].Score
		}
		if proposals[i].Type != proposals[j].Type {
			return proposals[i].Type < proposals[j].Type
		}
		return proposals[i].PatternKey < proposals[j].PatternKey
	})

	result := SuggestionScanResult{
		Type:                 scanType,
		WatchRoots:           len(status.WatchDirs),
		DirectoriesInspected: inspected,
		Truncated:            truncated,
	}
	for _, proposal := range proposals {
		if len(result.Suggestions) >= limit {
			break
		}
		suppressed, err := suggestionScanSuppressed(db, proposal.Type, proposal.PatternKey, now)
		if err != nil {
			return SuggestionScanResult{}, err
		}
		if suppressed {
			continue
		}
		suggestion, err := UpsertSuggestion(SuggestionUpsert{
			HomeDir:      status.HomeDir,
			DatabasePath: status.DatabasePath,
			Type:         proposal.Type,
			PatternKey:   proposal.PatternKey,
			Title:        proposal.Title,
			Reason:       proposal.Reason,
			Confidence:   "high",
			Lane:         "baseline",
			EvidenceJSON: proposal.EvidenceJSON,
		})
		if err != nil {
			return SuggestionScanResult{}, err
		}
		result.Suggestions = append(result.Suggestions, suggestion)
	}
	result.Message = suggestionScanMessage(result)
	return result, nil
}

func scanSuggestionDirectories(status RunStatus, maxDirectories int, candidates map[string]*suggestionScanCandidate) (int, bool, error) {
	inspected := 0
	truncated := false
	seen := map[string]bool{}
	conservativeRoots := map[string]bool{}
	for _, root := range status.ConservativeWatchDirs {
		conservativeRoots[filepath.Clean(root)] = true
	}

	for _, root := range status.WatchDirs {
		root = filepath.Clean(root)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.IsDir() {
				return nil
			}
			path = filepath.Clean(path)
			if seen[path] {
				return filepath.SkipDir
			}
			if shouldIgnorePath(path, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames) {
				return filepath.SkipDir
			}
			if path != root && filepath.Dir(path) == root && conservativeRoots[root] && !looksLikeWorkDirectory(path) {
				return filepath.SkipDir
			}
			if inspected >= maxDirectories {
				truncated = true
				return filepath.SkipAll
			}
			seen[path] = true
			inspected++

			candidatePath, candidateName := generatedSuggestionCandidate(root, path)
			if candidatePath != "" {
				candidate := ensureSuggestionScanCandidate(candidates, candidatePath, candidateName)
				candidate.DirectoryCount++
				candidate.addSamplePath(path)
			}
			return nil
		})
		if err != nil {
			return 0, false, fmt.Errorf("scan watch root %q: %w", root, err)
		}
		if truncated {
			break
		}
	}
	return inspected, truncated, nil
}

func scanSuggestionEvents(db *sql.DB, status RunStatus, now time.Time, candidates map[string]*suggestionScanCandidate) error {
	since := now.Add(-suggestionScanEventWindow).Format(time.RFC3339Nano)
	rows, err := db.Query(`SELECT id, path FROM (
		SELECT id, COALESCE(json_extract(payload_json, '$.path'), '') AS path, timestamp
		FROM events
		WHERE source = 'file' AND timestamp >= ?
		ORDER BY timestamp DESC
		LIMIT ?
	) ORDER BY timestamp ASC`, since, suggestionScanEventLimit)
	if err != nil {
		return fmt.Errorf("query suggestion scan events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var eventID, path string
		if err := rows.Scan(&eventID, &path); err != nil {
			return fmt.Errorf("scan suggestion event: %w", err)
		}
		if strings.TrimSpace(path) == "" {
			continue
		}
		if shouldIgnorePath(path, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames) {
			continue
		}
		root := containingWatchRoot(status.WatchDirs, path)
		if root == "" {
			continue
		}
		candidatePath, candidateName := generatedSuggestionCandidate(root, filepath.Dir(path))
		if candidatePath == "" {
			continue
		}
		candidate := ensureSuggestionScanCandidate(candidates, candidatePath, candidateName)
		candidate.addEvent(eventID, path)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("query suggestion scan events: %w", err)
	}
	return nil
}

func buildSuggestionScanProposals(candidates map[string]*suggestionScanCandidate, watchLimit int) ([]suggestionScanProposal, error) {
	paths := make([]string, 0, len(candidates))
	for path := range candidates {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var proposals []suggestionScanProposal
	nameGroups := map[string][]*suggestionScanCandidate{}
	for _, path := range paths {
		candidate := candidates[path]
		if len(candidate.EventIDs) > 0 {
			nameGroups[candidate.Name] = append(nameGroups[candidate.Name], candidate)
		}
		if candidate.DirectoryCount < suggestionScanDirectoryThreshold && len(candidate.EventIDs) < suggestionScanEventThreshold {
			continue
		}
		proposal, err := pathSuggestionScanProposal(candidate, watchLimit)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, proposal)
	}

	names := make([]string, 0, len(nameGroups))
	for name := range nameGroups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		group := nameGroups[name]
		eventCount := 0
		for _, candidate := range group {
			eventCount += len(candidate.EventIDs)
		}
		if eventCount < suggestionScanEventThreshold || len(group) < suggestionScanDistinctPathThreshold {
			continue
		}
		proposal, err := nameSuggestionScanProposal(name, group, eventCount)
		if err != nil {
			return nil, err
		}
		proposals = append(proposals, proposal)
	}
	return proposals, nil
}

func pathSuggestionScanProposal(candidate *suggestionScanCandidate, watchLimit int) (suggestionScanProposal, error) {
	sources := []string{}
	signals := []string{"generated directory name: " + candidate.Name}
	reasons := []string{}
	if candidate.DirectoryCount > 0 {
		sources = append(sources, "filesystem_scan")
		signals = append(signals, fmt.Sprintf("directory pressure: %d directories", candidate.DirectoryCount))
		share := float64(candidate.DirectoryCount) * 100 / float64(watchLimit)
		reasons = append(reasons, fmt.Sprintf("subtree represents %.1f%% of the %d-directory watch budget", share, watchLimit))
	}
	if len(candidate.EventIDs) > 0 {
		sources = append(sources, "recent_file_events")
		signals = append(signals, fmt.Sprintf("recent event volume: %d file events", len(candidate.EventIDs)))
		reasons = append(reasons, fmt.Sprintf("%d captured file events occurred in the preceding 24 hours", len(candidate.EventIDs)))
	}
	score := evidenceThresholdScore(candidate.DirectoryCount, suggestionScanDirectoryThreshold, len(candidate.EventIDs), suggestionScanEventThreshold)
	evidence := suggestionScanEvidence{
		EventIDs:       boundedStrings(candidate.EventIDs, suggestionScanEvidenceEventLimit),
		Paths:          boundedStrings(candidate.SamplePaths, suggestionScanEvidencePathLimit),
		Sources:        sources,
		Score:          score,
		MatchedSignals: signals,
		Reasons:        reasons,
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return suggestionScanProposal{}, fmt.Errorf("encode path scan evidence: %w", err)
	}
	reason := fmt.Sprintf("Generated-looking path %s contains %d directories and %d recent file events.", candidate.Path, candidate.DirectoryCount, len(candidate.EventIDs))
	return suggestionScanProposal{
		Type:         "ignore_path",
		PatternKey:   candidate.Path,
		Title:        "Ignore generated path consuming watch capacity",
		Reason:       reason,
		Score:        score,
		EvidenceJSON: string(encoded),
	}, nil
}

func nameSuggestionScanProposal(name string, group []*suggestionScanCandidate, eventCount int) (suggestionScanProposal, error) {
	var eventIDs, paths []string
	for _, candidate := range group {
		eventIDs = append(eventIDs, candidate.EventIDs...)
		paths = append(paths, candidate.Path)
	}
	score := evidenceThresholdScore(len(group), suggestionScanDistinctPathThreshold, eventCount, suggestionScanEventThreshold)
	evidence := suggestionScanEvidence{
		EventIDs: boundedStrings(eventIDs, suggestionScanEvidenceEventLimit),
		Paths:    boundedStrings(paths, suggestionScanEvidencePathLimit),
		Sources:  []string{"recent_file_events"},
		Score:    score,
		MatchedSignals: []string{
			"generated basename: " + name,
			fmt.Sprintf("repeated path count: %d candidate paths", len(group)),
			fmt.Sprintf("recent event volume: %d file events", eventCount),
		},
		Reasons: []string{fmt.Sprintf("%d captured file events occurred under %d candidate paths in the preceding 24 hours", eventCount, len(group))},
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return suggestionScanProposal{}, fmt.Errorf("encode name scan evidence: %w", err)
	}
	return suggestionScanProposal{
		Type:         "ignore_name",
		PatternKey:   name,
		Title:        "Ignore recurring generated basename",
		Reason:       fmt.Sprintf("%d captured file events occurred under generated basename %s across %d candidate paths.", eventCount, name, len(group)),
		Score:        score,
		EvidenceJSON: string(encoded),
	}, nil
}

func generatedSuggestionCandidate(root, path string) (string, string) {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ""
	}
	candidate := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		candidate = filepath.Join(candidate, component)
		if looksGeneratedSuggestionName(component) {
			return candidate, component
		}
	}
	return "", ""
}

func looksGeneratedSuggestionName(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	for _, marker := range []string{"cache", "generated", "tmp", "temp", "derived", ".noindex", "userdata"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	switch normalized {
	case "build", "dist", "out", "target", "release", "releases":
		return true
	}
	return strings.HasSuffix(normalized, ".xcarchive")
}

func ensureSuggestionScanCandidate(candidates map[string]*suggestionScanCandidate, path, name string) *suggestionScanCandidate {
	if candidate := candidates[path]; candidate != nil {
		return candidate
	}
	candidate := &suggestionScanCandidate{
		Path:       path,
		Name:       name,
		eventIDSet: map[string]bool{},
		pathSet:    map[string]bool{},
	}
	candidates[path] = candidate
	return candidate
}

func (candidate *suggestionScanCandidate) addSamplePath(path string) {
	if candidate.pathSet[path] {
		return
	}
	candidate.pathSet[path] = true
	if len(candidate.SamplePaths) < suggestionScanEvidencePathLimit {
		candidate.SamplePaths = append(candidate.SamplePaths, path)
	}
}

func (candidate *suggestionScanCandidate) addEvent(eventID, path string) {
	if !candidate.eventIDSet[eventID] {
		candidate.eventIDSet[eventID] = true
		candidate.EventIDs = append(candidate.EventIDs, eventID)
	}
	candidate.addSamplePath(path)
}

func containingWatchRoot(roots []string, path string) string {
	best := ""
	for _, root := range roots {
		if sameOrChild(path, root) && len(root) > len(best) {
			best = root
		}
	}
	return best
}

func evidenceThresholdScore(first, firstThreshold, second, secondThreshold int) int {
	score := 0
	if firstThreshold > 0 {
		score = first * 70 / firstThreshold
	}
	if secondThreshold > 0 {
		if candidate := second * 70 / secondThreshold; candidate > score {
			score = candidate
		}
	}
	if score > 100 {
		return 100
	}
	return score
}

func boundedStrings(values []string, limit int) []string {
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]string(nil), values...)
}

func suggestionScanSuppressed(db *sql.DB, suggestionType, patternKey string, now time.Time) (bool, error) {
	rows, err := db.Query(`SELECT until_at FROM suggestion_suppressions WHERE type = ? AND pattern_key = ?`, suggestionType, patternKey)
	if err != nil {
		return false, fmt.Errorf("query suggestion scan suppression: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var until sql.NullString
		if err := rows.Scan(&until); err != nil {
			return false, fmt.Errorf("scan suggestion suppression: %w", err)
		}
		if !until.Valid {
			return true, nil
		}
		untilAt, err := time.Parse(time.RFC3339, until.String)
		if err != nil || now.Before(untilAt) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("query suggestion scan suppression: %w", err)
	}
	return false, nil
}

func suggestionScanMessage(result SuggestionScanResult) string {
	lines := []string{
		"Suggestion scan complete",
		"Type: " + result.Type,
		fmt.Sprintf("Watch roots: %d", result.WatchRoots),
		fmt.Sprintf("Directories inspected: %d", result.DirectoriesInspected),
		fmt.Sprintf("Scan truncated: %s", yesNo(result.Truncated)),
		fmt.Sprintf("Suggestions recorded: %d", len(result.Suggestions)),
	}
	if len(result.Suggestions) == 0 {
		lines = append(lines, "No deterministic ignore candidates found.")
		return strings.Join(lines, "\n")
	}
	for _, suggestion := range result.Suggestions {
		lines = append(lines, fmt.Sprintf("- %s %s %s", suggestion.ID, suggestion.Type, suggestion.PatternKey))
	}
	lines = append(lines,
		"No ignore settings were changed.",
		"Run 'workgraph suggestions show <id>' to inspect evidence.",
		"Run 'workgraph suggestions approve <id>' to apply a suggestion.",
	)
	return strings.Join(lines, "\n")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
