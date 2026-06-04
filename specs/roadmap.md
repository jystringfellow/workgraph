# workgraph Roadmap

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
   - [ ] Background polling from stored calendar connector settings.
- [ ] Meeting ingestion (Zoom, Google Meet, Microsoft Teams metadata/transcripts when explicitly available)
   - [ ] Meeting notes archive with index, decisions, and action items.
- [ ] Work tracking ingestion (Jira, Azure DevOps, Linear)
- [ ] Knowledge base ingestion (Notion, Confluence, Google Docs/Drive)
   - [ ] Knowledge claim notes for durable beliefs and decision rationale.
   - [ ] Rich local HTML artifacts/reports linked to memory and evidence.
- [ ] Configurable connector framework
   - [ ] Memory routing/index file for loading relevant context by task.

## Phase 3.5: Enterprise security and compliance
- [ ] IT-readable Slack/compliance document
- [ ] SQLite encryption at rest
- [ ] OS credential-store backed encryption keys
- [ ] Connector credential hardening
- [ ] Hosted LLM opt-in controls
- [ ] Local outbound LLM filtering for secrets and configured sensitive patterns
- [ ] Network destination transparency

## Phase 4: Suggestions and intelligence
- [ ] Suggest watch roots from external signals
- [ ] Suggest ignore rules from noisy tracked activity
- [ ] Session summaries
- [ ] Task extraction
- [ ] “What next?” suggestions
- [ ] Resume improvements

## Phase 5: Personalization
- [ ] Voice/tone learning
- [ ] Preference modeling
- [ ] Decision heuristics

## Phase 6: Actions
- [ ] Draft responses (Slack/GitHub)
- [ ] Draft PR comments
- [ ] Suggested commits
- [ ] Approval-based execution

## Phase 7: Platform
- [x] CI runs full Go tests on pull requests to main
- [ ] Plugin system
- [ ] Desktop UI (Tauri)
- [ ] Open-source release
