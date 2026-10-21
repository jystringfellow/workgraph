package workgraph

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const projectLinkSummaryMaxRunes = 120

const projectLinksSchema = `CREATE TABLE IF NOT EXISTS project_links (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 unit_id TEXT NOT NULL,
 unit_kind TEXT NOT NULL,
 container TEXT NOT NULL DEFAULT '',
 target TEXT NOT NULL CHECK (target IN ('project', 'new_project', 'none')),
 project TEXT NOT NULL DEFAULT '',
 proposed_project_json TEXT,
 source TEXT NOT NULL CHECK (source IN ('deterministic', 'llm', 'user')),
 kind TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL DEFAULT '',
 confidence REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
 evidence_json TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('proposed', 'applied', 'rejected')),
 auto INTEGER NOT NULL DEFAULT 0,
 rule TEXT NOT NULL DEFAULT '',
 profile TEXT NOT NULL DEFAULT '',
 run_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 decided_at TEXT,
 decision TEXT NOT NULL DEFAULT '' CHECK (decision IN ('', 'approved', 'confirmed', 'corrected', 'undone'))
)`

const projectLinksCurrentIndex = `CREATE UNIQUE INDEX IF NOT EXISTS project_links_current_unit
 ON project_links (unit_id) WHERE state IN ('proposed', 'applied')`

const projectLinkColumns = `id, unit_id, unit_kind, container, target, project, proposed_project_json, source, kind, summary,
 confidence, evidence_json, state, auto, rule, profile, run_id, created_at, decided_at, decision`

var projectLinkKinds = map[string]bool{
	"review": true, "build": true, "coordination": true, "advice": true,
	"unblock": true, "decision": true, "fyi": true,
}

type ProjectLinkStoreConfig struct {
	HomeDir      string
	DatabasePath string
	Now          time.Time
}

type ProposedProject struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Workstream  string `json:"workstream,omitempty"`
}

type ProjectLink struct {
	ID         int64
	UnitID     string
	UnitKind   string
	Container  string
	Target     string
	Project    string
	NewProject *ProposedProject
	Source     string
	Kind       string
	Summary    string
	Confidence float64
	Evidence   []string
	State      string
	Auto       bool
	Rule       string
	Profile    string
	RunID      string
	CreatedAt  time.Time
	DecidedAt  time.Time
	Decision   string
}

type ProjectLinkProposal struct {
	UnitID     string
	UnitKind   string
	Container  string
	Target     string
	Project    string
	NewProject *ProposedProject
	Kind       string
	Summary    string
	Confidence float64
	Evidence   []string
	Rule       string
	Apply      bool
}

type ProjectLinkRun struct {
	Source  string
	Profile string
	RunID   string
	Links   []ProjectLinkProposal
}

type ProjectLinkRunResult struct {
	RunID   string
	Written []ProjectLink
	Skipped []string
}

type ProjectLinkAssignment struct {
	UnitID     string
	UnitKind   string
	Container  string
	Evidence   []string
	Target     string
	Project    string
	NewProject *ProposedProject
	Summary    string
}

type ProjectLinksQuery struct {
	UnitID string
	RunID  string
}

func RecordProjectLinkRun(config ProjectLinkStoreConfig, run ProjectLinkRun) (ProjectLinkRunResult, error) {
	var result ProjectLinkRunResult
	err := withProjectLinksTx(config, func(tx *sql.Tx, now time.Time) error {
		var err error
		result, err = recordProjectLinkRunTx(tx, now, run)
		return err
	})
	return result, err
}

func recordProjectLinkRunTx(tx *sql.Tx, now time.Time, run ProjectLinkRun) (ProjectLinkRunResult, error) {
	if run.Source != "deterministic" && run.Source != "llm" {
		return ProjectLinkRunResult{}, fmt.Errorf("unsupported project link run source %q", run.Source)
	}
	result := ProjectLinkRunResult{RunID: run.RunID}
	if result.RunID == "" {
		id, err := newEventID()
		if err != nil {
			return ProjectLinkRunResult{}, fmt.Errorf("create run id: %w", err)
		}
		result.RunID = "run_" + id
	}
	for _, proposal := range run.Links {
		link := ProjectLink{
			UnitID:     proposal.UnitID,
			UnitKind:   proposal.UnitKind,
			Container:  proposal.Container,
			Target:     proposal.Target,
			Project:    proposal.Project,
			NewProject: proposal.NewProject,
			Source:     run.Source,
			Kind:       proposal.Kind,
			Summary:    proposal.Summary,
			Confidence: proposal.Confidence,
			Evidence:   proposal.Evidence,
			State:      "proposed",
			Rule:       proposal.Rule,
			Profile:    run.Profile,
			RunID:      result.RunID,
		}
		if run.Source == "deterministic" {
			link.Confidence = 1
		}
		if proposal.Apply {
			link.State, link.Auto = "applied", true
		}
		current, err := currentProjectLinkTx(tx, link.UnitID)
		if err != nil {
			return ProjectLinkRunResult{}, err
		}
		if current != nil {
			result.Skipped = append(result.Skipped, link.UnitID)
			continue
		}
		written, err := insertProjectLink(tx, now, link)
		if err != nil {
			return ProjectLinkRunResult{}, err
		}
		result.Written = append(result.Written, written)
	}
	return result, nil
}

