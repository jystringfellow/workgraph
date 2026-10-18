# Workgraph development rig

CAO runs coding agents; this rig supplies their task, evidence, and stopping
rules. It is external development tooling. Workgraph's CLI and AI sessions do
not orchestrate agents or select fallback providers.

## Current execution path

The [Feature Engineering pod](pods/feature-engineering.md) has four stable
[seats](profiles/README.md): lead, developer, independent reviewer, and release
gate. It uses CAO assign/send_message callbacks and ordinary prose. A supplied
valid plan avoids an architect call. Agents never run tests in this lane.

`pod.py` is the consolidated host-side runner. Evidence from its AI handoff trial
run is not committed to this repository. It prepares private run profiles and task input, starts the lead, executes
allowlisted verification requests, and checks candidate identity and callback
provenance. It is not a general orchestration engine. The lead owns engineering
judgments, correction dispositions, and the per-stage correction budgets; the
host additionally bounds assignments, check requests, and elapsed time.

CAO provider compatibility fixes belong upstream.

The independent reviewer runs a different model family (Copilot,
`claude-haiku-5.5`) and reads source files directly with read-only file tools.
The gate shares the developer's and lead's model family (Codex, `gpt-6.1-sol`);
its independence comes from a fresh context and a read-only sandbox, not model
diversity. The assurance this pod provides is fresh-context review by a second
model plus human inspection, not proof of correctness. Session deletion belongs
to the host; the lead has no auto-approved terminal deletion.

## Running a task

Use the Python environment containing your installed CAO. Its PyYAML dependency
is needed to prepare run-specific profiles. The runner otherwise uses the Python
standard library. CAO server and native CLIs must already be configured and
running locally. `CAO_HOME_DIR`, `CAO_API_HOST`, and `CAO_API_PORT` select the same
local CAO instance for the runner and every seat's CAO MCP client.

Set `CAO_PYTHON` to that installation's Python interpreter. The runner calls CAO
as a module of the Python that runs it, and `sync.sh` calls the `cao` entry point
found first on `PATH`; the commands below put both on the same installation.

The native Codex profiles `cao_workspace` and `cao_readonly` must exist.
[`codex-profiles.toml`](codex-profiles.toml) is the reference sandbox setting
for each (`workspace-write` and `read-only`). The runner does not inspect native
Codex configuration; confirm `cao_readonly` is read-only before a run.
Profiles retain scoped MCP approval overrides: CAO's `--auto-approve` skips its
launch confirmation, but does not grant Codex tool approval or broaden a sandbox.
The explicit native Codex tool names can produce CAO vocabulary warnings; keep
the provider grants intact. Prompt-only tool restrictions are not OS enforcement.

Optional stable-profile installation, without agent calls:

```sh
PATH="$(dirname "$CAO_PYTHON"):$PATH" bash .cao/sync.sh
```

Write a short task brief containing objective, implementation status, scope,
acceptance criteria, and relevant repository paths. Prepare a run with explicit
plan sources (AGENTS.md is always included):

```sh
"$CAO_PYTHON" .cao/pod.py prepare \
  --brief /tmp/task-brief.md \
  --plan specs/ai-handoff.md --plan features/ai_handoff.feature
```

Preparation only writes a fresh `.cao/pod-runs/pod-<id>/` directory and prints
its path. It snapshots the verifier as `verify.sh` and records its SHA-256 in
`manifest.json`. Review `packet.md`, `seats.json`, and `plan-state.json` there. Then
explicitly start the run; **this command uses model quota**:

```sh
"$CAO_PYTHON" .cao/pod.py run .cao/pod-runs/pod-REPLACE_WITH_ID
```

The runner registers unique aliases, validates them, installs the Copilot custom
agent, and performs full baseline checks before launching any paid seat. Initial
verification failure stops with evidence. The native CLIs retain their own
credential handling and CAO's environment sanitization. No `--yolo`, credential
forwarding, model substitution, or live calls occur during unit tests.

Launch uses `--headless --async` so CAO delivers the initial task. To observe a
session, use the printed session name with `tmux attach-session -t SESSION`;
detach with Ctrl-B, then D. Default sync does not prune previously installed
profiles. Never overwrite a Copilot agent whose ownership CAO cannot verify.

## Verification and evidence

The host runs the prepared snapshot of `verification/verify.sh`: `go vet ./...`
and `go test ./...`. At run startup, before registration, baseline checks, or
seat launches, it reads the snapshot once and checks its hash against the in-memory
manifest. Every full check executes those retained bytes; later edits to the source,
snapshot, or manifest cannot replace the command. Preparation artifacts must be
trusted when the run starts.
It removes active Workgraph AI session bindings from the child environment.
Each check stops after 15 minutes or at the supervision deadline, whichever is
sooner, and a timed-out check is never passing or red evidence.
The test process uses umask 022 so permission-sensitive fixtures are predictable;
run directories remain private. Native compiler/toolchain settings remain
operator-controlled. Passing output becomes a compact receipt. Failures retain
test names and file/line locations, not raw logs or arbitrary assertion text.
Read those primary sources for triage; stop for missing evidence rather than guess.

A lead writes `verify.request` with an increasing request number:

```text
1 red TestExactRegressionFact
2 full
```

