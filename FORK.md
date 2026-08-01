# AxonHub Fork 维护说明

本文记录本仓库相对已合入 upstream 基线仍然存在的、有意保留的功能差异。受众是维护者和 AI coding agents。

本文不是 changelog，也不重复 Git 历史。已经被 upstream 等价吸收的条目应删除；历史原因由提交记录承担。

## 维护契约

1. 只记录当前仍生效且有意保留的行为差异，不记录纯格式化、生成产物噪声、普通 merge commit 或已经完全回滚的改动。
2. 新增、修改或删除 fork 行为时，必须在同一个提交中更新本文。至少同步更新意图、不变量、代码锚点、验证方式和生命周期。
3. 合并 upstream 时，以本文的“不变量”为审核标准。不能仅按文件冲突选择 ours/theirs，也不能因为 upstream 出现同名字段就认定已经等价。
4. `长期保留` 表示这是 fork 的产品、运维或兼容性契约；除非明确改变本地策略，否则合并时必须保留。
5. `等待上游吸收` 表示这是通用修复或时效性数据；upstream 具备等价行为和回归测试后，删除本地实现和本文条目。
6. 提交锚点只用于追溯原始思路。最终审核必须检查当前代码，因为后续 merge resolution 可能扩展或移动实现。
7. 本文中的命令是验证索引。执行时仍须遵守 `AGENTS.md`，尤其是未经用户明确要求不得运行 lint 或 build。

## Upstream 基线

- Fork 分支：`beta`
- Upstream 默认分支：`unstable`
- 最近一次已合入 upstream 的 merge commit：`a302544a1eafe5b17cdd9cced72bdc94f2b64dc9`
- 该 merge 的 upstream parent，也是本文比较基线：`9d4b2a8b6c26d5354317688a3b2ece3e1818dfb9`
- 审计范围：`git diff 9d4b2a8b..HEAD`

`upstream/unstable` 的移动 HEAD 只是待合并候选，不是本文基线。尚未合入的 upstream commit 不应被反向记录为 fork 功能。

每次完成 upstream merge 后，必须在同一提交中：

1. 将上述 merge commit 和 upstream parent 更新为新值。
2. 重新检查所有 `等待上游吸收` 条目，删除已经等价吸收的条目。
3. 检查所有 `长期保留` 不变量是否仍成立，并更新移动过的代码和测试锚点。
4. 用新的 upstream parent 重新生成最终树差异，而不是沿用旧审计结论。

常用审计命令：

```bash
git status --short --branch
git show -s --format='%H %P %s' <latest-upstream-merge>
git diff --stat <upstream-parent>..HEAD
git diff <upstream-parent>..HEAD -- <path>
git show <commit>
git show --remerge-diff <merge-commit>
```

## 长期保留

### F01 Fork 发布、更新和外部资产归属

- 生命周期：`长期保留`
- 原始意图：fork 的 release、Docker/Helm 制品、更新检查、问题链接和开发者模型目录必须指向当前 fork，不能静默回落到 `looplj/axonhub` 的版本或镜像。
- 必须保持：`beta`/`stable` fork 发布通道独立；多架构 manifest 和 Helm 默认镜像属于 fork；版本比较识别 fork 后缀和 upstream prerelease；仓库、release、issues、developer catalog URL 可由构建环境覆盖，默认指向 `shay-wong/axonhub` 的 `beta`。
- 代码锚点：`.github/workflows/stable-fork-release.yml`、`.github/workflows/docker-publish.yml`、`.github/workflows/helm-chart.yml`、`.goreleaser.yml`、`deploy/helm/values.yaml`、`internal/build/info.go`、`internal/server/biz/version.go`、`frontend/src/config/external-urls.ts`、`frontend/src/features/models/data/providers.ts`。
- 提交锚点：`fc687607`、`de2aa90a`、`6b147cbb`、`cee45b4b`、`b77f8e5f`、`47ffb508`、`ce626e28`、`923482f8`、`a94eb373`。
- 合并审核：重点检查 workflow 中 repository owner、tag、`latest` manifest、Chart image、`AXONHUB_UPDATE_CHANNEL` 和前端外链；不要接受重新硬编码 upstream 仓库的变更。
- 吸收/删除条件：只有在 upstream 提供完全仓库无关的发布、更新和目录来源机制，且本 fork 不再需要本地默认值时才能删除。
- 验证：`go test ./internal/server/biz -run 'TestSelectLatestGitHubRelease|Test.*Version'`；`cd frontend && node --test src/config/external-urls.test.mjs`；静态检查 workflow 和 Helm 默认镜像。

### F02 API Key 稳定身份、别名、权重和路由

