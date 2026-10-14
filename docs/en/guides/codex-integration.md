# Codex Integration Guide

---

## Overview
AxonHub can act as a drop-in replacement for OpenAI endpoints, letting Codex connect through your own infrastructure. This guide explains how to configure Codex and how to combine it with AxonHub model profiles for flexible routing.

### Key Points
- AxonHub performs AI protocol/format transformation. You can configure multiple upstream channels (providers) and expose a single OpenAI-compatible interface for Codex.
- You can aggregate Codex requests from the same conversation by enabling `server.trace.codex_trace_enabled` (uses `Session_id`) or adding extra headers via `server.trace.extra_trace_headers`.
- Codex scheduled automations are supported even when the app starts them with an `automation_update` output that has no `call_id`.

### Codex request headers

Codex Responses, native Images, and alpha-search requests use AxonHub's default Codex User-Agent unless the channel's User-Agent pass-through switch is enabled. Client attribution headers (`Http-Referer`, `X-Title`, `X-Platform`, `X-Zcode-*`, and `X-Os-*`) are excluded from these outbound requests. The original headers remain available if a retry switches to a different provider that consumes them. Explicit Responses Lite selection and session identity are preserved. Native Images keeps its JSON protocol while normalizing the final session header.

### Choose an entrypoint

Use the `/codex` compatibility entrypoint for Codex personal access token (PAT) authentication and a live native model catalog. Existing custom-provider configurations can continue using `/v1` with an API key and the bundled catalog described below. These are separate configurations; the `/codex` switch and catalog source are not required for `/v1`.

### PAT prerequisites and AxonHub setup
- AxonHub instance reachable from your development machine.
- Valid AxonHub API key with project access.
- Access to Codex (OpenAI compatible) application.
- An existing Codex channel in AxonHub. The channel supplies the upstream base URL, credentials, and HTTP transport.

Before configuring Codex, open the **Codex Compatibility** settings in AxonHub:

1. Enable the compatibility switch.
2. Select the Codex channel to use for the native model catalog.
3. Use the channel's **Test** action to verify that its `/models` endpoint is reachable. Testing does not save the setting, enable the channel, or change its status.

The switch is off by default. Saving an enabled configuration requires a selected existing Codex channel. The catalog test can be run while the switch is off, and a failed test does not prevent saving. Catalog results are filtered to the models visible to the caller; this does not authorize a manually selected model, which is still checked by the normal inference permission path.

### Configure PAT authentication and `/codex` routes
1. Create `${HOME}/.codex/auth.json` with the AxonHub API key as the Codex personal access token. Keep the value in this file; do not use `CODEX_ACCESS_TOKEN`, `login --with-access-token`, or an `api_key` auth mode:
   ```json
   {"personal_access_token":"<your-axonhub-api-key>"}
   ```
2. Edit `${HOME}/.codex/config.toml` and point the provider and auth API at the same `/codex` compatibility surface:
   ```toml
   model = "gpt-5"
   model_provider = "openai"
   openai_base_url = "https://gateway.example/codex"
   chatgpt_base_url = "https://gateway.example/codex"

   web_search = "live"
   suppress_unstable_features_warning = true

   [features]
   fast_mode = true
   context_management = true
   ```
3. Set the required auth API base in the same shell that starts Codex:
   ```bash
   export CODEX_AUTHAPI_BASE_URL="https://gateway.example/codex"
   ```
   Replace the example host with your AxonHub address. Do not set a separate catalog URL; the `/codex` base is used for whoami, models, and Responses requests.
4. Restart Codex to apply the configuration.

The compatibility surface used by this guide is:

- `GET /codex/v1/user-auth-credential/whoami` — local API-key identity.
- `GET /codex/api/codex/accounts/check` — account metadata for Codex account selection.
- `GET /codex/models` — selected-channel native catalog, intersected with caller-visible model IDs.
- `POST /codex/images/generations` — native image generation through ordinary model/channel routing.
- `POST /codex/images/edits` — native image editing through ordinary model/channel routing.
- `POST /codex/responses` — Responses HTTP/SSE inference.
- `GET /codex/responses` — Responses WebSocket inference.

The `/codex/models` endpoint fetches the selected channel's catalog and keeps only exact matches for caller-visible model IDs. It preserves upstream metadata and visibility, without synthesizing entries for aliases or unknown models. Concurrent matching fetches are shared, but there is no persistent catalog cache or bundled fallback; an unavailable catalog source can make discovery fail even when inference channels are available. It uses the selected channel's credentials, not the caller's API key.

