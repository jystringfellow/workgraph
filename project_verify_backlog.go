package workgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	verifyBacklogDefaultLimit = 50

	projectLinkBacklogChecksSchema = `CREATE TABLE IF NOT EXISTS project_link_backlog_checks (
 link_id INTEGER NOT NULL,
 verifier_profile TEXT NOT NULL,
 checked_at TEXT NOT NULL,
 PRIMARY KEY (link_id, verifier_profile)
)`
)

type LLMVerifyBacklogConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	GitEmails    []string
	DryRun       bool
	Limit        int
	Since        time.Duration
	Now          time.Time
	Location     *time.Location
	HTTPClient   *http.Client
	ctx          context.Context
}

type LLMVerifyBacklogResult struct {
	Message     string
	RunID       string
	Waiting     int
	Sent        int
	Verified    int
	Applied     int
	Kept        int
	Corrected   int
	Skipped     int
	Remaining   int
	SetAside    int
	Discarded   []AssociateDiscard
	VerifyError string
	Accuracy    ProjectLinkAccuracyReport
}

type verifyBacklogGroup struct {
	sent  associateSent
	links []ProjectLink
}

var errNoVerifierRouted = errors.New("no llm profile is routed for associate-verify; route one with `workgraph llm use <profile> --for associate-verify`")

func VerifyLLMBacklog(config LLMVerifyBacklogConfig) (LLMVerifyBacklogResult, error) {
	if config.Limit < 0 {
		return LLMVerifyBacklogResult{}, errors.New("--limit must be a positive number")
	}
	if config.Since < 0 {
		return LLMVerifyBacklogResult{}, errors.New("--since must be a positive duration")
	}
	if config.Limit == 0 {
		config.Limit = verifyBacklogDefaultLimit
	}
	associate := config.associateConfig()
	run, err := newVerifyBacklogRun(associate)
	if err != nil {
		return LLMVerifyBacklogResult{}, err
	}
	if !config.DryRun {
		if err := run.checkVerifierPolicy(associate); err != nil {
			return LLMVerifyBacklogResult{}, err
		}
	}
	waiting, setAside, err := run.backlogLinks(associate, config.Since)
	if err != nil {
		return LLMVerifyBacklogResult{}, err
	}
	result := LLMVerifyBacklogResult{Waiting: len(waiting), SetAside: setAside}
	if len(waiting) == 0 {
		result.Message = run.backlogMessage(config, result, nil)
		return result, nil
	}
	if !config.DryRun {
		id, err := newEventID()
		if err != nil {
			return LLMVerifyBacklogResult{}, fmt.Errorf("create run id: %w", err)
		}
		result.RunID = "run_" + id
	}
	var example *llmOutboundFilterResult
	handled := 0
	for start := 0; start < len(waiting) && result.Sent < config.Limit && result.VerifyError == ""; {
		end := min(start+min(associateUnitLimit, config.Limit-result.Sent), len(waiting))
		run.recheck = map[string]bool{}
		for _, link := range waiting[start:end] {
			run.recheck[link.UnitID] = true
		}
		groups, skipped, err := run.backlogGroups(associate, waiting[start:end])
		if err != nil {
			return LLMVerifyBacklogResult{}, err
		}
		start = end
		result.Skipped += skipped
		for _, group := range groups {
			verify, primaries := run.backlogVerifyRequest(group)
			result.Sent += len(verify.payload.Units)
			if config.DryRun {
				if example == nil {
					filtered, err := run.filteredVerifyMessages(verify)
					if err != nil {
						return LLMVerifyBacklogResult{}, err
					}
					example = &filtered
				}
				continue
			}
			var verdicts LLMAssociateResult
			merged, sent := run.verifySent(associate, verify, primaries, &verdicts)
			result.Discarded = append(result.Discarded, verdicts.Discarded...)
			if verdicts.VerifyError != "" {
				result.VerifyError = verdicts.VerifyError
				break
			}
			written, err := run.writeBacklogVerdicts(associate, result.RunID, group.links, merged, sent, &result)
			if err != nil {
				return LLMVerifyBacklogResult{}, err
			}
			handled += written
		}
	}
	result.Remaining = result.Waiting - handled
	result.Message = run.backlogMessage(config, result, example)
	return result, nil
}

func (config LLMVerifyBacklogConfig) associateConfig() LLMAssociateConfig {
	return LLMAssociateConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath, MemoryDir: config.MemoryDir, GitEmails: config.GitEmails, Now: config.Now, Location: config.Location, HTTPClient: config.HTTPClient, ctx: config.ctx}
}

