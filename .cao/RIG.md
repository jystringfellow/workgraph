# Workgraph Rig

## Purpose

Build and maintain Workgraph with high confidence while preserving its
human-first, local-first, privacy-conscious product philosophy.

The rig exists to make engineering work more reliable without turning
Workgraph itself into an AI orchestration system.

Workgraph remains the durable substrate for understanding human work,
context, priorities, decisions, and continuity. The rig is an engineering
system used to build Workgraph.

## Principles

1. Understand before changing.
2. Specifications and observable behavior outrank assumptions.
3. Prefer the smallest coherent solution.
4. Preserve compatibility unless change is intentional and specified.
5. Tests and deterministic checks are evidence, not obstacles.
6. Never weaken a test merely to make an implementation pass.
7. Prefer deterministic evidence over model judgment when facts can be
   established directly.
8. Do not spend model context rediscovering facts that ordinary tooling can
   compute.
9. Review independently. Do not rubber-stamp previous agents.
10. Distinguish concrete defects from stylistic preference.
11. Workgraph's privacy boundaries are product requirements.
12. Minimize unnecessary model calls, duplicated reasoning, and context.
13. Prefer sequential improvement over redundant parallel implementations.
14. Done means tested, documented, understandable, and release-ready.

## Sources of Truth

When sources disagree, use the following order of authority:

1. Explicit task intent and acceptance criteria.
2. Workgraph specifications and documented product invariants.
3. Observable repository behavior and deterministic evidence.
4. Tests that encode intended behavior.
5. Existing implementation.
6. Agent interpretation.

Existing code is evidence of current behavior, not proof of intended behavior.

An agent's explanation is never authoritative merely because another agent
produced it earlier in the workflow.

## Organizational Model

The rig contains pods specialized for types of engineering work.

The first pod is Feature Engineering.

A pod defines:

- its mission
- stable seats
- deterministic stages
- review and feedback flow
- completion criteria

Seats are stable responsibilities rather than model identities.

Models and providers occupying seats may change without changing the
responsibilities of the seat.

## Execution Model

Use agents for work that benefits from judgment:

- understanding intent
- architecture and planning
- implementation
- substantive review
- release assessment

Use ordinary tooling for facts that can be established deterministically:

- compilation
- tests
- static analysis
- formatting
- repository state
- command exit status
- other machine-verifiable release criteria

Deterministic verification runs outside model context whenever practical.

Agent-run checks are development aids. Workflow-owned deterministic
verification is authoritative. Agents may run narrowly scoped checks, but
should not duplicate repository-wide verification when the workflow provides
it. A sandbox inability to execute the full suite is not evidence of a
product defect.

Successful checks should normally be represented to agents as compact
structured evidence rather than raw command output.

When a check fails, provide the responsible agent with the smallest useful
failure evidence needed to diagnose and correct the problem.

Do not feed large successful logs into model context without a specific reason.

## Operator Observation

Use `.cao/observe.sh <run-id>` to view a compact, read-only projection of a
workflow run. It reports CAO status metadata, known step/seat progress,
iteration counts, working-tree counts, and an active CAO terminal when CAO
exposes one. It never displays prompts, transcripts, source, diffs, or
environment variables.

## Work Flow

Prefer a sequential evidence-driven loop:

1. Understand and plan.
2. Implement.
3. Run deterministic verification.
4. If verification fails, return concise failure evidence to the developer.
5. Repeat implementation and verification until deterministic checks pass.
6. Perform independent substantive review.
7. Resolve material findings.
8. Re-run deterministic verification after material changes.
9. Perform the release gate.
10. Finish only when objective release criteria and substantive review are
    satisfied.

When correction cycles are bounded, the bound applies to Developer
corrections. Each permitted correction must still receive the subsequent
review or release-gate assessment needed to establish whether it resolved the
finding.

Parallel execution is appropriate when work is genuinely independent.

Do not create multiple implementations of the same task merely to compare
agents unless there is an explicit reason to do so.

## Review Independence

Reviewers should reconstruct intended behavior from primary sources:

- the task
- acceptance criteria
- specifications
- repository state
- tests
- deterministic verification evidence

Developer summaries may be useful navigation aids but must not substitute for
independent inspection.

Reviews should report concrete findings supported by evidence.

Avoid:

- stylistic churn
- speculative concerns without evidence
- broad cleanup unrelated to the task
- redesign solely because the reviewer prefers another approach

## Deterministic Verification

Deterministic verification is infrastructure, not an agent seat.

For the current Workgraph repository, the baseline local verification mirrors
the core CI checks:

- `go vet ./...`
- `go test ./...`

The verification harness may grow as Workgraph develops, but local checks
should remain aligned with CI and documented release requirements.

A passing model review does not override failing deterministic verification.

A model should not claim that a deterministic check passed unless the workflow
has evidence that it actually ran successfully.

## Jev Shadow Decisions

