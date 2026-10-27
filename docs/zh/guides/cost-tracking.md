# 成本追踪

AxonHub 为每一次请求提供实时的成本计算和用量追踪。

## 概述

AxonHub 的成本追踪功能帮助您监控和控制跨所有供应商和模型的 AI 支出。每个请求都会被自动分析，并根据可配置的定价计算成本。

费用展示保留所选币种、地区格式、窄货币符号及各页面的小数精度，默认币种为 USD；翻译库升级不改变这些展示规则。

## 功能特性

### 分析时间范围

分析页支持秒级时间选择，包含起止时刻，最多覆盖统计时区的 366 个本地日。默认及重置覆盖浏览器时区近 30 日，近 12 个月快捷项使用浏览器时区近 365 日，为时区差异预留余量。闰年最后一天使用“本年”时会提示手动选择较短范围，不静默截断数据或提交可能超限的范围。每日图表按配置的统计时区聚合，并处理夏令时变化。性能排行继续只统计生产执行及实际调用模型，清理主请求记录后仍保留。

`AnalyticsFilter.startTime`/`endTime` 与 `analyticsFilterOptions` 时间参数现使用 GraphQL `Time`，格式为 RFC3339，例如 `2026-10-27T08:30:00+08:00`；`analyticsMetadata.earliestTime` 替代 `earliestDate`。升级后请刷新已打开的页面；API 调用方需更新旧的纯日期字符串和 `String` 变量声明。

### Token 类型追踪

AxonHub 追踪以下 Token 类型：

| Token 类型 | 说明 | 代码标识 |
|------------|------|----------|
| **输入 (Prompt)** | 发送到模型的 Token（不含缓存 Token） | `prompt_tokens` |
| **输出 (Completion)** | 模型生成的 Token | `completion_tokens` |
| **缓存读取** | 从缓存读取的 Token | `prompt_cached_tokens` |
| **缓存写入** | 写入缓存的 Token | `prompt_write_cached_tokens` |
| **推理** | 推理 Token（支持该功能的模型）| `completion_reasoning_tokens` |
| **缓存写入 (5分钟)** | Anthropic 5分钟 TTL 缓存写入 | `prompt_write_cached_tokens_5m` |
| **缓存写入 (1小时)** | Anthropic 1小时 TTL 缓存写入 | `prompt_write_cached_tokens_1h` |

### 定价模式

AxonHub 支持三种定价模式：

| 模式 | 说明 | 适用场景 |
|------|------|----------|
| **固定费用 (flat_fee)** | 每次请求收取固定费用 | 固定成本场景 |
| **按单位计费 (usage_per_unit)** | 按每百万 Token 计费 | 大多数模型 |
| **分层计费 (usage_tiered)** | 根据用量分层定价 | 用量折扣场景 |

### 成本明细

每个请求都包含详细的成本明细：

```json
{
  "costItems": [
    {
      "itemCode": "prompt_tokens",
      "quantity": 1000,
      "subtotal": 0.0015
    },
    {
      "itemCode": "completion_tokens",
      "quantity": 500,
      "subtotal": 0.0020
    },
    {
      "itemCode": "prompt_cached_tokens",
      "quantity": 2000,
      "subtotal": 0.0005
    }
  ],
  "totalCost": 0.0050
}
```

### 分层定价明细

使用分层定价时，成本明细包含层级分解：

```json
{
  "itemCode": "prompt_tokens",
  "quantity": 150000,
  "tierBreakdown": [
    {
      "upTo": 100000,
      "units": 100000,
      "subtotal": 0.1000
    },
    {
      "upTo": null,
      "units": 50000,
      "subtotal": 0.0750
    }
  ],
  "subtotal": 0.1750
}
```

## 配置

### 模型定价

在管理后台为每个渠道和模型配置定价：

1. 进入 **渠道** → **模型价格**
2. 为每个模型设置定价项目：

![模型定价](../../screenshots/axonhub-model-price.png)

   - 输入价格 (`prompt_tokens`)
   - 输出价格 (`completion_tokens`)
   - 缓存读取价格 (`prompt_cached_tokens`)
   - 缓存写入价格 (`prompt_write_cached_tokens`)

#### 服务档位价格

可以为 `priority`、`ultrafast` 等服务档位分别添加精确价格。OpenAI 的 `fast` 和 `priority` 共用 `priority` 价格键。AxonHub 同时记录请求档位和实际应用的档位，并优先按实际档位计费；如果 Codex 响应未返回档位或返回 `default`，则显式请求的 Fast 或 Ultrafast 仍作为计费兜底。独立档位价格优先；部分覆盖中省略的价格项仍继承当前有效基础价格。

渠道未配置某模型的价格时，AxonHub 使用模型库的固定输入、输出和缓存单价兜底，API `usage.cost` 与落库费用同样遵循服务档位规则。显式渠道价格优先。实时兜底不会导入上下文阶梯、定时价格或独立档位价格；这些应在 **渠道 → 模型价格** 配置。

开启费用注入时，Responses 自动事件保真路径的 JSON 和流式响应也会包含计算后的 `usage.cost`，不会替换上游 ID 或扩展字段。显式开启请求体透传时，仍沿用原始响应的既有行为。

