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
  And the daemon can discover IM and MPIM conversations
  And direct messages are not collected without that opt-in

Scenario: Configure Slack direct messages after connecting
  Given Slack is already connected
  When I run "workgraph slack configure --include-dms"
  Then workgraph stores the direct-message opt-in locally
  And the output explains that DMs and group DMs are enabled

Scenario: Connect Slack with OAuth
  Given workgraph has been initialized
  When I run "workgraph slack connect"
  Then workgraph opens Slack in the browser for authorization
  And approving access stores local Slack connector settings
  And the daemon can discover visible Slack channels without repeated token flags
