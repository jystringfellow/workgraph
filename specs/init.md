# Init Command

`workgraph init` creates local workgraph state.

Init is safe to run more than once. It creates missing local state and preserves existing user data.

`workgraph init --force` refreshes init-owned defaults without deleting captured
events or memory files.

## Created Paths

By default, init creates:

- `~/.workgraph/`
- `~/.workgraph/workgraph.db`
- `~/.workgraph/settings.json`
- `~/workgraph-memory/`
- `~/workgraph-memory/projects/`

The home, database, and memory paths may be overridden by CLI flags.

## Database

Init creates the active Phase 0 SQLite schema and leaves tables empty. Existing events, sessions, and memory docs are preserved when init runs again.

## Config

Init creates `settings.json` when it does not already exist.

The default config:

- watches existing common user-facing folders such as Desktop, Documents, Downloads, Code, Projects, Developer, Work, source, and repos
- avoids recursively watching the entire home directory when common folders exist
- ignores the workgraph home directory
- stores watch and ignore paths as normalized absolute paths
- includes high-noise ignored names such as `.git`, `node_modules`, and common build output directories

Init must not overwrite an existing settings file by default. User edits to watch
roots or ignore rules are preserved.

When `--force` is provided, init overwrites `settings.json` with the current
default config. This is the supported way to pick up newer default ignore rules
after workgraph changes.

See `specs/config.md` for the settings file contract.

## Output

Init reports the initialized paths:

- workgraph home
- database
- memory repo
- project memory directory
- settings file

On macOS, init also explains that capture may need access to protected folders
such as Documents, Desktop, and Downloads when those folders are watched. It
should suggest granting Full Disk Access once to the terminal app or installed
workgraph binary to avoid repeated per-folder prompts.
