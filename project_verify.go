package workgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const projectLinkVerificationsSchema = `CREATE TABLE IF NOT EXISTS project_link_verifications (
 link_id INTEGER PRIMARY KEY,
 primary_profile TEXT NOT NULL,
 primary_json TEXT NOT NULL,
 verifier_profile TEXT NOT NULL,
 verifier_json TEXT NOT NULL
)`

type associateAnswer struct {
	Target     string           `json:"target"`
	Project    string           `json:"project,omitempty"`
	NewProject *ProposedProject `json:"new_project,omitempty"`
	Kind       string           `json:"kind"`
	Summary    string           `json:"summary"`
	Confidence float64          `json:"confidence"`
}

type projectLinkVerification struct {
	PrimaryProfile  string
	Primary         associateAnswer
	VerifierProfile string
	Verifier        associateAnswer
}

type associateSent struct {
	payload associatePayload
	hosted  bool
}

type associateSnapshot struct {
	Projects  []associateProject      `json:"projects"`
	Units     []associateSnapshotUnit `json:"units"`
	Decisions []associateDecision     `json:"recent_decisions"`
	Hosted    bool                    `json:"hosted"`
}

type associateSnapshotUnit struct {
	Unit       associateUnit `json:"unit"`
	LinkUnitID string        `json:"link_unit_id"`
	LinkKind   string        `json:"link_kind"`
	LinkSource string        `json:"link_source"`
	Members    []string      `json:"members"`
}

func snapshotOf(sent associateSent) *associateSnapshot {
	snapshot := &associateSnapshot{Projects: sent.payload.Projects, Units: []associateSnapshotUnit{}, Decisions: sent.payload.Decisions, Hosted: sent.hosted}
	for _, unit := range sent.payload.Units {
		members := make([]string, 0, len(unit.members))
		for id := range unit.members {
			members = append(members, id)
		}
		sort.Strings(members)
		unit.PrimaryAnswer = nil
		snapshot.Units = append(snapshot.Units, associateSnapshotUnit{Unit: unit, LinkUnitID: unit.linkUnitID, LinkKind: unit.linkKind, LinkSource: unit.linkSource, Members: members})
	}
	return snapshot
}

func (snapshot *associateSnapshot) within(current associatePayload) associateSent {
	sent := associateSent{payload: associatePayload{Schema: associateRequestSchema, Projects: []associateProject{}, Units: []associateUnit{}, Decisions: []associateDecision{}}}
	if snapshot == nil {
		return sent
	}
	sent.hosted = snapshot.Hosted
	currentUnits := map[string]associateUnit{}
	for _, unit := range current.Units {
		currentUnits[unit.linkUnitID] = unit
	}
	offered := map[string]bool{}
	for _, stored := range snapshot.Units {
		unit := stored.Unit
		unit.linkUnitID, unit.linkKind, unit.linkSource = stored.LinkUnitID, stored.LinkKind, stored.LinkSource
		unit.members = map[string]bool{}
		for _, id := range stored.Members {
			unit.members[id] = true
		}
		now, found := currentUnits[unit.linkUnitID]
		if !found || associateMembersHash(now) != associateMembersHash(unit) {
			continue
		}
		still := map[string]bool{}
		for _, slug := range now.Projects {
			still[slug] = true
		}
		projects := []string{}
		for _, slug := range unit.Projects {
			if still[slug] {
				projects = append(projects, slug)
				offered[slug] = true
			}
		}
		unit.Projects = projects
		sent.payload.Units = append(sent.payload.Units, unit)
	}
	for _, project := range snapshot.Projects {
		if offered[project.Slug] {
			sent.payload.Projects = append(sent.payload.Projects, project)
		}
	}
	permitted := map[associateDecision]bool{}
	for _, decision := range current.Decisions {
		permitted[decision] = true
	}
	for _, decision := range snapshot.Decisions {
		if permitted[decision] {
			sent.payload.Decisions = append(sent.payload.Decisions, decision)
		}
	}
	return sent
}

