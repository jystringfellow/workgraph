Feature: Connector runtime

Scenario: Connected providers are capture-ready
  Given workgraph has connected Slack, calendar, mail, and Notion accounts
  When I run "workgraph start"
  Then workgraph polls enabled connected providers at visible intervals
  And manual capture commands remain available for imports and debugging

Scenario: Inspect connector polling
  Given workgraph has enabled connectors
  When I run "workgraph connectors list"
  Then workgraph shows each connector id, enabled state, polling interval, and last poll result

Scenario: Change connector polling without disconnecting
  Given Notion is connected
  When I run "workgraph connectors disable notion"
  Then workgraph stops polling Notion
  And the Notion account remains connected for later re-enabling

Scenario: Change connector interval
  Given Notion is connected
  When I run "workgraph connectors interval notion 30m"
  Then workgraph stores the Notion polling interval without changing Notion credentials
