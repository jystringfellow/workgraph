Feature: Local AI coding session continuity
  As a user of CLI AI coding agents
  I want durable local restart context
  So that I can stop work, reboot, or change agents on the same machine without depending on a vendor transcript

  Background:
    Given workgraph has been initialized locally

  Scenario: Record one wrapped child-process lifetime
    When I run "workgraph ai run -- codex" in a Git checkout
    Then workgraph launches "codex" directly without an implicit shell
    And the child receives the resolved session id, workgraph home, and database
    And workgraph stores one ai.session_started event after the child starts
    And workgraph stores one ai.session_ended event after the child exits
    And the wrapper returns the child's exit outcome
    And no event contains the child's remaining arguments, environment, or terminal output

  Scenario: Bind a Codex-native session without reading its transcript
    Given Codex was launched by "workgraph ai run"
    When Codex sends a trusted SessionStart or SessionEnd callback
    Then workgraph appends an ai.session_native_bound event for the injected workgraph session
    And the event stores the Codex session id and bounded lifecycle source
    And transcript paths, prompts, model data, and unknown callback fields are not persisted
    And an untrusted or unavailable callback does not invalidate the wrapped session

  Scenario: Bind and resume a Claude Code native session
    Given Claude Code was launched by "workgraph ai run"
    When Claude sends a SessionStart or SessionEnd callback
    Then workgraph stores only its exact native session id and bounded lifecycle source
    When I resume the ended workgraph session
    Then workgraph launches the stored Claude executable with "--resume <claude-session-id>"

  Scenario: Assign and resume a direct GitHub Copilot CLI session
    When I run "workgraph ai run -- copilot"
    Then workgraph supplies a fresh exact UUID with "--session-id"
    And the start event stores that native session id
    When I resume the ended workgraph session
    Then workgraph launches the stored Copilot executable with "--resume=<copilot-session-id>"
    And workgraph does not enable remote control or remote export

  Scenario: Bind and resume an OpenCode native session
    Given OpenCode was launched by "workgraph ai run"
    Then workgraph injects a local session-binding plugin without changing project or global OpenCode configuration
    When the plugin reports a parent OpenCode session lifecycle event
    Then workgraph stores only its exact native session id and bounded lifecycle source
    When I resume the ended workgraph session
    Then workgraph launches the stored OpenCode executable with "--session <opencode-session-id>"

  Scenario: Fail safely when a started child cannot be recorded
    Given the AI session start event cannot be persisted
    When I run "workgraph ai run -- codex"
    Then workgraph terminates and reaps the child
    And workgraph reports the persistence failure
    And no untracked child remains running

  Scenario: Abort before launch when the required launch observation fails
    Given Git metadata collection fails for a reason other than "not a repository"
    When I run "workgraph ai run -- codex"
    Then workgraph reports the observation failure
    And workgraph does not launch the child
    And workgraph writes no start event

  Scenario: Preserve a signal-terminated child outcome
    Given a wrapped child is terminated by SIGTERM
    When "workgraph ai run" finishes waiting for the child
    Then the end event records signal "SIGTERM"
    And the wrapper exits with status 143
    And an end persistence failure does not change status 143

  Scenario: Append a cooperative checkpoint
    Given an AI session was started in a Git checkout
    When the agent submits valid structured context with "workgraph ai checkpoint --stdin"
    Then workgraph uses the injected session and storage identity
    And workgraph stores an immutable ai.session_checkpointed event
    And the event separates workgraph-observed state from agent-stated context
    And dirty paths are relative to the worktree root, deduplicated, sorted, and bounded
    When the agent submits another valid checkpoint
    Then workgraph appends another checkpoint without changing the first

  Scenario: Explicitly ask the current agent to record a checkpoint
    Given a CLI agent was launched by "workgraph ai run"
    When I invoke "$workgraph-ai-checkpoint" or ask the agent to save a handoff
    Then the agent submits only the allowed agent-stated fields to "workgraph ai checkpoint --stdin"
    And workgraph collects the observed state itself
    And I do not need to write JSON, pipe printf, or use a shell escape
    And the command reports the stored session id and immutable event id
    And no checkpoint is generated on a schedule or merely because the process exits

  Scenario: Bind checkpoints to the original local working directory
    Given an AI session was started in one Git checkout
    When I submit its checkpoint from another checkout
    Then workgraph rejects the checkpoint without writing an event
    When I submit its checkpoint with an explicit storage path that disagrees with the injected path
    Then workgraph rejects the checkpoint without writing an event

  Scenario: Support a non-Git working directory
    Given an AI session was started outside Git
    When I checkpoint from the launch directory or one of its descendants
    Then workgraph stores empty Git metadata and the observed current directory
    When I checkpoint from outside the launch directory tree
    Then workgraph rejects the checkpoint without writing an event

  Scenario Outline: Reject invalid checkpoint input without persistence
    Given an AI session is eligible for a checkpoint
    When stdin contains <invalid-input>
    Then workgraph rejects the entire checkpoint
    And workgraph does not persist or echo the submitted text

    Examples:
      | invalid-input                                      |
      | more than 65,536 raw bytes                         |
      | invalid UTF-8                                      |
      | a second trailing JSON value                       |
      | a duplicate field                                  |
      | an unknown field                                    |
      | a null, numeric, boolean, or nested object value    |
      | only empty or whitespace-only content               |
      | a disallowed control character                      |
      | a recognized high-confidence credential pattern     |

  Scenario Outline: Derive session status from conservative evidence
    Given a session has started and <evidence>
    When I run "workgraph ai sessions"
    Then the session status is <status>

    Examples:
      | evidence                                                   | status        |
      | an end event exists                                        | ended         |
      | its recorded boot differs from the current boot            | interrupted   |
      | its original PID is definitively absent                    | interrupted   |
      | its PID and process-start identity match                    | running       |
      | its PID exists with a different process-start identity      | interrupted   |
      | process identity cannot be verified                         | unknown       |

  Scenario: List every known session deterministically
    Given workgraph has multiple AI sessions in different states
    When I run "workgraph ai sessions"
    Then every known session appears by latest event time descending and session id ascending
    And each entry shows the full session id, tool, status, project, started time, latest checkpoint time, and latest event time
    And the overview does not show paths or agent-stated checkpoint text

  Scenario: Show stored evidence without live inspection
    Given an AI session has multiple observed snapshots and agent checkpoints
    When I run "workgraph ai show <session-id>"
    Then workgraph shows the latest stored supported observation with its time
    And workgraph separately shows the latest stored supported agent checkpoint with its time
    And workgraph does not inspect the current Git checkout or filesystem
    And workgraph does not launch an agent or execute a vendor resume command

  Scenario: Show a session without an agent checkpoint
    Given an AI session has a start event and no checkpoint event
    When I run "workgraph ai show <session-id>"
    Then workgraph shows the stored launch observation
    And workgraph reports "No agent-stated checkpoint recorded."

  Scenario: Degrade safely for a newer event schema
    Given a session has supported evidence followed by an event with an unsupported schema version
    When I run "workgraph ai show <session-id>"
    Then workgraph does not interpret the unsupported event content
    And any older supported evidence is labeled as older than an unsupported event
    And workgraph displays an explicit compatibility warning

  Scenario Outline: Resume a verified native session through its workgraph id
    Given an ended workgraph session has a stored <tool> session id
    When I run "workgraph ai resume <workgraph-session-id>"
    Then workgraph launches the stored <tool> executable directly with <resume-arguments>
    And it launches from the latest stored working directory
    And it records a new workgraph session with the requested session as predecessor
    And it does not persist the constructed command arguments

    Examples:
      | tool     | resume-arguments                |
      | Codex    | "resume <native-session-id>"   |
      | Claude   | "--resume <native-session-id>" |
      | Copilot  | "--resume=<native-session-id>" |
      | OpenCode | "--session <native-session-id>" |

  Scenario: Link an explicitly resumed Codex process
    Given a prior workgraph session stores Codex native session id "codex-id"
    When I run "workgraph ai run -- codex resume codex-id"
    Then the new start event stores "codex-id"
    And it links the most recent matching workgraph session as predecessor

  Scenario: Refuse to guess unsupported or ambiguous native resume behavior
    Given a session has no native id or uses a tool without a verified native adapter
    When I run "workgraph ai resume <session-id>"
    Then workgraph reports that native resume is unavailable
    And workgraph launches no process

  Scenario: Reject conflicting Codex lifecycle overrides before launch
    Given Codex is launched with a user CLI override for hooks.SessionStart or hooks.SessionEnd
    When I run "workgraph ai run -- codex <arguments>"
    Then workgraph reports that the Codex lifecycle override conflicts with its native adapter
    And workgraph does not launch Codex or write a start event
    But unrelated Codex configuration overrides remain allowed

  Scenario: Keep AI continuity local and privacy-bounded
    When workgraph records start, checkpoint, and end events
    Then all events remain in the selected local SQLite database
    And workgraph makes no synchronization or remote-storage request
    And stored events contain no transcript, prompt, argv, environment value, terminal output, file content, source diff, or persistent machine identifier
    And curated checkpoint text is the only agent-authored content eligible for storage

  Scenario: Reject a nested wrapped session
    Given WORKGRAPH_AI_SESSION_ID is already present
    When I run "workgraph ai run -- codex"
    Then workgraph rejects the nested session before launching a child
    And workgraph writes no nested start event

  Scenario: Explain a stale or inherited session variable during resume
    Given WORKGRAPH_AI_SESSION_ID is already present
    When I run "workgraph ai resume <session-id>"
    Then workgraph does not silently clear the nested-session guard
    And workgraph tells me to resume outside the wrapped agent or unset a known-stale value
    And workgraph launches no child process
