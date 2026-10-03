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

Successful checks should normally be represented to agents as compact
structured evidence rather than raw command output.

When a check fails, provide the responsible agent with the smallest useful
failure evidence needed to diagnose and correct the problem.

Do not feed large successful logs into model context without a specific reason.

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