Jev may be used for inexpensive bounded judgments about workflow state.

Initially, all Jev decisions operate in shadow mode.

Shadow decisions:

- may be recorded for later evaluation
- may be compared with subsequent review findings and release outcomes
- must not skip required review
- must not approve or reject a release
- must not trigger implementation changes
- must not alter workflow control flow

Jev should receive the minimum state necessary for the decision.

Prefer metadata and structured evidence over source code, diffs, prompts,
Workgraph memory, or verbose logs.

Useful shadow questions may include:

- change risk
- likely review focus
- probability that deep review is warranted

Confidence is telemetry, not proof.

Jev may become authoritative only after its behavior has been evaluated on
representative Workgraph changes and an explicit workflow policy adopts it.

## Privacy

Workgraph's privacy model applies to the engineering process as well as the
product.

Do not expose source, prompts, Workgraph memory, user content, environment
variables, credentials, or other sensitive information to external systems
without a concrete need and an understood boundary.

When external model or decision services are used, send the minimum necessary
context.

Never include secrets in agent prompts, workflow artifacts, logs, or committed
configuration.

## Completion

A change is not complete because an agent says it is complete.

Completion requires objective evidence appropriate to the change.

At minimum:

- required deterministic checks pass
- acceptance criteria are satisfied
- material review findings are resolved
- relevant tests exist and pass
- documentation/help is updated when behavior changes
- privacy and compatibility implications have been considered
- repository state contains no unintended artifacts
- the release gate finds no unresolved material blocker

The repository and its observable behavior are the final evidence.

## Feature Engineering Failure Evidence

The workflow writes local evidence directly, before emitting CAO output. Locate
it under `git rev-parse --absolute-git-dir`:

    cao-evidence/<CAO_WORKFLOW_RUN_ID>/<execution-id>/

Each script execution receives a fresh UUID directory, including recovery of
the same CAO run. `local` is used only when CAO's run identity is absent (tests
or direct invocation). Invalid run identities fail before launching agents.
Every execution is retained separately; recovery never overwrites prior
executions. This is evidence isolation, not permission to replay a developer.
No evidence is added to the worktree or synced by `.cao/sync.sh`. CAO lint
warns about UUID nondeterminism; UUIDs isolate artifact directories only, never
step identities, budgets, seat selection or engineering decisions.

Numbered JSON records contain step starts/results, correction before/after
state, deterministic verification status, and assessments. Each assessment
records the logical seat, assessment number, explicit outcome, curated findings,
verification result, review/gate correction counts, HEAD and state fingerprint.
Invalid assessments record a reason and response-shape diagnostics instead of
agent text. `failure.json` embeds the latest invalid assessment separately from
the last valid assessment, which remains null until one has been parsed.
`failure.json` retains the final assessment and correction context, even if
`emit_output()` fails. Its absolute location appears in the failure message.
A completion record is also written before CAO output. Files are written by
atomic replacement with file fsync, mode 0600, inside a mode 0700 execution
directory. A process interrupted during a write may leave a `.tmp` file; the
previous JSON records remain readable. Storage errors fail visibly.

The fingerprint hashes index entries (including staged blobs and modes), the
unstaged binary Git diff, and the names, modes and bytes of non-ignored untracked
files. No diff or file contents are stored. HEAD is recorded separately; committed
content changes appear in index blobs. Staging changes alter index/diff evidence;
untracked content hashes distinguish edits to existing untracked files. A change
followed by restoration of the original effective index/worktree state is a
no-op, even when commit/revert commits changed HEAD. Ignored files, reflog history, and external state are outside
this boundary. Run against an established Git HEAD, in an exclusively used
worktree; concurrent edits invalidate workflow evidence. Read-only assessment
changes or a changed state since verification halt the workflow.

### Curated Output and Privacy

Review and gate output must contain only `FINDING: <JSON>` lines followed by
exactly one final `FINDINGS: YES/NO` or `RELEASE_BLOCKED: YES/NO` line, respectively.
Each finding has exactly four nonempty string fields: `id`, `summary`,
`evidence`, and `requirement`. Use stable IDs and repository/spec/test references.
There are at most 12 findings; each field is at most 240 characters. A YES
requires findings; a NO requires none. Empty, malformed, missing, quoted,
conflicting, or prose-only assessments remain invalid; the parser never
normalizes Markdown or guesses approval. The workflow permits exactly one
replacement assessment for formatting or ambiguous completed capture, using the same provider/profile/seat,
original task, correction context and verified Git state. Its separate CAO step
ID ends in `-format-1`; the logical assessment number stays unchanged and no
developer correction is consumed. CAO creates a new step/session, so the retry
must independently reconstruct findings from primary sources rather than
assuming access to its previous response. No raw invalid response is forwarded.
Material findings in a valid replacement enter the usual correction loop.
A second invalid response/capture fails closed. An ambiguous capture is never
accepted by selecting its final marker; the replacement must independently pass
the whole protocol. This retry applies only to read-only assessment seats, never
architect/developer outputs or potentially side-effecting corrections.
Execution failures, incomplete steps,
replays and repository modifications are not eligible for formatting retries.
Each logical review/gate assessment therefore costs at most two agent calls.

