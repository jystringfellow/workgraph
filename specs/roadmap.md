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
- `P1` Fix the existing GitHub connector's rate-limit pre-check and search
  bucket, and bridged identity duplicates. [spec: `specs/github-pr-watch.md`,
  Related `github` Connector Fixes]
- `P1` Add preference modeling and locally resettable ranking weights while
  preserving the explicit suggestion lifecycle. [spec:
  `specs/personalization-feedback.md`]

## Later

- `P2` Approval-based actions: draft responses, draft PR comments, suggested
  commits, and explicit execution approval.
- `P2` Dispatch `next` items to local or cloud agents for triage, consuming
  the `next --watch --json` feed, with deterministic classification, leases,
  enforced write scope, per-run attribution, and explicit approval.
- `P2` Receive GitHub webhooks (or a relay) and turn them into local poll
  hints.
- `P2` Follow PRs after reviewing them: re-review after new commits and
  replies in the user's review threads.
- `P2` Stronger local security: SQLite encryption, OS credential-store-backed
  keys, Windows credential ACLs, and Windows CI coverage.
- `P2` Broader distribution: Scoop, plugin expansion, and an open-source
  release workflow. [spec: `specs/distribution.md`]
- `P2` Desktop UI after the local CLI and data contracts are stable.

## Recently Delivered

- [x] Backlog verification and distinct decisions: `llm verify-backlog`
  sends waiting floor-to-threshold proposals to the `associate-verify`
  profile with dry run, limit, since, and resumable checks, and
  `recent_decisions` collapses repeated decisions into one counted entry.
  [spec: `specs/projects.md` D7]

- [x] Mapping changes relink their units and bulk project review: `memory
  link` and `memory unlink` move container-mapping links and resolve pending
  proposals at once, `projects review --recheck` reruns deterministic rules
  over proposals, and `--approve`, `--reject`, `--assign`, and
  `--approve-above` decide many links in one process. [spec:
  `specs/projects.md` D6]

- [x] Associate thresholds and verifier: per-profile auto-apply thresholds and
  verify floors (`llm add --auto-apply-confidence`, `llm set`), a per-profile
  accuracy gate whose required accuracy is the profile's threshold, and an
  `associate-verify` route that sends floor-to-threshold units to a second
  profile, directly or through the bridge. [spec: `specs/projects.md` D5]

- [x] System One decision-model profiles: `llm add --provider system-one`
  for Jev, Clef, Clef-flash, Microsoft-Decision-1, and local System One
  servers, used by `associate` with one choice question per unit, the
  returned probability as confidence, loopback-as-local classification, and
  direct-only execution. [spec: `specs/llm-integration.md`]

- [x] Bridge drain reliability: categorization alternates with claimable
  capture, `window_too_large` failures narrow bounded windows in place down to
  one hour, and Claude drains refuse to claim without every workgraph MCP
  permission. [spec: `specs/bridged-capture.md`; also `specs/projects.md`]

- [x] Scheduled categorization: hourly and 05:00 `llm.associate` outbox
  requests with kind-aware emit, claim, and complete, run directly by the
  daemon or by the routed bridge client, with a 24h no-progress cooldown and
  capture-only workers unchanged. [spec: `specs/projects.md` D4; also
  `specs/bridged-capture.md`]

- [x] Daily loop surfaces for projects: a `categorize` group in `next`, and
  `today` and the `resume` morning overview grouped by workstream, then
  project, then container, with agent-opened PRs and remaining decisions.
  [spec: `specs/projects.md` D4]

- [x] LLM categorization: `llm associate` with dry run, per-source opt-in
  over the whole payload (including persisted Slack conversation type),
  validation, auto-apply at confidence 0.95 with an accuracy gate and pause,
  and the accuracy metric in `projects review` and `workgraph review`. Direct
  and manual runs only; scheduling and bridge execution are D4. [spec:
  `specs/projects.md` D3; also `specs/llm-integration.md`]

- [x] Units of work and deterministic linking: Slack thread and burst, GitHub
  PR, git session, Notion page, and calendar units; the `project_links`
  table; shared-identifier and container-mapping rules; `projects explain`,
  `undo`, flag-form and interactive `projects review`, and applied unit
  counts in `projects list`. [spec: `specs/projects.md` D2]

- [x] Project catalog from memory frontmatter (workstream, aliases,
  description), `projects list`, container to workstream mappings, workstream
  suggestions in memory doctor, and "container" in user-facing text. [spec:
  `specs/projects.md` D1]

- [x] `workgraph next`: one local list of what is waiting on the user, with
  stable item ids, `explain`, `done`, `snooze`, a `--watch --json` feed, and a
  read-only `next_items` MCP tool; items appear in `resume`, with a count in
  `today`. [spec: `specs/next.md`]

