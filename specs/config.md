# Configuration

workgraph keeps local configuration in the workgraph home:

```text
~/.workgraph/settings.json
```

The settings file answers what local paths workgraph watches, where it looks for
git repositories, and what paths it must never record.

Project-level settings are a non-goal for now. workgraph should use global user
settings plus optional admin-managed settings so local repositories cannot
quietly change capture or LLM behavior by carrying project config.

Connector-specific secrets are stored separately from `settings.json`. Slack OAuth
setup writes local connector settings to:

```text
~/.workgraph/slack.json
```

That file stores the Slack access token and explicitly configured channel ids
with user-only file permissions.

## Defaults

`workgraph init` creates `settings.json` when it does not already exist.

File watching is opt-in. A fresh `settings.json` watches no directories:
`watch_dirs` is empty until the user adds a directory with `workgraph settings
add-watch`. workgraph does not guess which folders to watch, and it never falls
back to watching the home directory or the current working directory.

Git repository discovery is separate from file watching. `workgraph init`
records existing common code folders under the current user's home directory in
`repo_dirs` so git and GitHub capture can find local repositories without
registering any file watches. Paths are resolved with Go's `os.UserHomeDir()`
and persisted as absolute paths, not as shell tokens such as `$HOME`.

Default `repo_dirs` candidates are:

- Code
- Projects
- Developer
- Work
- source
- repos

Only candidates that already exist are included. If none exist, `repo_dirs` is
empty.

Example on macOS:

```json
{
  "watch_dirs": [],
  "repo_dirs": ["/Users/craig/Code"],
  "ignore_paths": ["/Users/craig/.workgraph"],
  "ignore_names": [".git", "node_modules", "DerivedData", ".noindex", "xcuserdata", "bin", "obj", "dist", "build", "target", ".build", ".gradle"]
}
```

Example on Windows:

```json
{
  "watch_dirs": [],
  "repo_dirs": ["C:\\Users\\Craig\\repos"],
  "ignore_paths": ["C:\\Users\\Craig\\.workgraph"],
  "ignore_names": [".git", "node_modules", "DerivedData", ".noindex", "xcuserdata", "bin", "obj", "dist", "build", "target", ".build", ".gradle"]
}
```

The default must always ignore the workgraph home directory so database writes, logs, PID files, and config updates do not recursively become user work events.

## Fields

### watch_dirs

`watch_dirs` is a list of absolute directory paths the user chose to watch for
local file activity. Foreground and background capture watch these directories
when no `--watch` flag is provided. When no directory is configured, capture
watches nothing and reports that file watching is off along with the command
that adds a directory.

Users add a watch root with:

```text
workgraph settings add-watch [path]
```

If `path` is omitted, the command adds the current working directory. Added
paths are resolved to absolute paths and placed before existing watch roots.
Adding a path that is already watched is idempotent. Watched roots are traversed
recursively, subject to ignore rules and the watch budget; when the configured
roots exceed the budget, capture reports the watch limit and suggests
prioritizing directories with `workgraph settings add-watch`.

Watched roots are also scanned for git repositories, so a repository under an
explicitly watched directory is captured even when it is not under `repo_dirs`.

### repo_dirs

`repo_dirs` is a list of absolute directory paths that git and GitHub capture
scan for local git repositories. These directories are walked on each git or
GitHub poll; they are never registered as file watches. Entries that no longer
exist are skipped. An explicit `"repo_dirs": []` disables repository scanning
outside watched roots and is preserved when workgraph rewrites settings; an
absent or `null` value means the settings predate `repo_dirs`. `--watch` flags change only which directories are watched for that run;
repository discovery still scans `repo_dirs` plus the watched roots.

### conservative_watch_dirs

`conservative_watch_dirs` is written only by older workgraph versions. It lists
the `watch_dirs` entries that `workgraph init` populated automatically, and
`workgraph settings add-watch` removed a path from it when the user added that
path explicitly. workgraph uses it to tell the two apart on upgrade; see
Upgrading from automatic watch roots.

## Upgrading from automatic watch roots

Older versions of `workgraph init` populated `watch_dirs` with existing common
folders such as Desktop, Documents, Downloads, and Code and recorded the same
paths in `conservative_watch_dirs`. Capture reads such settings without
rewriting them:

- A `watch_dirs` entry that is also listed in `conservative_watch_dirs` was
  populated automatically and is not watched.
- Every other `watch_dirs` entry was added by the user and is watched exactly as
  before.
- When `repo_dirs` is absent, the automatically populated entries are used as
  repository scan roots, so git and GitHub capture keep finding the same local
  repositories.

A background capture daemon started by an older version records its watch roots
in daemon state without saying where they came from. When a connector setup
command restarts background capture, workgraph passes through only watch roots
that the running daemon recorded as `--watch` overrides, stored as resolved
absolute paths so a restart from another working directory watches the same
roots; all other roots are
resolved again from settings by the new daemon, so automatic roots stay
unwatched after a restart.

The settings file is never modified by this upgrade rule. Users who want to
watch a previously automatic folder add it with `workgraph settings add-watch`,
which marks it as user-added.

