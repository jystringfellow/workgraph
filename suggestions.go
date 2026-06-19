package workgraph

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type SuggestionUpsert struct {
	HomeDir      string
	DatabasePath string
	ID           string
	Type         string
	PatternKey   string
	Title        string
	Reason       string
	Confidence   string
	Lane         string
	EvidenceJSON string
}

type Suggestion struct {
	ID           string
	Type         string
	PatternKey   string
	Title        string
	Reason       string
	Confidence   string
	Lane         string
	Status       string
	EvidenceJSON string
	CreatedAt    string
	UpdatedAt    string
	ResolvedAt   string
}

type SuggestionStatusUpdate struct {
	HomeDir      string
	DatabasePath string
	ID           string
	Status       string
	ReasonCode   string
	FeedbackNote string
}

type SuggestionFeedbackEvent struct {
	HomeDir      string
	DatabasePath string
	SuggestionID string
	Action       string
	ReasonCode   string
	Note         string
}

type SuggestionSuppressionChange struct {
	HomeDir      string
	DatabasePath string
	Type         string
	PatternKey   string
	Reason       string
	UntilAt      string
}

type SuggestionListConfig struct {
	HomeDir      string
	DatabasePath string
	Status       string
	Limit        int
}

type SuggestionListResult struct {
	Suggestions []Suggestion
	Message     string
}

func UpsertSuggestion(config SuggestionUpsert) (Suggestion, error) {
	if err := validateSuggestionUpsert(config); err != nil {
		return Suggestion{}, err
	}
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return Suggestion{}, err
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	id := strings.TrimSpace(config.ID)
	if id == "" {
		id = stableSuggestionID(config.Type, config.PatternKey)
	}

	_, err = db.Exec(`INSERT INTO suggestions
		(id, type, pattern_key, title, reason, confidence, lane, status, evidence_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'proposed', ?, ?, ?)
		ON CONFLICT(type, pattern_key) DO UPDATE SET
			title = excluded.title,
			reason = excluded.reason,
			confidence = excluded.confidence,
			lane = excluded.lane,
			evidence_json = excluded.evidence_json,
			updated_at = excluded.updated_at`,
		id,
		strings.TrimSpace(config.Type),
		strings.TrimSpace(config.PatternKey),
		strings.TrimSpace(config.Title),
		strings.TrimSpace(config.Reason),
		strings.TrimSpace(config.Confidence),
		strings.TrimSpace(config.Lane),
		strings.TrimSpace(config.EvidenceJSON),
		now,
		now,
	)
	if err != nil {
		return Suggestion{}, fmt.Errorf("store suggestion: %w", err)
	}
	return readSuggestionByPattern(db, config.Type, config.PatternKey)
}

func UpdateSuggestionStatus(config SuggestionStatusUpdate) error {
	status := strings.TrimSpace(config.Status)
	if !validSuggestionStatus(status) || status == "proposed" || status == "acted" {
		return fmt.Errorf("unsupported suggestion lifecycle status %q", config.Status)
	}
	if strings.TrimSpace(config.ID) == "" {
		return errors.New("suggestion id is required")
	}
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("start suggestion status update: %w", err)
	}
	defer tx.Rollback()

	resolvedAt := sql.NullString{}
	if status == "approved" || status == "dismissed" || status == "snoozed" {
		resolvedAt = sql.NullString{String: now, Valid: true}
	}
	result, err := tx.Exec(`UPDATE suggestions SET status = ?, updated_at = ?, resolved_at = ? WHERE id = ?`,
		status,
		now,
		resolvedAt,
		strings.TrimSpace(config.ID),
	)
	if err != nil {
		return fmt.Errorf("update suggestion status: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check suggestion status update: %w", err)
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	if err := appendSuggestionFeedback(tx, SuggestionFeedbackEvent{
		SuggestionID: strings.TrimSpace(config.ID),
		Action:       feedbackActionForStatus(status),
		ReasonCode:   config.ReasonCode,
		Note:         config.FeedbackNote,
	}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit suggestion status update: %w", err)
	}
	return nil
}

func AppendSuggestionFeedback(config SuggestionFeedbackEvent) error {
	if strings.TrimSpace(config.SuggestionID) == "" {
		return errors.New("suggestion id is required")
	}
	if !validSuggestionFeedbackAction(strings.TrimSpace(config.Action)) {
		return fmt.Errorf("unsupported suggestion feedback action %q", config.Action)
	}
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()
	return appendSuggestionFeedback(db, config)
}

func AddSuggestionSuppression(config SuggestionSuppressionChange) (string, error) {
	if err := validateSuggestionSuppression(config); err != nil {
		return "", err
	}
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return "", err
	}
	defer db.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	id := stableSuggestionID("suppress:"+config.Type, config.PatternKey)
	_, err = db.Exec(`INSERT INTO suggestion_suppressions
		(id, type, pattern_key, reason, until_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(type, pattern_key) DO UPDATE SET
			reason = excluded.reason,
			until_at = excluded.until_at`,
		id,
		strings.TrimSpace(config.Type),
		strings.TrimSpace(config.PatternKey),
		nullableString(config.Reason),
		nullableString(config.UntilAt),
		now,
	)
	if err != nil {
		return "", fmt.Errorf("store suggestion suppression: %w", err)
	}
	return id, nil
}

