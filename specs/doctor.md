# Doctor

`workgraph doctor` reports local readiness without contacting provider APIs or
exposing secrets.

The first health report checks:

- workgraph home and database initialization
- daemon running state
- configured watch roots and whether each exists
- OAuth-backed connector config presence and token presence
- LLM profile configuration and whether the selected profile has required local
  inputs such as API key environment variables

Doctor output is diagnostic only. It should suggest visible setup actions, but
it must not start capture, refresh tokens, contact APIs, or modify local state.
