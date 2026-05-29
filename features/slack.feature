Feature: Slack integration

Scenario: Capture Slack channel messages from an export
  Given WorkGraph has been initialized
  And a Slack event export contains a channel message
  When I run "workgraph slack capture --events-file slack-events.json"
  Then WorkGraph stores a Slack message event
  And the event includes channel, actor, text, and permalink evidence

Scenario: Capture Slack thread replies from an export
  Given WorkGraph has been initialized
  And a Slack event export contains a thread reply
  When I run "workgraph slack capture --events-file slack-events.json"
  Then WorkGraph stores a Slack thread reply event
  And recapturing the export does not duplicate the event

Scenario: Daemon collects configured Slack messages
  Given WorkGraph has been initialized
  And Slack access is configured for a channel
  When I run "workgraph run"
  Then WorkGraph periodically collects new Slack messages from that channel
  And WorkGraph stores available thread replies
  And WorkGraph does not post or react in Slack

Scenario: Opt into Slack direct messages
  Given WorkGraph has been initialized
  When I run "workgraph slack connect --include-dms"
  Then WorkGraph requests Slack direct-message history scopes
  And the daemon can discover IM and MPIM conversations
  And direct messages are not collected without that opt-in

Scenario: Configure Slack direct messages after connecting
  Given Slack is already connected
  When I run "workgraph slack configure --include-dms"
  Then WorkGraph stores the direct-message opt-in locally
  And the output explains that DMs and group DMs are enabled

Scenario: Connect Slack with OAuth
  Given WorkGraph has been initialized
  When I run "workgraph slack connect"
  Then WorkGraph opens Slack in the browser for authorization
  And approving access stores local Slack connector settings
  And the daemon can discover visible Slack channels without repeated token flags
