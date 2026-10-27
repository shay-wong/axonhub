# Channel Configuration Guide

This guide explains how to configure AI provider channels (like OpenAI, Anthropic, DeepSeek) in AxonHub.

## What is a Channel?

A **channel** is AxonHub's connection to an AI provider. Think of it as a "provider connection line" — each channel connects to one AI service (like OpenAI, Claude, or DeepSeek).

Through channels, you can:
- Connect to multiple AI providers simultaneously
- Set up model name conversion rules
- Enable or disable providers
- Configure multiple API Keys for load balancing

## Where Channel Model Mapping Fits in the Request Flow

Channel model mapping is the **last** step in a three-layer pipeline. For the full picture, see [Request Processing Guide](../getting-started/request-processing.md#core-concept-three-layers-of-model-settings).

In short: **API Key Profile renames → Model Association selects channel → Channel renames → Send upstream**

## Creating a Channel

### Basic Steps

1. Go to AxonHub management interface → **Channel Management**
2. Click **New Channel**
3. Fill in basic information:
   - **Name**: Give your channel a name (e.g., "OpenAI Main", "DeepSeek Backup")
   - **Type**: Select provider type (OpenAI, Anthropic, DeepSeek, etc.)
   - **Base URL**: API address (usually use the default)
   - **API Key**: The key from your provider

### Configuration Examples

**OpenAI Channel:**

| Field | Value |
|-------|-------|
| Name | OpenAI Main |
| Type | openai |
| Base URL | https://api.openai.com/v1 |
| API Key | sk-your-openai-key |
| Supported Models | gpt-4o, gpt-4o-mini, gpt-5 |

**DeepSeek Channel:**

| Field | Value |
|-------|-------|
| Name | DeepSeek China |
| Type | deepseek |
| Base URL | https://api.deepseek.com/v1 |
| API Key | sk-your-deepseek-key |
| Supported Models | deepseek-chat, deepseek-reasoner |

## Multiple API Keys

Multiple API keys can share a channel. Requests are assigned using the selected strategy.

Simply add all keys in the API Keys field, one per line:
```
sk-key-1
sk-key-2
sk-key-3
```

### Key selection strategies

The request log's Key column and execution details prefer the alias recorded at execution time, falling back to `keyN` when absent. Details retain the safe key suffix. Viewing key identity requires channel-write permission. Historical records without an alias snapshot are not matched against today's key order; adding or changing an alias does not rewrite past records.

The channel editor has one strategy selector, stored as `settings.apiKeyStrategy`:

| Strategy | Behavior |
|---|---|
| `sticky` (default) | Keep the same key for a Trace when possible; choose randomly without a Trace |
| `weighted_sticky` | Distribute new Traces by weight and retain affinity; use weighted random selection without a Trace |
| `failover` | Prefer the highest available weight; break ties by Trace or randomly; prefer higher-weight keys again after recovery |
| `random` | Choose uniformly on every selection without Trace affinity |
| `round_robin` | Follow list order, switching after N calls, including failures |
| `round_robin_success` | Switch after N successes; failures do not advance the success counter |
| `priority` | Prefer the first available key in list order, including after its recovery |
| `fixed` | Keep the current key until disabled or removed, then move forward; recovered earlier keys do not take over |

`settings.apiKeyRoundRobinSwitchAfter` sets N, with a minimum of 1. Weights only affect `weighted_sticky` and `failover`. Sticky, round-robin, success-based round-robin, and fixed selection state survives channel cache reloads, but not process restarts.

Legacy `settings.apiKeySelectionStrategy` remains readable. Without the new field, `trace_sticky` maps to `sticky`, while the other two legacy strategies keep their meaning. Saving in the editor writes the new field and clears the legacy field. All strategies use structured credentials, disabled-key filtering, and request-local failed-key exclusions. Temporary substitutes for fixed/success-based selection do not move the cursor used by subsequent requests.

### Retry per API key

Leave **Single-Channel Retries** empty to inherit the global count; 0 disables ordinary same-channel retries. The initial attempt is not a retry: five available keys require at least four retries to have a chance to try each key. With **Retry per API key** off, the editor warns when the budget cannot cover all keys. If the global setting is inaccessible, it reports that the count cannot be determined instead of guessing.

**Retry per API key** (`settings.retryPerAPIKey`) defaults to off, including for existing channels without this field. When enabled, the effective budget is `max(channel or global retry count, available key count - 1)`, so even a channel count of 0 permits attempts with other keys. Unlike the previous fork's automatic budget increase, enable this option after upgrading to retain the opportunity to try every available key.

Both modes exclude keys that failed within the current request and stop on success; exhausting the keys does not restart the cycle. Disabling retries globally overrides this option. Cancellation, non-retryable errors, and streams already delivering output do not restart requests to visit more keys. Connection failures switch channels under the existing policy. See [Load Balancing](load-balance.md) for more detail.

Manually adding `429` to a channel's **Retryable Status Codes** does not reuse a key that already failed in the current request: other available keys are tried first, then the request switches channels when keys or retry budget run out. Explicit `429` enables ordinary same-channel retries only on paths where key rotation cannot handle the failure.

Gemini Vertex credentials pair each API key with a project and region. Editing or reordering a key preserves its existing alias and weight; keeping the last key when deleting disabled keys also keeps its project binding. Model discovery skips disabled keys, tries the next key only after failure, and stops after the first success. Project-bound discovery uses the matching key's project and region; the provider's public catalog may be used first.

## Auto-disable policy compatibility

Global rules and the fork's separate channel/API key status-code policies are configured independently in retry settings. Custom channel rules run first, followed by global rules; a matching rule owns the failure even before its threshold is reached. Unmatched failures fall back to the separate status-code policies, with API key rules taking precedence over channel rules. Channel mode **Off** skips all automatic disabling.

Upgrading preserves existing temporary durations and the 429 **Retry-After** option. Network failures do not disable individual keys; the separate channel policy can still handle them. Legacy policy migration does not re-enable an explicitly disabled policy.

Custom and global rules count failures independently for each key. Other errors do not reset a rule's count; a successful request on that key clears its counts. Success on another key in the same channel does not affect them.

## Codex-style Responses

`codex` and `fenno` channels can opt into **Use Codex-style Responses** under **Settings → Transform Options**. The option is disabled by default.

When enabled, AxonHub fills missing Codex-native Responses defaults, including `service_tier: priority`, while preserving values explicitly sent by the caller. Image requests are not changed. Leave the option disabled when the upstream should receive the caller's original Responses semantics unchanged.

An explicit `service_tier: ultrafast` is preserved whether this option is enabled or disabled. As of 2026-10-13, [OpenAI lists GPT-6 Astra and GPT-6.1 Sol for Ultrafast](https://developers.openai.com/api/docs/guides/ultrafast-mode). Unsupported models or accounts receive the upstream error unchanged. See [service-tier pricing](cost-tracking.md#service-tier-prices) for billing rules and limitations.

## Model Renaming

AxonHub provides multiple mechanisms to rename or alias models at the channel level. When a request arrives, the channel resolves the request model to the actual upstream model through the following priority chain:

1. **Direct match** — the request model is directly in the Supported Models list
2. **Extra Model Prefix** — adds a prefix alias for all supported models
3. **Auto-Trimmed Model Prefixes** — strips known prefixes from supported models to create trimmed aliases
4. **Model Mappings** — explicit `from → to` alias pairs

> **Note**: If multiple mechanisms produce the same request model name, the first match wins (based on the order above).

### Model Mappings

**When do you need model mappings?**

When you want the client to use one name, but send a different name to the upstream provider.

**Common Scenarios:**

1. **Client uses simplified names**: Client requests `gpt-4`, but OpenAI receives `gpt-4o`
2. **Unify model names across channels**: Both `claude-sonnet` and `gpt-4` point to the same actual model
3. **Legacy compatibility**: Old model names automatically map to newer versions

**How to Configure:**

In the channel's **Settings** → **Model Mappings**, add `from → to` pairs:

| From (Client Requests) | To (Sent to Provider) |
|------------------------|----------------------|
| gpt-4o-mini | gpt-4o |
| claude-3-sonnet | claude-3.5-sonnet |

**Note**: The target model (`to`) must be in the Supported Models list. If the target model is not supported, the mapping is silently ignored.

### Extra Model Prefix

Adds a **prefixed alias** for every model in the Supported Models list, allowing clients to request models with or without the prefix.

**Use case**: You want to namespace all models in a channel under a common prefix (e.g., `deepseek/`).

**Example:**
- Supported Models: `deepseek-chat`, `deepseek-reasoner`
- Extra Model Prefix: `deepseek`

The channel now accepts **both** of the following request formats:
- `deepseek-chat` → sends `deepseek-chat` upstream
- `deepseek/deepseek-chat` → sends `deepseek-chat` upstream

This is useful when you want to differentiate models from different channels that share the same name — clients can prefix the model with the channel's namespace.

### Auto-Trimmed Model Prefixes

Automatically **strips specified prefixes** from model names in the Supported Models list, creating trimmed aliases. This is the inverse of Extra Model Prefix.

**Use case**: Providers like OpenRouter or SiliconFlow add vendor prefixes to model names (e.g., `openai/gpt-5.4`). You want clients to request using the short name `gpt-5.4` without manually creating mappings for every model.

**Example:**
- Supported Models: `openai/gpt-5.4`, `anthropic/claude-sonnet-4`, `deepseek-ai/deepseek-chat`
- Auto-Trimmed Model Prefixes: `openai`, `anthropic`, `deepseek-ai`

The channel now accepts both the original and trimmed names:
- `gpt-5.4` → sends `openai/gpt-5.4` upstream
- `claude-sonnet-4` → sends `anthropic/claude-sonnet-4` upstream
- `deepseek-chat` → sends `deepseek-ai/deepseek-chat` upstream
- `openai/gpt-5.4` → still works as a direct match

> **Tip**: This is the recommended approach for providers that use vendor-prefixed model IDs. It enables batch model ID rewriting without having to create individual model mappings.

### Lowercase Model Name

When enabled, model name matching keys are converted to lowercase. Enable when channel-provided model names contain uppercase letters, to balance load with other lowercase model names. The actual model name sent to the provider retains its original casing.

**Example**: Converts `GLM-5.1` to `glm-5.1`, allowing the channel to share load with other channels that provide `glm-5.1`.

> **Note**: When enabled, model names that differ only in case (e.g., `GPT-4` and `gpt-4`) are treated as the same model. In case of collision, the highest-priority entry wins (direct > auto_trim > mapping > prefix).

### Visibility Controls

Two options control which model names are exposed in the model list (e.g., when clients call the `/v1/models` endpoint):

| Option | Effect |
|--------|--------|
| **Hide Original Models** | Hides the original (direct) model names. Only transformed names (from prefix, auto-trim, or mapping) are shown. |
| **Hide Mapped Models** | Hides the `from` names of model mappings. Only the original model names are shown. |

**Example — Hide Original Models:**
- Supported Models: `openai/gpt-5.4`
- Auto-Trimmed Prefixes: `openai`
- Hide Original Models: enabled

The `/v1/models` response only shows `gpt-5.4`, not `openai/gpt-5.4`. Both names still work for requests.

**Example — Hide Mapped Models:**
- Supported Models: `gpt-4o`
- Model Mapping: `gpt-4` → `gpt-4o`
- Hide Mapped Models: enabled

The `/v1/models` response shows `gpt-4o` but hides `gpt-4`. Both names still work for requests.

## Testing and Enabling Channels

### Test Connection

Before enabling a channel, test the connection:

1. Find your channel in the channel list
2. Click the **Test** button
3. Wait for the result
4. If successful, proceed to enable

### Enable Channel

After testing passes, click **Enable**. The channel status changes to **Active** and can now receive requests.

## Base URL Special Configuration

### Default URLs

| Provider | Default Base URL |
|----------|-----------------|
| OpenAI | `https://api.openai.com/v1` |
| Anthropic | `https://api.anthropic.com` |
| DeepSeek | `https://api.deepseek.com/v1` |
| Gemini | `https://generativelanguage.googleapis.com/v1beta` |

### Custom URLs

For proxies or private deployments, you can modify the Base URL.

**Disable automatic version appending**: Add `#` at the end
```
https://custom-proxy.example.com/api#
# Actual request: /api/messages (no /v1 added)
```

**Fully raw mode**: Add `##` at the end
```
https://custom-gateway.example.com/api##
# Actual request: /api (no version or endpoint added)
```

## Multi-Protocol Endpoints and Model Protocol Overrides

A single channel can host multiple outbound endpoints covering chat, responses, messages and other protocols, so different clients connect with their native protocol.

### Endpoint Configuration

Manage endpoints in the channel **Endpoints** dialog:

- Each endpoint consists of `api_format`, an optional `base_url` (inherits the channel Base URL when empty), and an optional `path` (replaces the protocol's default path; version appending is skipped when set).
- Each channel type ships with built-in default endpoints (e.g. zhipu provides `openai/chat_completions`, `zhipu_anthropic` provides `anthropic/messages`); custom endpoints can add other formats or override same-named ones.

### Endpoint Auto-Detection

The **Endpoints** dialog can probe the upstream for the three relay protocols (`openai/chat_completions`, `openai/responses`, `anthropic/messages`), so a channel that supports all three can pass through almost every client protocol natively.

- Click **Auto-detect** in the **Current Endpoints** section. The backend sends one lightweight probe per protocol using the channel Base URL, credentials and default test model.
- Detected protocols are added to the custom endpoint list. They are not saved automatically — review the list and press **Save** to persist.
- A route is reported as supported when the upstream replies with anything other than `404`/`405`; `401`/`403` are reported as an authentication problem, and `5xx` or connection failures as errors.
- The channel list **Endpoints** column summarizes the configured relay protocols as four icons, including the native Gemini Contents endpoint: colored when configured, dimmed otherwise.

### Model Protocol Overrides (ModelProtocols)

Force the available outbound protocols for a single model in the **Model Protocol Overrides** block:

```json
{ "model": "glm-5.3-flash", "apiFormats": ["openai/responses", "openai/chat_completions"], "enabled": true }
```

- `apiFormats` expresses priority in configuration order; when the client's inbound protocol is in the list it connects directly, otherwise the first listed protocol is used through the unified conversion pipeline.
- Enabled overrides may only reference `api_format`s the channel already provides (default or custom endpoints), validated on save; overrides are cleaned up automatically when a model is removed from the channel's supported list.
- Endpoints and protocol overrides are committed atomically in a single save.

### Responses HTTP and WebSocket endpoints

Responses has two distinct endpoint formats:

| `api_format` | Transport | Default request path |
|---|---|---|
| `openai/responses` | HTTP | `/v1/responses` |
| `openai/responses-ws` | WebSocket | `/v1/responses` |

Configure both formats on the same channel when the channel can serve both
transports. The endpoint format is the source of truth for the outbound
transport. A WebSocket scheme in a legacy endpoint is normalized to
`openai/responses-ws`; a legacy HTTP Responses endpoint remains
`openai/responses`. Existing explicit HTTP-only and WebSocket-only endpoint
sets remain single-mode until they are saved with a new configuration.

For a request whose inbound format matches an allowed endpoint on the selected
channel, AxonHub selects that exact format first. If no same-format endpoint is
available, it selects the first allowed endpoint using the configured endpoint
order and converts the Responses request, response, or event envelope between
HTTP and WebSocket. This fallback does not change the channel ranking or make
an Images or Models request a Responses request.

For model protocol overrides, list the exact formats the model may use:

```json
{
  "model": "gpt-5",
  "apiFormats": ["openai/responses", "openai/responses-ws"],
  "enabled": true
}
```

The override may reference only endpoint formats already available on that
channel. Use the same channel for a Responses continuation whenever possible.
If the selected transport or channel changes and AxonHub cannot certify the
complete prior input, the request is rejected before upstream dispatch rather
than sent with incomplete history.

### Responses continuation and bounded recovery

`previous_response_id` is bound to the channel, endpoint origin, model,
authentication identity, and WebSocket connection generation that produced it.
Native WebSocket continuation sends the next delta on its matching live
upstream session. A stale, missing, expired, or over-budget context cannot be
silently replayed on another endpoint or channel.

When the complete context is unavailable, AxonHub returns HTTP 400 or a
WebSocket error with code `previous_response_not_found` and asks the client to
resend the full input without `previous_response_id`. This recovery error does
not enter ordinary channel failover. One failed WebSocket lane does not close
unrelated lanes on the same downstream connection.

The current bounded defaults are 16 MiB for one retained HTTP record or stream
capture, 64 MiB for aggregate retained payload, and 128 MiB for one frame or
queued frame per downstream connection. Exceeding a retention budget removes
optional replay history, but keeps valid native response identity when the
provider session can continue. If a later turn needs history that was not
retained, the client must resend the full input.

**Example** (GLM Coding Plan channel, three endpoints + override):

| endpoint | api_format | base_url |
|---|---|---|
| 1 | `openai/chat_completions` | `https://open.bigmodel.cn/api/coding/paas/v4` |
| 2 | `openai/responses` | `https://open.bigmodel.cn/api/v1` |
| 3 | `anthropic/messages` | inherits channel Base URL (`https://open.bigmodel.cn/api/anthropic`) |

| Client | Inbound protocol | Selected outbound | Converted? |
|---|---|---|---|
| Claude Code | `anthropic/messages` | `anthropic/messages` | No |
| Codex | `openai/responses` | `openai/responses` | No |
| OpenAI SDK | `openai/chat_completions` | `openai/chat_completions` | No |
| Codex (override limited to chat) | `openai/responses` | `openai/chat_completions` | Yes |

### Provider Family Routing

Custom chat endpoints select the channel family's native transformer instead of the generic OpenAI one (the generic transformer assumes `/v1` and produces wrong URLs for non-`/v1` upstreams):

| Channel types | Transformer | URL version |
|---|---|---|
| zhipu / zai (incl. `*_anthropic`) | zai | v4 |
| xiaomi (incl. `xiaomi_anthropic`) | zai | v1 |
| doubao / volcengine (incl. `*_anthropic`) | doubao | v3 |

Family transformers also apply provider dialect handling: strip `metadata`, clamp `user_id` to 6–128 characters, and convert `reasoning_effort` to `thinking.type`.

## Reasoning Effort

Unified effort values: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`.

### Channel Mapping (ReasoningEffortMapping)

Configure `{from, to}` entries in the channel **Transform Options**; the first matching `from` wins. The mapping is applied once before outbound conversion and affects every client and every outbound protocol (chat / responses / messages), including the native effort marker in Anthropic metadata.

### Conversion Rules

- Anthropic messages: the client's native expression wins (`adaptive` / `budget_tokens` round-trip verbatim); explicit effort passes through unchanged (including `max` and `xhigh`); `minimal` is normalized to `low` (the only automatic conversion).
- OpenAI chat / responses: `reasoning_effort` / `reasoning.effort` pass through unchanged.
- If the upstream rejects a level, its original error is forwarded to the client; incompatible levels can be remapped via the channel mapping.

## FAQ

### Q: Connection test failed?

- Check if the API Key is correct (no extra spaces when copying)
- Verify the Base URL is accessible
- Check if your provider account has sufficient credits

### Q: "Model not found" error?

- Confirm the model is in the Supported Models list
- Check if model mapping is configured correctly
- Verify the channel is enabled

### Q: How to set up multiple API Keys?

Enter keys one per line in the channel editor and choose a key strategy above. Enable **Retry per API key** to allow attempts with every available key after retryable failures.

### Q: How to restore a disabled API Key?

Go to channel details, find the key in the **Disabled List**, and click **Restore**.

## Related Documentation

- [Model Management Guide](model-management.md) - Configure model-channel associations
- [API Key Profiles Guide](api-key-profiles.md) - Configure model mappings and permissions
- [Request Processing Guide](../getting-started/request-processing.md) - Understand the full request flow
