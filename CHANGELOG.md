## [Unreleased]

### Added

- Added checksum-verified in-app updates, recent version history, and rollback with process-supervisor restart support for supported release and Docker deployments.
- Added per-channel API key rules with status/keyword matching, configurable error thresholds, temporary auto-recovery, and permanent disable/delete actions.
- Allowed Fenno channels to opt into Codex-style Responses defaults; the option remains disabled by default.
- Added Codex/OpenAI `ultrafast` service-tier forwarding, request tracking, and display support, with billing defaulting to twice the effective Fast price when no exact Ultrafast price is configured.

### Fixed

- Kept the **Include Beta versions** update-check option enabled while switching between System Settings tabs.
- Preserved usage and cost records when clients disconnect after a streaming response has already completed, without leaving a stale cancellation error on the completed execution.
- Allowed Codex scheduled automations to start when the app sends an `automation_update` output without a `call_id`.

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