func newVerifyBacklogRun(config LLMAssociateConfig) (associateRun, error) {
	homeDir, err := resolveLLMHomeDir(config.HomeDir)
	if err != nil {
		return associateRun{}, err
	}
	stored, err := readLLMConnectorConfig(homeDir)
	if err != nil {
		return associateRun{}, err
	}
	verifierName := strings.TrimSpace(stored.TaskProfiles[llmTaskAssociateVerify])
	if verifierName == "" {
		return associateRun{}, errNoVerifierRouted
	}
	verifier, ok := stored.Profiles[verifierName]
	if !ok {
		return associateRun{}, fmt.Errorf("llm profile %q for %s is not configured", verifierName, llmTaskAssociateVerify)
	}
	if verifier.Provider == llmProviderSystemOne {
		return associateRun{}, fmt.Errorf("llm profile %q for %s: %w", verifierName, llmTaskAssociateVerify, errSystemOneCannotVerify)
	}
	if associateExecutesInBridge(verifier) {
		return associateRun{}, fmt.Errorf("llm profile %q for %s runs in the bridge; llm verify-backlog calls the verifier directly, so route a profile without \"execution\": \"bridge\"", verifierName, llmTaskAssociateVerify)
	}
	now := config.Now
	if now.IsZero() {
		now = time.Now()
	}
	enabled := map[string]bool{}
	for _, source := range stored.TaskSources[llmTaskAssociate] {
		enabled[source] = true
	}
	catalog, err := LoadProjectCatalog(config.MemoryDir)
	if err != nil {
		return associateRun{}, err
	}
	return associateRun{homeDir: homeDir, stored: stored, verifierName: verifierName, verifier: verifier, hasVerifier: true, enabled: enabled, catalog: catalog, now: now.UTC()}, nil
}

func (run associateRun) backlogLinks(config LLMAssociateConfig, since time.Duration) ([]ProjectLink, int, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: run.homeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return nil, 0, err
	}
	db, err := openDatabase(dbPath)
	if err != nil {
		return nil, 0, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if present, err := projectLinksTablePresent(db); err != nil || !present {
		return nil, 0, err
	}
	where := `WHERE state = 'proposed' AND source = 'llm' AND decision = '' AND profile <> '' AND profile <> ?
		AND unit_id NOT IN (SELECT unit_id FROM project_links WHERE decision <> '')`
	args := []any{run.verifierName}
	if verified, err := sqliteTablePresent(db, "project_link_verifications"); err != nil {
		return nil, 0, err
	} else if verified {
		where += ` AND id NOT IN (SELECT link_id FROM project_link_verifications)`
	}
	if checked, err := sqliteTablePresent(db, "project_link_backlog_checks"); err != nil {
		return nil, 0, err
	} else if checked {
		where += ` AND id NOT IN (SELECT link_id FROM project_link_backlog_checks WHERE verifier_profile = ?)`
		args = append(args, run.verifierName)
	}
	candidates, err := queryProjectLinks(db, where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, 0, err
	}
	var waiting []ProjectLink
	setAside := 0
	for _, link := range candidates {
		if since > 0 && link.CreatedAt.Before(run.now.Add(-since)) || !run.inVerifyBand(link) {
			continue
		}
		if !run.declaredTarget(link) {
			setAside++
			continue
		}
		waiting = append(waiting, link)
	}
	return waiting, setAside, nil
}

func (run associateRun) inVerifyBand(link ProjectLink) bool {
	profile := run.stored.Profiles[link.Profile]
	return link.Confidence >= profile.verifyFloor() && link.Confidence < profile.autoApplyThreshold()
}

func (run associateRun) declaredTarget(link ProjectLink) bool {
	return link.Target == "none" || (link.Target == "project" && catalogHasProject(run.catalog, link.Project))
}

func (run associateRun) backlogHosted(link ProjectLink) bool {
	profile, configured := run.stored.Profiles[link.Profile]
	return !configured || isHostedLLMProfile(profile)
}