func ApproveProjectLink(config ProjectLinkStoreConfig, linkID int64) (ProjectLink, error) {
	var approved ProjectLink
	err := withProjectLinksTx(config, func(tx *sql.Tx, now time.Time) error {
		link, err := readProjectLinkTx(tx, linkID)
		if err != nil {
			return err
		}
		switch {
		case link.State == "proposed":
			link.State, link.Auto, link.Decision = "applied", false, "approved"
		case link.State == "applied" && link.Auto && link.Decision == "":
			link.Decision = "confirmed"
		default:
			return fmt.Errorf("project link %d is already decided", linkID)
		}
		link.DecidedAt = now
		approved = link
		return decideProjectLink(tx, link)
	})
	return approved, err
}

func AssignProjectLink(config ProjectLinkStoreConfig, assignment ProjectLinkAssignment) (ProjectLink, error) {
	var assigned ProjectLink
	err := withProjectLinksTx(config, func(tx *sql.Tx, now time.Time) error {
		var err error
		assigned, err = assignProjectLinkTx(tx, now, assignment)
		return err
	})
	return assigned, err
}

func RejectProjectLink(config ProjectLinkStoreConfig, linkID int64) (ProjectLink, error) {
	var assigned ProjectLink
	err := withProjectLinksTx(config, func(tx *sql.Tx, now time.Time) error {
		link, err := readProjectLinkTx(tx, linkID)
		if err != nil {
			return err
		}
		if !projectLinkCurrent(link) {
			return fmt.Errorf("project link %d is not proposed or applied", linkID)
		}
		assigned, err = assignProjectLinkTx(tx, now, ProjectLinkAssignment{UnitID: link.UnitID, Target: "none"})
		return err
	})
	return assigned, err
}

func assignProjectLinkTx(tx *sql.Tx, now time.Time, assignment ProjectLinkAssignment) (ProjectLink, error) {
	link := ProjectLink{
		UnitID:     assignment.UnitID,
		UnitKind:   assignment.UnitKind,
		Container:  assignment.Container,
		Target:     assignment.Target,
		Project:    assignment.Project,
		NewProject: assignment.NewProject,
		Source:     "user",
		Summary:    assignment.Summary,
		Confidence: 1,
		Evidence:   assignment.Evidence,
		State:      "applied",
	}
	current, err := currentProjectLinkTx(tx, assignment.UnitID)
	if err != nil {
		return ProjectLink{}, err
	}
	if current != nil {
		link.UnitKind = firstNonEmpty(link.UnitKind, current.UnitKind)
		link.Container = firstNonEmpty(link.Container, current.Container)
		link.Summary = firstNonEmpty(link.Summary, current.Summary)
		link.Kind = current.Kind
		if len(link.Evidence) == 0 {
			link.Evidence = current.Evidence
		}
		current.State, current.Decision, current.DecidedAt = "rejected", "corrected", now
		if err := decideProjectLink(tx, *current); err != nil {
			return ProjectLink{}, err
		}
	}
	return insertProjectLink(tx, now, link)
}

func UndoProjectLink(config ProjectLinkStoreConfig, linkID int64) (ProjectLink, error) {
	var replacement ProjectLink
	err := withProjectLinksTx(config, func(tx *sql.Tx, now time.Time) error {
		link, err := readProjectLinkTx(tx, linkID)
		if err != nil {
			return err
		}
		replacement, err = undoProjectLinkTx(tx, now, link)
		return err
	})
	return replacement, err
}