### ignore_paths

`ignore_paths` is a list of absolute paths. A captured source path is ignored when it is the same as an ignored path or is a descendant of an ignored path.

This field is for user-owned directories or files that should never be tracked, such as caches, private folders, generated output, or the workgraph home.

Users manage path rules without editing JSON directly:

```text
workgraph settings add-ignore-path <path>
workgraph settings remove-ignore-path <path>
```

Both commands require a path, resolve it to an absolute path without requiring
it to exist, and update the local settings file. Adding an existing rule and
removing a missing rule are successful no-ops. Other settings and rule order are
preserved.

### ignore_names

`ignore_names` is a list of file or directory basenames. A captured source path is ignored when any path segment matches one of these names.

This field is for high-noise project internals and generated content that commonly appear under watched roots, such as `.git`, `node_modules`, Xcode `DerivedData`, Xcode `xcuserdata`, Apple `.noindex` directories, and common build output names including `bin`, `obj`, `dist`, `build`, `target`, `.build`, and `.gradle`.

Users manage basename rules with:

```text
workgraph settings add-ignore-name <name>
workgraph settings remove-ignore-name <name>
```

Names must be non-empty basenames, not paths, and cannot be `.` or `..`.
Adding an existing rule and removing a missing rule are successful no-ops.

Every ignore command reports the settings file and normalized rule. A command
that changes settings tells the user to restart capture because an already
running daemon keeps the rules loaded when it started.

## Precedence

Normal capture path configuration uses this order when no managed setting is
locked:

```text
CLI flags > unlocked managed settings > settings file > defaults
```

If one or more `--watch` flags are provided, capture uses those watch roots for that run. Ignored paths and ignored names still apply.

Some deployments may provide admin-controlled managed settings outside the
workgraph home directory. Managed settings are intended for company-managed
devices or other controlled environments where an administrator wants
workgraph's own behavior to be verifiable and consistent.

Managed settings are not a general security boundary for arbitrary local
software. They constrain approved workgraph builds and should be paired with
normal endpoint, network, OAuth app, and software distribution controls when a
company needs enforcement.

Suggested managed settings locations:

```text
/etc/workgraph/managed-settings.json
/Library/Application Support/workgraph/managed-settings.json
%ProgramData%\workgraph\managed-settings.json
```

At runtime, managed settings are only read from the platform-managed path.
They are not redirected by user settings, CLI flags, or environment variables.
Facts may use internal test helpers to avoid writing to system directories, but
that override is not part of the user-facing command surface.

Managed settings can either provide defaults or lock specific settings. A locked
managed setting takes precedence over CLI flags and user config because the
administrator has explicitly made that value non-overridable by workgraph.

Effective precedence is therefore:

```text
locked managed settings > CLI flags > unlocked managed settings > settings file > defaults
```

Example:

```json
{
  "version": 1,
  "llm": {
    "hosted_enabled": {
      "value": false,
      "locked": true
    },
    "allowed_base_urls": {
      "value": ["http://localhost:11434/v1"],
      "locked": true
    },
    "allowed_providers": {
      "value": ["openai-compatible", "bedrock"],
      "locked": true
    },
    "openai_compatible": {
      "allowed_models": {
        "value": ["llama3.1:8b-instruct-q4_K_M"],
        "locked": true
      },
      "require_model_probe": {
        "value": true,
        "locked": true
      }
    },
    "bedrock": {
      "allowed_model_arns": {
        "value": [
          "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
        ],
        "locked": true
      },
      "allowed_inference_profile_scopes": {
        "value": [
          {
            "account_id": "123456789012",
            "region": "us-west-2"
          }
        ],
        "locked": true
      }
    }
  },
  "connectors": {
    "allowed_ids": {
      "value": ["git", "github", "slack", "calendar.google", "calendar.microsoft", "notion"],
      "locked": true
    },
    "disabled_ids": {
      "value": ["mail.google", "mail.microsoft", "azure.boards"],
      "locked": true
    },
    "slack": {
      "include_dms": {
        "value": false,
        "locked": true
      }
    },
    "mail": {
      "capture_body": {
        "value": false,
        "locked": true
      }
    }
  }
}
```

Commands that display configuration should show whether each effective value
came from defaults, user config, CLI flags, or managed settings. Diagnostics
such as `workgraph settings get`, `workgraph settings doctor`, and `workgraph
doctor` should report the managed settings file path when one is active and
should not expose secrets.

`workgraph settings get --format json` should return the same effective
settings in a machine-readable form for endpoint verification and admin review.
The JSON output should include:

- whether managed settings are active and which fixed platform path was checked
- effective managed controls, their locked state, and their provenance
- local settings path and non-secret counts for watch, repository scan, and ignore rules

The JSON output must not include connector tokens, OAuth client secrets, raw
captured data, memory contents, or unknown fields from managed settings files.

## Portability

Configuration should store normalized absolute paths using Go's `filepath` behavior for the current operating system. Friendly tokens such as `~` may be accepted later, but they are not required for Phase 0.