未配置独立档位价格时，使用以下兜底倍率，依据 2026-10-13 核实的 [OpenAI API 官方价格](https://developers.openai.com/api/docs/pricing)：

| 渠道实际模型 | 未配置 Fast 价格 | 未配置 Ultrafast 价格 |
| --- | --- | --- |
| `gpt-6-astra`、`gpt-6.1-sol` | 当前有效基础价 × 2 | 当前有效 Fast 价 × 3；Fast 也未配置时为基础价 × 6 |
| `gpt-6-sol`、`gpt-6-luna`、`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna` | 当前有效基础价 × 2 | 保留旧估算：已配置 Fast 价 × 2，否则基础价 × 2 |
| 其他模型 ID | 当前有效基础价 | 保留旧估算：已配置 Fast 价 × 2，否则基础价 × 2 |

规则精确匹配渠道实际模型 ID，不按请求别名、前缀或未来模型名称推断。基础价包含当前生效的定时价格表，保留自定义价格及渠道价格倍率。其他未配置档位仍使用基础价。旧估算不代表官方支持 Ultrafast：目前官方 [Ultrafast 文档](https://developers.openai.com/api/docs/guides/ultrafast-mode) 仅列出 GPT-6 Astra 和 GPT-6.1 Sol，上游权限控制仍然生效。

GPT-6 Astra 官方短上下文价格如下，单位为美元/百万 token：

| 档位 | 输入 | 缓存读取 | 缓存写入 | 输出 |
| --- | --- | --- | --- | --- |
| Standard | $10 | $1 | $12.50 | $50 |
| Fast | $20 | $2 | $25 | $100 |
| Ultrafast | $60 | $6 | $75 | $300 |

#### 长上下文价格

将价格项设为 **输入上下文阶梯**（`usage_context`），即可按请求总输入 token（包括缓存读取和写入）选择费率，再用选中的单价计算该项全部用量。输出 token 不计入阈值。原有“全量阶梯”仍按该项自己的用量选档，行为不变。

目录导入会把 `cost.tiers` 中明确的上下文阈值转换成新模式。明确阈值优先于旧字段 `context_over_200k`；只有旧字段时才使用 200,000。服务档位自带的上下文价格优先；上表中的 GPT-6/GPT-5.6 Fast 模型，以及 Astra/GPT-6.1 Sol Ultrafast，在缺少独立长上下文价格时，可以按目录中各价格项的长/短费率比例补全。其他模型需要明确提供服务档位的上下文价格。

Astra 输入 **不超过 272,000** token 时用短价；超过时，各服务档位对整次请求使用长价，输入及缓存费率为短价的 2 倍，输出为 1.5 倍：

| 档位 | 长上下文输入 | 长上下文缓存读取 | 长上下文缓存写入 | 长上下文输出 |
| --- | --- | --- | --- | --- |
| Standard | $20 | $2 | $25 | $75 |
| Fast | $40 | $4 | $50 | $150 |
| Ultrafast | $120 | $12 | $150 | $450 |

例如，300,000 非缓存输入 token 加 100,000 输出 token，在未加自定义价格倍率时，Standard 为 $13.50，Fast 为 $27，Ultrafast 为 $81。新模式可用于定时价格、服务档位覆盖和缓存写入变体，并完整保留在价格 JSON 导入导出及备份中。

**已有固定单价不会自动转换。** 请到 **渠道 → 模型价格** 重新导入模型，核对上下文阈值及各档位价格后保存，或手动选择新模式配置；仅影响之后的请求。保存新模式前，应先部署配套的前后端版本。

已有价格配置和历史费用不会被覆盖。如需替换旧的独立 Fast/Ultrafast 价格，请在 **渠道 → 模型价格** 手动修改，或核对预览后重新导入模型价格。新的兜底规则用于之后缺少独立档位价格的请求。API token 价格与 Codex 订阅的 credits 倍率是两套规则。

[Tibo 于北京时间 2026-10-12 发布的公告](https://x.com/thsottiaux/status/2109430894604026039) 取消的是 Codex/Work 中 GPT-6.1 Sol **使用订阅额度时**的长上下文附加费，不改变 AxonHub 费用估算所依据的 API 费率。对于订阅账号上游，估算金额不等于实际订阅消耗；若希望采用不按上下文加价的统计策略，应单独配置渠道固定单价。

#### 缓存写入变体（Anthropic）

对于 Anthropic 模型，可以配置缓存写入的 TTL 变体：
- **5分钟** (`five_min`): 短期缓存
- **1小时** (`one_hour`): 长期缓存

### 成本计算逻辑

1. **输入 Token 计算**: `PromptTokens - CachedTokens - WriteCachedTokens`
   - 缓存 Token 单独计费，不计入输入 Token 成本

2. **缓存写入变体处理**:
   - 如果存在 `WriteCached5MinTokens` 或 `WriteCached1HourTokens`，分别使用对应变体价格
   - 否则使用默认的缓存写入价格

3. **价格版本控制**:
   - 每次价格变更都会创建新的版本记录
   - 使用日志通过 `cost_price_reference_id` 关联到具体价格版本

### API 响应 (`usage.cost`)

是否把费用写进客户端响应，由系统设置里的 **注入 usage.cost** 开关控制，默认关闭。打开开关、且渠道给模型配置了价格时，AxonHub 按渠道价格计算本次请求的费用，写进客户端响应里的 `usage.cost`。

几点说明：

- 这个数字是 AxonHub 按渠道模型价格算出来的，不是上游供应商的账单。
- 上游响应自带的 `usage.cost`、`cost_details` 会被丢弃，不会转发给客户端（pass-through 请求除外）。
- 开关关闭时，普通请求路径下客户端看不到 `cost`。
- 渠道没配价格、响应没有 `usage`、或开关关闭时，`cost` 字段会被省略。

#### Pass-through 与二进制接口

开启 pass-through 的请求原样返回上游 body，包括上游自己带上的 `cost`，AxonHub 不计算、也不改动。`POST /v1/audio/speech` 等二进制接口返回音频数据，没有 JSON `usage`，也不会返回费用。

#### 支持的响应字段

- OpenAI Chat Completions / Completions / Responses / Compact：`usage.cost`
- OpenAI Embeddings / Image Generation / Video：`usage.cost`
- Anthropic Messages：`usage.cost`（流式时在 `message_delta` 里）
- Gemini `generateContent`：`usageMetadata.cost`（AxonHub 扩展字段，官方 Gemini 响应没有该字段）
- Jina Embeddings / Rerank：`usage.cost`
- AI SDK text：`usage.cost`

```json
{
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 50,
    "total_tokens": 150,
    "cost": 0.000005
  }
}
```

#### 流式响应

`usage.cost` 在流式响应里的位置因协议而异：

- OpenAI Chat Completions / Completions：`cost` 在携带 `usage` 的那一帧，通常是 `[DONE]` 之前的最后一块；需要客户端在请求里带上 `stream_options.include_usage`。
- OpenAI Responses：`usage.cost` 在 `response.completed` 事件上。
- Anthropic Messages：`usage.cost` 在 `message_delta` 的 `usage` 里，流以 `message_stop` 结束。
- Gemini `generateContent`：`usageMetadata.cost`，流以 `candidates[].finishReason` 结束。
- AI SDK text：`usage.cost` 在 `e: finish` 事件的 `usage` 里。

## 查看成本

### 使用日志

每个请求的使用日志包含：

- **Token 用量**: prompt_tokens, completion_tokens, total_tokens
- **Token 详情**: 各类细分 Token 数量
- **成本信息**: total_cost, cost_items, cost_price_reference_id

流式请求分别记录上游执行完成、向客户端交付结果和计费。成功终态写给客户端后，主请求才记为完成；交付失败时，渠道执行仍可能成功，并保留最终用量和费用。写出成功不保证客户端已经读取或展示结果。成功只计一次，已发送终态后不再追加超时错误。

对于 Chat Completions、Responses 和 Anthropic Messages，没有最终计费证据的中间 usage 不计费；上游明确返回失败、取消或不完整终态时，若仍提供最终用量，也可记录费用。Fast/Ultrafast 计价和上游实际档位优先规则不变，收尾阶段获得的最终用量同样适用。历史记录不会重新计算。

上游明确返回失败、取消或不完整状态时，转为 Chat Completions 后仍保留原终态。即使已经收到内容和 usage，连接正常结束也不会再补成功结束标记或 `[DONE]`。

执行记录中的 API Key 序号、别名和后缀要求渠道编辑权限。统计性能列保留无归属用量和已删除 Key 的历史归属，与用量总计保持一致。

### GraphQL API

查询使用日志及成本信息：

```graphql
query GetUsageLogs {
  usageLogs {
    edges {
      node {
        modelId
        promptTokens
        completionTokens
        totalTokens
        totalCost
        costItems {
          itemCode
          quantity
          subtotal
          tierBreakdown {
            upTo
            units
            subtotal
          }
        }
        costPriceReferenceId
      }
    }
  }
}
```

### 成本字段说明

| 字段 | 类型 | 说明 |
|------|------|------|
| `totalCost` | Float | 总成本（基于渠道模型价格计算） |
| `costItems` | JSON | 成本明细项目数组 |
| `costPriceReferenceId` | String | 用于成本计算的价格版本引用 ID |

## 最佳实践

1. **配置模型价格**: 在使用前为每个渠道和模型配置准确的价格
2. **监控缓存使用**: 缓存可以显著降低成本，监控缓存命中率
3. **使用价格版本**: 价格变更时会自动创建版本，便于成本追溯
4. **定期审查**: 查看使用日志和成本报告以优化使用

## 故障排除

### 缺少成本数据

如果成本未显示：

1. 确认渠道模型价格已配置
2. 检查供应商是否返回 Token 使用量
3. 查看使用日志中的 `costPriceReferenceId` 是否正确

### 成本不正确

如果成本似乎不正确：

1. 验证定价配置（特别是缓存价格）
2. 检查使用日志中的 Token 数量明细
3. 确认是否使用了正确的价格版本
4. 对于分层定价，检查层级配置是否正确