- 生命周期：`长期保留`
- 原始意图：一个 channel 可以管理多个 upstream API Key，并在不泄露完整 secret 的前提下识别、测试和按权重路由每个 Key。
- 必须保持：优先读取 `apiKeyConfigs`，兼容旧 `apiKey`/`apiKeys`；Key 去重且非正权重归一为 `100`；支持 `trace_sticky`、`weighted_sticky` 和 `failover`；日志和 UI 只显示别名及安全后缀；失败重试优先排除当前 Key 并轮换同一 channel 的其他可用 Key。
- 代码锚点：`internal/objects/channel.go`、`internal/server/biz/channel_apikey_identity.go`、`internal/server/biz/channel_apikey.go`、`internal/server/biz/channel_apikey_provider.go`、`internal/server/orchestrator/retry.go`、`frontend/src/features/channels/data/api-key-display.ts`、`frontend/src/features/channels/components/channels-action-dialog.tsx`、`frontend/src/features/channels/components/channels-test-api-keys-dialog.tsx`。
- 提交锚点：`d6e092ba`、`2909ddaa`、`88980c6e`、`1a69f0c4`、`31b3ad18`、`d53787b1`。
- 合并审核：区分“Key 路由能力”和下文等待 upstream 吸收的“禁用/恢复修复”；不得把结构化配置降级回无权重字符串数组，也不得把完整 Key 加入日志或 GraphQL 非敏感字段。
- 吸收/删除条件：只有 fork 明确放弃多 Key 权重策略，或 upstream 提供等价的稳定身份、路由算法、兼容迁移和脱敏展示时才能删除。
- 验证：`go test ./internal/server/biz ./internal/server/orchestrator -run 'APIKey|Weighted|Failover|Retry'`；`cd frontend && node --test src/features/channels/data/api-key-display.test.mjs`。

### F03 按渠道启用 Codex 风格 Responses 转换

- 生命周期：`长期保留`
- 原始意图：运营人员可以只对选定 channel 启用 Codex 风格的 Responses 默认值，而不是全局改变所有 OpenAI-compatible 请求。
- 必须保持：`TransformOptions.CodexStyleResponses` 是按 channel 的显式开关；关闭时保留调用方原始语义；开启时补充 Codex 原生字段和缺失默认值，包括缺失 `service_tier` 时使用 `priority`，但不能覆盖显式值，也不能影响图像请求。
- 代码锚点：`internal/objects/channel.go`、`internal/server/orchestrator/transform_options.go`、`llm/transformer/openai/codex/outbound.go`、`frontend/src/features/channels/components/channels-transform-options-dialog.tsx`、`frontend/src/features/channels/data/schema.ts`。
- 提交锚点：`f5cdeb74`；相关 merge resolution：`543e25b7`、`cf45f92e`。
- 合并审核：如果 upstream 新增类似 preset，仍要确认它是按 channel 显式启用，并逐项比较默认字段、显式值优先级、session ID 和图像请求例外。
- 吸收/删除条件：只有 upstream 提供相同配置粒度和请求语义时，才可用 upstream 实现替换；能力本身长期保留。
- 验证：`go test ./internal/server/orchestrator -run TestApplyTransformOptions_CodexStyleResponses`；`cd llm && go test ./transformer/openai/codex -run CodexStyleResponses`。

### F04 Fast tier 识别、计费、价格表和展示

- 生命周期：`长期保留`
- 原始意图：准确识别 Codex/OpenAI/Anthropic 的 Fast 请求意图和 provider 实际应用的 tier，并用正确价格计费、持久化和展示。
- 必须保持：request intent、provider-applied tier 和 request-derived pricing override 分开建模；Codex `priority` 只在 provider tier 为空或 default 时覆盖计费；明确的非 default provider tier 优先；Anthropic `speed=fast` 使用 Fast price key；定时价格表保留 prompt cache variants；请求列表展示最终/请求 tier，并在移动端默认隐藏高密度列但保留用户旧版列偏好。
- 代码锚点：`llm/service_tier.go`、`internal/server/orchestrator/service_tier.go`、`internal/objects/price.go`、`internal/ent/schema/request_execution.go`、`internal/ent/schema/usage_log.go`、`internal/server/biz/usage_log.go`、`frontend/src/features/channels/data/model-price-form.ts`、`frontend/src/features/requests/utils/service-tier.ts`、`frontend/src/features/requests/components/requests-columns.tsx`、`frontend/src/features/requests/components/requests-table.tsx`、`frontend/src/locales/en/requests.json`、`frontend/src/locales/zh-CN/requests.json`。
- 提交锚点：`d4793bf7`、`7c45f58e`、`251b8770`、`f155d3d8`、`822da75c`、`8c64997f`；相关 merge resolution：`864c15a3`、`ee209fd8`。
- 合并审核：schema、Ent、GraphQL、backup、billing 和 UI 必须作为一个行为链审核；禁止让 request-side Fast 意图覆盖 provider 明确返回的其他 tier；生成文件冲突应修改 schema 后重新生成。
- 吸收/删除条件：只有 upstream 的字段、价格键、计费优先级、备份兼容和 UI 含义全部等价时才能替换本地实现。
- 验证：`cd llm && go test ./transformer/openai ./transformer/openai/responses ./transformer/anthropic`；`go test ./internal/server/orchestrator ./internal/server/biz ./internal/server/backup ./internal/server/gql`；`cd frontend && node --test src/features/channels/data/model-price-catalog.test.mjs src/features/channels/data/model-price-form.test.mjs src/features/requests/utils/service-tier.test.mjs src/features/requests-mobile-columns.test.mjs`。

