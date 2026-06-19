# workgraph Roadmap

## Recommended Priority Order
1. **P0a now**: connector reliability and setup UX so capture is dependable and easy to configure.
2. **P0b now**: shared suggestion substrate: durable suggestions, evidence, confidence, lifecycle, feedback, and suppression.
3. **P0c now**: first deterministic suggestion producers, starting with watch-root or ignore-rule suggestions.
4. **P0d now**: local-only feedback loop and personal effectiveness review once suggestion lifecycle records exist.
5. **P1 next**: deterministic cross-source association baseline, then optional semantic association and hosted LLM controls.
6. **P2 later**: action automation and broader platform/distribution work after trust and relevance loops are stable.

Priority labels used below:

- `P0a`: immediate reliability/setup foundation
- `P0b`: immediate suggestion storage/trust substrate
- `P0c`: first suggestion producers
- `P0d`: feedback/review loop built on suggestion records
- `P1`: next, after P0 trust loops are stable
- `P2`: later

## Phase 0: Core loop (weekend V1)
- [x] CLI: workgraph init
- [x] CLI: workgraph start
- [x] Foreground file capture
- [x] Background capture controls
- [x] CLI: workgraph today
- [x] CLI: workgraph resume <project>
- [x] Local config file
- [x] Sane default watch roots
- [x] Configurable ignored paths and names
- [x] SQLite event store
- [x] File system watcher
- [x] Basic project inference (repo/folder name)
- [x] Git-root project inference
- [x] Session grouping (time-based)
- [x] Simple output (no LLM)

## Phase 1: Initial integrations
- [x] Git integration (commits, branches)
- [x] GitHub ingestion (PRs, issues)

## Phase 2: Active memory layer
- [x] Markdown memory repo
- [x] Load memory into system
- [x] Resume explicit project from memory-only context
- [x] Personal memory (role, priorities, principles, preferences, working style, AI collaboration)
- [x] Organization memory (strategic themes, strategy, planning notes, operating principles)
- [x] Team memory (strategy, people, operating norms, rituals, ownership, goals)
- [x] Evidence can suggest memory updates without becoming memory automatically
- [x] Link events ↔ memory (projects, people)

## Phase 3: Connectors
- [x] Slack ingestion (messages, threads)
   - [x] Fix Slack thread polling so replies added to already-seen parent messages are captured.
   - [x] Make Slack DM opt-in OAuth-aware.
   - [x] Resolve Slack conversation and user display names.
   - [ ] People memory files or index for recurring collaborators discovered through connectors.
- [ ] Calendar ingestion (Google Calendar, Outlook Calendar)
   - [x] Normalized calendar.event capture from provider-neutral JSON export.
   - [x] Google Calendar event capture maps provider API events into calendar.event.
   - [x] Google Calendar OAuth connect stores local connector settings.
   - [x] Google Calendar browser OAuth uses PKCE with the default workgraph client id.
   - [x] Google Calendar OAuth token exchange uses the workgraph Cloudflare relay with local `.dev.vars` development setup.
   - [x] Google Calendar disconnect revokes the stored token and removes local connector settings.
   - [x] Google Calendar token refresh.
   - [x] Microsoft publisher-domain verification file is hosted from the workgraph Pages site.
   - [x] Microsoft Calendar OAuth connect uses PKCE and stores local connector settings.
   - [x] Microsoft Calendar disconnect removes local connector settings while preserving other providers.
   - [x] Background polling from stored calendar connector settings. [P0a, spec: `specs/connector-runtime.md`]
- [ ] Mail ingestion (Gmail, Outlook Mail)
   - [x] Google Mail uses the existing Google OAuth app.
   - [x] Only full-content mail capture, no separate modes.
   - [x] Google Mail OAuth planning for Restricted Gmail scopes.
   - [x] Google Mail OAuth connect stores local connector settings.
   - [x] Google Mail disconnect revokes and removes local connector settings.
   - [x] Google Mail capture into normalized mail events.
   - [x] Microsoft Graph Mail OAuth planning with incremental delegated consent.
   - [x] Microsoft Mail OAuth connect stores local connector settings.
   - [x] Microsoft Mail disconnect removes local connector settings.
   - [x] Microsoft Mail capture into normalized mail events.
- [ ] Meeting ingestion (Zoom, Google Meet, Microsoft Teams metadata/transcripts when explicitly available)
   - [ ] Meeting notes archive with index, decisions, and action items.
- [ ] Work tracking ingestion (Jira, Azure DevOps, Linear)
   - [x] Azure DevOps authentication via Microsoft Entra ID as a separate connector from Microsoft Graph mail/calendar.
   - [ ] Advanced manual-token/PAT setup for enterprise environments where OAuth app approval is blocked or slow.
- [ ] Knowledge base ingestion (Notion, Confluence, Google Docs/Drive)
   - [x] Notion OAuth connect/disconnect.
   - [x] Notion page/database capture into normalized knowledge events.
   - [x] Advanced manual-token setup for Notion internal integrations when OAuth is not practical.
   - [ ] Knowledge claim notes for durable beliefs and decision rationale.
   - [ ] Rich local HTML artifacts/reports linked to memory and evidence.
