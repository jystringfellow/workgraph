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

Future provider commands may add provider-specific flags for OpenAI, Anthropic,
Google, Bedrock, and Azure without changing the task command surface.

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

- selected `today` events from SQLite
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

### `associate_events` (future)

Purpose: propose cross-platform work associations, such as linking a Slack
thread, calendar event, GitHub PR, Notion page, and local file activity to the
same project or work item.

Input:

- candidate events and artifacts selected by deterministic heuristics
- existing memory project aliases or known associations

Output:

- proposed links
- confidence or strength of evidence
- reasons grounded in event ids, source ids, timestamps, names, and titles

Persistence:

- pending association suggestions only
- no automatic mutation of events or memory

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
- [ ] Hosted provider support for OpenAI, Anthropic, Google, and similar APIs.
- [ ] Association suggestions across Slack, calendar, GitHub, Notion, mail, and
  local file events.
- [ ] Memory suggestion drafts with explicit approval before memory changes.
- [ ] Embeddings or search only if deterministic context selection becomes too
  slow or weak.