### F05 历史 fork 备份恢复兼容和事务安全

- 生命周期：`长期保留`
- 原始意图：旧 fork 版本导出的备份必须能在当前版本安全恢复，失败时不能留下半套配置，也不能破坏未包含在恢复载荷中的现有关系。
- 必须保持：旧模型价格 code/variant 可归一化但非法 tier、重复 code 和非递增区间必须拒绝；channel ID 在 model settings、API Key profiles 等引用中正确重映射；未恢复 channel 的关联保留；无效 system config/proxy preset 导致整体回滚；storage policy 优先于旧 `storeChunks`；proxy preset 保存和删除前统一归一化。
- 代码锚点：`internal/server/backup/restore.go`、`internal/server/backup/restore_test.go`、`internal/server/backup/backup_ops.go`、`internal/server/backup/types.go`、`internal/server/biz/system_proxy.go`。
- 提交锚点：`41d63f9e`、`a5bac9ac`、`8d4aa21c`；相关 merge resolution：`32d0699e`、`864c15a3`。
- 合并审核：不要用当前 schema 的理想结构重写 legacy 分支；先保留旧输入解析，再验证归一化、ID 映射、冲突策略和事务回滚。对外部存储和 inline 数据都要检查。
- 吸收/删除条件：不能仅因 upstream 有新的 backup 实现就删除；必须证明仍在支持期内的历史 fork 备份可恢复，或明确结束对应兼容期。
- 验证：`go test ./internal/server/backup ./internal/server/biz -run 'Restore|ProxyPreset'`。

### F06 Thread 保留、trace 独立状态和 GC 保护

- 生命周期：`长期保留`
- 原始意图：保留 thread 是对整条会话的数据保护，不应覆盖用户对单个 trace 已做出的 archive/retain 选择。
- 必须保持：`Retain`/`Unretain` 只改变 thread 状态；trace 自身状态保持不变；GC 将“trace retained”或“所属 thread retained”都视为保护条件，并保护相关 request、execution 和 usage log。
- 代码锚点：`internal/server/biz/thread.go`、`internal/server/biz/thread_test.go`、`internal/server/gc/gc.go`、`internal/server/gc/gc_test.go`。
- 提交锚点：merge resolution `3356f5bd`。
- 合并审核：upstream 当前的级联状态写入语义与本地策略不同；解决冲突时不能恢复级联 retain/unretain，也不能只保护 trace 表而删除关联 request/usage 数据。
- 吸收/删除条件：只有本地明确改回 upstream 的级联策略，或 upstream 接受相同的父级保护和子级状态独立语义时才能删除。
- 验证：`go test ./internal/server/biz ./internal/server/gc -run 'TestThreadService_RetainPreservesTraceStatuses|TestWorker_cleanupTracesPreservesActiveTraceUnderRetainedThread'`。

## 等待 Upstream 吸收

### U01 API Key/渠道禁用、恢复和状态操作

- 生命周期：`等待上游吸收`
- 原始意图：单个坏 Key 不应误伤整个 channel，自动禁用和人工恢复必须有可解释、可授权且稳定的状态转换。
- 必须保持：Key-scoped provider 错误只累计和禁用对应 Key；transport/credential-agnostic failure 不禁用 Key；API Key policy 已处理的错误不再触发 channel 级禁用；临时禁用到期可恢复；状态图标只暴露当前状态允许且当前用户有权限的动作；倒计时、原因和安全身份显示一致。
- 代码锚点：`internal/server/biz/channel_auto_disable.go`、`internal/server/biz/channel_apikey.go`、`internal/server/biz/channel_metrics.go`、`internal/server/orchestrator/performance.go`、`frontend/src/features/channels/data/channel-status-policy.ts`、`frontend/src/features/channels/data/disabled-api-key-status.ts`、`frontend/src/features/channels/components/channels-columns.tsx`。
- 提交锚点：`2909ddaa`、`fe7809e8`、`c9f85a35`、`fda1159c`；相关 merge resolution：`94d4f989`。
- 合并审核：逐一验证错误分类、计数所有权、永久/临时禁用、恢复动作和 permission gating；不要把完整 Key 暴露给只读用户。
- 上游吸收条件：upstream 具备相同状态机、错误分类、权限和前端回归测试。
- 验证：`go test ./internal/server/biz ./internal/server/orchestrator -run 'DisableAPIKey|AutoDisable|CredentialAgnostic|TransportFailure'`；`cd frontend && node --test src/features/channels/data/channel-status-policy.test.mjs src/features/channels/data/disabled-api-key-status.test.mjs src/features/channels/data/disabled-api-key-dialog-contract.test.mjs`。

