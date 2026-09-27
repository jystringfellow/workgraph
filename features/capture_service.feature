Feature: Opt-in supervised capture
  Scenario: Install a user service
    Given workgraph is initialized and capture is stopped
    When I install the capture service
    Then login starts the daemon and failures are restarted
    And the service runs the daemon worker with the selected home
  Scenario: Explicit stop and removal
    Given a capture service is installed
    When I stop capture
    Then the supervisor stops capture and disables login startup
    When I uninstall the service
    Then its definition is removed and captured data is preserved
