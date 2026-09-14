Feature: Bridged connector capture
  As a user whose provider access is approved through an AI client
  I want workgraph to schedule capture through that client
  So that my local work context stays current without workgraph owning provider credentials

  Background:
    Given workgraph has been initialized locally

  Scenario: Configure a remote connector for bridged capture
    When I connect a supported remote connector in bridged mode
    Then workgraph enables it without requesting or storing provider credentials
    And direct credentials already stored for that connector are preserved
    And git cannot be configured or ingested as bridged

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

  Scenario: Project bridged Notion pages locally
    Given a bridge returns a bounded normalized Notion page
    When workgraph ingests the page
    Then the event and notion index snapshot commit together
    And an older revision cannot overwrite a newer snapshot
    And no Notion network request is made by workgraph

  Scenario: Guide setup through a supported AI client
    Given Claude Code or Codex has approved provider connectors on macOS
    When I ask it to set up workgraph bridges
    Then it proposes available connectors, bounded scopes, and cadences
    And it changes no configuration before I approve
    And it verifies the first local capture without workgraph-managed provider OAuth

  Scenario: Install both reference integrations
    When I install the Claude Code or Codex bridge integration on macOS
    Then workgraph idempotently registers the same local MCP contract and shared skill
    And it preserves unrelated client settings
    And bridge doctor verifies local operation without contacting a provider