- [ ] LLM connector
   - [x] Local config for provider/model selection.
   - [ ] Explicit opt-in hosted LLM credentials and outbound request controls. [P1, spec: `specs/llm-integration.md`]
   - [ ] Fact-backed summary/suggestion command path using the configured LLM.
- [ ] Configurable connector framework
   - [x] Connected services poll automatically from `workgraph start` with visible controls. [P0a, spec: `specs/connector-runtime.md`]
   - [ ] Memory routing/index file for loading relevant context by task.
   - [x] Connector setup handoff state: `draft`, `ready`, `error`, validation timestamps, validation errors, and `connectors status`. [P0a, spec: `specs/connector-runtime.md` and `specs/connector-setup.md`]
   - [ ] Interactive connector setup wizard for required/optional params with inline help. [P0a, spec: `specs/connector-setup.md`]
   - [ ] Connector setup validation flow (test connection before save, draft-and-resume support). [P0a, spec: `specs/connector-setup.md`]

## Phase 3.5: Enterprise security and compliance
- [ ] IT-readable Slack/compliance document
- [ ] Admin-controlled managed settings file that overrides local user config for workgraph's own behavior. [spec: `specs/config.md` and `specs/enterprise-security.md`]
  - [x] First managed settings reader with fixed platform-managed runtime paths and internal fact-only path injection.
  - [x] `workgraph settings get` reports effective LLM managed settings without exposing secrets.
  - [x] Managed LLM policy is enforced before provider calls.
- [ ] Managed setting provenance in `workgraph doctor`, `workgraph settings get`, and machine-readable diagnostics.
- [x] Admin controls for disabling hosted LLM providers or restricting OpenAI-compatible LLM endpoints to approved local/company URLs.
- [ ] Admin controls for connector enablement and high-risk connector options such as Slack DM capture and mail body capture.
- [ ] Machine-readable security/config report for endpoint verification.
- [ ] SQLite encryption at rest
- [ ] OS credential-store backed encryption keys
- [ ] Connector credential hardening
- [x] Manual-token connector setup pattern: OAuth remains the default, while `connect-token` style commands support local-only PAT/internal-token use with clear warnings.
- [ ] Hosted LLM opt-in controls [P1, spec: `specs/enterprise-security.md`]
- [ ] Local outbound LLM filtering for secrets and configured sensitive patterns
- [ ] Network destination transparency [P1, spec: `specs/enterprise-security.md`]

## Phase 4: Suggestions and intelligence
- [ ] Suggest watch roots from external signals
- [x] Suggest ignore rules from noisy tracked activity
- [ ] Session summaries
- [ ] Task extraction
- [ ] “What next?” suggestions [P1, spec: `specs/today.md`]
- [ ] Resume improvements
- [x] Shared suggestion storage: ids, type, reason, evidence, confidence, lane, lifecycle state, feedback, and suppression. [P0b, spec: `specs/suggestion-explainability.md` and `specs/db-contracts.md`]
- [ ] Explainable suggestion evidence trails with per-suggestion suppression controls. [P0b, spec: `specs/suggestion-explainability.md`]
- [x] First deterministic suggestion producer: ignore-rule or watch-root suggestions. [P0c, spec: `specs/ignore-suggestions.md` and `specs/watch-suggestions.md`]
- [ ] Cross-source event association baseline (deterministic IDs + local fuzzy heuristics) without LLM dependency. [P1, spec: `specs/event-associations.md`]
- [ ] Optional semantic association lane (LLM/embeddings) behind explicit opt-in and confidence gates. [P1, spec: `specs/event-associations.md` and `specs/llm-integration.md`]
- [ ] Local personal effectiveness review (no telemetry): acceptance rate, dismissal reasons, freshness, time-to-useful-suggestion. [P0d, spec: `specs/effectiveness-review.md`]

## Phase 5: Personalization
- [ ] Voice/tone learning
- [ ] Preference modeling [P1, spec: `specs/personalization-feedback.md`]
- [ ] Decision heuristics
- [ ] Local feedback event capture (accept, dismiss, snooze, complete) for continuous reranking. [P0b, spec: `specs/personalization-feedback.md` and `specs/db-contracts.md`]
- [ ] Per-user ranking weights learned locally with reset/export controls. [P1, spec: `specs/personalization-feedback.md`]
- [ ] Advanced editable local preference rules in addition to interaction-driven learning. [P1, spec: `specs/personalization-feedback.md`]

## Phase 6: Actions
- [ ] Draft responses (Slack/GitHub)
- [ ] Draft PR comments
- [ ] Suggested commits
- [ ] Approval-based execution

## Phase 7: Platform
- [x] CI runs full Go tests on pull requests to main
- [ ] Harden facts for GitHub Actions portability, including temp directory assumptions and long-running daemon/start tests.
- [ ] Distribution
   - [ ] Homebrew formula/tap.
   - [ ] Scoop manifest.
   - [ ] Versioned release archives and checksums.
- [ ] Plugin system
- [ ] Desktop UI (Tauri)
- [ ] Open-source release
