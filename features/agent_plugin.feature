Feature: Agent plugin installation
  As a workgraph user with an approved AI coding client
  I want one workgraph plugin containing its local integration skills
  So that I can start using workgraph without assembling MCP and skill files

  Scenario: Install the workgraph plugin for a supported client
    Given Codex, Claude Code, or GitHub Copilot CLI is installed
    When I install the workgraph plugin for that client
    Then workgraph registers one local plugin named workgraph
    And the plugin contains the bridge, memory, and AI checkpoint skills
    And the plugin registers the local workgraph MCP
    And unrelated client settings are preserved
    And Claude Code pre-approves only the workgraph MCP tools required to drain capture
    And optional provider tools require exact-name opt-in
    And Claude Code trusts the workgraph home without replacing unrelated trust settings
    And the unattended worker discovers project settings without a settings override
    And the launchd worker receives an explicit non-secret HOME and executable PATH
    And install output says to restart the daemon after an executable upgrade
    And install output says to start a new client session for the MCP server

  Scenario: Grant Copilot drain permissions per invocation
    Given the workgraph plugin is installed for GitHub Copilot CLI
    And I approved exact provider tools in server(tool) form
    When the unattended worker drains capture requests
    Then Copilot is allowed only the workgraph drain tools and the approved provider tools
    And Copilot can see no other tool except the skill loader
    And Copilot is never allowed shell, file write, or all tools
    And saved Copilot approvals, COPILOT_ALLOW_ALL, and custom instruction directories do not widen the drain
    When a stored provider tool is not an exact provider server(tool) name
    Then the drain fails without launching Copilot
    When no provider tool has been approved
    Then capture requests remain pending

  Scenario: Replace approved Copilot provider tools
    Given Copilot provider tools were approved earlier
    When I run "workgraph plugin install --client copilot --clear-provider-tools --allow-provider-tool 'provider(exact_read_tool)'"
    Then only the newly named provider tool remains approved

  Scenario: Reinstall the Copilot plugin from a different install root
    Given the Copilot workgraph marketplace was registered from an earlier install root
    When I install the Copilot plugin into a new install root
    Then Copilot's workgraph marketplace points at the new install root

  Scenario: Update an existing plugin installation
    Given the workgraph plugin was already installed
    When I run the same plugin install command again
    Then the canonical plugin files and skills are refreshed
    And the operation succeeds without duplicate configuration
    And an already loaded launchd worker is reloaded
    And the regenerated worker environment preserves provider executable discovery

  Scenario: Persist an unattended worker model
    Given I install the workgraph plugin with a model selected for one client
    When I reinstall the plugin without a model flag
    Then workgraph preserves that client's model in durable worker configuration
    And bridge drain passes the selected model to the client
    And plugin doctor reports the selected model
    And the generated launchd file is not treated as model configuration
    When I reinstall with --clear-model
    Then bridge drain returns to the client's default model

  Scenario: Diagnose the plugin locally
    Given the workgraph plugin is installed
    When I run plugin doctor
    Then workgraph verifies every bundled skill and the local MCP contract
    And it completes a disposable bridge lifecycle check
    And it verifies the installed launchd environment when a worker is installed
    And it does not contact a provider

  Scenario: Preserve bridge command compatibility
    Given an existing setup uses the bridge install commands
    When I install or diagnose through those commands
    Then workgraph operates on the same broader workgraph plugin

  Scenario: Keep model execution separate
    Given the workgraph plugin is installed
    Then no LLM profile or hosted-model consent is enabled automatically
    And client-backed LLM setup remains available through llm connect
