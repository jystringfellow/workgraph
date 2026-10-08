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
replacement assessment for formatting, using the same provider/profile/seat,
original task, correction context and verified Git state. Its separate CAO step
ID ends in `-format-1`; the logical assessment number stays unchanged and no
developer correction is consumed. CAO creates a new step/session, so the retry
must independently reconstruct findings from primary sources rather than
assuming access to its previous response. No raw invalid response is forwarded.
Material findings in a valid replacement enter the usual correction loop.
A second invalid response fails closed. Execution failures, incomplete steps,
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
also represents recovery halt/divergence. Consequently all step exceptions are
conservatively recorded as `execution_uncertain`, without retaining exception
messages/bodies. There is no supported transactional provider-switch handshake
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
