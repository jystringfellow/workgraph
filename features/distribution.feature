Feature: Inspectable workgraph distribution

  Scenario: Find a Go-installed workgraph executable
    Given I installed workgraph with the Go toolchain
    When I follow the documented shell setup
    Then the Go binary directory is added to PATH instead of executed
    And I can verify the executable with "command -v workgraph"

  Scenario: Inspect the installed version locally
    Given workgraph was built from a checkout or a tagged release
    When I run "workgraph version"
    Then workgraph prints its version, commit, and build time
    And the command does not contact a network service or print a local path

  Scenario: Publish versioned release archives
    Given a trusted maintainer pushes a valid semantic version tag
    When the release workflow runs
    Then workgraph passes vet and the full Go suite before publishing
    Then native runners build macOS, Linux, and Windows archives
    And release metadata is injected into "workgraph version"
    And workgraph publishes SHA-256 checksums for every archive

  Scenario: Prepare a Homebrew upgrade path
    Given release archives and their checksums exist
    When the release workflow renders the Homebrew formula
    Then the formula pins the archive checksum for each supported Mac and Linux architecture
    And the formula verifies the installed version
    And tap publication occurs only when its separate write token is configured

  Scenario: Keep executable replacement explicit
    Given workgraph has a newer release
    When a user chooses to upgrade
    Then the user invokes Go install or their package manager explicitly
    And workgraph does not silently check for or install an update
