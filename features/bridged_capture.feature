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
    And bridged-only notion.activity cannot be configured or polled as direct

  Scenario: Emit durable work at the configured cadence
    Given a bridged connector is due
    When the workgraph daemon schedules connector capture
    Then workgraph makes no provider call
    And it emits one pending capture request with declared capture semantics
    And another scheduler pass coalesces while that request is active
    And emission alone does not report capture success

  Scenario: Claim bridged work safely
    Given a bridged capture request is pending
    When two bridge workers try to claim it
    Then only one worker receives a claim token
    And each claimant receives the canonical non-secret scope
    And status never exposes the claim token
    And an expired claim is retried with its original request bounds

  Scenario: Preserve a worker failure after its lease expires
    Given a bridge worker still holds the matching token for an expired claim
    When it reports why the request failed
    Then workgraph records the bounded error and releases the claim for retry
    And a different or previously reaped token remains invalid

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
    And capture and connector status results report running and on-disk MCP builds
    And a client session whose executable changed reports that its MCP server is stale

  Scenario: Skip work that the unattended client cannot execute
    Given a healthy connector has pending bridged work
    And the unattended client lacks an authorized provider read tool for it
    When the bridge worker inspects available work
    Then it reads that connector's fetch and identity requirements from the canonical registry
    And it leaves that request pending without claiming it
    And it does not overwrite the connector's prior success with a failure
    And it does not fall back to a CLI claim file

  Scenario: Observe Slack List rows without provider revisions
    Given a bridged Slack List is available as a complete CSV without item ids or row timestamps
    When the worker reads the configured List with slack_read_file and submits its raw CSV
    Then the request declares complete-snapshot semantics
    And workgraph parses quoted CSV fields into complete rows server-side
    And workgraph derives stable row keys, normalized Done state, observation time, and canonical content-hash revisions
    And unchanged observations deduplicate while changed Done state creates a new revision
    And duplicate or missing row keys reject the entire snapshot
    And the contract does not claim that an absent row is completed or deleted

  Scenario: Interpret bridged Slack Lists per List
    Given bridged Slack Lists use different state and identity columns
    When the worker submits each complete List snapshot
    Then workgraph applies state, interest columns, and row keys per List
    And all CSV fields and completed rows remain captured evidence
    And Lists without state configuration leave completion unknown

  Scenario: Apply provider-specific recipe correctness
    Given a bridged request needs secondary identity data or client-side filtering
    When the worker preflights and executes the provider recipe
    Then Microsoft calendar converts Pacific Standard Time occurrences with DST-aware rules
    And Microsoft calendar preserves provider-local start and end values in the normalized payload
    And Microsoft calendar verifies the resource read needed for its revision marker
    And Microsoft mail pads provider filters in mailbox-local time and filters exact UTC bounds client-side
    And Microsoft mail follows contiguous offset pagination until results pass the lower bound
    And Microsoft mail rejects non-contiguous or truncated pagination
    And Slack threads use detailed output and filter replies against exact bounds after reading
    And Azure Boards supplies an accessible project as routing context for collection-wide participant WIQL

  Scenario: Prove an empty bounded bridge capture
    Given a bounded bridged request has no matching provider events
    When the worker submits an empty capture
    Then workgraph accepts it only with an exhaustive query or passed control-query proof
    And workgraph rejects a bare empty array as unproven

  Scenario: Bridge Notion participant activity that the public API cannot express
    Given the public Notion API cannot filter search by editor or creator workspace-wide
    When I configure notion.activity with participant scope and edited/created includes
    Then workgraph rejects notion.activity in direct mode with an explanatory message
    And the daemon schedules notion.activity capture requests at its configured cadence
    And the worker queries Notion MCP edited_by_user_ids and created_by_user_ids for the identity
    And a date-only provider filter is padded to the enclosing day and filtered client-side to exact bounds
    And a response of exactly 50 results is treated as truncated and reported as a failure, never ingested
    And emitted events reuse the direct notion connector's page_updated/database_updated types and external id shape
    And overlapping capture between notion and notion.activity deduplicates instead of double-counting
