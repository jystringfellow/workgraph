# WorkGraph Roadmap

## Phase 0: Core loop (weekend V1)
- [x] CLI: workgraph init
- [x] CLI: workgraph run
- [x] Foreground file capture
- [ ] Background daemon controls
- [x] CLI: workgraph today
- [ ] CLI: workgraph resume <project>
- [ ] SQLite event store
- [ ] File system watcher
- [ ] Basic project inference (repo/folder name)
- [x] Session grouping (time-based)
- [x] Simple output (no LLM)

## Phase 1: External signals
- [ ] Git integration (commits, branches)
- [ ] GitHub ingestion (PRs, issues)
- [ ] Slack ingestion (messages, threads)

## Phase 2: Memory layer
- [ ] Markdown memory repo
- [ ] Load memory into system
- [ ] Link events ↔ memory (projects, people)

## Phase 3: Intelligence
- [ ] Session summaries
- [ ] Task extraction
- [ ] “What next?” suggestions
- [ ] Resume improvements

## Phase 4: Personalization
- [ ] Voice/tone learning
- [ ] Preference modeling
- [ ] Decision heuristics

## Phase 5: Actions
- [ ] Draft responses (Slack/GitHub)
- [ ] Draft PR comments
- [ ] Suggested commits
- [ ] Approval-based execution

## Phase 6: Platform
- [ ] Plugin system
- [ ] Configurable connectors
- [ ] Desktop UI (Tauri)
- [ ] Open-source release
