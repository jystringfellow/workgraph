# WorkGraph Roadmap

## Phase 0: Core loop (weekend V1)
- [x] CLI: workgraph init
- [x] CLI: workgraph run
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
- [x] Personal memory (priorities, principles, preferences, working style)
- [x] Organization memory (strategy memos, planning docs, operating principles)
- [x] Team memory (squad strategy, rituals, ownership, current goals)
- [x] Evidence can suggest memory updates without becoming memory automatically
- [x] Link events ↔ memory (projects, people)

## Phase 3: Connectors
- [x] Slack ingestion (messages, threads)
- [ ] Calendar ingestion (Google Calendar, Outlook Calendar)
- [ ] Meeting ingestion (Zoom, Google Meet, Microsoft Teams metadata/transcripts when explicitly available)
- [ ] Work tracking ingestion (Jira, Azure DevOps, Linear)
- [ ] Knowledge base ingestion (Notion, Confluence, Google Docs/Drive)
- [ ] Configurable connector framework

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
- [ ] Plugin system
- [ ] Desktop UI (Tauri)
- [ ] Open-source release


```text
We were working on WorkGraph Slack integration. Please inspect the current repo state and continue from the latest specs/roadmap.

Context:
- Slack OAuth browser connect exists.
- Cloudflare Pages static relay exists at `public/slack/callback/index.html`.
- Default Slack redirect is `https://workgraph.pages.dev/slack/callback`.
- Local callback is `http://localhost:2727/slack/callback`.
- Slack polling works: messages and thread replies are being stored in SQLite.
- Channel discovery works for visible public/private channels.
- `workgraph slack connect` stores Slack config in `~/.workgraph/slack.json`.
- Empty channel list means auto-discover visible public/private channels.
- `--include-dms` exists, but current behavior is misleading:
  `workgraph slack configure --include-dms` only flips local config and does not reauthorize Slack scopes.

Important issue to address:
Slack DM opt-in needs OAuth-aware behavior.

Expected fix direction:
- Store granted Slack user scopes from OAuth response.
- When enabling DMs, check whether required DM scopes are present:
  `im:read`, `im:history`, `mpim:read`, `mpim:history`.
- If missing, do not claim DMs are enabled.
- Either tell the user to run `workgraph slack connect --include-dms`, or automatically start the Slack OAuth flow for incremental reauthorization.
- Update success messaging to distinguish local preference from Slack-granted permissions.
- Add facts so this cannot regress.

Also worth considering later:
- Resolve Slack user IDs to display names.
- Filter or separately type Slack join/system messages.
- Add IT/security compliance docs from `specs/enterprise-security.md`.

Follow the repo workflow:
write spec → write feature → write failing fact → implement → pass → cross off roadmap
```

Looks good. That confirms DM/IM capture is working too:

```text
D0B6HU2HY6P
D0B68QHT41M
```

Those `D...` projects are Slack DM conversation IDs, which is expected with the current implementation. Later we should improve that by resolving conversation/user names so DMs don’t show up as raw IDs.

Useful notes for next slice:
- DM capture works after reconnect/restart.
- Need OAuth-aware DM opt-in messaging.
- Need Slack conversation naming:
  - public/private channels: use channel names
  - IMs: resolve other participant name
  - MPIMs: resolve group DM/member names
- Need Slack user ID resolution for `actor`.

For now, the core collection path is working end to end.