func RemoveSuggestionSuppression(config SuggestionSuppressionChange) error {
	if strings.TrimSpace(config.Type) == "" {
		return errors.New("suggestion type is required")
	}
	if strings.TrimSpace(config.PatternKey) == "" {
		return errors.New("suggestion pattern key is required")
	}
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.Exec(`DELETE FROM suggestion_suppressions WHERE type = ? AND pattern_key = ?`,
		strings.TrimSpace(config.Type),
		strings.TrimSpace(config.PatternKey),
	); err != nil {
		return fmt.Errorf("remove suggestion suppression: %w", err)
	}
	return nil
}

func ListSuggestions(config SuggestionListConfig) (SuggestionListResult, error) {
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return SuggestionListResult{}, err
	}
	defer db.Close()

	status := strings.TrimSpace(config.Status)
	limit := config.Limit
	if limit <= 0 {
		limit = 25
	}

	query := `SELECT id, type, COALESCE(pattern_key, ''), title, reason, confidence, lane, status, evidence_json, created_at, updated_at, COALESCE(resolved_at, '')
		FROM suggestions`
	args := []any{}
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY updated_at DESC, created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return SuggestionListResult{}, fmt.Errorf("list suggestions: %w", err)
	}
	defer rows.Close()

	var suggestions []Suggestion
	for rows.Next() {
		suggestion, err := scanSuggestion(rows)
		if err != nil {
			return SuggestionListResult{}, err
		}
		suggestions = append(suggestions, suggestion)
	}
	if err := rows.Err(); err != nil {
		return SuggestionListResult{}, fmt.Errorf("list suggestions: %w", err)
	}

	result := SuggestionListResult{Suggestions: suggestions}
	result.Message = suggestionsListMessage(result)
	return result, nil
}

func DismissSuggestion(config SuggestionStatusUpdate) (Suggestion, error) {
	config.Status = "dismissed"
	if err := UpdateSuggestionStatus(config); err != nil {
		return Suggestion{}, err
	}
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return Suggestion{}, err
	}
	defer db.Close()
	return readSuggestionByID(db, config.ID)
}

func ApproveSuggestion(config SuggestionStatusUpdate) (Suggestion, error) {
	db, err := openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return Suggestion{}, err
	}
	suggestion, err := readSuggestionByID(db, config.ID)
	db.Close()
	if err != nil {
		return Suggestion{}, err
	}

	switch suggestion.Type {
	case "ignore_path":
		if _, err := addIgnorePath(SettingsIgnoreConfig{HomeDir: config.HomeDir, Path: suggestion.PatternKey}); err != nil {
			return Suggestion{}, err
		}
	case "ignore_name":
		if _, err := addIgnoreName(SettingsIgnoreConfig{HomeDir: config.HomeDir, Name: suggestion.PatternKey}); err != nil {
			return Suggestion{}, err
		}
	default:
		return Suggestion{}, fmt.Errorf("approval is not implemented for suggestion type %q", suggestion.Type)
	}

	config.Status = "approved"
	if strings.TrimSpace(config.ReasonCode) == "" {
		config.ReasonCode = "user_approved"
	}
	if err := UpdateSuggestionStatus(config); err != nil {
		return Suggestion{}, err
	}

	db, err = openSuggestionDatabase(config.HomeDir, config.DatabasePath)
	if err != nil {
		return Suggestion{}, err
	}
	defer db.Close()
	return readSuggestionByID(db, config.ID)
}

func openSuggestionDatabase(homeDir string, databasePath string) (*sql.DB, error) {
	status, err := prepareRunStatus(RunConfig{HomeDir: homeDir, DatabasePath: databasePath})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := createSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("ensure suggestion schema: %w", err)
	}
	return db, nil
}

func validateSuggestionUpsert(config SuggestionUpsert) error {
	if strings.TrimSpace(config.Type) == "" {
		return errors.New("suggestion type is required")
	}
	if strings.TrimSpace(config.PatternKey) == "" {
		return errors.New("suggestion pattern key is required")
	}
	if strings.TrimSpace(config.Title) == "" {
		return errors.New("suggestion title is required")
	}
	if strings.TrimSpace(config.Reason) == "" {
		return errors.New("suggestion reason is required")
	}
	if !validSuggestionConfidence(strings.TrimSpace(config.Confidence)) {
		return fmt.Errorf("unsupported suggestion confidence %q", config.Confidence)
	}
	if !validSuggestionLane(strings.TrimSpace(config.Lane)) {
		return fmt.Errorf("unsupported suggestion lane %q", config.Lane)
	}
	if !json.Valid([]byte(strings.TrimSpace(config.EvidenceJSON))) {
		return errors.New("suggestion evidence_json must be valid JSON")
	}
	return nil
}

