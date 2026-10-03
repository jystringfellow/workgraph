# Feature Engineering Pod

## Mission

Take a Workgraph feature from human intent through a tested, reviewed,
release-ready implementation.

The pod uses agents where judgment is valuable and deterministic tooling where
facts can be established directly.

## Flow

The normal feature flow is:

1. Architect
2. Developer
3. Deterministic Verification
4. Jev Shadow Evaluation
5. Adversarial Reviewer
6. Developer Fix Loop, when necessary
7. Release Gate

Deterministic verification is repeated after material implementation changes.

Jev currently observes the workflow but does not control it.

## Seats

### Architect

Owns understanding and planning.

The Architect begins from primary sources rather than assuming the requested
change is already well understood.

Produces:

- problem statement
- relevant existing behavior
- relevant specifications and invariants
- proposed design
- affected components
- non-goals
- compatibility considerations
- privacy considerations
- risks and edge cases
- concrete acceptance criteria

Acceptance criteria should be observable and testable whenever practical.

The Architect should identify uncertainties explicitly rather than silently
inventing requirements.

The Architect does not implement and does not modify the repository.

The Architect's output is a plan, not authority. Later agents should verify
its assumptions against primary sources.

### Developer

Owns implementation of the agreed feature.

The Developer starts from:

- task intent
- Architect plan
- acceptance criteria
- relevant specifications
- current repository state

Produces:

- the smallest coherent implementation
- appropriate tests
- documentation and help changes when required
- compatibility handling when required
- concise implementation notes useful to later stages

The Developer should:

- preserve existing behavior outside the intended change
- follow existing repository conventions where appropriate
- avoid unrelated cleanup
- add or update tests for changed behavior
- treat privacy requirements as functional requirements
- resolve deterministic verification failures rather than weakening checks

The Developer may inspect and modify the repository.

The Developer should not declare its own work release-ready.

## Deterministic Verification

Deterministic Verification is a workflow stage, not an agent seat.

Its purpose is to establish machine-verifiable facts without spending model
context on discovering them.

The baseline Workgraph verification commands are:

- `go vet ./...`
- `go test ./...`

These mirror the core checks in Workgraph CI.

Verification should run outside LLM context whenever practical.

On success, downstream agents should receive compact evidence such as:

    go_vet: pass
    go_test: pass

Successful raw logs should not normally be placed into model context.

On failure, the Developer should receive the smallest useful diagnostic
evidence, including:

- failing check
- exit status
- relevant error output
- failing test names when available

The Developer fixes the problem and verification runs again.

A feature does not advance past this stage while required deterministic checks
are failing.

Additional deterministic checks may be added when required by the feature or
repository, including:

- formatting
- generated-file consistency
- CLI smoke tests
- migration validation
- repository cleanliness
- targeted privacy invariants
- targeted compatibility tests

Checks should be concrete and reproducible.

## Jev Shadow Evaluation

Jev provides inexpensive bounded judgments about the change after deterministic
state has been collected.

Jev is currently experimental and operates only in shadow mode.

It does not control the workflow.

Its output must not:

- skip the Adversarial Reviewer
- bypass deterministic verification
- send work back to the Developer
- approve a release
- reject a release
- change release-gate behavior

Initial shadow questions may include:

### Change Risk

Classify the change as:

- low
- medium
- high

### Review Focus

Select the most useful primary review focus from:

- privacy
- compatibility
- data integrity
- CLI behavior
- architecture
- general

### Deep Review Probability

Estimate whether the change warrants deep substantive review.

This is recorded as telemetry even though substantive review is currently
required regardless of the result.

Jev should receive minimal structured state such as:

- deterministic verification results
- number of changed files
- changed paths
- insertion/deletion counts
- whether tests changed
- whether specifications changed
- other explicitly derived non-sensitive signals

Do not send source code, complete diffs, prompts, Workgraph memory, verbose test
logs, credentials, or unrelated user context merely to improve a shadow
prediction.

