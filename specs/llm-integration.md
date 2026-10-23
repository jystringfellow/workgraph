# LLM Integration

workgraph should use LLMs as an optional local capability for summarization,
association suggestions, and supplemental insight. Core capture, storage, today,
resume, and memory behavior must continue to work without an LLM.

The LLM connector must support:

- local models, especially OpenAI-compatible local endpoints such as Ollama, LM
  Studio, llama.cpp servers, and similar tools
- cloud-account models such as AWS Bedrock and Azure-hosted model endpoints
- third-party model subscriptions such as OpenAI, Anthropic, Google, and other
  provider APIs when users bring their own credentials
- signed-in AI clients such as Codex and Claude Code, without requiring a
  separate model API key in workgraph

## Principles

- **Optional enhancement**: LLM features are disabled until configured. A failed
  model call must not break non-LLM commands.
- **Local-first by default**: Local model profiles should work without outbound
  hosted AI calls or API keys.
- **User-owned credentials**: Credentials are configured locally. workgraph must
  not require a workgraph-operated LLM service.
- **Explicit outbound consent**: Hosted LLM use is opt-in at configuration time.
  Dry-run and debug modes must let users inspect outbound prompts without
  sending them.
- **Focused context**: workgraph must send only the selected task context, never
  an unbounded database or memory dump.
- **Suggestions before facts**: LLM output is supplemental unless the user
  approves it. LLMs may propose summaries, associations, and memory edits, but
  memories remain concrete user-owned records.
- **Provider-independent tasks**: Commands such as summarize and associate should
  target task contracts, not provider-specific request shapes.

## Configuration

workgraph stores LLM configuration locally under the workgraph home directory:

```text
~/.workgraph/llm.json
```

The file must be written with local-user-only permissions when supported by the
operating system.

Configuration should support multiple named profiles, one default profile, and
optional task-specific profile overrides:

```json
{
  "default_profile": "local-gemma",
  "task_profiles": {
    "summarize": "local-gemma",
    "associate": "bedrock-work"
  },
  "profiles": {
    "local-gemma": {
      "provider": "openai-compatible",
      "base_url": "http://localhost:11434/v1",
      "model": "gemma-4-12b"
    },
    "bedrock-work": {
      "provider": "bedrock",
      "aws_profile": "work",
      "region": "us-east-1",
      "model_arn": "arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-3-5-sonnet-20241022-v2:0"
    },
    "openai-personal": {
      "provider": "openai",
      "model": "gpt-4.1-mini",
      "api_key_env": "OPENAI_API_KEY"
    }
  }
}
```

For the first slice, local OpenAI-compatible profiles do not require credentials.
Hosted API profiles should prefer environment variable references such as
`api_key_env` over storing raw API keys. Bedrock profiles should prefer normal
AWS local credential resolution through `aws_profile`, region, and `model_arn`.
The ARN may identify a foundation model, provisioned throughput, or inference
profile supported by Bedrock Runtime's Converse API. A plain `model_id` can be
accepted as a convenience later, but ARNs are the preferred durable shape
because tools such as Claude Code and cloud governance workflows commonly expect
them.

Initial commands:

```text
workgraph llm add <profile> --provider openai-compatible --base-url <url> --model <model>
workgraph llm add <profile> --provider bedrock --aws-profile <profile> --region <region> --model-arn <arn>
workgraph llm list
workgraph llm use <profile>
workgraph llm use <profile> --for summarize
workgraph llm test
workgraph llm test --profile <profile>
workgraph llm summarize today --dry-run
workgraph llm summarize today
workgraph llm summarize today --no-stream
```

## Signed-in AI client profiles

Codex and Claude Code are supported as local execution clients for hosted LLM
tasks. This is distinct from bridged capture: capture uses an asynchronous
outbox because workgraph cannot call client-owned provider connectors, while an
LLM task can synchronously invoke a signed-in local client process.

The guided setup is:

```text
workgraph llm connect codex
workgraph llm connect claude-code
workgraph llm connect codex --name personal-codex --for summarize
workgraph llm use claude-code --for categorize
```

`llm connect` verifies that the selected client executable is available and
stores an `ai-client` profile. The default profile name is the client id; an
explicit `--name` allows more than one profile. `--for <task>` immediately
routes that task to the profile. Without `--for`, setup does not replace an
existing default or task route.

The stored shape is provider-neutral:

```json
{
  "profiles": {
    "codex": {
      "provider": "ai-client",
      "client": "codex"
    },
    "claude-code": {
      "provider": "ai-client",
      "client": "claude-code",
      "model": "sonnet"
    }
  },
  "task_profiles": {
    "summarize": "codex",
    "categorize": "claude-code"
  }
}
```

