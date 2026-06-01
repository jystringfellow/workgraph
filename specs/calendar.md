# Calendar

workgraph calendar ingestion captures scheduled work context from calendar
providers without requiring the rest of the system to know provider-specific API
shapes.

The first calendar contract is a normalized JSON capture path:

```text
workgraph calendar capture --events-file <calendar-events.json>
```

The first provider adapter is Google Calendar:

```text
workgraph calendar capture --provider google --calendar-id primary --token <access-token>
```

The Google adapter reads events from the Google Calendar events endpoint and
maps Google-specific fields into the same normalized event contract. OAuth setup
and token refresh are separate follow-up work; this slice accepts an access
token so the capture and storage behavior can be tested independently.

Each exported event uses a provider-neutral shape:

- `provider`: `google` or `microsoft`
- `calendar_id`: provider calendar id
- `event_id`: provider event id
- `title`: event title
- `start`: RFC3339 timestamp
- `end`: RFC3339 timestamp
- `all_day`: whether the event is all-day
- `location`: physical or textual location
- `meeting_url`: online meeting URL
- `organizer`: organizer display name or email
- `attendees`: participant display names or emails
- `status`: provider status such as `confirmed`, `cancelled`, `tentative`
- `project`: optional explicit project association

Captured calendar events are stored as `calendar.event` records with source
`calendar`. The event timestamp is the event start time. The summary is the
event title. The actor is the organizer. The payload preserves the normalized
provider, ids, timing, attendees, location, meeting URL, status, and title.

Calendar events are deduplicated by provider, calendar id, and event id. If a
later capture includes the same provider event with a newer or changed payload,
workgraph refreshes the stored record instead of creating a duplicate.

Google Calendar and Outlook/Microsoft Calendar should be implemented as provider
adapters that produce this same normalized event shape. Google is the first
adapter; Outlook/Microsoft Calendar should reuse the same storage contract.