### U02 Codex Responses Lite 字段和约束保真

- 生命周期：`等待上游吸收`
- 原始意图：Codex Responses Lite 的 provider-private 字段不能在 inbound -> common model -> outbound 往返中丢失。
- 必须保持：Lite header 与 `reasoning.context=all_turns` 成对保留；`parallel_tool_calls` 约束不丢失；provider-private 数据保存在现有 `ProviderExtensions` sidecar，不污染通用 `llm.Request`；clone 和 retry 后仍存在。
- 代码锚点：`llm/model.go`、`llm/provider_extensions.go`、`llm/transformer/openai/responses/request_extensions.go`、`llm/transformer/openai/responses/model.go`、`llm/transformer/openai/responses/inbound.go`、`llm/transformer/openai/responses/outbound_convert.go`、`llm/transformer/openai/codex/outbound_executor_test.go`。
- 提交锚点：`f60fb767`、`753b2f26`。
- 合并审核：必须同时比较 headers 和 JSON body；只保留 Lite header 而丢失 context 会形成 upstream 拒绝的非法组合。
- 上游吸收条件：upstream 有真实 inbound-to-Codex-outbound 测试，覆盖 context、parallel tool calls、clone 和 retry。
- 验证：`cd llm && go test ./transformer/openai/codex ./transformer/openai/responses -run 'ResponsesLiteRequirements|ConvertReasoning'`。

### U03 Tool Search 和跨协议工具调用语义

- 生命周期：`等待上游吸收`
- 原始意图：OpenAI Responses、Anthropic 和 Chat 转换之间必须完整传递 Tool Search、deferred tools 和工具调用参数。
- 必须保持：Tool Search 定义、调用和 output 可往返；`tool_search_output` 回放满足 upstream 必填字段；流式空参数不会生成错误调用；Responses 并行调用转 Chat 时正确聚合；Anthropic bridge 保留 deferred tools；仅在 done 事件出现的函数参数仍被保存；不同 namespace tool 不混淆。
- 代码锚点：`llm/tools.go`、`llm/metadata.go`、`llm/transformer/anthropic/`、`llm/transformer/openai/responses/`。
- 提交锚点：`e1d68898`、`8bf41241`、`5c201803`、`62edb839`、`41ba05ab`、`8a2a02c6`；相关 merge resolution：`24d949cd`。
- 合并审核：按 tool definition、call、delta、done、output 和 replay 六个阶段检查；不能只验证非流式 happy path。
- 上游吸收条件：upstream 在 OpenAI Responses、Anthropic 和 Chat 三条转换链提供等价 round-trip 测试。
- 验证：`cd llm && go test ./transformer/openai/responses ./transformer/anthropic -run 'ToolSearch|tool_search|FunctionCall|NamespaceTool|Deferred'`。

### U04 流式响应完整性和终态错误保真

- 生命周期：`等待上游吸收`
- 原始意图：转换器不能丢失混合内容、usage、reasoning 顺序或 upstream 错误，也不能让遥测空 chunk 污染客户端流。
- 必须保持：Responses 混合流式内容保持分段和顺序；Cline 空 `choices` 遥测不向客户端透出但最终 usage 保留；interleaved reasoning 按 item 顺序输出；空或不可解析的 upstream error 回退到 status/raw status/通用消息，不返回空字符串；direct stream 和 aggregation 语义一致。
- 代码锚点：`llm/transformer/openai/responses/inbound_stream.go`、`llm/transformer/openai/responses/outbound.go`、`llm/transformer/openai/responses/aggregator.go`、`llm/transformer/anthropic/inbound_stream.go`、`llm/transformer/cline/outbound.go`。
- 提交锚点：`9909a8cc`、`a686efc3`、`042d41ec`；相关 merge resolution：`94d4f989`。
- 合并审核：分别检查 direct stream、aggregate、normal completion、incomplete、provider error 和 transport error；不要把 empty-success retry 与 HTTP error formatting 混为一谈。
- 上游吸收条件：upstream 覆盖混合分段、reasoning 顺序、最终 usage、空 error 和 direct/aggregate 一致性。
- 验证：`cd llm && go test ./transformer/openai/responses ./transformer/anthropic ./transformer/cline`。

### U05 失败执行响应体和终态诊断持久化

