## [Unreleased]

### Added

- Unified channel key selection into one eight-strategy selector, retaining weighted sticky and weighted failover settings. Added opt-in per-key retry coverage and a warning when the configured count may miss keys; existing channels now obey configured retry counts unless this option is enabled.
- Added checksum-verified in-app updates, recent version history, and rollback with process-supervisor restart support for supported release and Docker deployments.
- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.
- NeuralWatt channels now show the elapsed monthly energy window marker on the kWh bar and an estimated period quota, matching the other windowed providers.
- Allowed Fenno channels to opt into Codex-style Responses defaults; the option remains disabled by default.
- Added Codex/OpenAI `ultrafast` service-tier forwarding, request tracking, and display support with independent tier prices.

### Fixed

- Preserved Fast/Ultrafast prices, provider errors, and original Responses events with delivery-aware stream accounting; stopped blocked pass-through producers safely on conversion failure.
- Preserved Vertex key project/region bindings, aliases, and weights across edits, disabled-key deletion, and model-discovery fallback.
- Preserved configured `usage.cost` injection in automatically forwarded Responses JSON and streams while retaining provider event identities and extension fields.
- Preserved Fast/Ultrafast tracking and canonical session headers over native Responses WebSocket connections; retained failure metadata and tool-search output across pre-content retry handling.
- Applied Fast/Ultrafast billing rules to model-library fallback prices consistently in API usage and stored costs.
- Applied Codex User-Agent opt-in, client fingerprint filtering, and stable session headers to the new native Images path without changing its JSON protocol.
- Kept multi-key retry budgets stable while disabled credentials are refreshed, and kept single-key diagnostics pinned to the selected key across retries.
- Preserved original client headers during Codex fingerprint filtering so failover to other providers retains their attribution headers.
- Aligned missing Fast/Ultrafast prices for published GPT-6/GPT-5.6 models with official API ratios: Astra and GPT-6.1 Sol Ultrafast now falls back to 3× Fast or 6× base. Recognized OpenAI's `fast` alias in billing, catalog imports, and request display; preserved explicit saved prices and historical costs.
- Added input-context pricing for all token items, including service-tier, schedule, and cache-write variant prices. Catalog imports now preserve context thresholds and supported OpenAI Fast/Ultrafast long-context rates. Re-import and save existing fixed prices to enable it; historical usage is unchanged.
- Preserved Responses Lite tool definitions across Responses, Codex relays, and Compact forwarding; unsupported conversion to other protocols now returns an explicit error instead of silently dropping tools.
- Prevented Chat streaming responses from appending a successful finish after an explicit upstream failure, cancellation, or incomplete outcome.
- Kept completed stream usage and costs after trailing disconnects without duplicate success counts or a trailing timeout error.
- Applied project permissions to post-login return URLs and channel-edit permissions to execution key indices; kept performance metrics for unattributed usage.

- Kept password and OIDC login destinations within the user's project permissions when sidebar entries are hidden.
- Excluded diagnostic requests from new single-channel and retry load-balancing counters, while counting production API key rotation attempts.

- Removed the obsolete global Codex image main model selector after adopting native Images endpoints. Legacy settings remain accepted but no longer affect generation or editing; relays must support native Images. Native Responses image tools remain available.

- Preserved separate channel/key disable policies, temporary durations, Retry-After, and explicit disabled settings when integrating global auto-disable rules.

- Returned complete, API-key-scoped Codex model catalogs for `/v1/models?client_version=…`, preserving model instructions and context metadata while keeping ordinary OpenAI discovery unchanged.
- Kept the **Include Beta versions** update-check option enabled while switching between System Settings tabs.
- Preserved usage and cost records when clients disconnect after a streaming response has already completed, without leaving a stale cancellation error on the completed execution.
- Allowed Codex scheduled automations to start when the app sends an `automation_update` output without a `call_id`.

### Fixed

- Dashboard analytics no longer rescan the full usage history for every widget: request, cost, and token widgets share one grouped query, expensive 30-day aggregates are cached, SQLite uses window functions instead of correlated `MAX(id)` scans, and off-screen sections load only when they approach the viewport.
- Personal API Key names can be reused by different creators in a project; creating or renaming a key rejects collisions with non-personal keys visible to its creator. Duplicate checks run after create authorization.
- Synthetic weekly credit amounts that parse to NaN or infinity are now ignored instead of leaking into the period money fields and breaking quota persistence.

### Removed

- Wafer quota monitor removed — Wafer is PAYG-only. Legacy wafer quota rows are purged at startup before schema migration when automatic migration is enabled.

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