func sqliteTablePresent(db projectLinkQueryer, name string) (bool, error) {
	rows, err := db.Query(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?`, name)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

func (run associateRun) backlogGroups(config LLMAssociateConfig, links []ProjectLink) ([]verifyBacklogGroup, int, error) {
	unitIDs := make([]string, 0, len(links))
	for _, link := range links {
		unitIDs = append(unitIDs, link.UnitID)
	}
	current, err := buildAssociatePayload(run.homeDir, config, run.now, run.catalog, run.enabled, unitIDs, nil, run.recheck)
	if err != nil {
		return nil, 0, err
	}
	group := verifyBacklogGroup{sent: associateSent{payload: current}}
	skipped := 0
	for _, link := range links {
		unit, found := unitOf(current, link.UnitID)
		if !found || unit.UnitID != unit.linkUnitID || !evidenceAmongMembers(unit, link) {
			skipped++
			continue
		}
		if run.backlogHosted(link) {
			group.sent.hosted = true
		}
		group.links = append(group.links, link)
	}
	if len(group.links) == 0 {
		return nil, skipped, nil
	}
	return []verifyBacklogGroup{group}, skipped, nil
}

func evidenceAmongMembers(unit associateUnit, link ProjectLink) bool {
	for _, id := range link.Evidence {
		if !unit.members[id] {
			return false
		}
	}
	return true
}

func unitOf(payload associatePayload, unitID string) (associateUnit, bool) {
	for _, unit := range payload.Units {
		if unit.linkUnitID == unitID {
			return unit, true
		}
	}
	return associateUnit{}, false
}

func (run associateRun) backlogVerifyRequest(group verifyBacklogGroup) (associateSent, []ProjectLinkProposal) {
	escalations := map[string]associateEscalation{}
	var primaries []ProjectLinkProposal
	for _, link := range group.links {
		unit, _ := unitOf(group.sent.payload, link.UnitID)
		answer := associateAnswer{Target: link.Target, Project: link.Project, Confidence: link.Confidence}
		primaries = append(primaries, ProjectLinkProposal{
			UnitID: link.UnitID, UnitKind: link.UnitKind, Container: link.Container,
			Target: link.Target, Project: link.Project, NewProject: link.NewProject,
			Kind: link.Kind, Summary: link.Summary, Confidence: link.Confidence,
			Evidence: link.Evidence, Profile: link.Profile,
		})
		escalations[link.UnitID] = associateEscalation{Profile: link.Profile, Members: associateMembersHash(unit), Evidence: link.Evidence, Answer: answer}
	}
	return associateSent{payload: verifyPayloadFor(group.sent.payload, escalations), hosted: group.sent.hosted}, primaries
}

func (run associateRun) writeBacklogVerdicts(config LLMAssociateConfig, runID string, links []ProjectLink, merged []ProjectLinkProposal, sent associatePayload, result *LLMVerifyBacklogResult) (int, error) {
	byUnit := map[string]ProjectLink{}
	for _, link := range links {
		byUnit[link.UnitID] = link
	}
	asked := map[string]bool{}
	for _, unit := range sent.Units {
		asked[unit.linkUnitID] = true
	}
	thresholds := thresholdsFromConfig(run.stored)
	var counts LLMVerifyBacklogResult
	handled := 0
	err := withProjectLinksTx(run.store(config), func(tx *sql.Tx, now time.Time) error {
		counts, handled = LLMVerifyBacklogResult{}, 0
		if _, err := tx.Exec(projectLinkBacklogChecksSchema); err != nil {
			return fmt.Errorf("ensure project_link_backlog_checks: %w", err)
		}
		accuracy, err := projectLinkAccuracy(tx, now, thresholds, run.verifierName)
		if err != nil {
			return err
		}
		if err := recordAutoApplyPauseTransition(tx, now, accuracy); err != nil {
			return err
		}
		counts.Accuracy = accuracy
		for _, proposal := range merged {
			original := byUnit[proposal.UnitID]
			if !asked[proposal.UnitID] {
				continue
			}
			still, err := backlogLinkUndecided(tx, original)
			if err != nil {
				return err
			}
			if !still {
				counts.Skipped++
				continue
			}
			handled++
			if proposal.verification == nil {
				counts.Kept++
				if _, err := tx.Exec(`INSERT OR IGNORE INTO project_link_backlog_checks (link_id, verifier_profile, checked_at) VALUES (?, ?, ?)`, original.ID, run.verifierName, now.Format(time.RFC3339Nano)); err != nil {
					return fmt.Errorf("record backlog check for link %d: %w", original.ID, err)
				}
				continue
			}
			if proposal.Apply, err = associateAutoApplies(tx, now, accuracy.For(run.verifierName), thresholds.of(run.verifierName), proposal); err != nil {
				return err
			}
			if err := replaceBacklogLink(tx, now, original, proposal, runID); err != nil {
				return err
			}
			if err := recordProjectLinkVerification(tx, original.ID, proposal.verification); err != nil {
				return err
			}
			counts.Verified++
			if proposal.Apply {
				counts.Applied++
			} else {
				counts.Kept++
			}
			if proposal.verification.Primary.target() != proposal.verification.Verifier.target() {
				counts.Corrected++
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	result.Verified += counts.Verified
	result.Applied += counts.Applied
	result.Kept += counts.Kept
	result.Corrected += counts.Corrected
	result.Skipped += counts.Skipped
	result.Accuracy = counts.Accuracy
	return handled, nil
}

func backlogLinkUndecided(tx *sql.Tx, original ProjectLink) (bool, error) {
	current, err := readProjectLinkTx(tx, original.ID)
	if err != nil {
		return false, err
	}
	if current.State != "proposed" || current.Decision != "" || current.Source != "llm" || current.Profile != original.Profile {
		return false, nil
	}
	var verified, decided int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM project_link_verifications WHERE link_id = ?`, original.ID).Scan(&verified); err != nil {
		return false, fmt.Errorf("read verification for link %d: %w", original.ID, err)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM project_links WHERE unit_id = ? AND decision <> ''`, original.UnitID).Scan(&decided); err != nil {
		return false, fmt.Errorf("read decisions for unit %s: %w", original.UnitID, err)
	}
	return verified == 0 && decided == 0, nil
}

func replaceBacklogLink(tx *sql.Tx, now time.Time, original ProjectLink, verdict ProjectLinkProposal, runID string) error {
	link := original
	link.Target, link.Project, link.NewProject = verdict.Target, verdict.Project, verdict.NewProject
	link.Kind, link.Summary, link.Confidence, link.Evidence = verdict.Kind, sanitizeProjectLinkSummary(verdict.Summary), verdict.Confidence, verdict.Evidence
	link.Profile, link.RunID, link.State, link.Auto = verdict.Profile, runID, "proposed", false
	if verdict.Apply {
		link.State, link.Auto = "applied", true
	}
	if err := validateProjectLink(link); err != nil {
		return err
	}
	evidence, err := json.Marshal(link.Evidence)
	if err != nil {
		return fmt.Errorf("encode project link evidence: %w", err)
	}
	var proposed any
	if link.NewProject != nil {
		encoded, err := json.Marshal(link.NewProject)
		if err != nil {
			return fmt.Errorf("encode proposed project: %w", err)
		}
		proposed = string(encoded)
	}
	updated, err := tx.Exec(`UPDATE project_links SET target = ?, project = ?, proposed_project_json = ?, kind = ?, summary = ?, confidence = ?,
		evidence_json = ?, state = ?, auto = ?, profile = ?, run_id = ?, created_at = ? WHERE id = ? AND state = 'proposed' AND decision = ''`,
		link.Target, link.Project, proposed, link.Kind, link.Summary, link.Confidence, string(evidence), link.State, link.Auto, link.Profile, link.RunID, now.Format(time.RFC3339Nano), link.ID)
	if err != nil {
		return fmt.Errorf("store verified link %d: %w", link.ID, err)
	}
	if count, _ := updated.RowsAffected(); count != 1 {
		return fmt.Errorf("project link %d changed while it was verified", link.ID)
	}
	return nil
}

func (run associateRun) backlogMessage(config LLMVerifyBacklogConfig, result LLMVerifyBacklogResult, example *llmOutboundFilterResult) string {
	title := "LLM verify backlog complete"
	if config.DryRun {
		title = "LLM verify backlog dry run"
	}
	lines := []string{
		title,
		"Verifier: " + run.verifierName,
		"Provider: " + run.verifier.Provider,
		"Model: " + llmProfileModelLabel(run.verifier),
		"Destination: " + llmProfileDestination(run.verifier),
		"Enabled sources: " + associateSourcesLabel(orderedAssociateSources(run.enabled)),
		fmt.Sprintf("Waiting in the verify band: %d", result.Waiting),
		fmt.Sprintf("Left in review, new or undeclared project: %d", result.SetAside),
		fmt.Sprintf("Limit: %d", config.Limit),
	}
	if result.Waiting == 0 {
		return strings.Join(append(lines, "No links are waiting in the verify band; nothing was sent."), "\n")
	}
	if config.DryRun {
		lines = append(lines, fmt.Sprintf("Would send: %d", result.Sent), fmt.Sprintf("Skipped: %d", result.Skipped))
		if example == nil {
			return strings.Join(append(lines, "No waiting link passes the source policy; nothing would be sent."), "\n")
		}
		return strings.Join(append(lines, outboundFilterMessage(*example), "", "Example request:", "", "System:", example.Messages[0].Content, "", "User:", example.Messages[1].Content), "\n")
	}
	lines = append(lines,
		"Run: "+result.RunID,
		fmt.Sprintf("Sent: %d", result.Sent),
		fmt.Sprintf("Verified: %d", result.Verified),
		fmt.Sprintf("Applied: %d", result.Applied),
		fmt.Sprintf("Kept for review: %d", result.Kept),
		fmt.Sprintf("Corrected: %d", result.Corrected),
		fmt.Sprintf("Skipped: %d", result.Skipped),
		fmt.Sprintf("Discarded: %d", len(result.Discarded)),
	)
	for _, discard := range result.Discarded {
		lines = append(lines, fmt.Sprintf("- %q: %s", discard.UnitID, discard.Reason))
	}
	if result.VerifyError != "" {
		lines = append(lines, "Verifier failed: "+result.VerifyError)
	}
	lines = append(lines, fmt.Sprintf("Remaining: %d", result.Remaining))
	if len(result.Accuracy.Profiles) > 0 {
		lines = append(lines, result.Accuracy.Summary())
	}
	return strings.Join(lines, "\n")
}