### Existing `/v1` custom-provider configuration

For an API-key custom provider, configure `${HOME}/.codex/config.toml` separately:

```toml
model = "gpt-5"
model_provider = "axonhub-responses"

[model_providers.axonhub-responses]
name = "AxonHub using Responses"
base_url = "http://127.0.0.1:8090/v1"
env_key = "AXONHUB_API_KEY"
wire_api = "responses"
query_params = {}
```

Export the key in the shell that starts Codex, then restart Codex:

```bash
export AXONHUB_API_KEY="<your-axonhub-api-key>"
```

#### `/v1` model catalog refresh

Codex discovery requests `GET /v1/models?client_version=0.153.4`. AxonHub treats a non-empty `client_version` query parameter as a request for the Codex `{"models":[{"slug":"…",…}]}` catalog. No extra AxonHub setting is required. Ordinary `/v1/models` requests still return the OpenAI `data/id` format; `include=all` alone does not select Codex format.

The catalog contains only models visible to the current API key, using the same project/profile and model-list settings as ordinary discovery. Enabled models are shown in the Codex picker, including entries hidden by the bundled upstream catalog. The endpoint does not forward your key to OpenAI or fetch a remote catalog per request.

Known models use the complete bundled Codex **0.153.4** descriptors, preserving instructions, reasoning options, tools, and service tiers. The client's longest-prefix and single provider-namespace lookup also applies to dated or prefixed names. For example, Astra retains a default `context_window` of **272000** and a `max_context_window` of **872000**; the maximum is not the default. This versioned snapshot is separate from AxonHub's general model/pricing catalog sync.

Unrecognized models use that Codex version's generic fallback instructions and metadata. An explicitly configured model card's positive context limit replaces the generic **272000** fallback; otherwise this is a client default, not a verified provider capacity. Configure the actual limit for custom models. Reasoning levels and service tiers are not guessed from other models.

To inspect the response with your existing key:

```bash
curl -fsS 'http://127.0.0.1:8090/v1/models?client_version=0.153.4' \
  -H "Authorization: Bearer $AXONHUB_API_KEY"
```

This fixes the response format when Codex makes a discovery request; it does not force the client to refresh. Codex 0.153.4 only attempts remote refresh for Codex-backend or command-auth configurations, not ordinary custom providers using only an API key. Explicitly selecting a model and sending chat requests remains a separate path. Older or newer clients with different catalog contracts may need a matching snapshot update.

#### Trace aggregation by conversation (important)
Enable the built-in Codex trace extraction to reuse the `Session_id` header as the trace ID:

```yaml
server:
  trace:
    codex_trace_enabled: true
```

If Codex sends a different stable conversation identifier header (for example `Conversation_id`), you can configure AxonHub to use it as a fallback trace header in `config.yml`:

```yaml
server:
  trace:
    extra_trace_headers:
      - Conversation_id
```

**Note**: Enabling this also ensures that requests from the same trace are prioritized to be sent to the same upstream channel, significantly improving provider-side cache hit rates (e.g., Anthropic Prompt Caching).

#### Testing
- Send a sample prompt; PAT configuration should use `/codex/responses`, while the API-key custom provider should use `/v1/responses`.
- For PAT configuration, confirm that the model list loads from `/codex/models` and that a suitable inference channel is available. The catalog source and inference channel need not be the same.
- If your Codex client uses account discovery, confirm `/codex/api/codex/accounts/check` is reachable with the same API key.
- Image generation and editing use `/codex/images/generations` and `/codex/images/edits`; the routed inference channel—not necessarily the catalog channel—must expose the corresponding native image endpoint and model.
- Enable tracing in AxonHub to inspect prompts, responses, and latency.

The current version uses the ordinary Responses path for remote compaction-v2: a `compaction_trigger` is sent to `/codex/responses`, and the returned compaction item can be used in a later Responses request. It does not implement the history/notes APIs or a standalone `/codex/responses/compact` route; those unsupported paths return JSON 404. `context_management` enables Codex's Responses compaction flow, while history notes remain part of future compatibility work. `fast_mode` selects the Responses `service_tier` requested by the client; the upstream model catalog and response determine whether that tier is available. There is no separate `ultrafast` endpoint or local capability probe. Existing `/v1` routes are unchanged.

### Working with Model Profiles
AxonHub model profiles remap incoming model names to provider-specific equivalents:
- Create a profile in the AxonHub console and add mapping rules (exact name or regex).
- Assign the profile to your API key.
- Switch active profiles to alter Codex behavior without changing tool settings.

