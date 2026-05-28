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
