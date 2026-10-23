Feature: LLM integration

Scenario: Configure named LLM profiles
  Given workgraph has been initialized
  When I run "workgraph llm add local-gemma --provider openai-compatible --base-url http://localhost:11434/v1 --model gemma-4-12b"
  Then workgraph stores a local LLM profile named "local-gemma"
  And the LLM config file is local-user-only
  And no API key is required for the local OpenAI-compatible profile
  When I run "workgraph llm list"
  Then the output includes "local-gemma", "openai-compatible", and "gemma-4-12b"

Scenario: Route LLM tasks to profiles
  Given a local LLM profile named "local-gemma"
  And a Bedrock profile named "bedrock-work"
  When I run "workgraph llm use local-gemma"
  Then "local-gemma" becomes the default profile
  When I run "workgraph llm use bedrock-work --for associate"
  Then association tasks use "bedrock-work"
  And summarize tasks still use the default profile unless explicitly configured

Scenario: Tune a profile's auto-apply threshold
  Given a local LLM profile named "local-gemma"
  When I run "workgraph llm set local-gemma --auto-apply-confidence 0.9 --verify-floor 0.5"
  Then the profile keeps its provider and model
  And "workgraph llm list" shows "auto-apply 0.90, verify-floor 0.50"
  When I run "workgraph llm set local-gemma --auto-apply-confidence 0.4"
  Then workgraph refuses the value and the profile is unchanged

Scenario: Test an OpenAI-compatible LLM profile
  Given workgraph has an OpenAI-compatible LLM profile
  When I run "workgraph llm test --profile local-gemma"
  Then workgraph sends a minimal chat completion request to the configured base URL
  And the output includes the selected profile, provider, model, destination, and response text
  And no connector data or memory content is sent for the test command

Scenario: Use a Bedrock inference profile for LLM tasks
  Given workgraph has a Bedrock LLM profile with an inference profile ARN
  When I run "workgraph llm test --profile bedrock-work"
  Then workgraph calls Bedrock Runtime Converse with the configured ARN as the model id
  And the output includes the selected profile, provider, ARN, destination, and response text
  When I route summarize tasks to "bedrock-work"
  Then "workgraph llm summarize today" uses the Bedrock profile for generation

Scenario: Connect signed-in AI clients as separate LLM profiles
  Given Codex and Claude Code are installed and signed in
  When I run "workgraph llm connect codex --for summarize"
  And I run "workgraph llm connect claude-code --for categorize"
  Then workgraph stores separate ai-client profiles without storing login tokens
  And summarize tasks use Codex
  And future categorize tasks use Claude Code
  And hosted LLM consent is still required before captured context is sent

Scenario: Bind a signed-in LLM profile to one client account context
  Given Claude Code has a dedicated configuration directory for a work organization
  When I run "workgraph llm connect claude-code --config-dir <work-config-dir> --for summarize"
  Then workgraph stores the absolute non-secret config directory with that profile
  And list and doctor report the stored config directory
  When workgraph invokes the profile
  Then it sets CLAUDE_CONFIG_DIR to the stored directory regardless of the ambient shell
  And it stores no client login token

Scenario: Summarize through a signed-in AI client
  Given workgraph has captured local events today
  And Codex or Claude Code is selected for summarize tasks
  And hosted LLM use is enabled
  When I run "workgraph llm summarize today"
  Then workgraph sends the filtered focused prompt to the client through standard input
  And the client runs non-persistently without provider connector access
  And workgraph prints only the validated final model response
  And workgraph stores no client login token

Scenario: Diagnose a signed-in AI client without sending data
  Given workgraph has a Codex or Claude Code LLM profile
  When I run "workgraph llm doctor"
  Then workgraph reports whether the registered client executable is available
  And workgraph does not invoke the model

Scenario: Restrict signed-in AI clients with managed settings
  Given managed settings allow the ai-client provider and only the Codex client
  When I connect Codex as an LLM profile
  Then the profile is accepted
  When I connect Claude Code as an LLM profile
  Then workgraph rejects it before invoking the client

Scenario: Preview today's LLM summary without sending data
  Given workgraph has captured local events today
  And workgraph has a configured LLM profile for summarize tasks
  When I run "workgraph llm summarize today --dry-run"
  Then workgraph prints the selected profile and request destination
  And workgraph prints the focused context and prompt that would be sent
  And workgraph does not call the LLM provider
  And workgraph does not write memory files

Scenario: Stream today's LLM summary
  Given workgraph has captured local events today
  And workgraph has a configured LLM profile for summarize tasks
  When I run "workgraph llm summarize today"
  Then workgraph prints the selected profile and request destination
  And workgraph shows that it is thinking before the first model chunk arrives
  And workgraph streams summary text as the provider returns it
  When I run "workgraph llm summarize today --no-stream"
  Then workgraph prints the complete summary after the provider call finishes

Scenario: Future LLM association suggestions stay reviewable
  Given workgraph has related Slack, calendar, GitHub, Notion, mail, and local file events
  When workgraph asks an LLM to associate candidate work items
  Then the LLM output is stored as pending association suggestions
  And each suggestion includes grounded reasons and source event ids
  And workgraph does not mutate events or memory without approval

Scenario: Configure a System One decision profile
  Given workgraph has been initialized
  When I run "workgraph llm add jev --provider system-one --base-url https://api.typesafe.ai --model jev-latest --api-key-env TYPESAFE_API_KEY"
  Then workgraph stores a system-one profile without the API key
  And "workgraph llm list" and "workgraph llm doctor" show the provider, model, and base URL host
  And neither prints the API key or its variable name
  When I add a system-one profile without --base-url
  Then workgraph refuses it
  When I add a system-one profile with --execution bridge
  Then workgraph refuses it because system-one profiles run direct only

Scenario: Classify System One base URLs as local or hosted
  Given a system-one profile whose base URL is a loopback address
  Then associate runs without hosted LLM opt-in and sends unfiltered context
  Given a system-one profile whose base URL is a remote host
  Then associate refuses to send anything until hosted LLM use is enabled
  And managed settings that disable hosted providers block it
  And look-alike hosts such as localhost.evil.example or 127.0.0.1@host are hosted
  And a redirect from the System One host fails the run without replaying the request
  And call errors name only the scheme and host with a fixed message and never echo provider output

Scenario: Associate units with a System One decision model
  Given a system-one profile is routed to associate
  And units of work are eligible for association
  When workgraph associates them
  Then each unit is sent as one System One request with a project choice and a kind choice
  And the project choice offers opaque option ids for the unit's candidate projects plus no_project
  And project slugs are not option keys, and hosted state and option descriptions are filtered
  And each link's confidence is the returned probability of the chosen option
  And an answer whose choice was not offered is discarded for that unit with a reason
  And units that cannot be expressed as a choice are skipped with a reason
