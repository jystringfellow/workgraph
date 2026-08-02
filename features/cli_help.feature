Feature: CLI help

Scenario: Discover top-level commands
  When I run "workgraph help"
  Then workgraph lists every public top-level command
  And workgraph explains how to request command-specific help
  And workgraph does not list internal daemon commands

Scenario: Get help for every public command
  Given a public workgraph command or nested command group
  When I request help before or after its command path
  Then workgraph prints the command usage and description
  And command groups list their immediate subcommands
  And workgraph exits successfully without running the command

Scenario: Request help for an unknown command
  When I run "workgraph help does-not-exist"
  Then workgraph identifies the unknown help path
  And workgraph points me to "workgraph help"
