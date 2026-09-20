Feature: Notion connection

Scenario: Capture shared Notion pages and databases
  Given Notion is connected
  When I run "workgraph notion capture"
  Then workgraph searches Notion for pages and databases shared with the connection
  And workgraph stores notion.page and notion.database records
  And the records preserve object id, title, URL, created time, last edited time, and parent metadata
  And each record attributes the last editor when Notion provides one
  And each inventory record is explicitly classified with no direct involvement
  And nested records share the stable page or database root below the workspace as their project
  And recapturing the same page or database updates the existing event instead of creating duplicates

Scenario: Store personal Notion edits separately from workspace inventory
  Given a previously indexed Notion object was edited by the connected user
  When I run "workgraph notion capture"
  Then workgraph stores a derived activity event with edited involvement
  And the workspace inventory record remains ambient evidence

Scenario: Notion capture bootstraps a cursor and resumes incrementally
  Given Notion is connected
  When I run "workgraph notion capture"
  Then the search request is sorted descending by last edited time
  And workgraph writes each fetched page's objects immediately instead of waiting for full pagination
  And workgraph stores a notion capture cursor after a complete run
  When a later capture only needs to catch up since the stored cursor
  Then pagination stops once it reaches an object older than the cursor
  And the work is bounded by elapsed time rather than total workspace size

Scenario: A failed Notion capture leaves partial progress instead of nothing
  Given Notion is connected
  When "workgraph notion capture" fails partway through pagination
  Then objects from already-fetched pages remain stored in notion_index and events
  And the capture cursor advances through the last fully processed page

Scenario: Connect Notion with OAuth
  Given workgraph has been initialized
  When I run "workgraph notion connect"
  Then workgraph opens Notion OAuth and stores local Notion connector settings after approval
  And workgraph does not start OAuth again when Notion is already connected
  And the OAuth request targets Notion's public connection authorization endpoint without PKCE
  And the OAuth code exchange goes through the workgraph Notion token relay
  When I run "workgraph notion connect --no-browser"
  Then workgraph prints a Notion OAuth authorization URL
  And workgraph does not store local Notion connector settings yet
  When I rerun "workgraph notion connect" with the OAuth code and matching state
  Then workgraph stores local Notion connector settings

Scenario: Disconnect Notion
  Given Notion is already connected
  When I run "workgraph notion disconnect"
  Then workgraph removes local Notion connector settings
  And the output explains Notion access must be revoked from Notion workspace connection settings
  And disconnect succeeds when Notion is already disconnected

Scenario: Exchange Notion OAuth tokens through a Worker relay
  Given workgraph has a public Notion connection
  When the local CLI exchanges a Notion OAuth authorization code
  Then workgraph sends the token request through the Notion token relay
  And the relay injects the Notion client secret from Cloudflare secrets
  And the relay sends Notion a JSON token request using HTTP Basic authentication
  And the relay does not log OAuth codes or tokens
  And the same relay supports refresh-token exchange
