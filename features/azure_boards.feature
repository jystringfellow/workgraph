Feature: Azure Boards integration

Scenario: Connect Azure Boards with Microsoft OAuth
  Given workgraph has been initialized
  When I run "workgraph azure boards connect --organization acme --project Work --team Platform --area-path Work\Platform"
  Then workgraph uses Microsoft OAuth with PKCE
  And approving access stores local Azure Boards connector settings
  And the settings include organization, project, team, and area path filters
  And Azure Boards is enabled for shared connector polling

Scenario: Capture Azure Boards work items from a WIQL query
  Given workgraph has Azure Boards organization, project, and read credentials
  And workgraph has a configured WIQL query for active work
  When I run "workgraph azure boards capture"
  Then workgraph runs the WIQL query to select work item ids
  And workgraph fetches full work item details for those ids
  And workgraph stores read-only "azure_boards.work_item" events
  And workgraph preserves id, URL, title, state, type, assigned user, changed date, tags, iteration, area, priority, and raw fields
  And workgraph does not change Azure Boards work items

Scenario: Daemon captures connected Azure Boards work items
  Given Azure Boards is connected with organization and project settings
  When I run "workgraph start"
  Then workgraph periodically captures matching Azure Boards work items
  And workgraph reports "azure.boards" in monitored connectors
  And workgraph does not change Azure Boards work items