Diagnostics retain only output kind, text/byte/line counts, digest, marker counts,
conflicting-marker flag, whether a marker was final, last-line category, finding
line count, code-fence presence and a truncation-indicator flag. They distinguish
empty/non-text capture from protocol rejection without retaining text or arbitrary
object fields. A truncation indicator is a clue, not proof of truncation.

Corrections end with `DISPOSITION: <JSON>` containing `status` (`corrected`,
`rejected`, or `unable`) and `evidence` (a nonempty repository/spec/test reference,
at most 480 characters). The disposition must address every material finding.
Do not put secrets, user content, source excerpts, complete prompts, environment
values, or terminal transcripts in either format. The workflow retains only
these bounded curated fields, Git digests/HEADs, check statuses, safe exception
type names, and requested configuration metadata. It does not retain raw
assessment output, correction summaries, verification failure logs, or diffs.
Raw failure diagnostics remain transient input to the correcting developer;
rerun `.cao/verification/verify.sh` to investigate deterministic failures.

Curating and length limits reduce retention; they are **not secret detection**.
A model can still put sensitive material into a valid field. Treat the local
evidence as confidential. Do not upload it automatically, commit it, or send
it to Jev. Retention is operator controlled: no automatic expiry or deletion;
after investigating a run, remove its evidence directory when no longer needed.
CAO's own diagnostics and output have separate retention behavior.

### Corrections, Independence, and Bounds

Every developer correction has workflow-owned Git snapshots immediately before
and after execution. `corrected` without an effective change stops before
another paid review. Missing/invalid dispositions and `unable` also stop.
A no-change `rejected` disposition permits one independent reassessment with
fresh verification and concrete dispute evidence. The reviewer inspects primary
sources, including every previous material finding; it must not merely agree
with the developer. If that reassessment reports material issues, the workflow
halts instead of spending another correction. Verification failures cannot be
resolved by a no-change rejection.

Subsequent reviews receive compact previous findings, the disposition, before/
after fingerprints and HEAD identifiers, and fresh verification evidence.
Jev shadow results never enter these prompts or control this loop.

Review corrections share a single budget across the entire run: at most three
(default three), each followed by verification and an independent assessment,
including a final assessment after correction three. Gate corrections are
separately capped at two (default two), each followed by verification,
independent adversarial review, and another gate assessment. Renewed review
issues use the **remaining** review budget, never a reset or nested fresh budget.
Exhaustion retains the final unresolved findings and prevents further agent
calls. Verification repairs remain separately bounded by `max_verify_attempts`;
all corrections, including verification repairs, require Git/disposition evidence.
Gate-fix verification failure halts immediately. Initial verification stops
after the final failed attempt without launching an unassessed extra repair.

CAO replay cannot establish a fresh correction or independent assessment.
Replayed correction/review/gate results halt with an evidence artifact. Inspect
prior execution records and reconcile the worktree before starting a new CAO
run. Do not blindly resume a potentially side-effecting developer step.

## Provider Fallback Policy (Disabled)

`FALLBACK_POLICY` in the workflow is a disabled configuration seam: `enabled`
is false, `chains` is empty, and `max_switches` is zero. Changing it fails before
any agent is launched. This pass introduces no production model switching,
retry wrapper, environment forwarding, or new local-model requirement.

`classify_failure()` is a deterministic policy function for **explicitly
established categories**, not a classifier of model prose or generic exceptions:

| Category | Future fallback candidate | Current action |
| --- | --- | --- |
| Provider/CLI unavailable | Yes | Stop; establish availability outside the run |
| Authentication or quota | Yes | Stop; repair authorization/quota or approve another provider |
| Rate limit | Yes | Stop; bounded wait/switch would require typed evidence |
| Terminal/session startup | Yes | Stop; establish that execution never began |
| Agent execution / unknown failure | No | Stop; reconcile uncertain side effects |
| Valid material findings | No | Follow bounded engineering correction policy |
| Failed deterministic tests | No | Follow bounded verification policy |

A future chain must explicitly name provider, CLI, model, profile, interface/
permission validation and assurance level for each candidate, with a finite
switch cap and no cycles. Logical seat identity remains fixed. Reviewer and gate
assurance cannot silently decrease. Models assigned to supposedly independent
seats cannot silently become identical; any already shared assignments must be
explicit. Local models are optional candidates only after CAO terminal and tool
permission compatibility is verified. Provider-specific credential boundaries
and CAO environment sanitization must remain in force. A developer cannot be
retried/switched until reliable evidence establishes that it did not execute,
or a human has reconciled its side effects. Availability alone is insufficient.

