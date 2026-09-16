Feature: Bridged connector capture
  As a user whose provider access is approved through an AI client
  I want workgraph to schedule capture through that client
  So that my local work context stays current without workgraph owning provider credentials

  Background:
    Given workgraph has been initialized locally

  Scenario: Configure a remote connector for bridged capture
    When I connect a supported remote connector in bridged mode with its approved non-secret scope
    Then workgraph enables it without requesting or storing provider credentials
    And missing or malformed required scope is rejected without changing connector state
    And status and doctor identify legacy bridged connectors that need scope
    And changing scope cancels active work created for the prior scope
    And direct credentials already stored for that connector are preserved
    And direct-only git and Notion cannot be configured or ingested as bridged

  Scenario: Emit durable work at the configured cadence
    Given a bridged connector is due
    When the workgraph daemon schedules connector capture
    Then workgraph makes no provider call
    And it emits one pending capture request with bounded scope
    And another scheduler pass coalesces while that request is active
    And emission alone does not report capture success

  Scenario: Claim bridged work safely
    Given a bridged capture request is pending
    When two bridge workers try to claim it
    Then only one worker receives a claim token
    And each claimant receives the canonical non-secret scope
    And status never exposes the claim token
    And an expired claim is retried with its original request bounds

  Scenario: Complete one bridged capture transaction
    Given an approved bridge worker has claimed a request
    When it ingests a valid normalized event batch
    Then workgraph atomically stores new events and required projections
    And it marks the request completed
    And it advances the connector cursor to the request until time
    And it schedules the next request from completion time

  Scenario: Reject invalid bridged capture atomically
    Given an approved bridge worker has claimed a request
    When it submits malformed data, a mismatched source, or a stale claim token
    Then workgraph stores no partial events or projections
    And it does not complete the request or advance its cursor

  Scenario: Deduplicate overlapping capture windows
    Given a bridged event has a revision-aware external id
    When the same event is ingested more than once
    Then exactly one event row exists for that source and external id
    And a mutable provider revision receives a distinct event id

  Scenario: Keep event time separate from capture progress
    Given a calendar request returns an event whose start is in the future
    When the request completes
    Then the event timestamp remains the calendar start in UTC
    And the capture cursor advances only to the request until time

  Scenario: Reject an incomplete provider bridge
    Given the reference Notion client cannot paginate search to exhaustion
    When I try to configure Notion for bridged capture
    Then workgraph rejects it as direct-only
    And status and doctor flag any legacy bridged Notion configuration as unsupported
    And the message points to direct Notion setup

  Scenario: Guide setup through a supported AI client
    Given Claude Code or Codex has approved provider connectors on macOS
    When I ask it to set up workgraph bridges
    Then it proposes available connectors, bounded scopes, and cadences
    And it changes no configuration before I approve
    And it verifies the first local capture without workgraph-managed provider OAuth

  Scenario: Install both reference integrations
    When I install the workgraph plugin for Claude Code or Codex on macOS
    Then workgraph idempotently registers the same local MCP contract and canonical skills
    And it preserves unrelated client settings
    And Claude Code receives only the unattended workgraph MCP permissions needed to drain capture
    And Claude Code trusts the workgraph home while preserving unrelated user configuration
    And the Claude worker discovers its project settings without overriding the normal settings chain
    And provider read tools are added only through explicit exact-name opt-in
    And reinstall reloads an existing launchd worker idempotently
    And bridge doctor verifies local operation without contacting a provider

  Scenario: Return MCP collections in valid structured content
    When a client lists or claims bridge requests or inspects connector status over MCP
    Then the successful structured content is an object
    And requests, claims, and connectors are returned in named collection fields

  Scenario: Skip work that the unattended client cannot execute
    Given a healthy connector has pending bridged work
    And the unattended client lacks an authorized provider read tool for it
    When the bridge worker inspects available work
    Then it leaves that request pending without claiming it
    And it does not overwrite the connector's prior success with a failure
    And it does not fall back to a CLI claim file

  Scenario: Observe Slack List rows without provider revisions
    Given a bridged Slack List exposes stable row values but no item revision
    When the bridge normalizes the current snapshot
    Then canonical row content hashes may identify changed observations
    And the contract does not claim that an absent row is a deletion
