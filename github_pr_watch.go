package workgraph

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	githubPRWatchDefaultMaxOpenPRs   = 200
	githubPRWatchDefaultCommentRunes = 8000
	githubPRWatchMaxCommentRunes     = 65536
	githubPRWatchMinInterval         = time.Minute
	githubPRWatchPollInterval        = 5 * time.Minute
	githubPRWatchDefaultRetryAfter   = time.Minute
	githubPRWatchMinRemaining        = 200
)

var errGitHubPRWatchFenced = errors.New("github.pr_watch poll discarded because the watch changed during the poll")

type GitHubPRWatchCaptureConfig struct {
	HomeDir       string
	DatabasePath  string
	WatchDirs     []string
	GitHubCommand string
	Context       context.Context
	Now           func() time.Time
	takeHints     func(*sql.DB) (githubPRWatchHints, error)
}

type GitHubPRWatchCaptureResult struct {
	HomeDir      string
	DatabasePath string
	EventsStored int
	Message      string
}

type githubPRWatchParams struct {
	Scope               string         `json:"scope"`
	Identity            string         `json:"identity"`
	IdentityLogin       string         `json:"identity_login,omitempty"`
	Roles               []string       `json:"roles"`
	ReviewRequests      string         `json:"review_requests"`
	IgnoreActors        []string       `json:"ignore_actors"`
	IgnoreTeams         []string       `json:"ignore_teams"`
	CommentBodyMaxRunes *int           `json:"comment_body_max_runes"`
	AgentReplyMarker    string         `json:"agent_reply_marker"`
	MaxOpenPRs          map[string]int `json:"max_open_prs"`
}

func parseGitHubPRWatchParams(raw json.RawMessage) (githubPRWatchParams, error) {
	params := githubPRWatchParams{}
	if len(bytesTrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return githubPRWatchParams{}, fmt.Errorf("parse github.pr_watch scope: %w", err)
		}
	}
	if params.Scope == "" {
		params.Scope = "open_prs"
	}
	if params.Scope != "open_prs" {
		return githubPRWatchParams{}, fmt.Errorf("github.pr_watch scope must be open_prs")
	}
	params.Identity = strings.TrimSpace(params.Identity)
	if params.Identity == "" {
		params.Identity = "@me"
	}
	if len(params.Roles) == 0 {
		params.Roles = []string{githubPRRoleAuthor, githubPRRoleReviewer}
	}
	for _, role := range params.Roles {
		if role != githubPRRoleAuthor && role != githubPRRoleReviewer {
			return githubPRWatchParams{}, fmt.Errorf("github.pr_watch roles must be author or reviewer, got %q", role)
		}
	}
	if params.ReviewRequests == "" {
		params.ReviewRequests = "direct_and_team"
	}
	if params.ReviewRequests != "direct" && params.ReviewRequests != "direct_and_team" {
		return githubPRWatchParams{}, fmt.Errorf("github.pr_watch review_requests must be direct or direct_and_team")
	}
	if params.IgnoreActors == nil {
		params.IgnoreActors = []string{}
	}
	if params.IgnoreTeams == nil {
		params.IgnoreTeams = []string{}
	}
	if params.CommentBodyMaxRunes == nil {
		limit := githubPRWatchDefaultCommentRunes
		params.CommentBodyMaxRunes = &limit
	}
	if *params.CommentBodyMaxRunes < 0 || *params.CommentBodyMaxRunes > githubPRWatchMaxCommentRunes {
		return githubPRWatchParams{}, fmt.Errorf("github.pr_watch comment_body_max_runes must be between 0 and %d", githubPRWatchMaxCommentRunes)
	}
	maxOpen := map[string]int{githubPRRoleAuthor: githubPRWatchDefaultMaxOpenPRs, githubPRRoleReviewer: githubPRWatchDefaultMaxOpenPRs}
	for role, limit := range params.MaxOpenPRs {
		if limit <= 0 {
			return githubPRWatchParams{}, fmt.Errorf("github.pr_watch max_open_prs.%s must be positive", role)
		}
		maxOpen[role] = limit
	}
	params.MaxOpenPRs = maxOpen
	return params, nil
}

func (params githubPRWatchParams) encode() (json.RawMessage, string, error) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, "", fmt.Errorf("encode github.pr_watch scope: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(sum[:]), nil
}

func (params githubPRWatchParams) ignoresActor(login string) bool {
	for _, ignored := range params.IgnoreActors {
		if strings.EqualFold(strings.TrimSpace(ignored), login) {
			return true
		}
	}
	return false
}