The installed `cao_workflow.step()` accepts provider/profile and recovery policy,
but returns only step ID, terminal ID, output, status and `replayed`. It exposes
transport/HTTP errors, not reliable typed provider-auth/quota/rate-limit/startup
versus execution failures. A generic HTTP status is not provider evidence; 409
also represents recovery halt/divergence. Unknown step exceptions are conservatively recorded as
`execution_uncertain`. A narrowly allowlisted Copilot initialization timeout
records a startup signal, HTTP status and operator action; neither exception
messages nor HTTP bodies are retained. The signal does not authorize a retry
or provider switch. There is no supported transactional provider-switch handshake
or attested actual-model metadata in this return value. Requested provider/
profile and available profile configuration are recorded; actual provider, CLI,
model and profile remain explicitly unknown rather than inferred from the
request. Do not invent a switch mechanism around these limitations.

## Preserve Literal Task Text When Launching

Shell backticks inside double-quoted task arguments execute command
substitution before CAO receives the task. Use a single-quoted heredoc for a
multiline task, then pass its variable expansion in double quotes. Backticks
inside the variable's value are preserved literally; they are not executed.
For example (replace the task body with the complete intended request):

```bash
cao_task=$(cat <<'TASK'
Continue the implementation described in `specs/ai-handoff.md`.
Verify the acceptance criteria for `workgraph ai handoff`.
TASK
)
printf '%s\n' "$cao_task"
cao workflow run feature_engineering.py \
  --input "task=$cao_task" \
  --input 'repo_path=/Volumes/Craig/Code/workgraph'
```

Inspect the printed text before submitting. This is a launch example, not an
automatic continuation: the workflow still requires reconciled repository state
and a fresh run after a failure/replay halt. The workflow preserves supplied
task text in initial and formatting-retry prompts; it cannot recover text that
the invoking shell already removed.

## Artifact Smoke v1

`profile_smoke.py` is an opt-in, sequential synthetic bug-fix probe. It uses
real CAO profiles only when explicitly launched; tests mock every agent call.
It does not run feature engineering, Go checks, Jev, or product work. A pass
establishes synthetic interoperability, not engineering/release assurance.

The sequence is:

1. Validate/reuse a cached plan, or ask the architect for a bounded brief.
2. Developer writes a handoff reproducing the planted `age > 18` defect.
3. Workflow runs fixed boundary checks in a separate Python process.
4. Reviewer independently inspects the failing candidate and records findings.
5. Developer proposes one correction with a finding-to-change map.
6. Workflow checks the corrected candidate, then runs fresh review and gate.

The failing reproduction is intentional test data. Initial review of that
candidate exercises finding capture; it does not authorize advancement of
failing product tests. The shared developer correction budget is exactly one.
No-op, unable/rejected, unmapped findings, failed post-fix checks, renewed
findings, or gate blockers stop with retained evidence. A plan challenge
invalidates the cache and stops for human clarification rather than launching
an architect loop. Reviewer/gate artifact errors allow one replacement per
logical assessment; architect/developer output and execution failures do not.
Normal execution uses six calls, or five with a reused plan; the maxima are
nine and eight with all three assessment replacements.

### Artifacts and Identity

Agents write only their individually assigned JSON output file under a private
`.cao/smoke-work/<run-token>/` directory, ignored by Git. The envelope has exactly
`schema_version`, `run_token`, `step_id`, `brief_id`, `candidate_id`, and `payload`.
The workflow supplies the identity fields; output must match them exactly.
Missing, stale, duplicate-key, oversized, linked or malformed files cannot
advance the run. Terminal replies are ignored: no bullet stripping or outcome
searching is used by this smoke. CAO still must initialize a terminal and return
a fresh completed step; errors in its own output extraction can still fail a
step even if a file exists. Replayed steps are rejected.

The architect payload matches the fixed objective, scope, non-goals, acceptance
criteria, check identifiers and invalidation conditions, plus a bounded summary.
The developer payload contains an allowlisted expression, disposition and
addressed finding IDs. The workflow materializes synthetic Python fixtures;
it never executes arbitrary agent code or plan-proposed commands.
Reviewer/gate payloads contain `outcome` (the existing explicit YES/NO marker),
`plan_valid`, and `findings`. Findings retain the four-field curated contract.
A YES requires findings; a NO requires none. A plan challenge requires a finding.

Candidate identity hashes the brief, repository HEAD/state fingerprint, and
synthetic source. Every verification receipt, finding and gate decision binds
to it. Each call guards repository state, cached plan content, and input files
against modification; only the current output path may change. These guards
are evidence checks, not an OS security sandbox: writes to arbitrary ignored
or external locations cannot be proven absent by the Git fingerprint.

### Plan Reuse and Verification

