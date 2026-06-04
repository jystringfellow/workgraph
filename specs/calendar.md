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
workgraph calendar disconnect google
```

By default, connect opens the Google authorization URL and completes OAuth
through a local PKCE callback. Browser connect binds a random loopback port and
uses a redirect URI shaped like `http://127.0.0.1:<port>`, matching Google's
installed-app loopback guidance. Google Calendar OAuth is always treated as a
local/native app flow; workgraph does not support non-PKCE calendar OAuth. The
user can override the default workgraph client id with `--client-id`, but
workgraph does not expose or accept a calendar client secret because users will
not provide secrets in normal local use.

Google's token exchange is performed through a narrow workgraph-controlled token
relay:

```text
https://workgraph-google-oauth-token.jystringfellow.workers.dev/calendar/google/token
```

The local CLI sends only the authorization code, PKCE code verifier, redirect
URI, client id, and grant type to the relay. The relay adds the Google OAuth
client secret from Cloudflare Worker secrets, forwards the token request to
Google, returns Google's JSON response, and does not store or log OAuth codes,
tokens, or request bodies. The relay is used only for OAuth token exchange; it
does not receive calendar event data.

Local development for the token relay should use Cloudflare Workers' `.dev.vars`
secret loading pattern. Developers can create
`workers/google-oauth-token/.dev.vars` with `GOOGLE_CLIENT_SECRET=...` and run
`wrangler dev` from the Worker directory. The local `.dev.vars` file must be
ignored by git; production still uses `wrangler secret put GOOGLE_CLIENT_SECRET`.

Manual connect with `--no-browser` prints a PKCE authorization URL and does not
write local connector settings until the user reruns it with an OAuth code,
matching state, and code verifier. After code exchange, workgraph stores Google
Calendar connector settings under the workgraph home directory with
local-user-only file permissions. Stored settings include access token, refresh
token when granted, token type, expiry, granted scopes, selected calendar ids,
and provider API base URL.

`workgraph calendar disconnect google` revokes the stored Google Calendar token
when possible and removes local Calendar connector settings. Disconnecting is
the supported way to return workgraph to a clean Google Calendar connection
state before reconnecting or recording the Google OAuth approval flow.

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