func (params githubPRWatchParams) hasRole(role string) bool {
	for _, candidate := range params.Roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func ConnectGitHubPRWatch(config ConnectorConnectConfig) (ConnectorConnectResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	if err := enforceConnectorManagedSettings(githubPRWatchConnectorID); err != nil {
		return ConnectorConnectResult{}, err
	}
	params, err := parseGitHubPRWatchParams(config.ParamsJSON)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	login, err := validateGitHubPRWatchAccess(homeDir, config.GitHubCommand)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	params.IdentityLogin = login
	if err := enableGitHubPRWatch(homeDir, params); err != nil {
		return ConnectorConnectResult{}, err
	}
	return ConnectorConnectResult{
		HomeDir: homeDir,
		ID:      githubPRWatchConnectorID,
		Message: connectorConnectMessage(homeDir, githubPRWatchConnectorID),
	}, nil
}

func validateGitHubPRWatchConnector(homeDir string, config ConnectorValidateConfig) (ConnectorValidateResult, error) {
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorValidateResult{}, err
	}
	raw := state.entry(githubPRWatchConnectorID).BridgeParams
	if len(raw) == 0 {
		return ConnectorValidateResult{}, fmt.Errorf("connector %s is not connected; run workgraph github prs connect", githubPRWatchConnectorID)
	}
	params, err := parseGitHubPRWatchParams(raw)
	if err != nil {
		return ConnectorValidateResult{}, err
	}
	login, err := validateGitHubPRWatchAccess(homeDir, config.GitHubCommand)
	if err != nil {
		return ConnectorValidateResult{}, err
	}
	params.IdentityLogin = login
	if err := enableGitHubPRWatch(homeDir, params); err != nil {
		return ConnectorValidateResult{}, err
	}
	return ConnectorValidateResult{
		HomeDir: homeDir,
		ID:      githubPRWatchConnectorID,
		Message: fmt.Sprintf("Connector %s validation passed\nConfig: %s", githubPRWatchConnectorID, connectorRuntimePath(homeDir)),
	}, nil
}

func validateGitHubPRWatchAccess(homeDir string, ghCommand string) (string, error) {
	gh := githubCommandOrDefault(ghCommand)
	fail := func(details string) (string, error) {
		details = strings.TrimSpace(details)
		if recordErr := recordConnectorValidationError(homeDir, githubPRWatchConnectorID, time.Now(), details); recordErr != nil {
			return "", recordErr
		}
		return "", fmt.Errorf("validate GitHub CLI access: %s", details)
	}
	if output, err := exec.Command(gh, "auth", "status").CombinedOutput(); err != nil {
		details := strings.TrimSpace(string(output))
		if details == "" {
			details = err.Error()
		}
		return fail(details + "; run gh auth login")
	}
	var probe struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
	}
	if _, err := runGitHubGraphQL(context.Background(), gh, githubPRWatchProbeQuery, map[string]any{}, &probe); err != nil {
		return fail(err.Error())
	}
	if strings.TrimSpace(probe.Viewer.Login) == "" {
		return fail("GitHub viewer probe returned no login")
	}
	return probe.Viewer.Login, nil
}

func enableGitHubPRWatch(homeDir string, params githubPRWatchParams) error {
	encoded, scopeHash, err := params.encode()
	if err != nil {
		return err
	}
	if err := withGitHubPRWatchDatabase(homeDir, func(db *sql.DB) error {
		return applyGitHubPRWatchFence(db, true, scopeHash)
	}); err != nil {
		return err
	}
	if _, err := connectRuntimeConnector(homeDir, githubPRWatchConnectorID, ""); err != nil {
		return err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return err
	}
	entry := state.entry(githubPRWatchConnectorID)
	entry.BridgeParams = encoded
	state.Connectors[githubPRWatchConnectorID] = entry
	return writeConnectorRuntimeFile(homeDir, state)
}

func DisconnectGitHubPRWatch(config ConnectorConnectConfig) (ConnectorConnectResult, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	if err := withGitHubPRWatchDatabase(homeDir, func(db *sql.DB) error {
		return applyGitHubPRWatchFence(db, false, "")
	}); err != nil {
		return ConnectorConnectResult{}, err
	}
	if err := clearRuntimeConnector(homeDir, githubPRWatchConnectorID); err != nil {
		return ConnectorConnectResult{}, err
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ConnectorConnectResult{}, err
	}
	entry := state.entry(githubPRWatchConnectorID)
	disabled := false
	entry.Enabled = &disabled
	entry.BridgeParams = nil
	state.Connectors[githubPRWatchConnectorID] = entry
	if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
		return ConnectorConnectResult{}, err
	}
	return ConnectorConnectResult{
		HomeDir: homeDir,
		ID:      githubPRWatchConnectorID,
		Message: strings.Join([]string{
			"GitHub PR watch disconnected",
			"Observations deleted; captured events are preserved.",
			"Config: " + connectorRuntimePath(homeDir),
		}, "\n"),
	}, nil
}