The accepted plan is cached at `.git/cao-plans/profile-smoke/plan.json` (using the
actual Git directory for worktrees). Reuse requires an exact schema, fixed task/
contract and check-script digest, valid state, and matching plan content digest.
Git HEAD changes alone do not invalidate it. Successful runs archive completion
without invalidating the reusable plan. Review and gate approvals are never
reused. `plan_policy=refresh` explicitly replans. A corrupted or outdated cache
also replans before any developer work. v1 has one fixed smoke task, not a
cache of arbitrary human feature plans.

`.cao/verification/smoke_checks.py` is the only executable check entrypoint.
The workflow runs it with isolated Python, a ten-second timeout, and one of two
allowed expressions. It checks ages 17/18/19 without `eval` or executing the
candidate source. Receipts retain check IDs, command, tool/script versions,
exit status, skipped checks, brief and candidate identity. Full synthetic
check results stay local; only failing cases go to agents for triage, and
successful checks become compact receipts. Harness failure stops the run.

### Profiles, Retention and Recovery

Dedicated `wg-architect-smoke`, `wg-developer-smoke`, `wg-reviewer-smoke`, and
`wg-gate-smoke` profiles retain the current providers/models and reasoning
settings: no Astra, with a distinct reviewer model. Codex smoke seats use the
existing `cao_workspace` profile because artifact output needs workspace write
access; normal feature seats retain their existing permissions and behavior.
The smoke profiles scope writes to the requested handoff. CAO's credential and
environment boundaries remain unchanged. Requested assignments are recorded;
actual-model attestation is not available through the current step API.

Copilot smoke explicitly grants `fs_read`, `fs_list`, and `fs_write`.
Reviewer role defaults deny writes, so selecting a workspace sandbox alone is
insufficient for artifact handoffs. Codex smoke grants native `exec`, `wait`,
`exec_command`, `write_stdin`, and `apply_patch` entrypoints under `cao_workspace`.
CAO injects Codex grants literally into a soft-enforcement instruction; its
abstract `fs_read`/`fs_write` names are not native Codex tools. Code-mode sessions
use `exec`/`wait` to call file access tools. Codex command access is necessary for
artifact reads and is constrained by the workspace sandbox and task instructions,
not a native file-only allowlist. Tests remain workflow-owned. The preflight
requires the provider-specific policy before any call. Web tools, unrestricted
`*` and MCP orchestration grants are absent. These policies do not enforce
requested-path isolation; requested-path restrictions and input integrity checks
still belong to the workflow. Existing feature-engineering policies are unchanged.

The installed CAO profile validator warns that `fs_write` is unrecognized,
although its runtime tool mapper supports that grant and maps it to Copilot's
`write` tool. A launch preview must allow `write` and block `shell`; the static
validator warning alone does not establish the effective permissions.
Codex native tool names also produce vocabulary warnings in that validator.
Artifact errors retain only the requested path, logical seat, profile configuration,
repository fingerprint and curated error category, distinguishing missing and
empty files from malformed JSON without saving terminal output.

Validated briefs, candidate representations/hashes, findings, dispositions,
verification receipts/logs and gate decisions are archived in the existing
run-scoped Git evidence directory. These are synthetic data and bounded curated
fields, never raw terminal transcripts, prompts, environments or source diffs.
Length/schema validation is not secret detection. Accepted text must exclude
sensitive material. Pending output files are removed after ingestion, including
invalid or partial output on handled failure. Success removes workflow-owned
scratch files; failure preserves input fixtures/receipts for inspection.
Unknown files are not recursively deleted. Retention has no automatic expiry;
archive deletion requires explicit operator action.

A local plan lock prevents simultaneous runs from modifying the same cache.
Handled failure releases it; an abrupt process kill can leave it and partially
written output behind. Inspect the lock's `owner.json`, the CAO terminal, and
its referenced evidence before manually removing a stale lock or sensitive
pending scratch output. Never infer safe developer retry from a timeout.
Fresh runs, including recovery under the same CAO run ID, use distinct UUIDs
and cannot reuse old review/gate artifacts. The scripts provide no automatic
continuation of potentially side-effecting steps.

Sync copies CAO profiles/workflows, validates them, and runs `cao install` for
smoke profiles declaring `provider: copilot_cli`. Other provider installations
remain operator-managed. Copilot discovers the generated
`~/.copilot/agents/<name>.agent.md` file, not CAO's profile-store copy. Missing
that install can leave an idle Copilot terminal reporting “Custom agent not
found”; terminal readiness alone does not attest that the requested seat loaded.
Installation requires CAO's explicit success confirmation; the installed CLI
can print an install error while returning exit code zero. Failure stops sync.
Restart an existing Copilot session to load the
newly installed agent; a session reporting that error is not a valid reviewer.

Sync, then explicitly launch:

```bash
.cao/sync.sh
cao workflow run profile_smoke.py \
  --input 'repo_path=/Volumes/Craig/Code/workgraph'
```