- 生命周期：`等待上游吸收`
- 原始意图：失败 request/execution 必须保留足够的 provider 证据，避免详情页只能显示 `{}`、`0 items` 或被包装后的空错误。
- 必须保持：当 `StoreResponseBody` 开启时，普通 HTTP error、流式 terminal event 和可聚合失败响应都保存原始/转换后的 body；外部存储和 inline 存储行为一致；原始 status/code/message 不被后续包装覆盖；进行中的部分响应仍隐藏；重试之间清除陈旧 raw response，避免串到下一次 execution。
- 代码锚点：`internal/server/orchestrator/inbound.go`、`internal/server/orchestrator/outbound.go`、`internal/server/orchestrator/request_execution.go`、`internal/server/biz/request.go`、`internal/server/biz/trace.go`、`frontend/src/features/requests/components/request-detail-content.tsx`。
- 提交锚点：`ea7371e1`、`bb119f4e`、`94133eee`、`098800e6`、`d926881f`。
- 合并审核：按 request 与每个 execution 分开验证；检查 JSON、plain text、429/5xx、terminal SSE、外部存储和 `StoreResponseBody=false`；不能只测 Responses-specific error。
- 上游吸收条件：upstream 覆盖普通 `httpclient.Error.Body` 和流式终态的完整持久化矩阵，并保持 active body 隐藏。
- 验证：`go test ./internal/server/orchestrator ./internal/server/biz -run 'Terminal|ResponseBody|RequestExecution|Failed'`。

### U06 时效性模型目录和默认模型

- 生命周期：`等待上游吸收`
- 原始意图：在 upstream 数据同步前提供当前可用的 GPT-5.6 系列和 Claude Opus 5 模型、价格、别名及默认模型。
- 必须保持：目录 schema 可解析；GPT-5.6 aliases/prices 与 Codex default version 一致；Claude Opus 5 同时存在于 developer catalog、channel default 和 Claude Code default model；不能只同步展示数据而漏掉 transformer allowlist/default。
- 代码锚点：`frontend/src/features/models/data/providers.json`、`frontend/src/features/models/data/providers.schema.ts`、`frontend/src/features/channels/data/config_channels.ts`、`llm/transformer/openai/codex/constants.go`、`llm/transformer/anthropic/claudecode/constants.go`。
- 提交锚点：`ab752d4b`、`0e91096d`、`44463a10`、`4eadf589`。
- 合并审核：下一次 upstream merge 逐项比较 ID、alias、价格、默认值和测试；当前 upstream 已有部分相似数据，不能因此整条盲删。
- 上游吸收条件：upstream 合入且当前 fork 实际使用的目录、alias、price 和 default 全部等价；等价部分可逐项删除。
- 验证：`cd llm && go test ./transformer/openai/codex ./transformer/anthropic/claudecode`；`cd frontend && node --test src/features/models/data/providers-schema.test.mjs src/features/channels/data/channel-config.test.mjs`。

### U07 SIGHUP 配置重载和 PID 文件生命周期

- 生命周期：`等待上游吸收`
- 原始意图：配置重载不能杀死 env-only 部署、误发信号给复用 PID 的进程，或允许两个进程同时声称同一个 PID 文件。
- 必须保持：env-only 启动也安装 SIGHUP handler；Unix PID file 使用贯穿进程生命周期的 advisory flock；reload 只接受仍被 owner 锁定的 PID file；stale/contended file 安全失败；Windows/AIX 等不支持平台跳过 PID management 并拒绝 reload。
- 代码锚点：`cmd/axonhub/config_reload_unix.go`、`cmd/axonhub/reload_unix.go`、`cmd/axonhub/pidfile_flock.go`、`cmd/axonhub/pidfile_unsupported.go`、`cmd/axonhub/main.go`。
- 提交锚点：`ab2dd4f4`。
- 合并审核：不要退回“读 PID 后直接 signal”的 TOCTOU 实现；`golang.org/x/sys` 是直接依赖，因为运行时代码直接使用 `unix.Flock`。
- 上游吸收条件：upstream 有等价 Unix ownership、env-only reload 和 unsupported-platform 测试。
- 验证：`go test ./cmd/axonhub -run 'Reload|PID|SIGHUP'`。

### U08 JSON 大整数精度

- 生命周期：`等待上游吸收`
- 原始意图：工具参数和 trace 规范化不能经过 `float64` 后改变超过 JavaScript safe integer 范围的 JSON 数字。
- 必须保持：Ollama 工具调用参数和 trace canonicalization 使用精确数字解码；区分 `9007199254740992` 与 `9007199254740993`；拒绝 trailing JSON content，而不是静默规范化错误输入。
- 代码锚点：`llm/transformer/ollama/outbound.go`、`llm/transformer/ollama/outbound_test.go`、`internal/server/biz/trace.go`、`internal/server/biz/trace_test.go`。
- 提交锚点：`ad2104ed`、`27d092af`。
- 合并审核：搜索新增的 `json.Unmarshal(..., any)` 或默认 `Decoder` 到工具参数/去重路径；不得用字符串格式化掩盖精度损失。
- 上游吸收条件：upstream 两条路径都使用等价的 `UseNumber`/raw-number 处理并保留回归测试。
- 验证：`cd llm && go test ./transformer/ollama`；`go test ./internal/server/biz -run 'Trace.*Precision|LargeInteger'`。

### U09 凭据可见性、敏感字段和 provider quota 边界

