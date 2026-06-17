# Calendar

workgraph calendar ingestion captures scheduled work context from calendar
providers without requiring the rest of the system to know provider-specific API
shapes.

The first calendar contract is a normalized JSON capture path:

```text
workgraph calendar capture --events-file <calendar-events.json>
```

The first provider adapters are Google Calendar and Microsoft Calendar:

```text
workgraph calendar capture --provider google --calendar-id primary --token <access-token>
workgraph calendar capture --provider microsoft --calendar-id primary --token <access-token>
```

The Google adapter reads events from the Google Calendar events endpoint and
maps Google-specific fields into the same normalized event contract. Direct
token capture is mainly a fact-friendly adapter path; user-facing use should go
through connection setup.

The Microsoft adapter reads events from Microsoft Graph calendar endpoints and
maps Graph-specific fields into the same normalized event contract. The default
`primary` calendar id maps to the user's default Graph calendar; explicit
calendar ids use `/me/calendars/{id}/events`. Microsoft capture requests UTC
event date-times with the Graph `Prefer: outlook.timezone="UTC"` header before
normalizing timestamps.

Google Calendar connection setup uses OAuth:

```text
workgraph calendar connect google
workgraph calendar connect google --no-browser
workgraph calendar connect google --code <oauth-code> --state <state>
workgraph calendar disconnect google
workgraph calendar connect microsoft
workgraph calendar connect microsoft --no-browser
workgraph calendar connect microsoft --code <oauth-code> --state <state>
workgraph calendar disconnect microsoft
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

The same token relay handles Google Calendar refresh-token exchanges. When
stored Google Calendar credentials are expired or close to expiry, capture
refreshes the access token before calling the Calendar API. The local CLI sends
the refresh token, client id, and grant type to the relay; it still does not
send or store the Google client secret.

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
provider API base URL, OAuth client id, and token relay URL when customized.

Calendar connect commands should be idempotent for already-connected providers.
If a provider already has local connector settings and the user runs
`workgraph calendar connect <provider>` without an explicit OAuth code,
workgraph should not open OAuth again. It should print that the provider is
already connected and return successfully.
If a different calendar provider is already connected, connecting another
provider must preserve the existing provider settings in `calendar.json`.

`workgraph calendar disconnect google` revokes the stored Google Calendar token
when possible and removes local Calendar connector settings. Disconnecting is
the supported way to return workgraph to a clean Google Calendar connection
state before reconnecting or recording the Google OAuth approval flow.
Disconnecting Google Calendar must preserve other calendar provider settings,
such as Microsoft Calendar, if they are present in the same local calendar
configuration file.
If Google Calendar is not connected, disconnect should print that it is already
disconnected and return successfully.

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

Microsoft Calendar setup starts with publisher-domain verification for the
workgraph Microsoft Entra application. The Cloudflare Pages site must host the
Microsoft identity association file at:

```text
https://workgraph.pages.dev/.well-known/microsoft-identity-association.json
```

The file should contain the Microsoft application id that owns the verification:

```text
413dce76-e10c-4a57-84b4-89f6b66ab265
```

This static verification file does not grant workgraph Microsoft account access
or process user data. Microsoft account data access must be covered separately
by OAuth scopes, local credential storage, and connector behavior before
implementation.

Microsoft Calendar connection setup uses the workgraph Microsoft Entra
application:

```text
413dce76-e10c-4a57-84b4-89f6b66ab265
```

Microsoft Calendar OAuth must use authorization code with PKCE, the Microsoft
identity platform v2 endpoints, and no client secret in local CLI requests.
The default manual redirect URI is:

```text
http://localhost:2727/calendar/microsoft/callback
```

The Microsoft Calendar connector should initially request Microsoft Graph
delegated scopes:

```text
openid
profile
email
offline_access
https://graph.microsoft.com/Calendars.Read
https://graph.microsoft.com/Calendars.Read.Shared
```

Azure DevOps access must not be bundled into the initial Microsoft Calendar
Graph authorization URL. Azure DevOps uses a different resource than Microsoft
Graph and should be handled by the work tracking connector.

Connected Microsoft Calendar settings are capture-ready once stored locally.
Routine connector polling uses the stored access token, selected calendar ids,
and Graph API base URL from `calendar.json`. When stored Microsoft Calendar
credentials are expired or close to expiry, capture refreshes the access token
through the configured Microsoft identity token endpoint before calling
Microsoft Graph.

`workgraph calendar disconnect microsoft` removes local Microsoft Calendar
connector settings but does not revoke Microsoft app consent remotely. Microsoft
does not provide the same narrow public OAuth token revocation endpoint that
workgraph uses for Google Calendar. The disconnect output must tell users that
local Microsoft Calendar credentials were removed and that Microsoft consent can
be revoked from their Microsoft account or tenant app consent settings.
Disconnecting Microsoft Calendar must preserve other calendar provider settings,
such as Google Calendar, if they are present in the same local calendar
configuration file.
If Microsoft Calendar is not connected, disconnect should print that it is
already disconnected and return successfully.

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
