# AI Handoff

## Purpose

`workgraph ai handoff` lets a human intentionally continue active work in a
different supported AI coding tool while preserving the minimum structured
context needed for continuity.

It is a human-directed continuity feature, not an agent orchestration system.

## Goals

- Allow a human to hand active work from one supported AI CLI to another.
- Preserve useful work state through an explicit structured checkpoint.
- Preserve lineage between the source and destination Workgraph AI sessions.
- Carry forward relevant repository state without transporting conversation
  history.
- Make the handoff inspectable through Workgraph's existing AI-session model.
- Preserve native session behavior where supported.

## Non-goals

- Autonomous agent routing or orchestration.
- Multi-agent teams, consensus, voting, or parallel implementations.
- Transcript translation between AI providers.
- Reconstructing one provider's conversation inside another provider.
- Cloud synchronization or cross-machine resume.
- Selecting which AI should work next on the user's behalf.

## User experience

The primary interface is:

    workgraph ai handoff <destination> [options]

A handoff starts from the current/selected Workgraph AI session and launches
the destination AI tool with compact continuation context.

The exact CLI shape should follow existing Workgraph command conventions.

## Handoff contract

A successful handoff should:

1. Identify the source Workgraph AI session.
2. Capture an explicit structured checkpoint describing:
   - goal
   - current state
   - completed work
   - next actions
   - blockers
   - decisions
3. Observe relevant repository state using Workgraph's existing mechanisms.
4. Create or identify the destination Workgraph AI session.
5. Record lineage connecting destination to source.
6. Launch the destination AI tool with compact continuation context.
7. Leave enough durable Workgraph state for the human to inspect and resume
   the work later.

The implementation should reuse the existing AI-session and checkpoint
architecture rather than create a second continuity model.

## Privacy boundary

Handoff may persist the same categories of data already permitted by the
Workgraph AI-session model, including structured checkpoint text, native
session identifiers, timestamps, executable identity, and relevant repository
metadata.

Handoff must not automatically persist or transport:

- conversation transcripts
- prompts
- terminal buffers
- stdout or stderr
- shell history
- environment variables
- complete command-line arguments
- file contents
- source diffs

The destination receives only deliberately constructed continuation context,
not the source tool's conversation history.

## Failure behavior

A failed handoff must not destroy or invalidate the source session.

Failures should be explicit and actionable.

Partial failures must not leave misleading lineage or a destination session
that appears to have been successfully handed off when it was not.

Unsupported source/destination combinations should fail safely.

## Compatibility

Existing `workgraph ai run`, `checkpoint`, `sessions`, `show`, `resume`,
archive/unarchive, and native resume behavior must continue to work.

The design should accommodate the existing supported AI adapters rather than
special-case only the tools used during development.

## Acceptance criteria

- A user can intentionally hand work from one supported AI CLI to another.
- The destination receives sufficient structured context to continue the task.
- Source → destination lineage is recorded and inspectable.
- The source session remains intact.
- Structured checkpoint information survives source-agent termination.
- Repository state relevant to continuity is represented using existing
  Workgraph mechanisms.
- No transcript or other prohibited data is persisted or transported.
- Same-agent handoff behavior is defined and tested.
- Unsupported/error cases fail safely without corrupting session state.
- Existing AI-session behavior remains compatible.
- User-facing command help and documentation explain the feature and its
  privacy behavior.
- `go vet ./...` passes.
- `go test ./...` passes.