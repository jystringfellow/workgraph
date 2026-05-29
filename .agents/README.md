# workgraph Agents

Agents are reusable capabilities for reasoning over workgraph data.

They are not autonomous systems. They are invoked tools with clear inputs and outputs.

Each agent should:

- have a clear purpose
- define inputs and outputs
- be testable or verifiable

Current capability docs:

- `capture.md`: normalize incoming signals into events
- `summarize.md`: summarize event groups into sessions
- `resume.md`: reconstruct project context for restarting work

Reusable agent skills live under `skills/` so users can symlink or copy them
into their preferred AI tool:

- `skills/workgraph-memory/`: draft and edit workgraph project memory

For now, `.agents/` is an agent-facing workspace, not a runtime or orchestration
system.
