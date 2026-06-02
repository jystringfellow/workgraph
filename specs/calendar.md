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
maps Google-specific fields into the same normalized event contract. Direct
token capture is mainly a fact-friendly adapter path; user-facing use should go
through connection setup.

Google Calendar connection setup uses OAuth:

```text
workgraph calendar connect google
workgraph calendar connect google --no-browser
workgraph calendar connect google --code <oauth-code> --state <state>
```

By default, connect opens the Google authorization URL and completes OAuth
through a local PKCE callback, so a client secret is not required. The user can
override the default workgraph client id with `--client-id`; manual or
confidential-client flows can also pass `--client-secret`. Manual connect with
`--no-browser` prints an authorization URL and does not write local connector
settings until the user reruns it with an OAuth code and matching state. After
code exchange, workgraph stores Google Calendar connector settings under the
workgraph home directory with local-user-only file permissions. Stored settings
include access token, refresh token when granted, token type, expiry, granted
scopes, selected calendar ids, and provider API base URL.

The Google Calendar connector requests:

```text
https://www.googleapis.com/auth/calendar.calendarlist.readonly
https://www.googleapis.com/auth/calendar.freebusy
https://www.googleapis.com/auth/calendar.calendars.readonly
https://www.googleapis.com/auth/calendar.events.owned.readonly
https://www.googleapis.com/auth/calendar.events.readonly
```

These scopes allow workgraph to list calendars, read availability, read calendar
metadata, read events on calendars the user owns, and read events on calendars
the user can access. workgraph does not request Google Calendar write access.

The Google OAuth app may show a broader scope inventory while the connector is
being reviewed. workgraph should only request scopes that are needed by current
behavior, but the full Google Calendar scope inventory under consideration is:

```text
https://www.googleapis.com/auth/calendar.calendarlist.readonly
https://www.googleapis.com/auth/calendar.freebusy
https://www.googleapis.com/auth/calendar.calendars.readonly
https://www.googleapis.com/auth/calendar.events.owned.readonly
https://www.googleapis.com/auth/calendar.events.readonly
```

Scope rationale:

- `calendar.calendarlist.readonly`: useful for listing calendars the user is
  subscribed to and selecting which calendars workgraph should capture.
- `calendar.events.readonly`: useful for reading events on calendars the user
  can access; this is the primary event ingestion scope.
- `calendar.freebusy`: useful for availability lookup but not sufficient for
  event titles, attendees, locations, or meeting links.
- `calendar.events.owned.readonly`: too narrow for workgraph because work
  meetings often appear on calendars the user can access but does not own.
- `calendar.calendars.readonly`: useful only if workgraph needs richer calendar
  metadata beyond the user's calendar list.

The Google OAuth app registration should use the workgraph homepage and privacy
policy/terms URLs:

```text
https://workgraph.pages.dev
https://workgraph.pages.dev/privacy.html
https://workgraph.pages.dev/terms.html
```

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

Justitifcation text:
```text
workgraph is a local-first personal work context tool. It reads calendar data only to create local work events, help users understand what meetings and scheduled work happened during a day, and restore context through local commands such as today and resume.
The app needs read-only access to calendar events so it can capture meeting titles, start/end times, organizers, attendees, locations, conferencing links, and event status. This data is stored locally on the user’s device and is not sold, used for advertising, or shared with third parties.
The app also needs read-only access to the user’s calendar list so users can identify and select which calendars workgraph should capture.
More limited scopes are not sufficient because events.owned.readonly only covers calendars owned by the user, while many work meetings appear on shared, delegated, subscribed, or organization-managed calendars. workgraph needs to read events on calendars the user can access, but it does not need write access.
```

Shorter:
```text
workgraph is a local-first personal work context tool. It uses Google Calendar data to create local
calendar.event records, show scheduled work in local context views, and help users resume work around
meetings.

calendar.calendarlist.readonly lets users see and select which calendars workgraph should capture.
calendar.events.readonly lets workgraph read events on calendars the user can access.
calendar.events.owned.readonly supports narrower owned-calendar event reads where applicable.
calendar.calendars.readonly provides calendar metadata such as title, description, and time zone.
calendar.freebusy supports availability/focus-time context without requiring event details.

workgraph requests read-only scopes only and does not modify Google Calendar data.
```
