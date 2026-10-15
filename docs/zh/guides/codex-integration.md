# Codex 集成指南

---

## 概览
AxonHub 可以作为 OpenAI 接口的直接替代方案，使 Codex 能够通过您自己的基础设施连接。本文将介绍配置方法，并说明如何结合 AxonHub 的模型配置文件功能实现灵活路由。

### 关键点
- AxonHub 支持多种 AI 协议/格式转换。你可以配置多个上游渠道（provider/channel），对外提供统一的 OpenAI 兼容接口，供 Codex 使用。
- 你可以开启 `server.trace.codex_trace_enabled`（使用 `Session_id`）或配置 `server.trace.extra_trace_headers` 将 Codex 同一次对话的请求聚合到同一条 Trace。
- Codex App 使用缺少 `call_id` 的 `automation_update` 输出启动定时任务时，AxonHub 也能兼容处理。

### Codex 请求头

Codex Responses、原生 Images 和 alpha-search 请求默认使用 AxonHub 的 Codex User-Agent，只有开启渠道的 User-Agent 透传开关时才使用客户端值。这些出站请求会过滤客户端归属字段（`Http-Referer`、`X-Title`、`X-Platform`、`X-Zcode-*`、`X-Os-*`），但保留原始请求头，供重试切换到需要这些字段的其他提供商时使用。调用方显式选择的 Responses Lite 和会话身份保持不变。原生 Images 在最终规范化会话头时仍保持 JSON 协议。

### 选择接入方式

HTTP 和 WebSocket Responses 请求都会保留显式 Fast/Ultrafast 档位，用于转发、请求日志和计费。WebSocket 连接隔离不会替换规范化的 Codex 会话头。输出内容前，上游结构化错误可以进入已配置的重试流程；拒绝内容和工具搜索结果也视为有效输出。响应完成后立即结束，不等待上游连接关闭，并保留最终用量。

使用 `/codex` 兼容入口可通过 Codex personal access token（PAT）认证，并读取实时原生模型目录。已有自定义 provider 配置可继续使用 `/v1`、API Key 和下文介绍的内置目录。这是两种独立配置；`/v1` 不要求开启 `/codex` 开关或选择目录来源。

### PAT 前置要求与 AxonHub 设置
- 开发机可以访问 AxonHub 实例。
- 拥有项目访问权限的 AxonHub API Key。
- 可以使用 Codex（OpenAI 兼容工具）。
- AxonHub 中已有一个 Codex 渠道。该渠道提供上游地址、凭据和 HTTP 传输配置。

配置 Codex 前，请在 AxonHub 的 **Codex 兼容**设置页中：

1. 开启兼容开关。
2. 选择用于原生模型目录的 Codex 渠道。
3. 点击渠道的**测试**按钮，确认其 `/models` 接口可访问。测试不会保存设置、启用渠道或修改渠道状态。

兼容开关默认关闭。保存开启的配置时必须选择一个现存 Codex 渠道。即使开关关闭，也可以执行目录测试；测试失败不会阻止保存。目录结果只会过滤为当前调用者可见的模型；这不等于允许手工指定该模型，推理请求仍由现有权限链路检查。

### 配置 Codex 认证与 `/codex` 路由
1. 编辑 `${HOME}/.codex/auth.json`，在两个支持的字段中都填入 AxonHub API Key。保留双字段可以兼容不同 Codex 客户端版本，避免认证字段变化导致不可预期的问题：
   ```json
   {
     "personal_access_token": "ah-***",
     "OPENAI_API_KEY": "ah-***"
   }
   ```
2. 编辑 `${HOME}/.codex/config.toml`，让 provider 和认证 API 都指向同一个 `/codex` 兼容入口：
   ```toml
   model = "gpt-6.1-sol"
   model_provider = "openai"
   openai_base_url = "https://your-axonhub-host/codex"
   chatgpt_base_url = "https://your-axonhub-host/codex"
   web_search = "live"

   [features]
   fast_mode = true
   context_management = true
   ```
3. 在 macOS/Linux 启动 Codex 的同一个 shell 中设置必需的认证 API 地址：
   ```bash
   export CODEX_AUTHAPI_BASE_URL=https://your-axonhub-host/codex
   ```
   Windows 用户请在**系统环境变量**中添加 `CODEX_AUTHAPI_BASE_URL=https://your-axonhub-host/codex`。不要设置独立的目录 URL；whoami、models 和 Responses 请求都使用 `/codex` 入口。
