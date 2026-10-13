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

### 前置要求
- 可访问的 AxonHub 实例。
- 拥有项目访问权限的 AxonHub API Key。
- Codex（OpenAI 兼容工具）的使用权限。
- （可选）已在 AxonHub 控制台配置好的一个或多个模型配置文件。

### 配置 Codex
1. 编辑 `${HOME}/.codex/config.toml`，将 AxonHub 注册为 provider：
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
2. 导出供 Codex 读取的 API Key：
   ```bash
   export AXONHUB_API_KEY="<your-axonhub-api-key>"
   ```
3. 重启 Codex 以加载配置。

#### 模型目录刷新

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
- 按以上配置发送测试 Prompt，AxonHub 日志中应出现 `/v1/responses` 调用。
- 启用 AxonHub 的追踪功能可查看提示词、回复及延迟信息。

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
- **Codex 认证失败**：确保在启动 Codex 的同一 shell 会话中设置了 `AXONHUB_API_KEY`。
- **模型结果异常**：检查 AxonHub 控制台中当前启用的配置文件映射，必要时禁用或调整规则。
- **定时任务提示 `function_call_output` 缺少 `call_id`**：升级到包含 Codex 定时任务启动项兼容修复的 AxonHub 版本。

### 原生图片生成与编辑

Codex Images 请求现在直接使用原生 `/images/generations` 和 `/images/edits` 端点，保留请求的图片模型（如 `gpt-image-2`），不再选择外层文本主模型。“模型 → 设置”中的默认生图主模型控件已移除；旧的全局和渠道字段仅保留 API 兼容，保存值不再影响生图。

上游必须支持原生 Images 端点。仅支持 Responses 生图工具的中转不能再通过已移除的桥接承接 Images API 请求；非 Codex 渠道需已有原生生图支持或显式配置 Images 端点。Images 只支持非流式请求，`stream: true` 会被拒绝。原生 `/responses` 请求中的 `image_generation` 工具继续沿用原有行为。参数和端点配置见[Images API](../api-reference/image-generation.md)。

### 相关文档
- [追踪指南](tracing.md)
- [OpenAI API 文档](../api-reference/openai-api.md)
- README 中的 [使用指南](../../../README.md#使用指南--usage-guide)
