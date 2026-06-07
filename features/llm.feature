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

Scenario: Test an OpenAI-compatible LLM profile
  Given workgraph has an OpenAI-compatible LLM profile
  When I run "workgraph llm test --profile local-gemma"
  Then workgraph sends a minimal chat completion request to the configured base URL
  And the output includes the selected profile, provider, model, destination, and response text
  And no connector data or memory content is sent for the test command

Scenario: Preview today's LLM summary without sending data
  Given workgraph has captured local events today
  And workgraph has a configured LLM profile for summarize tasks
  When I run "workgraph llm summarize today --dry-run"
  Then workgraph prints the selected profile and request destination
  And workgraph prints the focused context and prompt that would be sent
  And workgraph does not call the LLM provider
  And workgraph does not write memory files

Scenario: Future LLM association suggestions stay reviewable
  Given workgraph has related Slack, calendar, GitHub, Notion, mail, and local file events
  When workgraph asks an LLM to associate candidate work items
  Then the LLM output is stored as pending association suggestions
  And each suggestion includes grounded reasons and source event ids
  And workgraph does not mutate events or memory without approval
