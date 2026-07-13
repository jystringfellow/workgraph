# CI

workgraph should run the full Go test suite automatically before changes merge
to `main`.

Pull requests targeting `main` should trigger GitHub Actions CI. The workflow
should check out the repository, install the Go version from `go.mod`, and run
on both Linux and macOS:

```text
go vet ./...
go test ./...
```

Vet and test failures must fail the workflow. CI steps must not use
`continue-on-error` to turn those failures into successful checks.

The facts package should remain portable across the CI operating systems:

- use Go or operating-system temporary-directory APIs instead of hardcoded
  platform paths
- build the workgraph CLI once before facts run and reuse that binary for CLI
  subprocess facts
- apply timeouts to command behavior, not repeated cold compilation

Local development can still run focused facts or impacted package tests first;
the PR workflow provides the full unsandboxed regression pass before merge.
