# Architecture Improvements

This spec captures architectural cleanup that should guide upcoming feature
work. These are not standalone refactor mandates. Prefer landing them when they
support a behavior slice, so facts continue to drive the shape of the code.

## Goals

- keep command routing thin and easy to scan
- keep connector policy enforcement consistent across setup and runtime
- keep resume relevance and event association logic explainable and reusable
- avoid large refactors that do not improve an immediately testable behavior

## CLI Command Boundaries

`cmd/workgraph/main.go` should remain a small command router over time.

As command groups grow, move parsing and rendering into files grouped by
surface area, such as:

- `cmd/workgraph/resume.go`
- `cmd/workgraph/connectors.go`
- `cmd/workgraph/llm.go`
- `cmd/workgraph/settings.go`

The command package should keep CLI-only concerns such as flags, usage text,
stdout/stderr rendering, and exit codes. Core behavior should remain in the
root package behind testable config/result structs.

## Connector Policy And Runtime

Connector governance currently spans managed settings, connector runtime state,
provider setup functions, and `workgraph start` polling. Future connector
security work should consolidate policy decisions behind a small internal
surface before adding more connector controls.

Target shape:

```text
connectorPolicy.CanSetup(id)
connectorPolicy.CanEnable(id)
connectorPolicy.CanPoll(id)
```

The policy layer should answer whether an action is allowed and provide the
human-readable reason. Provider setup functions should ask policy before OAuth,
manual-token validation, or local credential writes. Runtime polling should ask
policy before both explicit one-shot polling and unattended daemon polling.

Provider-specific high-risk options should remain explicit when the semantics
are provider-specific. For example, Slack DM capture is not just another
connector id because it changes requested OAuth scopes and capture behavior
inside an otherwise allowed Slack connector.

## Resume Relevance

Bare `workgraph resume` should eventually list projects with user-work
evidence, not every project that has any stored event.

The relevance logic should live outside `resume.go` once it becomes more than a
simple grouping rule. It should answer questions such as:

- did the local user author or directly participate in this work?
- is this project active enough to be worth resuming?
- which concrete events explain why this project is shown?
- which events are weak evidence, such as cloned repository history authored by
  someone else?

Project-specific `workgraph resume <project>` should remain exact and complete.
Filtering should primarily affect the project list shown by bare
`workgraph resume`. A future `workgraph resume --all` can preserve the current
"all projects with events" behavior for debugging and audit.

## Identity And Ownership

Resume relevance and future association scoring need an explicit local identity
model. The first deterministic version can use:

- `git config --global user.email`
- repo-local `git config user.email`
- configured additional git email addresses
- configured GitHub logins when available

This should become inspectable local config rather than hidden inference. The
system should be conservative when identity is unknown.

## Event Associations

Cross-source association should start as a deterministic local baseline before
adding LLM or embedding ranking.

The baseline association layer should score candidate links using concrete
evidence:

- same project or repository
- same branch
- same commit SHA
- same pull request or issue number
- same URL
- same file path or directory
- close timestamp window
- same actor or participant
- normalized title or summary token overlap

Scores should be explainable. A user or fact should be able to inspect why two
events were considered related.

Suggested future command:

```text
workgraph associations explain <event-id>
```

Example output:

```text
Event github.pull_request:42
Likely related:
- git.commit:abc123
  score: 92
  reasons:
  - same repository workgraph
  - pull request references commit abc123
  - commit author matches local git identity
```

The semantic lane should only rank or explain a small deterministic candidate
set. It should not require sending broad raw event history to an LLM, and it
must remain optional under the existing LLM controls.

## Large Provider Files

Provider files such as Slack, calendar, mail, Notion, Azure Boards, and LLM
will likely continue to grow. Split them only when a behavior slice benefits
from the split.

Useful split boundaries are:

- setup and OAuth flow
- capture and provider API calls
- local storage and config files
- rendering and user-facing messages
- provider-specific facts or fixtures

Avoid introducing broad interfaces before multiple providers genuinely share a
stable shape.

## Non-Goals

- no repository-wide package split before behavior requires it
- no generic connector option bag that hides provider-specific security meaning
- no LLM-first association system
- no relevance scoring that silently mutates stored events

