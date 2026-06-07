# Specification: LLM Integration

## Context
To empower "workgraph" as a tool for personal work intelligence, it must be able to leverage Large Language Models (LLMs) for tasks such as summarization, categorization, and proactive insight generation. 

While the system is "Local-First," we recognize that some users prefer high-capability hosted models (OpenAI/Anthropic), while others require more private "Cloud" options (AWS Bedrock/Azure Foundry).

## Principles
1. **Provider Agnostic**: The core business logic of workgraph must not know which provider it is talking to. It should interact with a unified `LLMProvider` interface.
2. **Data Sovereignty**: Only the data required for the specific task (the "context") should be sent to any external API.
3. **Local-First Continuity**: The absence of an LLM or a failed connection to one must not break core functionality. LLM features are "enhancements."

## Architecture: Provider Pattern
We will implement a provider-based abstraction to handle the differences between local inference, cloud profiles (Bedrock/Azure), and standard API keys.

### 1. The Unified Interface
All integrations must satisfy a common interface in `internal/llm`:
- `Generate(ctx, request) -> Response`
- `Stream(ctx, request) -> Channel` (Optional for real-time UI)

### 2. Supported Categories
The system should support three categories of providers:
- **Standard API**: Standard HTTP calls to OpenAI, Anthropic, etc., requiring an API Key.
- **Cloud Inference Profiles**: Integration with AWS Bedrock and Azure Foundry, which allow the user to use high-powered models within their own cloud accounts/tenants.
- **Local Bridge**: Support for local inference engines (e.g., Ollama) via a standard OpenAI-compatible local endpoint.

## Configuration Requirements
The configuration must allow users to select an active provider and provide necessary credentials:
- `provider_type`: [openai, anthropic, bedrock, azure, ollama]
- `api_key`: (for 3rd party services)
- `inference_profile`: (specific for AWS Bedrock / Azure Foundry)
- `model_id`: The specific model identifier.

## Data Flow & Context Management

### 1. Just-In-Time (JIT) Context Gathering
To preserve privacy and manage token costs:
- The system should not send an entire database dump to the LLM.
- When a "Work" is performed (e.g., "Summarize my day"), a **Context Fetcher** identifies relevant records in SQLite based on dates or tags and constructs a focused prompt.

### 2. Prompt Templates
Prompts are defined as static templates with placeholders:
- `templates/` directory contains YAML or JSON files.
- Example: `"Please summarize the following notes from {{date}}: {{content}}"`
- This allows users to tweak the "personality" of the interaction without modifying the core code.

### 3. Handling Latency and Timeouts
Because LLM calls can be slow (or fail):
- All LLM calls must be performed in an asynchronous task or a worker thread.
- The UI/CLI should show a "Processing" state or use a background job status.
- Standard timeouts (e.g., 60s) must be enforced to prevent hanging connections.

## Error Handling & Fallbacks
1. **Partial Success**: If an LLM is unavailable, the system should still save the raw data and provide a "Retry" option for the AI operation later.
2. **Provider Failover** (Optional): Ability to define a secondary provider if the primary fails.
3. **Validation**: The output of any LLM call must be validated before being persisted as an "official" record in the system.

## Roadmap Checklist
- [ ] Define `llm_provider` interface in `internal/llm`.
- [ ] Implement standard OpenAI/Anthropic providers.
- [ ] Implement AWS Bedrock / Azure Foundry bridge.
- [ ] Implement Context Fetcher for automated prompt building.
- [ ] Add configuration UI/CLI for provider selection and key management.