Run the same command again to demonstrate plan reuse with fresh checks/review.
To invalidate the plan deliberately, add `--input 'plan_policy=refresh'`.
`step_timeout_seconds` defaults to 90 (allowed range 1–120). The workflow imports
repository helpers and checks, so launch from a stable checkout. CLI startup
still contributes time and cost. No real workflow is launched by the unit suite.

### Three-provider collaboration trial

`collaboration_trial.py` is a smaller fresh-run experiment alongside the existing
smoke. It uses a supplied synthetic plan, a Codex developer (`gpt-6.1-sol`),
Copilot reviewer (`claude-haiku-5.5`), advisory Claude Code reviewer, and a final
Codex assessment using an explicitly supplied distinct non-Astra `gate_model`.
There is no architect call, plan cache, Jev, clef classifier, provider fallback,
or automatic PR. Production feature engineering is unchanged.

The Claude profile omits its model and endpoint so native Claude Code settings
choose them. No credentials or environment values are copied into profiles or
evidence. Confirm the launched Claude session uses the intended local endpoint
and model; CAO completion does not attest to either. Local findings are retained
but do not veto acceptance or dismiss Copilot findings. A local execution or
invalid-output failure stops the collaboration trial because all three clients
must demonstrate participation.

Agents get ordinary file-based tasks. The developer edits a tiny Python candidate
directly; it does not produce a JSON envelope. Reviewers write bounded curated
findings and the existing explicit outcome marker to a fresh file. Terminal output
is ignored. The runner validates the candidate's AST and invokes the existing
external age-boundary checker; it never executes agent-authored arbitrary code.
Only failed checks go back to the developer. This limited synthetic checker does
not demonstrate running developer-authored tests or arbitrary project code.

One correction is shared across test and reviewer failures. Every reviewer fix
gets fresh checks and both renewed reviews; the final assessment can still block.
Normal success uses four agent calls, with at most seven when correcting review
findings. Missing or malformed review files fail without formatting retries.
Steps declare manual recovery; resume is rejected before dispatch. Reconcile any
uncertain side effects and use a fresh run instead.

The trial uses a private system-temporary directory outside Workgraph's instruction
hierarchy. It retains synthetic fixtures there for inspection on success and failure;
curated findings, candidate hashes, requested seat/model assignments, and verification
receipts live in the normal private run-scoped Git evidence directory. Actual model
identities remain unattested. Pending malformed review files are deleted after handled
validation failures; execution interruptions can leave partial files, requiring operator
inspection. Neither temporary directories nor evidence expire automatically. This
retention is for tiny synthetic fixtures only, not a policy for real project source.

```bash
bash .cao/sync.sh
cao workflow run collaboration_trial.py \
  --input 'repo_path=/Volumes/Craig/Code/workgraph' \
  --input 'gate_model=<your second Codex model>'
```

The Codex profiles still carry the documented native-tool-name workaround for CAO's
soft restriction instruction. This trial reduces handoff complexity; it does not
prove that adapter issue has been fixed. It remains a reason to switch execution
backends if the bounded live experiment requires further tool/terminal patches.

### Supervisor-driven cross-model smoke (no Python workflow)

This separate experiment follows CAO's `assign` and `orchestration` examples:
`wg-coordinator-smoke` uses Codex `gpt-6.1-sol`, `wg-codex-worker-smoke`
uses Codex `gpt-6-luna`, and `wg-copilot-peer-smoke` uses Copilot
`claude-haiku-5.5`. These explicit assignments are for this user-selected trial;
production seats and the existing smoke workflows are unchanged. All three
profiles configure the CAO MCP server. The developer and reviewer return ordinary
prose through `send_message`; the supervisor uses `assign` and ends its turn to
receive replies. Missing MCP tools are a failure, not permission to use built-in
subagents. A successful `assign` response means dispatched, not completed.

```bash
bash .cao/sync.sh
python3 -I .cao/verification/supervisor_smoke.py prepare
```

