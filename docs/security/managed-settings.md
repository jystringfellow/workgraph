# Managed Settings Deployment

workgraph managed settings are a local policy file for company-managed devices.
They let an administrator lock supported workgraph behavior such as hosted LLM
use and Slack DM capture. They are intended to be deployed with normal endpoint
management, software distribution, OAuth app approval, and network controls.

Managed settings do not create a general security boundary. They constrain
approved workgraph builds, but managed policy does not prevent a user from running unrelated software, building a modified binary, or using another tool outside company policy.

## Recommended Policy

The recommended starting policy is:

```text
docs/security/enterprise-managed-settings.recommended.json
```

It locks hosted LLM providers off, restricts OpenAI-compatible LLM traffic to a
local endpoint, restricts OpenAI-compatible model names to an approved local
model, and locks Slack DM capture off.

For organizations that approve AWS Bedrock inference profiles instead of local
models, use this as the starting point:

```text
docs/security/bedrock-inference-profiles.managed-settings.example.json
```

It locks LLM use to the `bedrock` provider and restricts Bedrock calls to
inference profile ARNs in the listed AWS account and region scopes. Replace the
example account id and region with the organization's approved Bedrock account
and region before deployment.

## Deployment Paths

Deploy the policy file as `managed-settings.json` at the platform-managed path:

```text
macOS:   /Library/Application Support/workgraph/managed-settings.json
Windows: %ProgramData%\workgraph\managed-settings.json
Linux:   /etc/workgraph/managed-settings.json
```

The path is fixed at runtime. Users cannot redirect it with workgraph user
settings, CLI flags, or environment variables.

## Current Controls

The current managed policy schema supports these controls:

- `llm.hosted_enabled`: disables hosted LLM providers when set to `false` and
  locked.
- `llm.allowed_providers`: restricts LLM use to listed providers such as
  `openai-compatible` or `bedrock`.
- `llm.allowed_base_urls`: restricts OpenAI-compatible LLM destinations to the
  listed base URLs when locked.
- `llm.openai_compatible.allowed_models`: restricts OpenAI-compatible LLM calls
  to listed model names when locked.
- `llm.bedrock.allowed_model_arns`: restricts Bedrock Runtime calls to listed
  model, provisioned throughput, or inference profile ARNs.
- `llm.bedrock.allowed_inference_profile_scopes`: allows Bedrock Runtime calls
  to any inference profile ARN in the listed AWS account and region scopes,
  while still blocking foundation-model ARNs and inference profiles from other
  accounts or regions.
- `connectors.slack.include_dms`: disables Slack direct-message and
  group-direct-message capture when set to `false` and locked.

The recommended policy is intentionally narrow. It addresses the highest-risk
controls implemented today without claiming broader connector governance than
the current binary enforces.

## Verification

After endpoint management deploys the file, verify the effective local policy:

```sh
workgraph settings get --format json
```

The JSON output should show:

- `managed_settings.active` is `true`
- `managed_settings.path` is the platform-managed path above
- `llm.hosted_enabled.value` is `false`
- `llm.hosted_enabled.locked` is `true`
- `llm.allowed_base_urls.locked` is `true`
- `llm.openai_compatible.allowed_models.locked` is `true` when
  OpenAI-compatible model allowlisting is used
- `llm.allowed_providers.locked` is `true` when provider allowlisting is used
- `llm.bedrock.allowed_model_arns.locked` is `true` when Bedrock ARN
  allowlisting is used
- `llm.bedrock.allowed_inference_profile_scopes.locked` is `true` when Bedrock
  account/region inference profile scope allowlisting is used
- `connectors.slack.include_dms.value` is `false`
- `connectors.slack.include_dms.locked` is `true`

The command reports effective policy and non-secret local settings counts. It
does not print connector credentials, OAuth client secrets, captured data, or
memory contents.

## Admin Notes

Managed settings are most useful when paired with:

- managed installation of a known workgraph build
- endpoint controls that protect the managed settings path from user edits
- approved OAuth apps and connector consent review
- network policy for hosted LLM providers and other external destinations
- periodic endpoint inventory if the organization wants fleet-level evidence

For manual review, an employee can include the output of
`workgraph settings get --format json` in an approval request. For stronger
assurance, admins should collect or verify the same command through endpoint
management rather than relying on screenshots.
