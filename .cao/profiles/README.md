# CAO profile catalog

Normal `bash .cao/sync.sh` selects the four supervisor-driven pod seats:

| Profile | Responsibility |
| --- | --- |
| `wg-pod-lead` | Coordinate assignments, verification requests, and closure |
| `wg-pod-developer` | Write facts and implement corrections |
| `wg-pod-reviewer` | Independently review the current candidate |
| `wg-pod-gate` | Independently assess final readiness |

The existing specification supplies the plan when valid. These seats do not
automatically activate the architect or switch models.

Sync validates these four profiles and parses their YAML front matter before
copying them into the CAO agent store. It stages each copy beside its destination
and renames it into place, leaves identical stored copies untouched, refuses to
overwrite a differing stored copy, and installs the Copilot custom agent. It does not launch agents or remove previously installed profiles.
The host runner in `../pod.py` creates unique installed aliases for each run.

The gate runs under the `cao_readonly` Codex profile without patch or terminal
input tools; it keeps command execution because Codex reads files through it.
The reference sandbox settings for both Codex profiles are in [`../codex/`](../codex/);
install them into `${CODEX_HOME:-~/.codex}` as described in [RIG.md](../RIG.md).

Run-specific aliases are disposable installed copies, not new seat definitions.
Before archiving one, check CAO terminal records and retained sessions. Preserve
the profile-store copy, installed context copy, and native custom agent together
in a private archive outside discovery directories, with original paths and
content hashes for recovery. Keep aliases referenced by retained sessions and
leave unknown or independently owned agents alone. Do not delete run evidence,
credentials, or native CLI settings as part of profile cleanup.