It ends its turn. The host waits up to 90 seconds, within the supervision
deadline, for every session terminal to be idle, completed, or in error before
checking the exact Go fact or the full suite. An error terminal has yielded;
this does not establish successful work or an approving verdict. Overall deadline
exhaustion takes precedence over a terminal yield timeout. It writes numbered
receipts and `checks.json`, then wakes the lead. Repeated requests are not rerun;
malformed, out-of-order, or over-budget requests stop execution.
The lead enforces red-first for behavioral changes: inspect the fact and failure
to establish that the intended assertion failed before authorizing implementation.
A compiler/setup failure or missing fact is not evidence of a reproduced defect.
The host records evidence but does not gate full checks on prior red receipts.
Failed full checks can be corrected and retried, confirming green red requests do
not block subsequent full checks, and behavior-preserving refactor corrections
need proportionate verification rather than a new failing fact. Correction and
request budgets still apply.
Receipts record `changed_paths` relative to HEAD, including pre-existing changes
and non-ignored untracked paths. Nested repositories appear as directory paths.

Candidate identity includes HEAD, index entries, tracked changes, and untracked
paths/content/symlink targets, including Git-visible contents of untracked nested
repositories. Ignored files, including run artifacts and ignored build inputs,
are outside the fingerprint; keep ignored inputs stable during a run. Nested
repositories use their own Git ignore rules. Effective no-op and
changed-then-reverted states match; staged changes and commits affect identity. This lane prohibits commits
during a run. Changes invalidate earlier checks and approvals. This is Git
state evidence, not OS enforcement against arbitrary writes outside the repository.

The lead retains concise findings, developer dispositions, budgets, and current
state in `handoff.md`. The host retains receipts, requested provider/model/profile,
terminal IDs and candidate-bound callback provenance. A final ready claim requires
current full checks and each independent seat's latest callback, delivered after
the inbox rowid captured at the full receipt, naming that HEAD and fingerprint.
An older callback cannot override a newer response, including a pending one.
The runner prints the verdicts for operator inspection.
**Callback provenance does not establish that a verdict approves, or that its
judgment is correct.** Inspect the actual verdicts and handoff before accepting the
result; `verdicts_require_human_inspection` remains true. There is no automatic PR,
commit, publication, merge, or release. Stage matching begins at the stage value,
so `Stage: fixing failed full checks` and `Stage: not ready for human review`
neither stop nor approve the run. Terminal stage values must occupy the whole
line, with an optional trailing period; put explanations on separate lines.
Indentation and `**Stage:**` are accepted. Final readiness also waits for every
session terminal to yield, using the same statuses as host checks.

## Recovery and retention

Each preparation creates a new private directory. `started.json` is an exclusive
claim: a run directory can be executed once, including when its first attempt
fails. Do not remove that claim to retry a possibly side-effecting step. Reconcile
the retained CAO session and worktree, then prepare a fresh run. Host assignment
or elapsed-time exhaustion requests cleanup of that run's session; cleanup errors
are recorded. The runner removes its run-specific profiles and Copilot custom
agent only if no session launched or session deletion was confirmed. Retained
sessions keep their registrations so live leads can still assign workers; failed
or unconfirmed deletion also preserves them. Operators remove these registrations
after ending retained sessions. The Copilot path remains `~/.copilot/agents/`;
no installer path override was discoverable in the available local code or docs.
Existing registrations are preserved; collisions stop the run. Registration
cleanup errors are retained in `result.json` and prevent a successful result.
The private run directory remains available for inspection. Other failures
preserve the session for inspection. An interrupt records `interrupted` in
`result.json`; a host crash or interrupt can leave agents alive, so inspect CAO
before another launch.

`result.json` records success metadata or the stop reason and evidence location.
Unresolved engineering findings remain in the lead's `handoff.md`. Missing callbacks
or startup failures are infrastructure failures, not evidence of a product defect.
The runner uses CAO's local terminal/inbox SQLite metadata, so CAO schema changes
can require adaptation; it records requested models without claiming runtime
model attestation. HTTP/API failures stop visibly without automatic retries.

Run artifacts have no automatic expiry. They may contain the human-authored task
brief and curated reviewer text, which can still be sensitive. Do not put secrets,
raw terminal transcripts, captured provider prompts, environment dumps, source
contents or full diffs into diagnostic evidence. Operators own retention and
removal. No run evidence, installed sessions, or native settings are deleted by
source cleanup or sync.

## Optional Jev experiment and fallback policy

[Jev shadow](experiments/jev/README.md) is retained as a disconnected experiment.
It can assess manually supplied metadata, but neither its output nor its failure
controls assignments, review, correction budgets, or the release gate. The pod
never invokes it or sends its telemetry to reviewers.

Automatic fallback is disabled; there are no fallback chains configured. Provider
unavailability, authentication/quota errors, rate limits and terminal startup
failures stop for operator diagnosis. Execution failure with uncertain effects
requires repository/session reconciliation before another developer invocation.
Engineering findings and failed checks go through the bounded correction protocol;
they never trigger provider substitution. Unknown failures fail closed. Logical
seats and assurance requirements stay fixed. Local models are optional future
candidates and need interface/permission validation before use. Native credential
boundaries must remain intact. CAO metadata and completion alone cannot prove a
side-effecting retry is safe, so this runner supplies no automatic switching seam.

## Offline checks

```sh
python3 -m unittest discover -s .cao/tests
bash -n .cao/sync.sh
bash -n .cao/verification/verify.sh
git diff --check
```

Tests mock CAO and Jev calls and use temporary Git repositories. They cover the
runner, sync, seat profiles and the Jev experiment. No paid agents are launched.
