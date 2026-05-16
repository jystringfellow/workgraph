# WorkGraph Agents

Agents are reusable capabilities for reasoning over WorkGraph data.

They are not autonomous systems. They are invoked tools with clear inputs and outputs.

Each agent should:

- have a clear purpose
- define inputs and outputs
- be testable or verifiable

Current capability docs:

- `capture.md`: normalize incoming signals into events
- `summarize.md`: summarize event groups into sessions
- `resume.md`: reconstruct project context for restarting work

For now, `.agents/` documents future capabilities only. It is not a runtime, orchestration system, or tool registry.
