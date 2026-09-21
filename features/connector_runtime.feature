Feature: Connector runtime

Scenario: Connected providers are capture-ready
  Given workgraph has connected Slack, Slack Lists, calendar, mail, Notion, and Azure Boards accounts
  When I run "workgraph start"
  Then workgraph polls enabled connected providers at visible intervals
  And manual capture commands remain available for imports and debugging

Scenario: Inspect connector polling
  Given workgraph has enabled connectors
  When I run "workgraph connectors list"
  Then workgraph shows each connector id, enabled state, polling interval, and last poll result
  And each connector reports its supported capture modes and normalized event source
  And each bridgeable connector reports its capture semantics

Scenario: Register a connector once
  Given workgraph has a canonical connector registry
  When a connector is registered as bridgeable
  Then connector status and bridged scheduling use that same registry entry
  And no independent bridged connector id list can omit it

Scenario: Declare complete provider requirements once
  Given workgraph has a canonical connector registry
  When a connector is registered as bridgeable
  Then it declares provider operations for both fetching and stable identity
  And "workgraph connectors required-tools" renders those declarations
  And the unattended bridge reads the same declarations before claiming work
  And a missing provider operation leaves the request pending

Scenario: Change connector polling without disconnecting
  Given Notion is connected
  When I run "workgraph connectors disable notion"
  Then workgraph stops polling Notion
  And the Notion account remains connected for later re-enabling

Scenario: Change connector interval
  Given Notion is connected
  When I run "workgraph connectors interval notion 30m"
  Then workgraph stores the Notion polling interval without changing Notion credentials

Scenario: Isolate a failed connector poll
  Given an enabled connector fails while workgraph capture is running
  When the connector records its poll error
  Then workgraph keeps filesystem capture running
  And workgraph logs the connector error
  And "workgraph status" shows the degraded connector and its latest error

Scenario: Bound a stalled connector independently
  Given an enabled connector stops responding
  When workgraph reaches that connector's poll deadline
  Then filesystem events and other connectors continue while it is stalled
  And workgraph records a timeout and a capped retry time for that connector

Scenario: Poll ready connectors at startup
  Given workgraph has enabled capture-ready connectors
  When I run "workgraph start"
  Then each connector polls immediately without waiting for its normal interval
  And successful polls record last-success and next-poll timestamps

Scenario: Stop retrying a connector that needs authentication
  Given one enabled connector rejects its stored credentials
  When its immediate poll returns an authentication error
  Then workgraph marks only that connector as needing reconnection
  And workgraph does not retry it until its setup is repaired

Scenario: Validate Azure CLI authentication with a real token request
  Given Azure Boards bridged capture uses Azure CLI authentication
  When I connect it or run "workgraph connectors doctor"
  Then workgraph runs "az account get-access-token"
  And a cached profile with an expired refresh token is reported as unhealthy
  And a successful token request remains valid even if login reported no subscriptions
