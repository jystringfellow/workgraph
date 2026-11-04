# CI

workgraph should run the full Go test suite automatically before changes merge
to `main`.

Pull requests targeting `main` should trigger GitHub Actions CI. The workflow
should check out the repository, install the Go version from `go.mod`, and run
`scripts/verify` on both Linux and macOS. `scripts/verify` is the one full
verification used by CI, local development, and agent harnesses:

```text
gofmt -l .
go vet ./...
scripts/go-test-failures ./...
```

The release workflow's "Verify tagged source" job runs `scripts/verify` before
any build or publish job. A release tag must pass the same formatting, vet, and
test checks as CI.

Formatting, vet, and test failures must fail the workflow. CI steps must not
use `continue-on-error` to turn those failures into successful checks.

`scripts/verify` and `scripts/check` print only failures: unformatted files, vet
findings, build errors, and the output of failing tests. Output from passing
tests is left out so a reader, human or agent, sees only what needs fixing.
`scripts/check` takes test name patterns and `./package` paths and runs just
those, uncached. Both clear any active wrapped workgraph AI session from the
environment so the facts for `workgraph ai run` see a clean session.

The facts package should remain portable across the CI operating systems:

- use Go or operating-system temporary-directory APIs instead of hardcoded
  platform paths
- build the workgraph CLI once before facts run and reuse that binary for CLI
  subprocess facts
- apply timeouts to command behavior, not repeated cold compilation

Local development can still run focused facts or impacted package tests first;
the PR workflow provides the full unsandboxed regression pass before merge.
