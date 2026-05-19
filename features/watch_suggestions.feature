Feature: Watch root suggestions

Scenario: Suggest watching a directory observed by another signal
  Given WorkGraph receives git activity from a local repository
  And the repository is not already in watch_dirs
  When WorkGraph processes the activity
  Then WorkGraph creates a pending suggestion to watch that repository
  And WorkGraph does not update config without approval

Scenario: Approve a suggested watch root
  Given WorkGraph has a pending watch-root suggestion
  When I approve the suggestion
  Then the suggested directory is added to the front of watch_dirs
  And the suggestion is marked accepted

Scenario: Coalesce duplicate watch suggestions
  Given WorkGraph already suggested watching a local directory
  When another signal observes activity in that same directory
  Then WorkGraph updates the existing suggestion
  And WorkGraph does not create a duplicate suggestion
