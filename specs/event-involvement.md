# Event Involvement

workgraph separates who performed an action from why the action belongs in the
user's work context.

- `actor` identifies who performed the recorded action when known.
- `involvement` is a sorted, de-duplicated array explaining the connected
  user's relationship to the event.
- connector scope explains why the provider was queried; it does not by itself
  prove that every returned item is personally relevant.

The initial involvement vocabulary is:

```text
assigned
attendee
authored
created
edited
explicitly_watched
involved
mentioned
organizer
recipient
review_requested
thread_participation
```

Unknown values are rejected at normalized ingest boundaries. Connectors must
not invent a more precise relationship than their provider evidence supports;
for example, a broad GitHub `involves` result is `involved` unless a more
specific matched query proves `review_requested`.

`events.involvement_json` has three deliberate states:

- `NULL`: legacy or not-yet-classified evidence. It remains visible in `today`
  during incremental migration so an upgrade does not silently erase prior
  activity.
- `[]`: explicitly classified with no direct involvement. It remains available
  in `events today` but is omitted from the default `today` view.
- a non-empty array: directly relevant activity, included in `today`.

`workgraph today --involvement <kind>` and `workgraph events today
--involvement <kind>` select events containing that exact relationship.
`events today` remains the complete evidence view unless a filter is supplied.
Its detailed rows render non-empty involvement, `none` for explicit `[]`, and
`unclassified` for `NULL` so migration state remains inspectable.
There is no generic `--mine-only`: actor equality is not equivalent to personal
relevance for received mail, assigned work, meetings, replies, or reviews.

Direct and bridged capture write the same normalized metadata. Provider recipe
facts must verify involvement alongside event identity, time bounds, and
completeness.
