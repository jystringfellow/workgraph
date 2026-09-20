# Provider Tool Requirements

Every bridged connector declares the provider operations required to fetch its
approved scope and to construct stable event identity. These declarations live
in the canonical connector registry beside capture modes, event source, scope
validation, and capture semantics.

A requirement contains:

- a stable logical operation name, independent of an MCP server namespace;
- whether it is required for `fetch`, `identity`, or both; and
- an optional scope condition for mutually exclusive recipe paths; and
- a short constraint describing the provider result needed by the recipe.

Logical operation names describe capabilities such as `slack.read_thread` or
`microsoft.read_resource`. They are not permission strings. A client preflight
must match each declared operation to an available read-only provider tool and
prove it with a harmless discovery call. Exact Claude permission names remain
explicit operator choices because provider MCP namespaces and exported tool
names can change independently from workgraph.

The worker applies conditional requirements against the request's validated
parameters. For example, Slack channel scope requires `slack.read_channel`,
participant or direct-message scope requires `slack.search_messages`, and all
paths require `slack.read_thread` for complete thread fetch and timestamp
identity.

`workgraph connectors required-tools [connector]` renders the declarations for
all bridgeable connectors or one exact connector. Direct-only connectors have
no bridged provider requirements and are rejected when selected explicitly.
The local bridge MCP exposes the same data through
`connector_required_tools`, rather than maintaining a second tool matrix.

Before an unattended worker claims a request, it must:

1. list pending requests;
2. read the candidate connector's registry requirements through
   `connector_required_tools`;
3. prove every fetch and identity operation is available and authorized; and
4. claim only that connector after the complete preflight succeeds.

A missing or denied operation leaves the request pending. A successful empty
search does not prove an identity operation that requires reading an item or a
revision marker. Failures are reported against a claimed request only after
the registry-driven preflight succeeded.

Registry completeness facts require every bridgeable connector to declare at
least one fetch requirement and at least one identity requirement. Direct-only
connectors must not declare bridged requirements. This makes an incomplete new
bridge recipe fail the build instead of relying on prose in a bundled skill.