<table>
  <tr align="center">
    <td align="center">
      <a href="../../screenshots/axonhub-profiles.png">
        <img src="../../screenshots/axonhub-profiles.png" alt="Model Profiles" width="250"/>
      </a>
      <br/>
      Model Profiles
    </td>
  </tr>
</table>

#### Example
- Request `gpt-4` → mapped to `deepseek-reasoner` for getting more accurate responses.
- Request `gpt-3.5-turbo` → mapped to `deepseek-chat` for reducing costs.

### Troubleshooting
- **Codex cannot see its tools through a relay**: upgrade to a build containing the `additional_tools` forwarding fix. Responses, Codex, and Compact forwarding preserve these input items regardless of hostname, without an extra switch. A target that does not support them may reject the request. Converting such a request to a non-Responses protocol returns HTTP 400 instead of silently dropping tools, and does not retry or switch channels.
- **API-key custom-provider authentication fails**: ensure `AXONHUB_API_KEY` is exported in the same shell session that launches Codex.
- **PAT authentication fails**: ensure `${HOME}/.codex/auth.json` contains a valid AxonHub API key in `personal_access_token`, and that `CODEX_AUTHAPI_BASE_URL` points to the same `/codex` base as `openai_base_url` and `chatgpt_base_url`.
- **The `/codex` model list is empty or unavailable**: confirm that the compatibility switch is enabled, a Codex channel is selected, and the channel's `/models` endpoint passes the settings-page test. Model IDs must exactly match the caller-visible IDs. Catalog filtering is not a substitute for inference authorization. For `/v1`, see the bundled-catalog and client-refresh limits above.
- **Unexpected model responses**: review active profile mappings in the AxonHub console; disable or adjust rules if necessary.
- **Scheduled automation reports that `function_call_output` requires `call_id`**: upgrade AxonHub to a build containing the Codex automation bootstrap compatibility fix.

---

## Provider Quota Tracking

AxonHub automatically tracks quota usage for Codex provider channels, displaying the current status with battery icons in the interface.

### How It Works

- **Automatic Polling**: AxonHub periodically polls your Codex account to check quota status
- **Storage**: Quota data is stored in the database and updated based on the configured check interval
- **Visual Indicators**: Battery icons show your remaining quota at a glance.

### Quota Windows

Codex uses multiple quota windows:
- **Primary window**: Main usage limit with a configurable duration (e.g., 5 hours, 1 day)
- **Secondary window**: Optional secondary usage limit with its own duration and reset schedule

The system shows both window percentages including the primary window duration and reset time.

### Configuration

Adjust the quota check interval in `config.yml`:

```yaml
provider_quota:
  check_interval: "5m"           # Check every 5 minutes (default)
```

Or via environment variable:

```bash
export AXONHUB_PROVIDER_QUOTA_CHECK_INTERVAL="30m"
```

Supported intervals: `1m`, `2m`, `3m`, `4m`, `5m`, `6m`, `10m`, `12m`, `15m`, `20m`, `30m`, `1h`, `2h`, etc.

**Recommendations:**
- **Development**: Use shorter intervals (e.g., `5m`) for quick feedback
- **Production**: Use `5m` (default) for timely quota detection; increase to `10m` or `20m` to reduce API calls

### Refreshing Quota Data

You can manually trigger a quota refresh by clicking the refresh icon in the quota status popover.

### Viewing Quota Status

1. Look for the battery icon next to the settings gear in the header
2. Click the battery icon to view detailed quota information including:
   - Primary window usage percentage and duration
   - Primary window reset time
   - Plan type (if available)
   - Secondary window usage (if configured)

### Native image generation and editing

Codex Images requests now use native `/images/generations` and `/images/edits` endpoints with the requested image model, such as `gpt-image-2`. No outer text model is selected. The default image main model control has been removed from Models → Settings; old global and channel values remain accepted for API compatibility but have no effect.

Your upstream must support native Images endpoints. A service that only supports Responses image tools can no longer serve Images API requests through the removed bridge. Non-Codex channels require existing native image support or an explicit Images endpoint. Images requests are non-streaming; `stream: true` is rejected. Native `/responses` requests with `image_generation` tools keep their existing behavior. See [Images API](../api-reference/image-generation.md) for parameters and endpoint configuration.

### Related Documentation
- [Tracing Guide](tracing.md)
- [OpenAI API](../api-reference/openai-api.md)
- README sections on [Usage Guide](../../../README.en-US.md#usage-guide)
