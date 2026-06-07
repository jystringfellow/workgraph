Feature: Notion connection

Scenario: Capture shared Notion pages and databases
  Given Notion is connected
  When I run "workgraph notion capture"
  Then workgraph searches Notion for pages and databases shared with the connection
  And workgraph stores notion.page and notion.database records
  And the records preserve object id, title, URL, created time, last edited time, and parent metadata
  And recapturing the same page or database updates the existing event instead of creating duplicates

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