- 生命周期：`等待上游吸收`
- 原始意图：系统级查询、GraphQL schema、日志和 quota cache 都不能泄露个人 Key 或 provider secret，也不能在凭据变化后继续展示旧配额。
- 必须保持：个人 API Key 始终只对创建者可见，系统 scope 不能绕过；OpenCode Go `authCookie` 只写不读，空更新保留、显式命令才清除，且 update log 不记录完整 input；API Key、disabled Key、workspace/cookie 等 quota identity 在事务提交后使缓存失效；内部 quota routing 可最小化 bypass，但 GraphQL 配置读取要求 `read_settings`。
- 代码锚点：`internal/scopes/rule_personal_apikey.go`、`internal/server/gql/dashboard.resolvers.go`、`internal/server/gql/axonhub.graphql`、`internal/server/biz/channel.go`、`internal/server/biz/channel_provider_quota_hook.go`、`internal/server/biz/provider_quota.go`、`frontend/src/features/channels/data/channel-input.ts`。
- 提交锚点：`b5bc14d2`、`e8656e4b`、`ccb025f8`；相关 merge resolution：`32d0699e`。
- 合并审核：分别检查 read path、write/clear path、日志、duplicate channel、cache cold/hot path 和事务 rollback；不能用前端隐藏代替服务端权限。
- 上游吸收条件：upstream 有个人 Key ownership、write-only cookie、quota identity invalidation 和 `read_settings` 测试。
- 验证：`go test ./internal/scopes ./internal/server/biz ./internal/server/gql -run 'Personal|AuthCookie|ProviderQuota|ReadSettings'`；`cd frontend && node --test src/features/channels/data/channel-input.test.mjs`。

### U10 反向代理来源 IP 信任边界

- 生命周期：`等待上游吸收`
- 原始意图：客户端不能通过伪造 forwarded headers 绕过 IP blocklist/rate limit 或污染访问日志。
- 必须保持：`trusted_proxies` 默认为空；只有显式代理 IP/CIDR 可提供 `X-Forwarded-For`/`X-Real-IP`；中间件统一使用 Gin 验证后的 `ClientIP()`；非法 proxy 配置启动即失败。
- 代码锚点：`internal/server/config.go`、`internal/server/server.go`、`internal/server/middleware/ip_blocklist.go`、`config.example.yml`、`docs/en/deployment/configuration.md`、`docs/zh/deployment/configuration.md`。
- 提交锚点：`fe27b4db`。
- 合并审核：检查默认值，不能为了反向代理开箱即用而恢复“信任所有代理”；IP 控制、限流和 access log 必须使用同一解析结果。
- 上游吸收条件：upstream 提供安全默认值、CIDR 配置和伪造 forwarded header 回归测试。
- 验证：`go test ./internal/server ./internal/server/middleware -run 'TrustedProxies|IPBlocklist|IPRateLimit'`。

### U11 Upstream 错误体截断证据

- 生命周期：`等待上游吸收`
- 原始意图：限制异常大 upstream error body 防止 OOM，同时让调用方明确知道错误证据是否被截断。
- 必须保持：最多保留 1 MiB；读取 `limit+1` 才能可靠判断截断；`Truncated` 从 `httpclient.Error` 贯穿 pipeline、transformer、非流式和流式 API error；只有实际截断时输出 `truncated: true`。
- 代码锚点：`llm/httpclient/client.go`、`llm/httpclient/errors.go`、`llm/pipeline/error.go`、`internal/server/api/chat.go`、`internal/server/api/upstream_error_policy.go`。
- 提交锚点：`fe27b4db`。
- 合并审核：不能只保留大小限制而丢掉 truncated evidence；检查 JSON error、plain text、streaming 和 policy-masked error。
- 上游吸收条件：upstream 同时具备上限、可靠检测和全链路传播测试。
- 验证：`cd llm && go test ./httpclient ./pipeline -run 'LimitsErrorResponseBody|Truncat'`；`go test ./internal/server/api -run Truncat`。

### U12 Codex session header 最终规范化

- 生命周期：`等待上游吸收`
- 原始意图：前置转换确定的 Codex session ID 不能在后续 inbound header merge、retry 或 WebSocket executor 中被旧别名覆盖。
- 必须保持：resolved session ID 写入 transformer metadata；真正发送前统一写入 `Session_id` 并删除 `session_id`；普通、流式、non-stream aggregation 和 WebSocket 复用使用同一 canonical identity；header merge 不修改源 map。
- 代码锚点：`llm/transformer/openai/codex/outbound.go`、`llm/transformer/openai/codex/outbound_executor_test.go`、`llm/httpclient/utils.go`、`llm/transformer/openai/responses/websocket_executor.go`。
- 提交锚点：`5f3eeb83`；相关 merge resolution：`cf45f92e`。
- 合并审核：追踪最终 executor 收到的 header，不要只看 `TransformRequest` 的中间结果；同时测试两种 header spelling 和 connection reuse。
- 上游吸收条件：upstream 在普通、流式和 WebSocket 路径都有 canonical session 测试。
- 验证：`cd llm && go test ./transformer/openai/codex ./transformer/openai/responses ./httpclient -run 'Session|Header|WebSocketExecutorReusesConnection'`。

