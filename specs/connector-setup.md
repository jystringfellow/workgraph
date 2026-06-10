# Connector Setup Experience

Connector setup should be guided, local-first, and resilient to partial
configuration so users can reach first successful sync without memorizing
provider-specific parameters.

## Goals

- reduce setup failures caused by missing or unclear parameters
- separate required and optional fields in a guided flow
- validate connection settings before connector polling is enabled
- allow draft-and-resume setup without re-entering prior values

## Principles

- **Guided but explicit**: users answer a small sequence of prompts instead of
  editing large JSON blobs by hand.
- **Required first**: setup asks required fields first, then optional tuning.
- **Local-only state**: drafts and final settings stay on the local machine.
- **No silent enablement**: connector polling does not start until validation
  succeeds and the user confirms.
- **Actionable errors**: validation failures point to the specific field or
  permission problem.

## Setup Flow

1. Choose connector and provider variant when relevant.
2. Collect required parameters.
3. Optionally collect advanced or enterprise parameters.
4. Run a local validation + test connection step.
5. Persist settings and mark connector setup state as ready.

If setup stops early, workgraph stores a local draft state and supports resume.

## Suggested CLI Surface

```text
workgraph connectors setup <connector>
workgraph connectors setup <connector> --resume
workgraph connectors validate <connector>
workgraph connectors status
```

Provider-specific connect commands may call the same setup flow internally.

## Validation Requirements

Validation should check:

- required fields are present and parseable
- credentials or tokens can authenticate when applicable
- minimum provider permissions or scopes are present
- endpoint URLs and tenant/workspace identifiers are valid

Validation should return field-scoped messages and avoid logging secrets.

## Draft And Ready States

Each connector should expose setup status:

- `draft`: setup started but not yet valid or confirmed
- `ready`: validation passed and connector can be polled
- `error`: latest validation failed; connector remains non-ready

Runtime polling must include only `ready` connectors.

## Reference Implementation

GitHub is the reference setup handoff path because it has a simple local
validation command:

```text
workgraph github connect
workgraph connectors status
```

Successful `gh auth status` marks GitHub `ready`. Failed validation marks
GitHub `error`, preserves actionable validation text, and keeps the connector
out of runtime polling. Later provider setup flows should reuse the same state
model.

## Security Notes

- secret inputs should be masked in prompts and logs
- secrets should use user-only file permissions and local credential storage
  when supported
- optional manual-token setup remains explicit and should include warnings about
  token scope and lifetime

## Non-Goals For First Slice

- graphical setup UI
- centralized cloud-hosted setup state
- automatic provider-side permission escalation