func UndoProjectLinkRun(config ProjectLinkStoreConfig, runID string) ([]ProjectLink, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, errors.New("run id is required")
	}
	var replacements []ProjectLink
	err := withProjectLinksTx(config, func(tx *sql.Tx, now time.Time) error {
		links, err := queryProjectLinks(tx, `WHERE run_id = ? AND state = 'applied' AND auto = 1 AND decision = '' ORDER BY id`, runID)
		if err != nil {
			return err
		}
		for _, link := range links {
			replacement, err := undoProjectLinkTx(tx, now, link)
			if err != nil {
				return err
			}
			replacements = append(replacements, replacement)
		}
		return nil
	})
	return replacements, err
}

func undoProjectLinkTx(tx *sql.Tx, now time.Time, link ProjectLink) (ProjectLink, error) {
	if link.State != "applied" || !link.Auto || link.Decision != "" {
		return ProjectLink{}, fmt.Errorf("project link %d is not an undecided auto-applied link", link.ID)
	}
	replacement := link
	replacement.State, replacement.Auto = "proposed", false
	link.State, link.Decision, link.DecidedAt = "rejected", "undone", now
	if err := decideProjectLink(tx, link); err != nil {
		return ProjectLink{}, err
	}
	return insertProjectLink(tx, now, replacement)
}

func ReadProjectLinks(config ProjectLinkStoreConfig, query ProjectLinksQuery) ([]ProjectLink, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'project_links')`).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	return queryProjectLinks(db, `WHERE (? = '' OR unit_id = ?) AND (? = '' OR run_id = ?) ORDER BY id`, query.UnitID, query.UnitID, query.RunID, query.RunID)
}

func withProjectLinksTx(config ProjectLinkStoreConfig, apply func(*sql.Tx, time.Time) error) error {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureProjectLinksSchema(tx); err != nil {
		return err
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	if err := apply(tx, now.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func ensureProjectLinksSchema(tx *sql.Tx) error {
	for _, statement := range []string{projectLinksSchema, projectLinksCurrentIndex} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("ensure project_links schema: %w", err)
		}
	}
	return nil
}

func validateProjectLink(link ProjectLink) error {
	if strings.TrimSpace(link.UnitID) == "" || strings.TrimSpace(link.UnitKind) == "" {
		return errors.New("project link requires a unit id and unit kind")
	}
	switch link.Target {
	case "project":
		if strings.TrimSpace(link.Project) == "" || link.NewProject != nil {
			return fmt.Errorf("project link for %s requires a project slug only", link.UnitID)
		}
	case "new_project":
		if link.Project != "" || link.NewProject == nil || strings.TrimSpace(link.NewProject.Slug) == "" || strings.TrimSpace(link.NewProject.Name) == "" {
			return fmt.Errorf("new project link for %s requires a proposed slug and name", link.UnitID)
		}
	case "none":
		if link.Project != "" || link.NewProject != nil {
			return fmt.Errorf("no-project link for %s cannot name a project", link.UnitID)
		}
	default:
		return fmt.Errorf("unsupported project link target %q", link.Target)
	}
	if link.Kind != "" && !projectLinkKinds[link.Kind] {
		return fmt.Errorf("unsupported project link kind %q", link.Kind)
	}
	if link.Confidence < 0 || link.Confidence > 1 {
		return fmt.Errorf("project link confidence %v is outside [0, 1]", link.Confidence)
	}
	if len(link.Evidence) == 0 {
		return fmt.Errorf("project link for %s requires evidence", link.UnitID)
	}
	for _, id := range link.Evidence {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("project link for %s has an empty evidence id", link.UnitID)
		}
	}
	return nil
}

func sanitizeProjectLinkSummary(summary string) string {
	runes := make([]rune, 0, len(summary))
	for _, r := range summary {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		runes = append(runes, r)
	}
	text := strings.Join(strings.Fields(string(runes)), " ")
	if runes = []rune(text); len(runes) > projectLinkSummaryMaxRunes {
		text = strings.TrimSpace(string(runes[:projectLinkSummaryMaxRunes]))
	}
	return text
}

func insertProjectLink(tx *sql.Tx, now time.Time, link ProjectLink) (ProjectLink, error) {
	link.Summary = sanitizeProjectLinkSummary(link.Summary)
	if err := validateProjectLink(link); err != nil {
		return ProjectLink{}, err
	}
	evidence, err := json.Marshal(link.Evidence)
	if err != nil {
		return ProjectLink{}, fmt.Errorf("encode project link evidence: %w", err)
	}
	var proposed any
	if link.NewProject != nil {
		encoded, err := json.Marshal(link.NewProject)
		if err != nil {
			return ProjectLink{}, fmt.Errorf("encode proposed project: %w", err)
		}
		proposed = string(encoded)
	}
	link.CreatedAt, link.DecidedAt, link.Decision = now, time.Time{}, ""
	result, err := tx.Exec(`INSERT INTO project_links (unit_id, unit_kind, container, target, project, proposed_project_json,
		source, kind, summary, confidence, evidence_json, state, auto, rule, profile, run_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		link.UnitID, link.UnitKind, link.Container, link.Target, link.Project, proposed,
		link.Source, link.Kind, link.Summary, link.Confidence, string(evidence), link.State, link.Auto,
		link.Rule, link.Profile, link.RunID, now.Format(time.RFC3339Nano))
	if err != nil {
		return ProjectLink{}, fmt.Errorf("store project link for %s: %w", link.UnitID, err)
	}
	if link.ID, err = result.LastInsertId(); err != nil {
		return ProjectLink{}, fmt.Errorf("store project link for %s: %w", link.UnitID, err)
	}
	return link, nil
}