### U13 OTLP HTTP exporter 旧拼写兼容

- 生命周期：`等待上游吸收`
- 原始意图：兼容旧文档曾发布的错误拼写 `oltphttp`，避免已部署配置升级后启动失败；规范拼写仍是 `otlphttp`。
- 必须保持：在合并 upstream `31be400ce2a30c1134177945f2dc16fd221c5519` 前，两种拼写都能启动同一个 OTLP HTTP exporter。
- 代码锚点：`internal/metrics/config.go`、`internal/metrics/provider.go`、`internal/metrics/provider_test.go`、`config.example.yml`。
- 提交锚点：`ce477a37`；upstream 处理：`31be400c` 只修正文档和示例。
- 合并审核：本地决定是跟随 upstream 结束旧拼写兼容；合并 `31be400c` 时删除 `oltphttp` alias、对应测试和本文条目，不要把 alias 当长期 fork 能力。
- 上游吸收条件：合并 `31be400c` 并确认部署配置已经使用 `otlphttp`。
- 验证：删除前运行 `go test ./internal/metrics -run TestNewProviderAcceptsPublishedOltpHTTPSpelling`；删除后验证 `otlphttp` 正常且旧拼写按预期被拒绝。

### U14 测试流量与生产渠道健康状态隔离

- 生命周期：`等待上游吸收`
- 原始意图：channel test/probe 是诊断行为，不能改变生产路由、自动禁用或成功率统计。
- 必须保持：test source 不进入 channel/API Key failure counters、EWMA、load balancer、auto-disable、dashboard/channel metrics；测试成功也不能清空生产已累计的失败状态；测试 request/execution 本身仍可保存和查看。
- 代码锚点：`internal/ent/schema/request_execution.go`、`internal/server/orchestrator/health_state.go`、`internal/server/orchestrator/performance.go`、`internal/server/biz/channel_metrics.go`、`internal/server/gql/channel_performance_helpers.go`、`internal/server/gql/qb/throughput.go`。
- 提交锚点：`c9080fa2`。
- 合并审核：同时检查实时内存状态、应用重启后的数据库重建、dashboard raw query 和 auto-disable；只在一层过滤不够。
- 上游吸收条件：upstream 在上述四层统一排除 test source，并保留诊断记录。
- 验证：`go test ./internal/server/biz ./internal/server/orchestrator ./internal/server/gql ./internal/server/db -run 'TestSource|SkipHealthStateTracking|Probe|BackfillRequestExecutionSource'`。

### U15 前端交互和权限回归修复

- 生命周期：`等待上游吸收`
- 原始意图：小型前端回归不应重置用户输入、暴露无权限操作或拒绝后端合法 ID。
- 必须保持：data storage 表格数据引用稳定，编辑输入不因 render 重置；禁用 Key 提示有可读对比度；thread/trace/detail 状态动作要求 `write_requests` 并携带当前 project header；prompt schema 接受 GraphQL project GUID；brand settings 可公开读取，但受保护 system settings 仍要求 `read_settings`。
- 代码锚点：`frontend/src/features/data-storages/index.tsx`、`frontend/src/features/channels/components/channels-columns.tsx`、`frontend/src/features/request-status-management.test.mjs`、`frontend/src/features/threads/components/thread-detail-page.tsx`、`frontend/src/features/traces/components/trace-detail-page.tsx`、`frontend/src/features/prompts/data/schema.ts`、`frontend/src/features/system/data/system-permissions.test.mjs`。
- 提交锚点：`b5d19692`、`151e1d4e`、`374cbd81`；相关 merge resolution：`3356f5bd`、`864c15a3`。
- 合并审核：以行为测试逐项判断是否吸收，不要因为 upstream 重构组件就删除测试覆盖。
- 上游吸收条件：对应 UI 行为和 permission test 已在 upstream 等价存在；允许逐项删除已吸收的小修复。
- 验证：`cd frontend && node --test src/features/request-status-management.test.mjs src/features/prompts/data/schema.test.mjs src/features/system/data/system-permissions.test.mjs`；data storage 输入需做组件交互检查。

### U16 多项目权限和邀请生命周期安全