Preparation creates a fresh private system-temporary directory and prints the
exact `cao launch` command to run. It does not launch agents. The generated
command uses `--headless --async`: this CAO version delivers the positional task
only on its headless path, then returns after confirmed delivery. Attach from a
real terminal with `tmux attach-session -t SESSION_NAME`, using the session name
printed by CAO. An interactive launch attaches without sending its positional
task; enter the instruction manually in that case.
The supplied `plan.md` avoids an architect call; it demonstrates reuse of a provided plan,
not general plan-cache validation. `start.md` supplies bounded coordination
instructions. The supervisor can read it via native tools. No working-directory
feature flag is needed: assigned workers inherit the supervisor's directory.
Do not add `--yolo` or forward credentials/environment overrides. The Codex
profiles retain the native-tool-name workaround and `cao_workspace` sandbox
profile. Their `codexConfig` overrides preapprove only the necessary local CAO
MCP tools: skill loading and replies on both Codex seats, plus assignment and
terminal cleanup on the supervisor. Other MCP tools retain their existing approval
policy. CAO forwards these scalar overrides at launch; adding approval fields only
to the `mcpServers` block would not forward them in the current Codex adapter.
`approval_policy = "never"` rejects calls requiring approval; it does not grant
MCP approval. `--auto-approve` skips CAO's launch confirmation and does not change
Codex tool approval or its workspace sandbox. Restart the blocked supervisor
after syncing these launch-time overrides; an existing session keeps its old
configuration. A launch from a shell without a TTY can create the session and then
fail to attach; attach to that existing session from a real terminal instead of
creating duplicates. Neither this test nor CAO completion attests to the actual model used.
Check the three native CLI displays before treating the requested identities as
observed identities.
Missing runtime model attestation is recorded as `actual model unverified`,
not a reason for the supervisor to stop before review. Agents' self-reports
cannot attest their exact models. Profile discovery and native `cao` shell
commands are not required trial tools and receive no additional approvals or
network permissions. A successful trial establishes configured cross-model
dispatch and collaboration; actual model identity needs separate operator
confirmation. Stop on execution uncertainty, unclear requirements, or a missing
required coordination tool instead.

The supervisor runs the external `supervisor_smoke.py check DIRECTORY` command
after implementation and every correction. The checker accepts only two fixed
Python AST shapes, tests ages 17/18/19 without executing agent-authored code, and
saves a compact receipt with candidate, plan, and checker SHA-256 hashes. Only
failures or a compact pass receipt reach the agents. `finish DIRECTORY` rejects
missing, failed, or stale receipts, but does not attest reviewer approval or
release readiness. The supervisor must obtain independent approval of the same
candidate hash, including renewed review after any fix. Human approval remains
required. This does not demonstrate running arbitrary project or developer-authored
tests, OS-enforced separation of test execution, or a production release gate.

One developer correction total and four worker assignments maximum are supervisor
instructions, not hard execution limits. The supervisor compares candidate hashes
and stops on no effective change, outstanding findings, unavailable tools, or
uncertain execution. Disagreement or plan ambiguity goes to the human. This
experiment deliberately tests agent-driven coordination; use the existing scripted
workflow when mechanically enforced budgets and resumable run evidence are needed.
There is no automatic fallback, Jev adjudication, architect escalation loop, PR,
or production workflow change. Copilot still uses CAO's provider initialization:
asynchronous assignment can return promptly while initialization later fails.
Inspect the terminal on failure and reconcile side effects before retrying.

Keep only the tiny synthetic candidate, plan, receipts, and a concise curated
`handoff.md` in the private trial directory. The handoff contains stage, candidate
hash, correction count, check status, unresolved findings with evidence, and
review approval provenance. Do not copy raw transcripts, complete prompts,
environment values, secrets, or unrelated source into it. The fixed generated
`start.md` is a synthetic instruction fixture. These files remain after success
or failure for human inspection; there is no automatic cleanup or expiration.
Unlike the scripted trials, this experiment has no workflow result or automatic
failure.json; inspect the retained handoff, receipt, and CAO terminal status.
It is fresh-run only: no automatic replay or retry of side-effecting workers.

### Reviewer-led correction trial

`python3 -I .cao/verification/correction_smoke.py prepare` creates a fresh
private local Git repository with a supplied plan and candidate. The fixture
has one local commit and no remotes, so normal repository inspection works.
It starts with independent
review, skips initial implementation and the architect, and permits one
developer correction followed by regression verification and a fresh review.
At most three worker assignments are permitted by the supervisor instructions.
If the first reviewer approves the seed, the trial stops without claiming that
correction was exercised. Findings are discovered by the reviewer; no expected
finding is supplied in its assignment.

The baseline check deliberately covers ordinary integer inputs and several
invalid types. Regression verification additionally covers booleans and an
integer subclass, which Python accepts as instances of `int`. The plan requires
exact built-in integers.
`check DIRECTORY --regression` writes the expanded receipt; `finish DIRECTORY`
requires that receipt to match fresh regression results for the current candidate.
A passing baseline receipt cannot satisfy final verification. The checker
recognizes a small set of equivalent input-guard AST shapes and evaluates those
known shapes without executing agent-authored code. This is a synthetic protocol
trial, not a general Python test runner. It uses the same privacy, retention,
fresh-run, hash evidence, and independent approval boundaries as the basic trial.

### Copilot Assessment Display Framing

Copilot can capture a `●` response cell, wrapped JSON, read/thinking/MCP status
rows, and a `Task complete` branch repeating the assessment. Completion status
does not make this a valid protocol response. Feature engineering uses a narrow adapter
before its strict assessment parser; artifact smoke ignores terminal framing. It accepts one complete curated
assessment cell and optionally one completion branch containing an exactly
agreeing parsed assessment. It joins indented JSON continuation rows with a
space; wraps that cannot be reconstructed as valid curated JSON remain invalid.
Only the observed thinking, read, and MCP lifecycle row formats are recognized.

