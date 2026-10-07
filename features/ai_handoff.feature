Feature: Privacy-bounded AI session handoff
  As a user of supported CLI AI coding agents
  I want to continue work in another agent
  So that durable local context survives without copying a provider transcript

  Background:
    Given workgraph has been initialized locally

  Scenario: Hand an active session to a verified destination
    Given a supported wrapped AI session has a structured checkpoint
    When I run "workgraph ai handoff <destination>"
    Then workgraph launches codex, claude, copilot, or opencode with compact checkpoint and repository context
    And the destination session records the source session as its predecessor
    And the destination receives its own workgraph session environment
    And no event or continuation context contains transcripts, prompts, terminal output, environment values, complete arguments, file contents, or diffs

  Scenario: Capture a fresh checkpoint during handoff
    Given the source AI session has not ended
    When I run "workgraph ai handoff <destination> --stdin" with a valid structured checkpoint
    Then workgraph appends the checkpoint through the existing checkpoint contract
    And launches the destination with that normalized checkpoint
    And permits only this handoff to cross the wrapped-session nesting guard

  Scenario: Continue from an ended source
    Given the source AI session has ended with a durable structured checkpoint
    When I select it with "--from <session-id>"
    Then workgraph reuses its latest checkpoint without appending to the ended session
    And launches a linked destination from its last observed working directory

  Scenario: Fail without misleading lineage
    Given the source session is missing, unsupported, lacks a checkpoint, or conflicts with the injected session
    Or the destination is unsupported or cannot be launched
    When I request a handoff
    Then workgraph reports an actionable error
    And no destination session or predecessor lineage is recorded

  Scenario: Reject stale compatibility fallback
    Given a source has valid handoff context followed by newer unsupported checkpoint or repository evidence
    When I request a handoff
    Then workgraph rejects the source before launch
    And does not silently transport the older compatible projection

  Scenario: Preserve a checkpoint when later launch fails
    Given an active source accepts a fresh checkpoint during handoff
    And the destination cannot be launched
    When I request a handoff with "--stdin"
    Then the source checkpoint remains durable
    And no destination session or predecessor lineage is recorded

  Scenario: Hand work to the same supported agent
    Given a supported AI session has a durable checkpoint
    When I hand it to the same AI tool
    Then workgraph starts a new linked workgraph lifetime with the compact continuation context
    And does not resume or copy the source provider conversation
