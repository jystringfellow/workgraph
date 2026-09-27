Feature: Capture survives transient filesystem activity
  Scenario: A download disappears while the watcher inspects it
    Given capture is running on a directory
    When transient files are created and immediately removed
    Then capture remains running
    And subsequent durable file activity is stored