Record enough information to later compare Jev's prediction with:

- actual Adversarial Reviewer findings
- finding categories
- whether Developer rework was required
- Release Gate outcome

Shadow data exists to determine empirically whether Jev deserves a future role
in workflow control.

## Adversarial Reviewer

Performs an independent substantive review after deterministic verification
passes.

The reviewer reconstructs intended behavior from primary sources rather than
trusting the Developer's explanation.

Primary inputs include:

- original task intent
- acceptance criteria
- relevant specifications
- actual repository changes
- tests
- deterministic verification results

Jev output should not bias the review.

During the shadow period, do not tell the Adversarial Reviewer what Jev
predicted before the reviewer completes its independent assessment.

Focus on:

- correctness
- architectural fit
- edge cases
- backward compatibility
- privacy boundaries
- data integrity
- CLI behavior and ergonomics
- failure and recovery behavior
- unnecessary complexity
- missing requirements
- insufficient tests for material behavior

Report concrete findings with evidence.

For each material finding, identify:

- what is wrong
- where it occurs
- why it matters
- what requirement, invariant, or observable behavior is violated
- enough evidence for the Developer to investigate

Do not report stylistic preference as a defect.

Do not propose unrelated refactoring merely because another design is
possible.

If no material findings exist, say so explicitly.

### Review Fix Loop

When the Adversarial Reviewer identifies a material finding:

1. Send the finding and supporting evidence to the Developer.
2. Developer makes the smallest coherent correction.
3. Run Deterministic Verification again.
4. If verification fails, return failure evidence to the Developer.
5. Once verification passes, review the corrected state again as appropriate.

The review correction limit bounds Developer correction cycles, not review
assessments. Every correction is followed by deterministic verification and a
fresh review that determines whether it resolved the finding. If the final
allowed correction has already been assessed and material findings remain, the
workflow fails unresolved without another correction.

The stopping condition is evidence, not agreement between agents.

## Release Gate

Performs final independent release-readiness assessment.

The Release Gate does not assume that earlier agents were correct merely
because the workflow reached this stage.

It checks the actual repository state against:

- original task intent
- acceptance criteria
- relevant specifications and invariants
- deterministic verification evidence
- resolved substantive review findings
- documentation/help requirements
- privacy requirements
- compatibility requirements

The Release Gate should distinguish:

- release-blocking findings
- non-blocking observations

A release-blocking finding must identify concrete evidence and the unmet
requirement or invariant.

The Release Gate does not modify the implementation.

If a release blocker is found:

1. Return it to the Developer.
2. Correct the implementation.
3. Re-run Deterministic Verification.
4. Repeat substantive review when the correction materially changes reviewed
   behavior.
5. Run the Release Gate again.

The release-gate correction limit likewise bounds Developer correction cycles.
Every correction receives a subsequent Release Gate assessment; if blockers
remain after that assessment of the final allowed correction, the workflow
fails unresolved.

## Release-Ready Criteria

A feature is release-ready only when:

- acceptance criteria are satisfied
- `go vet ./...` passes
- `go test ./...` passes
- feature-specific deterministic checks pass
- material Adversarial Reviewer findings are resolved
- no Release Gate blocker remains
- relevant tests cover changed behavior
- documentation/help reflects user-visible behavior
- compatibility changes are intentional and documented
- privacy boundaries remain satisfied
- no unintended repository artifacts remain

Agent consensus is not a release criterion.

Objective evidence is.

## Current Seat Assignment

The current intended assignments are:

- Architect — Codex / GPT-6 Astra
- Developer — Codex / GPT-5.6 Sol
- Adversarial Reviewer — GitHub Copilot CLI / independent capable model
- Release Gate — Codex / GPT-5.6 Sol

These assignments are implementation details of the current rig.

The responsibilities defined by this pod remain stable if models or providers
change.