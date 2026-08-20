# Releasing workgraph

workgraph releases are explicit, tag-driven builds. The application does not
check for updates or replace itself.

## One-time Homebrew setup

Create the `jystringfellow/homebrew-tap` repository with a `Formula/`
directory. Add a fine-grained `HOMEBREW_TAP_TOKEN` Actions secret to the
workgraph repository. Scope that token to the tap repository with contents
write access; it does not need access to captured workgraph data or any local
installation.

Without this secret, release archives, checksums, and the generated
`workgraph.rb` release asset are still published. Only the tap update is
skipped.

## Create a release

Before tagging, run:

```sh
go vet ./...
go test ./...
git status --short
```

Choose the next stable semantic version and push its tag:

```sh
git tag -a v0.1.0 -m "workgraph v0.1.0"
git push origin v0.1.0
```

The release workflow rejects other tag shapes. Native runners build five
archives because the SQLite dependency requires CGO:

- `workgraph_<version>_darwin_arm64.tar.gz`
- `workgraph_<version>_darwin_amd64.tar.gz`
- `workgraph_<version>_linux_arm64.tar.gz`
- `workgraph_<version>_linux_amd64.tar.gz`
- `workgraph_<version>_windows_amd64.zip`

The workflow creates `checksums.txt`, renders `workgraph.rb`, creates the GitHub
release for the existing tag, and updates the Homebrew tap when its token is
available.

## Verify a release

Download an archive and `checksums.txt` from the GitHub release, compare the
archive's SHA-256 digest, then inspect the embedded build identity:

```sh
shasum -a 256 workgraph_<version>_darwin_arm64.tar.gz
tar -xzf workgraph_<version>_darwin_arm64.tar.gz
./workgraph version
```

The reported version must match the tag and the commit must match the tagged
commit. After Homebrew publication, verify both installation and upgrades:

```sh
brew update
brew install jystringfellow/tap/workgraph
workgraph version
brew upgrade workgraph
```

Checksums are corruption evidence, not publisher-identity proof. Release
signing, provenance attestations, and macOS notarization remain follow-up work.
