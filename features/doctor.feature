Feature: Doctor

Scenario: Diagnose local readiness
  Given workgraph has been initialized
  And workgraph has watch roots, connector config, and LLM config
  When I run "workgraph doctor"
  Then workgraph reports database, daemon, watch root, OAuth connector, and LLM readiness
  And workgraph does not expose OAuth tokens