The model is optional for an AI-client profile. When omitted, the client uses
its own client-selected default. workgraph stores no client login token and does not
accept arbitrary command lines as LLM profiles.

An AI-client profile may bind its invocation to an existing client
configuration directory with `--config-dir`. The shared persistence, runtime
environment, and diagnostic contract is defined in
[`signed-in-client-binding.md`](signed-in-client-binding.md).

AI-client execution must:

- resolve only the registered `codex` or `claude` executable;
- pass the filtered prompt through standard input, never a process argument;
- use a non-persistent invocation and a private empty working directory;
- disable client tools and connector access where supported, and otherwise use
  the strictest non-writing sandbox available;
- apply a bounded timeout and bounded response size;
- return only the final model response, without client progress output;
- preserve the existing hosted-LLM opt-in and managed-provider policy checks.

Managed deployments may additionally set
`llm.ai_client.allowed_clients` to `codex`, `claude-code`, or both. This limits
which local signed-in client workgraph may invoke; it does not govern the
client's behavior outside workgraph.

`llm test` performs a minimal generation through the selected client.
`llm doctor` verifies executable availability without making a model call.
Client-backed summaries use the same focused context, outbound filtering, and
task routing as direct providers. A missing, unauthenticated, timed-out, or
malformed client invocation fails only the requested LLM command and never
affects capture.

Task routing is intentionally broader than the currently implemented task set.
The `categorize` route may be configured now. Project categorization is the
`associate_events` task below: it never rewrites captured events and
applies links automatically only under the gates in `specs/projects.md`.

Future provider commands may add provider-specific flags for OpenAI, Anthropic,
Google, Bedrock, and Azure without changing the task command surface.

## System One decision profiles

