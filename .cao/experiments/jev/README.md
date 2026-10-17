# Optional Jev shadow evaluation

This disconnected experiment retains `jev_shadow.py` and its repository metadata
collector. The active pod does not import, sync, call, or depend on either script.
Jev observations cannot approve code, dismiss findings, change seats/models,
bypass checks, or decide whether a correction budget is exhausted.

The collector reports changed paths, counts, tracked insertion/deletion totals,
and test/spec flags. It does not emit file contents, diffs, prompts or environment
values. **Paths and metadata can still disclose sensitive project information.**
Inspect and curate the JSON locally before explicitly sending it anywhere:

```sh
python3 .cao/experiments/jev/collect_change_metadata.py > /private/tmp/change-metadata.json
```

Only if you deliberately want an external Jev request, use an environment where
`typesafe_sdk` is installed and `TYPESAFE_API_KEY` is configured:

```sh
/path/to/experiment/python .cao/experiments/jev/jev_shadow.py < /private/tmp/change-metadata.json
```

That second command sends the supplied JSON to the service and may incur cost.
The script asks about risk, review focus and whether deeper review is warranted;
it reports answers as non-authoritative telemetry. It is not a deterministic
test, adjudicator or calibrated acceptance gate. The SDK/service integration
is retained for later evaluation and has not been live-validated during cleanup.
Tests use a fake SDK and make no external calls. Credentials stay in the native
SDK environment, outside task packets and reviewers' context. Output/metadata
retention is operator-owned; no automated collection or cleanup is enabled.