func decideProjectLink(tx *sql.Tx, link ProjectLink) error {
	_, err := tx.Exec(`UPDATE project_links SET state = ?, auto = ?, decision = ?, decided_at = ? WHERE id = ?`,
		link.State, link.Auto, link.Decision, link.DecidedAt.UTC().Format(time.RFC3339Nano), link.ID)
	if err != nil {
		return fmt.Errorf("record project link %d decision: %w", link.ID, err)
	}
	return nil
}

func projectLinkCurrent(link ProjectLink) bool {
	return link.State == "proposed" || link.State == "applied"
}

func currentProjectLinkTx(tx *sql.Tx, unitID string) (*ProjectLink, error) {
	links, err := queryProjectLinks(tx, `WHERE unit_id = ? AND state IN ('proposed', 'applied')`, unitID)
	if err != nil || len(links) == 0 {
		return nil, err
	}
	return &links[0], nil
}

func readProjectLinkTx(tx *sql.Tx, linkID int64) (ProjectLink, error) {
	links, err := queryProjectLinks(tx, `WHERE id = ?`, linkID)
	if err != nil {
		return ProjectLink{}, err
	}
	if len(links) == 0 {
		return ProjectLink{}, fmt.Errorf("project link %d not found", linkID)
	}
	return links[0], nil
}

type projectLinkQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func queryProjectLinks(db projectLinkQueryer, where string, args ...any) ([]ProjectLink, error) {
	rows, err := db.Query(`SELECT `+projectLinkColumns+` FROM project_links `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("query project links: %w", err)
	}
	defer rows.Close()
	var links []ProjectLink
	for rows.Next() {
		link, err := scanProjectLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query project links: %w", err)
	}
	return links, nil
}

func scanProjectLink(rows *sql.Rows) (ProjectLink, error) {
	var link ProjectLink
	var proposed, decidedAt sql.NullString
	var evidence, createdAt string
	if err := rows.Scan(&link.ID, &link.UnitID, &link.UnitKind, &link.Container, &link.Target, &link.Project, &proposed,
		&link.Source, &link.Kind, &link.Summary, &link.Confidence, &evidence, &link.State, &link.Auto,
		&link.Rule, &link.Profile, &link.RunID, &createdAt, &decidedAt, &link.Decision); err != nil {
		return ProjectLink{}, fmt.Errorf("scan project link: %w", err)
	}
	if err := json.Unmarshal([]byte(evidence), &link.Evidence); err != nil {
		return ProjectLink{}, fmt.Errorf("parse project link %d evidence: %w", link.ID, err)
	}
	if proposed.Valid {
		link.NewProject = &ProposedProject{}
		if err := json.Unmarshal([]byte(proposed.String), link.NewProject); err != nil {
			return ProjectLink{}, fmt.Errorf("parse project link %d proposed project: %w", link.ID, err)
		}
	}
	var err error
	if link.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return ProjectLink{}, fmt.Errorf("parse project link %d created_at: %w", link.ID, err)
	}
	if decidedAt.Valid {
		if link.DecidedAt, err = time.Parse(time.RFC3339Nano, decidedAt.String); err != nil {
			return ProjectLink{}, fmt.Errorf("parse project link %d decided_at: %w", link.ID, err)
		}
	}
	return link, nil
}