System One models (TypeSafe Jev, Cloudflare Clef and Clef-flash,
Microsoft-Decision-1, and Ollama's local System One endpoint) do not generate
text. A request carries a `state` and named `questions`, and each answer is a
probability distribution over the options the question offered. workgraph
supports them as a `system-one` provider for the `associate` route only.

```text
workgraph llm add <profile> --provider system-one --base-url <url> --model <model> [--api-key-env <VAR>]
workgraph llm use <profile> --for associate
```

```json
{
  "profiles": {
    "jev": {
      "provider": "system-one",
      "base_url": "https://api.typesafe.ai",
      "model": "jev-latest",
      "api_key_env": "TYPESAFE_API_KEY"
    },
    "clef-local": {
      "provider": "system-one",
      "base_url": "http://127.0.0.1:8000",
      "model": "clef-flash"
    }
  }
}
```

Configuration:

- `--base-url` and `--model` are required. The same adapter serves every
  System One host, so there is no default endpoint.
- workgraph sends `POST <base-url>/v1/systemone`, or `<base-url>/systemone`
  when the base URL path ends in `/v1`. A base URL whose path already ends in
  `/systemone`, or a Workers AI `/ai/run/<model>` URL, is used unchanged. The
  suffix is added to the path, so a query string such as `?api-version=...` is
  kept. The base URL must be `http` or `https` with a host, and a base URL with
  a fragment is refused.
- Redirects are refused: a 3xx response fails the run, and the request body is
  never replayed to another destination.
- The API key is read from the `--api-key-env` variable at call time and sent as
  `Authorization: Bearer <key>`. It is never written to `llm.json` or printed.
- A `localhost` or loopback base URL is local, exactly as for
  `openai-compatible`: no hosted opt-in, no outbound filter, and allowed when
  managed settings disable hosted providers. Every other base URL is hosted: it
  needs `workgraph llm hosted enable`, is filtered, and is subject to the
  managed `llm.allowed_base_urls` allowlist.
- `system-one` profiles run direct only. `--execution bridge` is refused, and a
  stored profile with an execution setting is refused before any request.
- `llm list` and `llm doctor` show the provider, model, and base URL host, never
  the path, key, or key variable name. `llm doctor` makes no request.
- Call errors never echo provider output or URL parts. They name only
  `scheme://host` with a fixed message: the HTTP status and "request was not
  accepted", "request failed before a response" (or "request timed out"), or
  "is not System One JSON". A discarded answer reports the question and a fixed
  reason, never the returned choice. The managed allowlist rejection prints
  `scheme://host` for `system-one`; for `openai-compatible` it prints the base
  URL without userinfo and with its query and fragment replaced by `REDACTED`.
- `llm test` and `llm summarize today` refuse `system-one` profiles because they
  need generated text.

Wire contract (confirmed against TypeSafe, Ollama, DigitalOcean, Cloudflare,
and the `irr/clef-flash` server):

```json
{
  "model": "jev-latest",
  "state": "<filtered unit context as JSON text>",
  "questions": {
    "project": {
      "type": "choice",
      "instructions": "...",
      "criteria": {"p1": "Atlas Bot", "p2": "Harbor Harness. Workstream: harbor", "no_project": "..."}
    },
    "kind": {
      "type": "choice",
      "instructions": "...",
      "criteria": {"review": "...", "build": "...", "fyi": "..."}
    }
  }
}
```

```json
{
  "model": "jev-1.13.0",
  "answers": {
    "project": {
      "type": "choice",
      "choice": "p1",
      "confidence": 0.91,
      "probabilities": {"p1": 0.93, "p2": 0.05, "no_project": 0.02}
    }
  },
  "usage": {"input_tokens": 512, "output_tokens": 0}
}
```

Unconfirmed by primary documentation: the Workers AI REST path and whether its
response is wrapped as `{"result": {...}}` (workgraph unwraps that envelope),
the Microsoft Foundry auth header (workgraph sends only a bearer token), and an
OpenRouter System One route (OpenRouter documents a different alpha decisions
endpoint, which this provider does not speak).

Association mapping, one request per unit:

- `state` is the unit (kind, container, time range, participants,
  identifiers, events) and the recent decisions, as JSON text.
- The `project` choice offers one opaque option id per candidate project
  (`p1`, `p2`, ... in slug order) plus `no_project`, and workgraph maps the
  chosen id back to the slug locally, so slugs are not used as option keys and
  the state carries no candidate slug list. Each option is described by title,
  workstream, aliases, and description, falling back to the slug when a
  project has no title. Recent decisions in the state also name project slugs.
  For hosted profiles the state, including those decisions, and every option
  description go through the outbound filter; instructions, option ids, and
  kind descriptions are static. A new-project proposal needs a generated slug
  and name, so it is not offered.
- The `kind` choice offers the seven link kinds.
- The link's confidence is `probabilities[choice]` for the `project` answer.
  The provider's `confidence` field is ignored because providers define it
  differently (normalised top probability, entropy, or top probability). The
  probability comes from the model's output distribution rather than
  self-reported text, but no provider documents it as calibrated.
- The summary is the unit's first event summary, because the model returns no
  text. Evidence is every member event of the unit.
- An answer for either question whose choice was not offered, or whose
  probability for that choice is missing, `null`, not a number, or outside
  [0, 1], is discarded for that unit with a reason. A non-2xx status
  or a response that is not System One JSON fails the run without writing
  links, as for other providers.
- A unit with no candidate project (one option) or more than 254 candidate
  projects (over the 255-option choice limit) is not sent and is reported as
  discarded with a reason.
- Validated entries go through the same validation, auto-apply gates, and
  persistence as every other associate profile, including scheduled daemon
  runs.
- `--dry-run` prints every request body that would be sent and makes no
  request.

## Task Contracts

LLM requests should be organized around workgraph tasks. Each task defines its
input selection, output shape, and persistence behavior.

### `test`

Purpose: verify a configured profile can complete a minimal generation request.

Input:

- a short deterministic user prompt

Output:

- provider name
- model id
- request destination
- response text

Persistence:

- none

### `summarize_today`

Purpose: summarize today's captured local work context.

Input:

- selected `today` events from SQLite, using the same local git identity
  filter as `workgraph today`
- relevant local memory excerpts when available
- event ids or source ids needed for traceability
- provider-specific previews when available, such as Notion page
  `content_preview`

Output:

- plain text summary
- notable threads or projects
- open loops or suggested next review items
- cited event ids where practical

Persistence:

- supplemental local summary event or draft
- no memory file changes

Dry-run behavior:

- prints the selected context and prompt
- prints selected profile and destination
- does not call the provider

Streaming behavior:

- `workgraph llm summarize today` streams provider output by default
- the command prints the selected profile, destination, and a `Thinking...`
  status before provider text arrives
- `--no-stream` keeps deterministic full-response output for debugging,
  tests, and terminals where streaming is undesirable

### `associate_events`

Purpose: link units of work, such as a Slack thread, GitHub PR, git commit
session, Notion page activity, or calendar event, to the user's declared
projects, and give each unit a kind and one-line summary. The full contract is
in `specs/projects.md`.

Route: `associate`, configured with `workgraph llm use <profile> --for
associate` or `llm connect ... --for associate`. Existing `categorize` routes
are read as `associate`.

Input:

- units that no deterministic rule linked, from sources the user enabled with
  `workgraph llm sources --for associate --enable <source>` (or `--disable`;
  with no flag it prints the enabled sources); none is enabled by default and
  Slack DMs are a separate source
- the project catalog from memory frontmatter: slug, title, workstream,
  aliases, and description
- recent user decisions

Output (`workgraph.associate_result.v1`), per unit:

- target: an existing project, a new-project proposal, or none
- kind: `review`, `build`, `coordination`, `advice`, `unblock`, `decision`, or
  `fyi`
- one-line summary
- confidence from 0 to 1
- cited evidence event ids, all members of the unit

Persistence:

- validated entries become `project_links` rows; invalid entries are discarded
  and counted
- an entry is applied automatically only at confidence 0.95 or higher, for an
  existing project or none, with no recent user rejection for that container
  and project, and while auto-apply is not paused; everything else is
  proposed for review
- auto-apply pauses when measured accuracy over at least 20 decided auto
  links is below 0.95; with fewer than 20 it stays on and every undecided
  auto link is listed for review
- the daemon schedules categorization through the outbox; an `ai-client`
  profile with `"execution": "bridge"` is run by the bridge worker instead of
  workgraph (see Scheduling in `specs/projects.md`)
- no mutation of events; memory changes only when the user creates a new
  project during review

Dry-run behavior:

- `workgraph llm associate --dry-run` prints the profile, destination, enabled
  sources, and exact filtered payload, and does not call the provider

### `memory_suggestions` (future)

Purpose: propose supplemental updates to memory files from repeated evidence or
approved summaries.

Input:

- selected evidence
- current memory snippets

Output:

- proposed Markdown additions or edits
- reasons and source event ids

Persistence:

- draft patch or suggestion only
- memory files change only after explicit user approval

## Provider Shape

The implementation should introduce provider adapters only after task contracts
are clear. The first provider should be OpenAI-compatible HTTP because it covers
local engines and many hosted-compatible gateways.

Provider adapters eventually need to account for:

- chat or responses API request shape
- streaming support
- JSON output support
- timeout and retry behavior
- max output tokens
- provider/model metadata
- provider-specific authentication
- context window and capability differences

The first implementation does not need embeddings, vector search, tool calling,
or streaming.

## Context And Prompt Handling

workgraph should build prompts just in time from local state. Prompt builders
must keep instructions separate from connector data and memory excerpts because
captured data is untrusted input and can contain prompt injection attempts.

Prompt templates may become user-editable later, but the first slice should use
small static prompts so facts can validate deterministic request structure.

Outbound filtering must run before hosted model calls. Filtering is a risk
reduction layer, not a guarantee. At minimum, hosted outbound context should
scrub common secrets, access tokens, credential-looking strings, and configured
internal patterns before transmission.

## Error Handling

- LLM calls must use bounded timeouts.
- Provider errors should include provider/profile names without logging secrets.
- `--dry-run` must not perform provider network calls.
- Failed LLM calls should not discard captured raw data.
- Persisted LLM output must be distinguishable from first-party captured events
  and user-authored memory.

## First Slice

The first executable slice is local OpenAI-compatible configuration and a
summary dry run:

- add/list/use named LLM profiles
- set default and task-specific profile routing
- test an OpenAI-compatible profile against a fake or local endpoint
- run `workgraph llm summarize today --dry-run` without sending data

After this slice, add real `summarize today` generation and persistence, then
Bedrock, then third-party hosted providers.

## Current Bedrock Slice

Bedrock profiles use the AWS SDK for Go v2 and Bedrock Runtime `Converse`.
workgraph passes the configured `model_arn` as the Converse `ModelId`, which
supports inference profile ARNs. Credentials are resolved through the normal AWS
SDK chain, optionally scoped by `aws_profile`, and requests use the configured
region.

Supported commands:

```text
workgraph llm test --profile <bedrock-profile>
workgraph llm use <bedrock-profile> --for summarize
workgraph llm summarize today
```

Dry-run behavior remains provider-independent and does not call Bedrock.

## Roadmap Checklist

- [x] LLM config file with named profiles and task routing.
- [x] OpenAI-compatible provider for local model endpoints.
- [x] `workgraph llm test`.
- [x] `workgraph llm summarize today --dry-run`.
- [x] `workgraph llm summarize today` supplemental summary output.
- [x] Streaming `workgraph llm summarize today` output.
- [x] Include captured content previews in summary context.
- [x] Bedrock profile support through local AWS credentials.
- [x] Signed-in Codex and Claude Code profiles with guided `llm connect` setup.
- [x] Client-backed `test`, `doctor`, and `summarize today` execution.
- [ ] Hosted provider support for OpenAI, Anthropic, Google, and similar APIs.
- [x] `system-one` decision-model profiles for the `associate` route.
- [ ] Project categorization of units across Slack, GitHub, git, Notion, and
  calendar (`associate_events`, `specs/projects.md`).
- [ ] Memory suggestion drafts with explicit approval before memory changes.
- [ ] Embeddings or search only if deterministic context selection becomes too
  slow or weak.
