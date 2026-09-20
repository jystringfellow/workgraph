# workgraph Roadmap

The roadmap records current bets and sequencing. It does not define behavior;
that belongs in the linked specs and executable facts.

## Now

These are the current bets, in priority order.

1. **Finish connector setup UX.** Make calendar, mail, and Azure Boards setup
   validation consistent with the existing connector runtime, including
   draft-and-resume setup and test-before-save flows. [specs:
   `specs/connector-setup.md` and `specs/connector-runtime.md`]
2. **Make managed policy inspectable and complete.** Report setting provenance
   in diagnostics and add administrator controls for connector enablement and
   high-risk capture options. [specs: `specs/config.md` and
   `specs/enterprise-security.md`]
3. **Improve local context routing.** Add memory routing and indexing so the
   relevant user-owned context can be selected by task without introducing
   silent automation. [spec: `specs/memory.md`]
4. **Expand deterministic coverage analysis.** Improve watch-root coverage and
   suggestion quality using inspectable signals before adding more semantic or
   hosted intelligence. [specs: `specs/watch-suggestions.md` and
   `specs/ignore-suggestions.md`]

Priority labels used below:

- `P0`: current reliability, trust, and usability work
- `P1`: next work after the current trust loop is stable
- `P2`: later platform or automation work

## Next

- `P1` Add optional semantic association behind explicit opt-in and confidence
  gates. [specs: `specs/event-associations.md` and
  `specs/llm-integration.md`]
- `P1` Add explicit hosted LLM credentials and outbound request controls.
  [specs: `specs/llm-integration.md` and `specs/enterprise-security.md`]
- `P1` Add recurring-collaborator people memory and task-based memory routing.
  [spec: `specs/memory.md`]
- `P1` Expand connector coverage to meetings, work tracking, and knowledge
  bases as user-verifiable integrations. [specs: `specs/calendar.md`,
  `specs/azure-boards.md`, and `specs/notion.md`]
- `P1` Add preference modeling and locally resettable ranking weights while
  preserving the explicit suggestion lifecycle. [spec:
  `specs/personalization-feedback.md`]

## Later

- `P2` Approval-based actions: draft responses, draft PR comments, suggested
  commits, and explicit execution approval.
- `P2` Stronger local security: SQLite encryption, OS credential-store-backed
  keys, Windows credential ACLs, and Windows CI coverage.
- `P2` Broader distribution: Scoop, plugin expansion, and an open-source
  release workflow. [spec: `specs/distribution.md`]
- `P2` Desktop UI after the local CLI and data contracts are stable.

## Recently Delivered

- Connector registry, setup state, polling isolation, deadlines, retry
  backoff, and provider tool requirements. [specs:
  `specs/connector-runtime.md` and `specs/provider-tool-requirements.md`]
- Bridged capture hardening, executable provider recipes, cancellation,
  worker model persistence, and operator documentation. [spec:
  `specs/bridged-capture.md`]
- Notion activity attribution, event involvement, and source-neutral actor
  filtering. [specs: `specs/notion.md` and `specs/event-involvement.md`]
- Suggestion storage, explainability, feedback lifecycle, deterministic
  producers, associations, and effectiveness review. [specs:
  `specs/suggestion-explainability.md`, `specs/event-associations.md`, and
  `specs/effectiveness-review.md`]
- AI session continuity, native continuation, lifecycle controls, and agent
  plugin installation. [specs: `specs/ai-sessions.md` and
  `specs/agent-plugin.md`]
- Managed settings, enterprise security reporting, local LLM filtering, CI,
  and cross-platform release packaging. [specs:
  `specs/enterprise-security.md`, `specs/llm-integration.md`,
  `specs/ci.md`, and `specs/distribution.md`]