4. 重启 Codex 以加载配置。

本指南使用的兼容入口如下：

- `GET /codex/v1/user-auth-credential/whoami`：本地 API Key 身份信息。
- `GET /codex/api/codex/accounts/check`：Codex 账户选择所需的账户元数据。
- `GET /codex/models`：选定渠道的原生目录与调用者可见模型 ID 的交集。
- `POST /codex/images/generations`：通过普通模型/渠道路由调用原生图片生成接口。
- `POST /codex/images/edits`：通过普通模型/渠道路由调用原生图片编辑接口。
- `POST /codex/responses`：Responses HTTP/SSE 推理。
- `GET /codex/responses`：Responses WebSocket 推理。

`/codex/models` 会从选定渠道获取目录，只保留与调用者可见模型 ID 完全一致的条目。它保留上游元数据与显示状态，不为别名或未知模型补充条目。同一来源和版本的并发拉取会合并，但没有持久目录缓存或内置回退；即使推理渠道可用，目录来源不可用也可能导致刷新失败。拉取使用选定渠道的凭据，不转发调用者的 API Key。

### 既有 `/v1` 自定义 provider 配置

使用 API Key 自定义 provider 时，单独配置 `${HOME}/.codex/config.toml`：

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

在启动 Codex 的 shell 中导出 Key，然后重启 Codex：

```bash
export AXONHUB_API_KEY="<your-axonhub-api-key>"
```

#### `/v1` 模型目录刷新

Codex 使用 `GET /v1/models?client_version=0.153.4` 发现模型。AxonHub 将非空的 `client_version` 查询参数视为请求 Codex 的 `{"models":[{"slug":"…",…}]}` 目录格式，无需额外开启 AxonHub 设置。普通 `/v1/models` 请求仍返回 OpenAI 的 `data/id` 格式；单独使用 `include=all` 不会切换到 Codex 格式。

目录只包含当前 API Key 可见的模型，沿用普通列表的项目、配置文件权限和模型列表设置。已启用的模型会显示在 Codex 选择器中，包括上游内置目录原本隐藏的条目。该接口不会把你的 Key 转发给 OpenAI，也不会在每次请求时拉取远端目录。

已知模型使用内置的 Codex **0.153.4** 完整描述，保留提示词、推理选项、工具和服务档位；带日期后缀或单级 provider 前缀的名称沿用客户端的最长前缀匹配规则。例如 Astra 的默认 `context_window` 保持 **272000**，`max_context_window` 保持 **872000**，不会把最大容量当作默认容量。此版本化快照与 AxonHub 通用模型/价格目录同步相互独立。

无法识别的模型使用该版 Codex 的通用回退提示词和元数据。若已配置模型卡片且上下文限制为正数，则使用该限制；否则采用客户端的 **272000** 默认值，这不代表已验证上游的实际容量。自定义模型请配置真实限制，不会借用其他模型的推理档位或服务档位。

可使用现有 Key 检查响应：

```bash
curl -fsS 'http://127.0.0.1:8090/v1/models?client_version=0.153.4' \
  -H "Authorization: Bearer $AXONHUB_API_KEY"
```

此适配修复的是 Codex 发起目录请求后的响应格式，不会强制客户端刷新。Codex 0.153.4 仅在 Codex backend 或 command-auth 配置下尝试远端刷新，普通纯 API Key 自定义 provider 不会自动刷新；手动指定模型后聊天仍是独立路径。目录契约不同的旧版或新版客户端可能需要更新对应快照。

#### 按对话聚合 Trace（重要）
开启内置 Codex 追踪提取后，AxonHub 会将 `Session_id` header 作为 trace ID 使用：

```yaml
server:
  trace:
    codex_trace_enabled: true
```

若 Codex 还会携带其他稳定的对话标识 header（例如 `Conversation_id`），可在 `config.yml` 中将其加入 `extra_trace_headers`，用于在主 trace header 缺失时进行聚合：

```yaml
server: 
  trace:
    extra_trace_headers:
      - Conversation_id
```

**提示**：开启此功能后，AxonHub 会将同一个 Trace 的请求优先转发到同一个上游渠道，从而大幅提高提供商端的缓存命中率（例如 Anthropic 的 Prompt Caching）。