type associateEscalation struct {
	Profile  string          `json:"profile"`
	Members  string          `json:"members"`
	Evidence []string        `json:"evidence"`
	Answer   associateAnswer `json:"answer"`
}

func answerOf(proposal ProjectLinkProposal) associateAnswer {
	return associateAnswer{Target: proposal.Target, Project: proposal.Project, NewProject: proposal.NewProject, Kind: proposal.Kind, Summary: proposal.Summary, Confidence: proposal.Confidence}
}

func (answer associateAnswer) target() string {
	return ProjectLinkTarget(ProjectLink{Target: answer.Target, Project: answer.Project, NewProject: answer.NewProject})
}

func recordProjectLinkVerification(tx *sql.Tx, linkID int64, verification *projectLinkVerification) error {
	if verification == nil {
		return nil
	}
	primary, err := json.Marshal(verification.Primary)
	if err != nil {
		return err
	}
	verifier, err := json.Marshal(verification.Verifier)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO project_link_verifications (link_id, primary_profile, primary_json, verifier_profile, verifier_json) VALUES (?, ?, ?, ?, ?)`,
		linkID, verification.PrimaryProfile, string(primary), verification.VerifierProfile, string(verifier)); err != nil {
		return fmt.Errorf("record verification for link %d: %w", linkID, err)
	}
	return nil
}

func loadProjectLinkVerifications(config ProjectLinkStoreConfig, links []ProjectLink) (map[int64]projectLinkVerification, error) {
	dbPath, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	return readProjectLinkVerifications(db, links)
}

func readProjectLinkVerifications(db projectLinkQueryer, links []ProjectLink) (map[int64]projectLinkVerification, error) {
	verifications := map[int64]projectLinkVerification{}
	rows, err := db.Query(`SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'project_link_verifications'`)
	if err != nil {
		return nil, err
	}
	present := rows.Next()
	rows.Close()
	if !present {
		return verifications, nil
	}
	for _, link := range links {
		rows, err := db.Query(`SELECT primary_profile, primary_json, verifier_profile, verifier_json FROM project_link_verifications WHERE link_id = ?`, link.ID)
		if err != nil {
			return nil, fmt.Errorf("read verification for link %d: %w", link.ID, err)
		}
		for rows.Next() {
			var verification projectLinkVerification
			var primary, verifier string
			if err := rows.Scan(&verification.PrimaryProfile, &primary, &verification.VerifierProfile, &verifier); err != nil {
				rows.Close()
				return nil, err
			}
			if json.Unmarshal([]byte(primary), &verification.Primary) == nil && json.Unmarshal([]byte(verifier), &verification.Verifier) == nil {
				verifications[link.ID] = verification
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return verifications, nil
}

func (verification projectLinkVerification) lines() []string {
	return []string{
		fmt.Sprintf("  Associate %s: %s (confidence %.2f)", verification.PrimaryProfile, verification.Primary.target(), verification.Primary.Confidence),
		fmt.Sprintf("  Verifier %s: %s (confidence %.2f)", verification.VerifierProfile, verification.Verifier.target(), verification.Verifier.Confidence),
	}
}

func (run associateRun) escalated(sent associatePayload, proposals []ProjectLinkProposal) map[string]bool {
	escalated := map[string]bool{}
	if !run.hasVerifier {
		return escalated
	}
	seen := map[string]bool{}
	for _, unit := range sent.Units {
		seen[unit.linkUnitID] = true
	}
	threshold, floor := run.profile.autoApplyThreshold(), run.profile.verifyFloor()
	for _, proposal := range proposals {
		if seen[proposal.UnitID] && proposal.Confidence >= floor && proposal.Confidence < threshold {
			escalated[proposal.UnitID] = true
		}
	}
	return escalated
}

func verifyPayloadFor(payload associatePayload, primaries map[string]associateEscalation) associatePayload {
	verify := associatePayload{Schema: payload.Schema, Projects: []associateProject{}, Units: []associateUnit{}, Decisions: payload.Decisions}
	offered := map[string]bool{}
	for _, unit := range payload.Units {
		primary, found := primaries[unit.linkUnitID]
		if !found || primary.Members != associateMembersHash(unit) {
			continue
		}
		answer := primary.Answer
		unit.PrimaryAnswer = &answer
		for _, slug := range unit.Projects {
			offered[slug] = true
		}
		verify.Units = append(verify.Units, unit)
	}
	for _, project := range payload.Projects {
		if offered[project.Slug] {
			verify.Projects = append(verify.Projects, project)
		}
	}
	return verify
}

func escalationsFor(payload associatePayload, proposals []ProjectLinkProposal, escalated map[string]bool) map[string]associateEscalation {
	units := map[string]associateUnit{}
	for _, unit := range payload.Units {
		units[unit.linkUnitID] = unit
	}
	primaries := map[string]associateEscalation{}
	for _, proposal := range proposals {
		if escalated[proposal.UnitID] {
			primaries[proposal.UnitID] = associateEscalation{Profile: proposal.Profile, Members: associateMembersHash(units[proposal.UnitID]), Evidence: proposal.Evidence, Answer: answerOf(proposal)}
		}
	}
	return primaries
}

func primaryProposals(verify associatePayload, primaries map[string]associateEscalation) []ProjectLinkProposal {
	var proposals []ProjectLinkProposal
	for _, unit := range verify.Units {
		primary := primaries[unit.linkUnitID]
		answer := primary.Answer
		proposals = append(proposals, ProjectLinkProposal{
			UnitID: unit.linkUnitID, UnitKind: unit.linkKind, Container: unit.linkSource,
			Target: answer.Target, Project: answer.Project, NewProject: answer.NewProject,
			Kind: answer.Kind, Summary: answer.Summary, Confidence: answer.Confidence,
			Evidence: primary.Evidence, Profile: primary.Profile,
		})
	}
	return proposals
}

func verifyMessages(payload associatePayload) ([]openAICompatibleMessage, error) {
	return associateTaskMessages(payload, []string{
		"A first model already linked each unit in the data section; its result is that unit's \"primary_answer\".",
		"Decide each unit yourself: keep the primary answer when the evidence supports it, or change it.",
	})
}

func (run associateRun) filteredVerifyMessages(verify associateSent) (llmOutboundFilterResult, error) {
	messages, err := verifyMessages(verify.payload)
	if err != nil {
		return llmOutboundFilterResult{}, err
	}
	if verify.hosted || isHostedLLMProfile(run.verifier) {
		return filterHostedLLMMessages(messages)
	}
	return llmOutboundFilterResult{Messages: append([]openAICompatibleMessage(nil), messages...)}, nil
}

func (run associateRun) checkVerifierPolicy(config LLMAssociateConfig) error {
	if !run.hasVerifier {
		return nil
	}
	if err := enforceLLMManagedSettings(run.verifier, config.HTTPClient); err != nil {
		return err
	}
	return enforceHostedLLMOptIn(run.stored, run.verifier)
}

func (run associateRun) callVerifier(config LLMAssociateConfig, verify associateSent) (string, error) {
	if !run.hasVerifier {
		return "", fmt.Errorf("no llm profile is routed for %s", llmTaskAssociateVerify)
	}
	if err := run.checkVerifierPolicy(config); err != nil {
		return "", err
	}
	filtered, err := run.filteredVerifyMessages(verify)
	if err != nil {
		return "", err
	}
	ctx := config.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return callLLMProfileContext(ctx, config.HTTPClient, run.verifier, filtered.Messages)
}

func (run associateRun) mergeVerdicts(verify associatePayload, primaries []ProjectLinkProposal, response string, result *LLMAssociateResult) ([]ProjectLinkProposal, error) {
	entries, err := parseAssociateResponse(response)
	if err != nil {
		return primaries, err
	}
	verdicts, discarded := validateAssociateEntries(verify, entries, run.catalog)
	for _, discard := range discarded {
		result.Discarded = append(result.Discarded, AssociateDiscard{UnitID: discard.UnitID, Reason: "verifier: " + discard.Reason})
	}
	byUnit := map[string]ProjectLinkProposal{}
	for _, verdict := range verdicts {
		byUnit[verdict.UnitID] = verdict
	}
	merged := make([]ProjectLinkProposal, 0, len(primaries))
	for _, primary := range primaries {
		verdict, found := byUnit[primary.UnitID]
		if !found {
			merged = append(merged, primary)
			continue
		}
		verdict.Profile = run.verifierName
		verdict.verification = &projectLinkVerification{PrimaryProfile: primary.Profile, Primary: answerOf(primary), VerifierProfile: run.verifierName, Verifier: answerOf(verdict)}
		result.Verified++
		if answerOf(primary).target() != answerOf(verdict).target() {
			result.VerifyChanged++
		}
		merged = append(merged, verdict)
	}
	return merged, nil
}

func (run associateRun) verifyDirect(config LLMAssociateConfig, verify associateSent, primaries []ProjectLinkProposal, result *LLMAssociateResult) []ProjectLinkProposal {
	response, err := run.callVerifier(config, verify)
	if err == nil {
		var merged []ProjectLinkProposal
		if merged, err = run.mergeVerdicts(verify.payload, primaries, response, result); err == nil {
			return merged
		}
	}
	result.VerifyError = sanitizeProjectLinkSummary(err.Error())
	return primaries
}

func otherActiveLLMRequest(tx *sql.Tx, currentID string) (string, bool, error) {
	var id string
	err := tx.QueryRow(`SELECT id FROM capture_requests WHERE connector_id = ? AND status IN ('pending', 'claimed') AND id <> ? LIMIT 1`, llmAssociateConnectorID, currentID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read active llm task request: %w", err)
	}
	return id, true, nil
}

func emitVerifyRequestTx(tx *sql.Tx, now time.Time, primaries map[string]associateEscalation, order []string, snapshot *associateSnapshot) (string, error) {
	requestID, err := newEventID()
	if err != nil {
		return "", fmt.Errorf("create verify request id: %w", err)
	}
	params, err := json.Marshal(llmTaskParams{Task: llmTaskAssociateVerify, UnitIDs: order, Primary: primaries, Snapshot: snapshot})
	if err != nil {
		return "", fmt.Errorf("encode verify request params: %w", err)
	}
	emittedAt := now.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO capture_requests (
		id, connector_id, kind, since, until, params_json, status, attempts, available_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, 'pending', 0, ?, ?)`,
		requestID, llmAssociateConnectorID, captureRequestKindLLMTask, emittedAt, emittedAt, string(params), emittedAt, emittedAt); err != nil {
		return "", fmt.Errorf("emit verify request: %w", err)
	}
	return requestID, nil
}

func escalationOrder(payload associatePayload, primaries map[string]associateEscalation) []string {
	var ids []string
	for _, unit := range payload.Units {
		if _, found := primaries[unit.linkUnitID]; found {
			ids = append(ids, unit.linkUnitID)
		}
	}
	return ids
}

func (result LLMAssociateResult) verifyLines() []string {
	if result.verifier == "" {
		return nil
	}
	lines := []string{fmt.Sprintf("Escalated: %d", result.Escalated)}
	switch {
	case result.VerifyRequest != "":
		lines = append(lines, fmt.Sprintf("Verify request emitted: %s (%d units)", result.VerifyRequest, result.verifyRequestUnits))
	case result.VerifyError != "":
		lines = append(lines, "Verifier failed: "+result.VerifyError)
	case result.verifySkipped != "":
		lines = append(lines, "Verify skipped: request "+result.verifySkipped+" is active")
	case result.Escalated > 0 || result.Verified > 0:
		lines = append(lines, fmt.Sprintf("Verified: %d by %s (%d changed)", result.Verified, result.verifier, result.VerifyChanged))
	}
	return lines
}