func setGitHubPRWatchEnabled(homeDir string, state connectorRuntimeFile, enabled bool) error {
	scopeHash := ""
	if raw := state.entry(githubPRWatchConnectorID).BridgeParams; len(raw) > 0 {
		params, err := parseGitHubPRWatchParams(raw)
		if err != nil {
			return err
		}
		if _, scopeHash, err = params.encode(); err != nil {
			return err
		}
	}
	return withGitHubPRWatchDatabase(homeDir, func(db *sql.DB) error {
		return applyGitHubPRWatchFence(db, enabled, scopeHash)
	})
}

func withGitHubPRWatchDatabase(homeDir string, use func(*sql.DB) error) error {
	db, err := sql.Open("sqlite3", filepath.Join(homeDir, "workgraph.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := ensureGitHubPRWatchSchema(db); err != nil {
		return err
	}
	return use(db)
}

func ensureGitHubPRWatchSchema(db *sql.DB) error {
	for _, statement := range []string{githubPRObservationsSchema, githubPRWatchStateSchema} {
		if _, err := db.Exec(statement); err != nil {
			return fmt.Errorf("prepare github.pr_watch schema: %w", err)
		}
	}
	return nil
}

type githubPRWatchFence struct {
	Enabled   bool
	ScopeHash string
	PollSeq   int64
}

func readGitHubPRWatchFence(db *sql.DB) (githubPRWatchFence, error) {
	var fence githubPRWatchFence
	err := db.QueryRow(`SELECT enabled, scope_hash, poll_seq FROM github_pr_watch_state WHERE connector_id = ?`, githubPRWatchConnectorID).Scan(&fence.Enabled, &fence.ScopeHash, &fence.PollSeq)
	if err == sql.ErrNoRows {
		return githubPRWatchFence{}, nil
	}
	if err != nil {
		return githubPRWatchFence{}, fmt.Errorf("read github.pr_watch state: %w", err)
	}
	return fence, nil
}

func applyGitHubPRWatchFence(db *sql.DB, enabled bool, scopeHash string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin github.pr_watch state update: %w", err)
	}
	defer tx.Rollback()
	var currentHash string
	err = tx.QueryRow(`SELECT scope_hash FROM github_pr_watch_state WHERE connector_id = ?`, githubPRWatchConnectorID).Scan(&currentHash)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read github.pr_watch state: %w", err)
	}
	if !enabled || currentHash != scopeHash {
		if _, err := tx.Exec(`DELETE FROM github_pr_observations WHERE connector_id = ?`, githubPRWatchConnectorID); err != nil {
			return fmt.Errorf("delete github.pr_watch observations: %w", err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO github_pr_watch_state (connector_id, enabled, scope_hash, poll_seq) VALUES (?, ?, ?, 1)
		ON CONFLICT(connector_id) DO UPDATE SET enabled = excluded.enabled, scope_hash = excluded.scope_hash, poll_seq = github_pr_watch_state.poll_seq + 1`,
		githubPRWatchConnectorID, enabled, scopeHash); err != nil {
		return fmt.Errorf("update github.pr_watch state: %w", err)
	}
	return tx.Commit()
}

func reconcileGitHubPRWatchFence(db *sql.DB, state connectorRuntimeFile) error {
	if err := ensureGitHubPRWatchSchema(db); err != nil {
		return err
	}
	desiredEnabled := false
	desiredHash := ""
	if raw := state.entry(githubPRWatchConnectorID).BridgeParams; len(raw) > 0 {
		params, err := parseGitHubPRWatchParams(raw)
		if err != nil {
			return err
		}
		if _, desiredHash, err = params.encode(); err != nil {
			return err
		}
		desiredEnabled = connectorEnabled(state, githubPRWatchConnectorID)
	}
	current, err := readGitHubPRWatchFence(db)
	if err != nil {
		return err
	}
	if current.Enabled == desiredEnabled && current.ScopeHash == desiredHash {
		return nil
	}
	return applyGitHubPRWatchFence(db, desiredEnabled, desiredHash)
}

func githubCommandOrDefault(command string) string {
	if gh := strings.TrimSpace(command); gh != "" {
		return gh
	}
	return "gh"
}
