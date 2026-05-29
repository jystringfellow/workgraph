Feature: Slack integration

Scenario: Capture Slack channel messages from an export
  Given workgraph has been initialized
  And a Slack event export contains a channel message
  When I run "workgraph slack capture --events-file slack-events.json"
  Then workgraph stores a Slack message event
  And the event includes channel, actor, text, and permalink evidence

Scenario: Capture Slack thread replies from an export
  Given workgraph has been initialized
  And a Slack event export contains a thread reply
  When I run "workgraph slack capture --events-file slack-events.json"
  Then workgraph stores a Slack thread reply event
  And recapturing the export does not duplicate the event

Scenario: Daemon collects configured Slack messages
  Given workgraph has been initialized
  And Slack access is configured for a channel
  When I run "workgraph run"
  Then workgraph periodically collects new Slack messages from that channel
  And workgraph stores available thread replies
  And workgraph does not post or react in Slack

Scenario: Opt into Slack direct messages
  Given workgraph has been initialized
  When I run "workgraph slack connect --include-dms"
  Then workgraph requests Slack direct-message history scopes
  And the output explains how to disconnect before reconnecting without DM scopes
  And the daemon can discover IM and MPIM conversations
  And direct messages are not collected without that opt-in

Scenario: Disconnect Slack
  Given Slack is already connected with direct-message scopes
  When I run "workgraph slack disconnect"
  Then workgraph revokes the stored Slack token
  And workgraph removes local Slack connector settings
  And a running background daemon is restarted to stop Slack polling

Scenario: Connect Slack with OAuth
  Given workgraph has been initialized
  When I run "workgraph slack connect"
  Then workgraph opens Slack in the browser for authorization
  And approving access stores local Slack connector settings
  And the output explains that enabling DMs later requires disconnecting and reconnecting
  And the daemon can discover visible Slack channels without repeated token flags
