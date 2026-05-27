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
- [ ] Organization memory (strategy memos, planning docs, operating principles)
- [ ] Team memory (squad strategy, rituals, ownership, current goals)
- [ ] Evidence can suggest memory updates without becoming memory automatically
- [ ] Link events ↔ memory (projects, people)

## Phase 3: Connectors
- [ ] Slack ingestion (messages, threads)
- [ ] Calendar ingestion (Google Calendar, Outlook Calendar)
- [ ] Meeting ingestion (Zoom, Google Meet, Microsoft Teams metadata/transcripts when explicitly available)
- [ ] Work tracking ingestion (Jira, Azure DevOps, Linear)
- [ ] Knowledge base ingestion (Notion, Confluence, Google Docs/Drive)
- [ ] Configurable connector framework

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
