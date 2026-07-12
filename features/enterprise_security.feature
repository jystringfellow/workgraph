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

Scenario: Audit endpoint security without exposing local data
  Given workgraph stores captured events and connector credentials locally
  When an administrator requests the machine-readable security report
  Then the report describes local permission and managed-policy controls
  And the report identifies storage encryption and credential-store gaps
  And the report does not expose credentials or captured work data

Scenario: Restrict local state to the current user
  Given workgraph stores captured data and daemon diagnostics locally
  When workgraph initializes or starts background capture
  Then supported POSIX platforms restrict those files to the current user
  And broader permissions from an older installation are repaired

Scenario: Filter hosted LLM requests locally
  Given hosted AI features are configured
  When workgraph prepares captured context for an LLM request
  Then sensitive patterns are scrubbed locally before the request is sent
  And hosted AI can be disabled entirely

Scenario: Apply admin-managed local policy
  Given workgraph is running on a company-managed device
  And managed settings disable hosted LLM providers
  And managed settings disable Slack direct-message capture
  When a local user config or CLI flag tries to override those locked values
  Then workgraph keeps the managed values
  And diagnostics report the managed settings source without exposing secrets
