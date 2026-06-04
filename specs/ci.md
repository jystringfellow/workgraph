# CI

workgraph should run the full Go test suite automatically before changes merge
to `main`.

Pull requests targeting `main` should trigger GitHub Actions CI. The workflow
should check out the repository, install the Go version from `go.mod`, and run:

```text
go test ./...
```

Local development can still run focused facts or impacted package tests first;
the PR workflow provides the full unsandboxed regression pass before merge.