func validateSuggestionSuppression(config SuggestionSuppressionChange) error {
	if strings.TrimSpace(config.Type) == "" {
		return errors.New("suggestion type is required")
	}
	if strings.TrimSpace(config.PatternKey) == "" {
		return errors.New("suggestion pattern key is required")
	}
	if strings.TrimSpace(config.UntilAt) != "" {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(config.UntilAt)); err != nil {
			return fmt.Errorf("suggestion suppression until_at must be RFC3339: %w", err)
		}
	}
	return nil
}

type suggestionFeedbackExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func appendSuggestionFeedback(db suggestionFeedbackExecutor, config SuggestionFeedbackEvent) error {
	action := strings.TrimSpace(config.Action)
	if !validSuggestionFeedbackAction(action) {
		return fmt.Errorf("unsupported suggestion feedback action %q", config.Action)
	}
	instant := time.Now().UTC()
	now := instant.Format(time.RFC3339)
	id := stableSuggestionID("feedback:"+strings.TrimSpace(config.SuggestionID)+":"+action, fmt.Sprintf("%d:%s:%s", instant.UnixNano(), config.ReasonCode, config.Note))
	_, err := db.Exec(`INSERT INTO suggestion_feedback
		(id, suggestion_id, action, reason_code, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id,
		strings.TrimSpace(config.SuggestionID),
		action,
		nullableString(config.ReasonCode),
		nullableString(config.Note),
		now,
	)
	if err != nil {
		return fmt.Errorf("append suggestion feedback: %w", err)
	}
	return nil
}

func readSuggestionByPattern(db *sql.DB, suggestionType string, patternKey string) (Suggestion, error) {
	row := db.QueryRow(`SELECT id, type, COALESCE(pattern_key, ''), title, reason, confidence, lane, status, evidence_json, created_at, updated_at, COALESCE(resolved_at, '')
		FROM suggestions WHERE type = ? AND pattern_key = ?`, strings.TrimSpace(suggestionType), strings.TrimSpace(patternKey))
	return scanSuggestion(row)
}

func readSuggestionByID(db *sql.DB, id string) (Suggestion, error) {
	row := db.QueryRow(`SELECT id, type, COALESCE(pattern_key, ''), title, reason, confidence, lane, status, evidence_json, created_at, updated_at, COALESCE(resolved_at, '')
		FROM suggestions WHERE id = ?`, strings.TrimSpace(id))
	return scanSuggestion(row)
}

type suggestionScanner interface {
	Scan(dest ...any) error
}

func scanSuggestion(scanner suggestionScanner) (Suggestion, error) {
	var suggestion Suggestion
	if err := scanner.Scan(
		&suggestion.ID,
		&suggestion.Type,
		&suggestion.PatternKey,
		&suggestion.Title,
		&suggestion.Reason,
		&suggestion.Confidence,
		&suggestion.Lane,
		&suggestion.Status,
		&suggestion.EvidenceJSON,
		&suggestion.CreatedAt,
		&suggestion.UpdatedAt,
		&suggestion.ResolvedAt,
	); err != nil {
		return Suggestion{}, err
	}
	return suggestion, nil
}

func suggestionsListMessage(result SuggestionListResult) string {
	lines := []string{
		"Suggestions",
		fmt.Sprintf("%d suggestion(s)", len(result.Suggestions)),
	}
	if len(result.Suggestions) == 0 {
		lines = append(lines, "No suggestions found.")
		return strings.Join(lines, "\n")
	}
	for _, suggestion := range result.Suggestions {
		lines = append(lines, fmt.Sprintf("- %s %s %s %s", suggestion.ID, suggestion.Type, suggestion.Status, suggestion.Title))
		lines = append(lines, "  confidence: "+suggestion.Confidence)
		lines = append(lines, "  lane: "+suggestion.Lane)
		if suggestion.PatternKey != "" {
			lines = append(lines, "  pattern: "+suggestion.PatternKey)
		}
		lines = append(lines, "  reason: "+suggestion.Reason)
	}
	return strings.Join(lines, "\n")
}

func stableSuggestionID(suggestionType string, patternKey string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(suggestionType) + "\x00" + strings.TrimSpace(patternKey)))
	return "sug_" + hex.EncodeToString(sum[:])[:16]
}

func feedbackActionForStatus(status string) string {
	if status == "approved" {
		return "accepted"
	}
	return status
}

func nullableString(value string) sql.NullString {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func validSuggestionConfidence(confidence string) bool {
	switch confidence {
	case "high", "medium", "low":
		return true
	default:
		return false
	}
}

func validSuggestionLane(lane string) bool {
	switch lane {
	case "baseline", "semantic", "manual":
		return true
	default:
		return false
	}
}

func validSuggestionStatus(status string) bool {
	switch status {
	case "proposed", "reviewed", "approved", "dismissed", "snoozed", "acted":
		return true
	default:
		return false
	}
}

func validSuggestionFeedbackAction(action string) bool {
	switch action {
	case "reviewed", "accepted", "approved", "dismissed", "snoozed", "completed", "undone":
		return true
	default:
		return false
	}
}
