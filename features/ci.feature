Feature: CI

Scenario: Run full tests on pull requests to main
  Given a pull request targets main
  When GitHub Actions runs CI
  Then workgraph checks out the repository
  And workgraph installs the Go version from go.mod
  And workgraph runs CI on Linux and macOS
  And workgraph runs "scripts/verify"
  And scripts/verify checks gofmt, runs "go vet ./...", and runs "go test ./..."
  And a formatting, vet, or test failure fails the workflow

Scenario: Report only failures
  Given a test run where one test fails and another passes with log output
  When scripts/go-test-failures runs the tests
  Then it prints the failing test and its output
  And it leaves out the passing test's output
  And it exits non-zero

Scenario: Reuse one CLI build across command facts
  Given command facts execute the workgraph CLI many times
  When the facts suite starts
  Then it builds the CLI once without a per-command timeout
  And command facts reuse that binary on every supported CI platform
