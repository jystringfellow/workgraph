# Event Associations

Event associations make captured events easier to inspect without rewriting raw
events or inventing unsupported summaries.

The model has two lanes:

- baseline: deterministic local evidence, available without an LLM
- semantic: an optional future lane for ranking an already bounded candidate set

This slice implements only the baseline lane.

## Existing Foundations

Project inference and artifact identity already exist:

- file capture prefers the nearest enclosing git repository name, then the
  configured watch-root name
- file events preserve the changed path in `payload_json.path`
- `today` groups consecutive same-project events into deterministic query-time
  sessions when the gap is no more than 30 minutes

Associations do not replace those behaviors.

## Production And Storage

`workgraph associations explain <event-id>` computes candidates on demand from
the local `events` table. Every qualifying pair is also coalesced into the shared
`suggestions` table with:

- `type = association`
- `lane = baseline`
- a canonical pair `pattern_key`
- stable suggestion id, score, confidence, reasons, matched signals, sources,
  and cited event ids

The existing `suggestions`, `suggestion_feedback`, and
`suggestion_suppressions` tables are the complete durable contract. This slice
does not add an associations table. Re-evaluation refreshes changed evidence but
does not reopen dismissed, snoozed, approved, or acted suggestions. Unchanged
evaluation does not change lifecycle or timestamps.

Approval of an association suggestion only records the shared suggestion
lifecycle transition and feedback. It does not create another link row and does
not mutate either event. Completion may subsequently mark it acted, like other
suggestions.

## Canonical Identity

An association always contains exactly two events from different `source`
values. Sort the event ids bytewise before constructing the pair. The canonical
pattern key is the compact JSON array of those two ids prefixed with `pair:`.
The suggestion id is the shared stable suggestion hash for type `association`
and that pattern key. Input order therefore cannot create a duplicate.

## Bounded Candidate Window

For a requested event, candidates must:

- have a different event id and a different `source`
- be timestamped within the closed interval of seven days before through seven
  days after the requested event
- be selected through the indexed event timestamp range
- be limited to the nearest 200 rows, ordered first by absolute timestamp
  distance, then timestamp, then event id

Only those rows are scored. This prevents an unqualified all-events Cartesian
scan. Malformed candidate timestamps are ignored. A malformed target timestamp
is an error because no honest window can be constructed.

## Signal Extraction

Signals come only from stored event columns and valid JSON payloads.

- URLs are collected from string values in the payload and from title-like
  text. HTTP(S) URLs are normalized by lowercasing scheme and host, removing a
  fragment and default port, sorting the query through standard URL encoding,
  and removing a trailing slash other than the root.
- repository identity is a lowercased `owner/repository` from a payload
  `repository` value or a recognized GitHub URL. A bare project or local folder
  name is not an exact repository identity.
- issue or pull-request identity is an explicit positive `number` paired with
  the same exact repository, or the repository and number parsed from a GitHub
  `/issues/<n>` or `/pull/<n>` URL. A number alone never matches.
- commit identity is a 7-to-40 character hexadecimal value from an explicit
  `commit`, `sha`, `head_sha`, or `headSha` field, or a recognized commit URL.
  Full and abbreviated SHAs match only when one is a prefix of the other.
- branch identity comes from an explicit `branch`, `head_ref`, or `headRefName`
  field after removing `refs/heads/` and lowercasing. A branch match is usable
  only with the same exact repository identity.
- project identity is a case-folded, trimmed event `project`. Generic container
  names such as `code`, `work`, `projects`, `documents`, `downloads`, `source`,
  `repos`, and `unknown` are not meaningful project evidence.
- title tokens come from `summary` and payload `title` or `subject`. Text is
  lowercased; non-letter and non-digit runes split tokens; duplicates,
  numeric-only tokens, tokens shorter than three runes, and the documented
  generic stop tokens are removed. Fuzzy evidence requires at least three
  tokens on both sides, at least two shared tokens, and Jaccard overlap of at
  least 0.50.

The generic stop-token set is exactly: `a`, `an`, `and`, `are`, `as`, `at`,
`be`, `by`, `email`, `for`, `from`, `in`, `into`, `is`, `issue`, `it`,
`meeting`, `message`, `of`, `on`, `or`, `project`, `pull`, `request`, `status`,
`task`, `the`, `to`, `update`, `updated`, `with`, `work`, and `working`.

Current mail thread ids, Slack thread timestamps, calendar ids, and provider
object ids are provider-scoped. They are not compared across sources in this
slice because current payloads do not establish a shared namespace. Their
canonical URLs may still provide exact cross-source evidence.

## Deterministic Scoring

Each distinct signal contributes once; the total is capped at 100:

| Evidence | Score |
| --- | ---: |
| identical normalized URL | 100 |
| same exact repository plus issue/PR number | 90 |
| matching commit SHA | 85 |
| same exact repository plus branch | 65 |
| normalized title-token Jaccard >= 0.80 | 50 |
| normalized title-token Jaccard >= 0.60 and < 0.80 | 40 |
| normalized title-token Jaccard >= 0.50 and < 0.60 | 30 |
| same meaningful project | 20 |
| timestamps no more than 15 minutes apart | 10 |
| timestamps more than 15 minutes but no more than 2 hours apart | 5 |

Timestamp points are added only when at least one non-time signal matched. Time
alone can never produce an association. A pair qualifies at 60 points:

- `high`: score 80 through 100
- `medium`: score 60 through 79
- below 60: insufficient evidence; no suggestion is stored or shown as related

Reasons and matched signals use the scoring-table order. Exact values are
ordered lexically before the first value is selected for explanation, so maps
or payload field order cannot change output.

## Explain Output

`workgraph associations explain <event-id>` lists qualifying related candidates
ordered by:

1. score descending
2. absolute timestamp distance ascending
3. related event id ascending

Every row includes score, confidence, lifecycle state, suggestion id, canonical
cited event ids, matched signals, and human-readable reasons. A missing target
event is an error. An existing event with no qualifying candidate succeeds and
says the evidence was insufficient. Suppressed pairs are identified during
explicit inspection but are not recreated as proposed suggestions.

Association evidence uses event timestamps and stable values only. It does not
record an evaluation timestamp. This keeps scoring, ordering, reasons, ids, and
refreshed evidence deterministic across runs.

## Today Visibility

The first slice does not add association output to `workgraph today`. Raw events
and existing sessions remain the primary evidence there. A later fact may add a
small high-confidence section after the inspection workflow has proved useful.

## Semantic Lane (Optional Future)

The semantic lane may rank a small deterministic candidate set after explicit
opt-in. It may not replace the baseline, send broad event history, or silently
mutate event data.

## Constraints

- local SQLite state only
- no LLM, embeddings, hosted service, telemetry, or learned ranking
- raw events remain the source of truth and are never rewritten or deleted
- no match from same-source repetition, bare issue numbers, generic titles, or
  timestamp proximity alone
