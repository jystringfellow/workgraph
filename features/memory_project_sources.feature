Feature: Explicit project evidence mappings
  Scenario: A thematic project spans captured repositories
    Given memory exists for MMO Harness
    And events were captured for two repositories
    When I explicitly link both repositories to MMO Harness
    Then suggest and resume include both repositories
    And promotion accepts their evidence
    And authored memory and captured project identifiers are preserved
  Scenario: Remove a mapping
    When I unlink a captured repository
    Then its events no longer appear as candidate evidence
    And previously promoted evidence remains linked
