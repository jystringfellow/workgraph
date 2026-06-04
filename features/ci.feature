Feature: CI

Scenario: Run full tests on pull requests to main
  Given a pull request targets main
  When GitHub Actions runs CI
  Then workgraph checks out the repository
  And workgraph installs the Go version from go.mod
  And workgraph runs "go test ./..."
