Feature: Enterprise security and compliance

Scenario: Explain Slack user scopes to enterprise IT
  Given workgraph can connect to Slack with user scopes
  When an employee requests approval to use workgraph
  Then the repository includes an IT-readable compliance document
  And the document explains what Slack scopes are requested
  And the document explains that raw captured data stays local by default

Scenario: Protect local captured context at rest
  Given workgraph stores captured connector events locally
  When local encryption is enabled
  Then the SQLite event store is encrypted at rest
  And encryption keys are stored in the operating system credential store

Scenario: Filter hosted LLM requests locally
  Given hosted AI features are configured
  When workgraph prepares captured context for an LLM request
  Then sensitive patterns are scrubbed locally before the request is sent
  And hosted AI can be disabled entirely