Conflicting or additional response cells, summary prose, quoted/Markdown
markers, sidebar contamination, unknown display rows and incomplete branches
remain invalid and use the existing single formatting retry. The adapter never
selects a convenient marker from a transcript or drops a material finding to
obtain approval. Evidence records normalization status and cell counts; invalid
assessment diagnostics also count Copilot response/completion rows and identify
side-border characters. No tool paths, display rows or raw response text are
persisted by these diagnostics. Only accepted curated findings cross the
existing retention boundary. This is a rig compatibility adapter, not an
upstream response-provenance guarantee; unfamiliar layouts fail closed.

### Codex Display Framing and Prompt Echoes

The installed CAO Codex extractor can return a displayed response with its
leading `•` response-cell bullet and trailing `Worked for … • … AM/PM` timing
line. Forwarding that bullet into the next multiline task prompt can also make
CAO mistake the echoed contract for a new assistant response and capture both
prompt text and the real answer. Completion status does not validate capture.

Feature engineering removes that narrowly recognized Codex display framing before
validating or forwarding replies. Artifact smoke consumes files instead. This is provider transport handling, not an
assessment approval heuristic: Markdown, quoted markers, arbitrary prose and
other providers' output remain subject to the original strict parser. Multiple
Codex response cells are ambiguous and remain invalid without extracting a convenient JSON
substring or last approval marker. A completed read-only assessment may use its
one formatting retry for a fresh clean capture; other seats halt immediately.
Capture records retain only framing flags or
sanitized shape diagnostics. Artifact smoke distinguishes invalid
handoffs from failed deterministic checks in `failure.json`.

The failed `run-1dcfb7ed348c4188` contained a correct developer expression inside
an ambiguous captured response. Its records remain unchanged; they cannot be
replayed as fresh verification. Sync the corrected scripts and start a new smoke
run. This workaround addresses the observed framing/capture defect; an upstream
CAO extractor fix would remove the need for it. No external CAO package files
are changed by this workaround.


### Copilot Startup Update-Banner Compatibility

A timeout can also report that the idle prompt was recognized but the status
did not settle to idle. That is a readiness/status-monitor failure, not an
unrecognized footer or malformed handoff. Artifact smoke reports an allowlisted
Copilot startup timeout directly and performs no automatic retry. The workflow
step timeout does not change the provider's 60-second initialization deadline.
If INFO logs cannot distinguish the cause, restart the server with
`CAO_LOG_LEVEL=DEBUG cao-server` using its existing host/port/backend options,
then explicitly reproduce the startup. DEBUG output can contain screen text:
inspect it locally, do not export raw logs into handoff artifacts. Preserve
active sessions when scheduling a server restart.

`cao launch` also has a separate HTTP client deadline: by default it reads for
30 seconds, shorter than Copilot's 60-second initialization loop. A client
timeout can leave a successfully initialized server-side terminal behind.
Inspect server logs/session state before launching another copy. For a deliberate
startup probe, `CAO_MCP_REQUEST_TIMEOUT=90 cao launch --agents wg-reviewer-smoke
--provider copilot_cli` gives the CLI more time without changing provider readiness
policy. It does not fix a provider that never becomes ready.

`run-345960ab6fb64bfc` passed architect/developer steps, then failed before the
reviewer task with `Copilot initialization timed out after 60 seconds`. The
server found an idle prompt but rejected the next row: a version-download notice
preceded the normal `← open sidebar` status hints. Increasing the workflow's
90-second timeout does not change CAO's separate 60-second readiness loop.

`.cao/verification/copilot_footer_compat.py` checks a narrow compatibility fix
against the installed CAO provider without launching Copilot or calling models.
Use the Python interpreter from CAO's own environment:

```bash
~/.local/share/uv/tools/cli-agent-orchestrator/bin/python -B \
  .cao/verification/copilot_footer_compat.py
```

Adding `--apply` changes only `COPILOT_HINT_BAR_PATTERN` in the installed
`copilot_cli.py`, accepting the specific version-download prefix before the
existing status row. It refuses unfamiliar source, is idempotent, and retains
an original backup as `copilot_cli.py.workgraph-original`. Busy/login/trust/error
screens remain non-idle. This is an explicit, local external-tool compatibility
patch, not fallback or a workflow monkeypatch; `.cao/sync.sh` does not apply it.
Restart the running CAO server to load the changed provider, then launch a new
smoke run. Preserve active runs when scheduling the restart. A CAO package
upgrade may replace this patch; rerun the check instead of blindly reapplying.

Both workflows now embed sanitized execution-failure metadata directly in
`failure.json`. The known Copilot initialization-timeout signal includes the
provider startup limit and a next action. Unknown errors retain only safe type/
HTTP metadata and remain uncertain. No raw exception message/body is persisted,
and no execution failure is automatically retried or switched to another model.