- 生命周期：`等待上游吸收`
- 原始意图：system scope、project membership 和 project role 不能跨项目拼接；公开邀请入口不能成为权限绕过、竞争条件或 token 泄露点。
- 必须保持：effective project scopes 只在所属项目内计算；登录后只跳转到用户真实可访问的 dashboard/playground/profile；邀请绑定 active project，正确处理过期、max uses、并发注册/删除项目和 deleted row；token 使用 32-byte randomness；公开 get/register endpoint 分别限流；access log 使用 route template，不能记录真实 invitation token；错误使用结构化 4xx code。
- 代码锚点：`frontend/src/config/route-permission.ts`、`frontend/src/features/auth/data/auth-redirect.ts`、`internal/server/biz/invitation.go`、`internal/server/biz/project.go`、`internal/server/api/invitation.go`、`internal/server/routes.go`、`internal/server/middleware/ip_rate_limit.go`、`internal/server/middleware/access_log.go`。
- 提交锚点：merge resolution `94d4f989`。
- 合并审核：分别验证 system/project scope、selected project、project deletion race、unlimited invitation expiry、token path logging 和 rate limit；前端 route guard 不是服务端授权替代品。
- 上游吸收条件：upstream 服务端和前端都提供等价边界及并发/权限测试。
- 验证：`go test ./internal/authz ./internal/server/biz ./internal/server/api ./internal/server/middleware -run 'Invitation|ProjectPermission|IPRateLimit|RouteTemplate|SystemScope'`；`cd frontend && node --test src/features/auth/data/auth-redirect.test.mjs`。

### U17 Analytics 查询正确性和权限隔离

- 生命周期：`等待上游吸收`
- 原始意图：analytics 结果必须可复现、受权限约束，并明确展示失败或截断，不能把测试流量或越权 identity 维度混入统计。
- 必须保持：只统计 production usage；日期范围有上限并按配置 timezone 正确跨越 DST；project/channel/API Key/user/model 维度分别校验 system scope，project membership scope 不能冒充 system scope；保留 deleted channel attribution；用户维度和 personal Key 过滤正确；前端只请求和展示获授权维度，错误与 truncation 明示。
- 代码锚点：`internal/authz/scope.go`、`internal/server/gql/analytics.graphql`、`internal/server/gql/analytics.resolvers.go`、`internal/server/gql/analytics_helpers.go`、`frontend/src/features/analytics/`、`frontend/src/stores/analyticsStore.ts`。
- 提交锚点：merge resolution `ee209fd8`。
- 合并审核：检查 SQL/Ent predicate、timezone segmentation、limit/truncation、deleted relation fallback 和前端 query enablement；不能只比较图表外观。
- 上游吸收条件：upstream 有等价 resolver、authorization、DST、production-only、deleted attribution 和前端 error/truncation 测试。
- 验证：`go test ./internal/authz ./internal/server/gql -run 'Analytics|SystemScope'`；运行 frontend analytics unit tests（若新增统一入口，应在本文同步命令）。

### U18 渠道候选去重和重试预算语义

- 生命周期：`等待上游吸收`
- 原始意图：`MaxChannelRetries` 表示可尝试的不同 channel 数量，不能被同一 channel 的多条模型关联消耗完。
- 必须保持：每个 priority group 先按 channel ID 去重；同一 channel 的 model fallback 可在选中后合并；跨 priority 仍只占一个 channel budget；不同 `APIFormat` 的候选绝不合并；selection tracking 每个实际候选 channel 只记一次。
- 代码锚点：`internal/server/orchestrator/candidates.go`、`internal/server/orchestrator/candidates_loadbalance_test.go`。
- 提交锚点：merge resolution `94d4f989`。
- 合并审核：不要按 association candidate 数量截断；用“同 channel 同 priority”“同 channel 跨 priority”“同 channel 不同 API format”三个矩阵检查。
- 上游吸收条件：upstream 提供相同 retry budget 含义和三类回归测试。
- 验证：`go test ./internal/server/orchestrator -run 'CountsDistinctChannels|DoesNotMergeModelsAcrossAPIFormats'`。

## Upstream Merge 审核清单

1. 确认 worktree、当前分支、upstream 默认分支和将要合入的精确 SHA。
2. 记录 merge 前本文基线；用路径级 diff 检查每个 `长期保留` 条目。
3. 对 conflict file 先定位所属能力，再选择或重写 resolution；必要时用 `git show --remerge-diff <merge>` 查看历史人工 resolution。
4. 对 `等待上游吸收` 条目比较行为和测试，不按提交标题或同名字段判断等价。
5. schema 变化遵循仓库生成规则；`llm/` 是独立 Go module，相关命令从 `llm/` 目录运行。
6. 先运行受影响能力列出的最小验证，再按用户授权和 `AGENTS.md` 扩大验证范围。
7. 更新本文基线、路径、提交锚点和生命周期；删除已吸收条目。
8. 确认代码和 `FORK.md` 在同一提交，且 `git diff --check` 通过。

## 明确不记录

- `cd94f9cf` 仅忽略本地 Codex 工作目录，不是产品能力。
- `fbb7dd63` 与 `a8c22a5c` 最终净效果为回滚，不作为当前行为差异。
- 纯 upstream merge commit 本身不作为能力；只有人工 merge resolution 引入且当前仍生效的行为才作为锚点记录。
- 尚未合入当前 upstream 基线的 upstream-only commits 不属于 fork 差异；合并后再更新本文。
