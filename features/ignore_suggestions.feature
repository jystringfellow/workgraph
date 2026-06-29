Feature: Ignore suggestions

Scenario: Suggest ignoring a noisy tracked path
  Given workgraph is watching a local directory
  And many file events occur under one generated-looking child path
  When workgraph evaluates recent activity
  Then workgraph creates a pending suggestion to ignore that path
  And workgraph does not update config without approval

Scenario: Suggest ignoring a noisy recurring basename
  Given workgraph is watching local code folders
  And many file events occur under directories with the same generated basename
  When workgraph evaluates recent activity
  Then workgraph creates a pending suggestion to ignore that basename
  And workgraph does not update config without approval

Scenario: Approve an ignore path suggestion
  Given workgraph has a pending ignore-path suggestion
  When I approve the suggestion
  Then the suggested path is added to ignore_paths
  And the suggestion is marked accepted

Scenario: Approve an ignore name suggestion
  Given workgraph has a pending ignore-name suggestion
  When I approve the suggestion
  Then the suggested name is added to ignore_names
  And the suggestion is marked accepted

Scenario: Coalesce duplicate ignore suggestions
  Given workgraph already suggested ignoring a noisy path or name
  When more matching noisy activity occurs
  Then workgraph updates the existing suggestion
  And workgraph does not create a duplicate suggestion

Scenario: Snoozed suggestion resurfaces after snooze window expires
  Given workgraph has a snoozed ignore suggestion with a past until_at suppression
  When I list suggestions
  Then the suggestion appears as proposed

Scenario: Snoozed suggestion stays hidden before its snooze window expires
  Given workgraph has a snoozed ignore suggestion with a future until_at suppression
  When I list suggestions
  Then the suggestion remains snoozed

Scenario: Suggestion list includes an evidence summary
  Given workgraph has a pending ignore suggestion with event and path evidence
  When I list suggestions
  Then the output includes the event count and path count from the evidence

Scenario: Suggestion show includes full evidence detail
  Given workgraph has a pending suggestion with evidence
  When I run "workgraph suggestions show <id>"
  Then the output includes all cited event ids
  And the output includes all cited paths