#### 验证
- 发送测试 Prompt；PAT 配置应请求 `/codex/responses`，API Key 自定义 provider 应请求 `/v1/responses`。
- 使用 PAT 时，确认模型列表从 `/codex/models` 加载，且有可用的推理渠道。目录来源和推理渠道不必相同。
- 如果 Codex 客户端使用账户发现功能，请确认使用同一 API Key 可以访问 `/codex/api/codex/accounts/check`。
- 图片生成和编辑分别使用 `/codex/images/generations` 与 `/codex/images/edits`；实际路由到的推理渠道不一定是目录渠道，但必须提供对应的原生图片端点和模型。
- 启用 AxonHub 的追踪功能可查看提示词、回复及延迟信息。

当前版本的远程 compaction-v2 复用普通 Responses 路径：向 `/codex/responses` 发送 `compaction_trigger`，再把返回的压缩项用于后续 Responses 请求。不实现 history/notes API，也不新增独立的 `/codex/responses/compact` 路由；这些未支持路径返回 JSON 404。`context_management` 会启用 Codex 的 Responses 压缩流程，history notes 仍属于后续兼容工作范围。`fast_mode` 会让客户端请求 Responses 的 `service_tier`；该等级是否可用由上游模型目录和响应决定。当前没有独立的 `ultrafast` 接口或本地能力探测。既有 `/v1` 路由保持不变。

### 使用模型配置文件
AxonHub 的模型配置文件支持将请求模型映射到具体提供商模型：
- 在 AxonHub 控制台创建配置文件并添加映射规则（精确名称或正则）。
- 将配置文件绑定到 API Key。
- 切换活动配置文件即可更改 Codex 的行为，无需调整本地工具设置。

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

#### 示例
- 请求 `gpt-4` → 映射到 `deepseek-reasoner` 以获取更准确的回复。
- 请求 `gpt-3.5-turbo` → 映射到 `deepseek-chat` 以降低成本。

### 常见问题
- **经过中转后 Codex 看不到工具**：升级到包含 `additional_tools` 转发修复的版本。Responses、Codex 和 Compact 转发均保留此类输入条目，不按域名删除，也无需额外开关；不支持它的目标服务可能拒绝请求。转换到非 Responses 协议时明确返回 HTTP 400，不再静默丢工具，也不会重试或切换渠道。
- **API Key 自定义 provider 认证失败**：确保在启动 Codex 的同一 shell 会话中设置了 `AXONHUB_API_KEY`。
- **PAT 认证失败**：确认 `${HOME}/.codex/auth.json` 的 `personal_access_token` 是有效的 AxonHub API Key，并确认 `CODEX_AUTHAPI_BASE_URL`、`openai_base_url` 和 `chatgpt_base_url` 指向同一个 `/codex` 入口。
- **`/codex` 模型列表为空或不可用**：确认兼容开关已开启、已选择 Codex 渠道，且该渠道的 `/models` 接口通过设置页测试。模型 ID 必须与调用者可见 ID 完全一致。目录过滤不代替推理授权。`/v1` 的内置目录和客户端刷新限制见上文。
- **模型结果异常**：检查 AxonHub 控制台中当前启用的配置文件映射，必要时禁用或调整规则。
- **定时任务提示 `function_call_output` 缺少 `call_id`**：升级到包含 Codex 定时任务启动项兼容修复的 AxonHub 版本。

### 原生图片生成与编辑

Codex Images 请求现在直接使用原生 `/images/generations` 和 `/images/edits` 端点，保留请求的图片模型（如 `gpt-image-2`），不再选择外层文本主模型。“模型 → 设置”中的默认生图主模型控件已移除；旧的全局和渠道字段仅保留 API 兼容，保存值不再影响生图。

上游必须支持原生 Images 端点。仅支持 Responses 生图工具的中转不能再通过已移除的桥接承接 Images API 请求；非 Codex 渠道需已有原生生图支持或显式配置 Images 端点。Images 只支持非流式请求，`stream: true` 会被拒绝。原生 `/responses` 请求中的 `image_generation` 工具继续沿用原有行为。参数和端点配置见[Images API](../api-reference/image-generation.md)。

### 相关文档
- [追踪指南](tracing.md)
- [OpenAI API 文档](../api-reference/openai-api.md)
- README 中的 [使用指南](../../../README.md#使用指南--usage-guide)
