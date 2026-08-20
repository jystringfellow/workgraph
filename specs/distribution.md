# Distribution

workgraph provides inspectable installation and upgrade paths without an
automatic self-updater. Package managers and explicit release downloads remain
in control of replacing the executable.

## Source and Go Installation

From a checkout, contributors install the CLI with:

```text
go install ./cmd/workgraph
```

Go writes the executable to `GOBIN` when configured and otherwise to
`$(go env GOPATH)/bin`. Documentation must show an actual executable invocation
or a `PATH` export; it must not present the binary directory by itself as a
command. For the normal empty-`GOBIN` case, the documented shell setup is:

```text
export PATH="$(go env GOPATH)/bin:$PATH"
command -v workgraph
```

Users installing through the Go toolchain install or explicitly upgrade with:

```text
go install github.com/jystringfellow/workgraph/cmd/workgraph@latest
```

## Version Command

`workgraph version` is a public, read-only command. It accepts no positional
arguments or flags other than the standard help forms and makes no network
request. Its text output is exactly three lines:

```text
workgraph <version>
commit: <revision>
built: <RFC3339-or-unknown>
```

Tagged release builds inject the full semantic tag such as `v0.1.0`, the full
Git commit, and an RFC3339 build timestamp. Other builds use Go build
information when available and conservatively render `dev` or `unknown` for
missing values. The command never prints a local path.

## GitHub Releases

Pushing a tag matching `vMAJOR.MINOR.PATCH` runs a dedicated release workflow.
The workflow validates the tag and passes `go vet ./...` plus `go test ./...`
before building. It has only the permissions it needs. Release builds run on
native GitHub-hosted runners because workgraph's SQLite dependency uses CGO.
Linux release binaries are built on Ubuntu 22.04 runners to avoid unnecessarily
raising the glibc baseline. macOS builds set a macOS 13 deployment target while
still using currently supported runner images.

The first supported release matrix is:

- macOS arm64
- macOS amd64
- Linux arm64
- Linux amd64
- Windows amd64

macOS and Linux artifacts are `.tar.gz` archives. Windows is a `.zip` archive.
Every archive contains the `workgraph` executable (`workgraph.exe` on Windows)
and the README. Artifact names include the version, operating system, and
architecture.

The release job downloads only artifacts produced by its build matrix,
generates `checksums.txt` with one SHA-256 digest per archive, and creates the
GitHub release for the already-pushed tag. The release contains all archives,
the checksum file, and the generated Homebrew formula.

## Homebrew

The release workflow renders one `workgraph.rb` formula from a repository-owned
template. The formula selects the matching macOS or Linux archive by operating
system and CPU architecture, pins its SHA-256 digest, installs only the
workgraph executable, and verifies `workgraph version` in its test block.

Publishing the generated formula to `jystringfellow/homebrew-tap` requires an
explicitly configured `HOMEBREW_TAP_TOKEN` Actions secret with contents write
access to that separate repository. If the secret is absent, the GitHub release
still succeeds and retains `workgraph.rb` as a release asset; the workflow
reports that tap publication was skipped. The release workflow must never log
the token or place it in an artifact.

Once the tap is active, install and upgrade commands are:

```text
brew install jystringfellow/tap/workgraph
brew upgrade workgraph
```

## Security and Deferred Behavior

V1 distribution does not include:

- an in-process self-updater
- automatic update checks
- silent executable replacement
- installation scripts piped from the network into a shell
- release signing, notarization, or package-manager publication beyond the
  Homebrew tap

Checksums detect accidental corruption and allow explicit verification, but do
not by themselves prove publisher identity. Signing, provenance attestations,
macOS notarization, a Scoop manifest, and additional package repositories remain
separate follow-up work.
