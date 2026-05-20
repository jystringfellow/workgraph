Feature: Ignore suggestions

Scenario: Suggest ignoring a noisy tracked path
  Given WorkGraph is watching a local directory
  And many file events occur under one generated-looking child path
  When WorkGraph evaluates recent activity
  Then WorkGraph creates a pending suggestion to ignore that path
  And WorkGraph does not update config without approval

Scenario: Suggest ignoring a noisy recurring basename
  Given WorkGraph is watching local code folders
  And many file events occur under directories with the same generated basename
  When WorkGraph evaluates recent activity
  Then WorkGraph creates a pending suggestion to ignore that basename
  And WorkGraph does not update config without approval

Scenario: Approve an ignore path suggestion
  Given WorkGraph has a pending ignore-path suggestion
  When I approve the suggestion
  Then the suggested path is added to ignore_paths
  And the suggestion is marked accepted

Scenario: Approve an ignore name suggestion
  Given WorkGraph has a pending ignore-name suggestion
  When I approve the suggestion
  Then the suggested name is added to ignore_names
  And the suggestion is marked accepted

Scenario: Coalesce duplicate ignore suggestions
  Given WorkGraph already suggested ignoring a noisy path or name
  When more matching noisy activity occurs
  Then WorkGraph updates the existing suggestion
  And WorkGraph does not create a duplicate suggestion
