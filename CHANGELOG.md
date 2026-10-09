## [Unreleased]

### Added

- Added checksum-verified in-app updates, recent version history, and rollback with process-supervisor restart support for supported release and Docker deployments.
- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.
- NeuralWatt channels now show the elapsed monthly energy window marker on the kWh bar and an estimated period quota, matching the other windowed providers.
- Allowed Fenno channels to opt into Codex-style Responses defaults; the option remains disabled by default.
- Added Codex/OpenAI `ultrafast` service-tier forwarding, request tracking, and display support, with billing defaulting to twice the effective Fast price when no exact Ultrafast price is configured.

### Fixed

- Preserved Responses Lite tool definitions across Responses, Codex relays, and Compact forwarding; unsupported conversion to other protocols now returns an explicit error instead of silently dropping tools.
- Prevented Chat streaming responses from appending a successful finish after an explicit upstream failure, cancellation, or incomplete outcome.
- Kept completed stream usage and costs after trailing disconnects without duplicate success counts or a trailing timeout error.
- Applied project permissions to post-login return URLs and channel-edit permissions to execution key indices; kept performance metrics for unattributed usage.

- Kept password and OIDC login destinations within the user's project permissions when sidebar entries are hidden.
- Excluded diagnostic requests from new single-channel and retry load-balancing counters, while counting production API key rotation attempts.

- Added global Codex image main model selection in Models → Settings, defaulting to `gpt-6-astra` while preserving the requested image tool model. This replaces per-channel overrides; users with a previous custom value must select it again globally. Saved changes apply to new requests without a restart.

- Preserved separate channel/key disable policies, temporary durations, Retry-After, and explicit disabled settings when integrating global auto-disable rules.

- Returned complete, API-key-scoped Codex model catalogs for `/v1/models?client_version=…`, preserving model instructions and context metadata while keeping ordinary OpenAI discovery unchanged.
- Kept the **Include Beta versions** update-check option enabled while switching between System Settings tabs.
- Preserved usage and cost records when clients disconnect after a streaming response has already completed, without leaving a stale cancellation error on the completed execution.
- Allowed Codex scheduled automations to start when the app sends an `automation_update` output without a `call_id`.

### Fixed

- Personal API Key names can be reused by different creators in a project; creating or renaming a key rejects collisions with non-personal keys visible to its creator. Duplicate checks run after create authorization.

v0.4.0

- Introduced thread-aware tracing with zero-SDK integration and configurable trace headers
- Added trace visualization interface for following end-to-end conversations
- Added configurable data storage policies to keep or trim trace payloads based on compliance needs

v0.3.0

- Launched project workspace management with per-project API keys and dashboards
- Improved project-scoped permissions and usage insights

v0.2.0

- Added multimodal image generation via chat completions and image_generation tools
- Documented provider support and sample usage for image workflows

v0.1.1

- Add OpenRouter outbound transformer
- Support Google Gemini OpenAI compatible API