- [x] Less noise in `next`: the user's own PRs that only need a checks or
  conflict fix get a `Needs your fix` section, PRs with no activity in 30+
  days collapse into one line per section (`--stale` lists them), team
  review previews show human-authored PRs first, and JSON items carry
  `section` and `stale`. [spec: `specs/next.md` N13-N15]

- [x] Team-only review requests in `next` are summarized per team with
  counts and the newest three, expandable with `--team-reviews`, and counted
  separately in `today` and `resume`. [spec: `specs/next.md` N12]

- [x] GitHub pull request watch (`github.pr_watch`) as the first `next`
  source: open PRs the user authored or is asked to review (directly or
  through a team) via the existing `gh` login, read-only transition events,
  untrusted comment handling, rate-limit deferral, and local push hints.
  [spec: `specs/github-pr-watch.md`]

- [x] A seven-day `resume` handoff with last-known-work fallback and a durable
  overview check-in for personal work captured since the previous run. [spec:
  `specs/resume.md`]

- [x] Explainable project-name and acronym candidates in memory doctor, today,
  resume and empty suggestions, with explicit interactive acceptance. [spec:
  `specs/memory.md`; #138]

- [x] Explicit, reversible many-source memory project mappings across suggest,
  resume and promotion. [spec: `specs/memory.md`; #138]

- [x] Opt-in launchd and systemd user capture services with explicit lifecycle
  controls. [spec: `specs/capture-reliability.md`; #139]

- [x] Keep capture running when transient watched files disappear. [spec:
  `specs/capture-reliability.md`; #139]

- [x] Opt-in file watching: a fresh install watches no directories, older
  automatic watch roots stop being watched, and git repository discovery uses
  its own `repo_dirs` scan roots. [spec: `specs/config.md`]

- [x] `start` waits through database upgrades and reports progress, failing
  only when the worker exits or stalls; schema migrations are coordinated
  across processes; the store uses WAL. [specs:
  `specs/capture-controls.md` and `specs/db-contracts.md`]

- [x] Readable `next` detail lines: thread replies on one line collapse with a
  count, failed checks are deduplicated and capped, conversation replies link
  the comment, and unknown dimensions read in plain words. Fallback project
  descriptions end at a word. `projects review` and `today` still list on a
  read-only database, with one warning. [specs: `specs/next.md`,
  `specs/projects.md`, and `specs/today.md`]

- [x] Opt-in agent-initiated AI checkpoints: one setting lets wrapped agents
  record short checkpoints at work milestones, each marked with its initiator
  in `ai show`, so `ai handoff` always has fresh context. [spec:
  `specs/ai-sessions.md`]

- Signed-in client config-dir bindings shared by bridge workers and LLM
  profiles, with runtime enforcement and inspectable diagnostics. [spec:
  `specs/signed-in-client-binding.md`]
- Connector registry, setup state, polling isolation, deadlines, retry
  backoff, and provider tool requirements. [specs:
  `specs/connector-runtime.md` and `specs/provider-tool-requirements.md`]
- Connector poll deferrals, retry-after deadlines, and early wake with
  minimum spacing. [spec: `specs/connector-runtime.md`]
- Bridged capture hardening, executable provider recipes, cancellation,
  worker model persistence, and operator documentation. [spec:
  `specs/bridged-capture.md`]
- Notion activity attribution, event involvement, and source-neutral actor
  filtering. [specs: `specs/notion.md` and `specs/event-involvement.md`]
- Suggestion storage, explainability, feedback lifecycle, deterministic
  producers, associations, and effectiveness review. [specs:
  `specs/suggestion-explainability.md`, `specs/event-associations.md`, and
  `specs/effectiveness-review.md`]
- AI session continuity, native continuation, privacy-bounded handoff,
  lifecycle controls, and agent plugin installation. Handoff rejects credential
  patterns in repository context while accepting ordinary underscore-separated
  names, rejects unorderable session evidence without creating phantom rows,
  identifies reused checkpoints, and gives saved-checkpoint retry guidance for
  context preparation and launch failures. `ai run` holds no database handle
  between lifecycle writes, keeps the agent running while the database is
  locked, and records the end event when the wrapper is signaled. [specs:
  `specs/ai-sessions.md`, `specs/ai-handoff.md`, and `specs/agent-plugin.md`]
- GitHub Copilot CLI support: a session-only checkpoint skill plugin on wrapped
  launches, the `workgraph` plugin package with per-drain permission grants,
  and signed-in tool-free LLM profiles. [specs: `specs/ai-sessions.md`,
  `specs/agent-plugin.md`, `specs/llm-integration.md`, and
  `specs/signed-in-client-binding.md`]
- Managed settings, enterprise security reporting, local LLM filtering, CI,
  and cross-platform release packaging. [specs:
  `specs/enterprise-security.md`, `specs/llm-integration.md`,
  `specs/ci.md`, and `specs/distribution.md`]
