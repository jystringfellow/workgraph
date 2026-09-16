Feature: Agent plugin installation
  As a workgraph user with an approved AI coding client
  I want one workgraph plugin containing its local integration skills
  So that I can start using workgraph without assembling MCP and skill files

  Scenario: Install the workgraph plugin for a supported client
    Given Codex or Claude Code is installed
    When I install the workgraph plugin for that client
    Then workgraph registers one local plugin named workgraph
    And the plugin contains the bridge, memory, and AI checkpoint skills
    And the plugin registers the local workgraph MCP
    And unrelated client settings are preserved
    And Claude Code pre-approves only the workgraph MCP tools required to drain capture

  Scenario: Update an existing plugin installation
    Given the workgraph plugin was already installed
    When I run the same plugin install command again
    Then the canonical plugin files and skills are refreshed
    And the operation succeeds without duplicate configuration

  Scenario: Diagnose the plugin locally
    Given the workgraph plugin is installed
    When I run plugin doctor
    Then workgraph verifies every bundled skill and the local MCP contract
    And it completes a disposable bridge lifecycle check
    And it does not contact a provider

  Scenario: Preserve bridge command compatibility
    Given an existing setup uses the bridge install commands
    When I install or diagnose through those commands
    Then workgraph operates on the same broader workgraph plugin

  Scenario: Keep model execution separate
    Given the workgraph plugin is installed
    Then no LLM profile or hosted-model consent is enabled automatically
    And client-backed LLM setup remains available through llm connect
