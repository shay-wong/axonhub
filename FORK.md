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
8. 面向用户的 fork 能力应同步到仍由 upstream 维护的 `docs/en`、`docs/zh` 入口及各自索引、英文/中文 README 与 `CHANGELOG.md`；不要为了少量 fork 差异恢复 upstream 已迁出的整套部署文档。`README.ja-JP.md` 当前是链接到英文文档的入口，不视为完整日文文档树。纯 upstream 能力进入基线时不重复登记为 fork changelog。

## Upstream 基线

- Fork 分支：`beta`
- Upstream 默认分支：`unstable`
- 本次 upstream merge 的 fork parent：`018053eaa254da4c2e9d2af3f2153f84e0236dd5`
- 本次 upstream merge 的 upstream parent，也是本文比较基线：`834eea2b9ea54eb86a4428939ca0801fef155b4d`
- 本次 merge base：`fd4158ef8e1a559ae7d597d7e421ae52f9ad5976`
- 审计范围：`git diff 834eea2b..HEAD`

本文记录固定的 merge 输入，不要求 merge commit 在自身内容中记录自身 SHA。`upstream/unstable` 后续移动不改变本文基线；尚未合入的新 upstream commit 不应被反向记录为 fork 功能。

每次完成 upstream merge 后，必须在同一提交中：

1. 将上述 fork parent、upstream parent 和 merge base 更新为新值。
2. 重新检查所有 `等待上游吸收` 条目，删除已经等价吸收的条目。
3. 检查所有 `长期保留` 不变量是否仍成立，并更新移动过的代码和测试锚点。
4. 用新的 upstream parent 重新生成最终树差异，而不是沿用旧审计结论。

常用审计命令：

```bash
git status --short --branch
git show -s --format='%H %P %s' HEAD
git diff --stat <upstream-parent>..HEAD
git diff <upstream-parent>..HEAD -- <path>
git show <commit>
git show --remerge-diff <merge-commit>
```

## Fork 发布版本

- Upstream 发布版本来源：`.github/workflows/stable-fork-release.yml` 从 upstream 的已发布 Git tag 中选择当前通道的最高版本；当前最高 beta tag 为 `v1.0.0-beta9`。
- 本次 upstream parent 包含 tag `v1.0.0-beta9`，源码中的 `internal/build/VERSION` 也已更新为 `v1.0.0-beta9`；fork 发布版本仍必须以 upstream 已发布 tag 为准，不能用源码常量替代发布基线。
- Fork 发布版本来源：`.github/workflows/stable-fork-release.yml` 创建的 annotated tag；`.github/workflows/docker-publish.yml` 和 `.goreleaser.yml` 使用该完整 tag 构建制品。
- 所有 fork release 必须使用 `<upstream-version>-fork.<N>`。upstream 版本变化时从 `fork.1` 开始；同一 upstream 版本后续发布递增 `N`。
- 最近已发布 fork tag 为 `v1.0.0-beta9-fork.2`；upstream 发布基线仍为 `v1.0.0-beta9`，因此下一个规范化 fork 版本为 `v1.0.0-beta9-fork.3`，发布前仍须重新确认该 tag 未被占用。

## 长期保留

### F01 Fork 发布、更新和外部资产归属

- 生命周期：`长期保留`
- 原始意图：fork 的 release、Docker/Helm 制品、更新检查、问题链接和 fork 自有模型目录增量必须属于当前 fork，不能静默回落到 `looplj/axonhub` 的版本、镜像或额外模型数据。
- 必须保持：`beta`/`stable` fork 发布通道独立；多架构 manifest 和 Helm 默认镜像属于 fork；版本比较识别 fork 后缀和 upstream prerelease；fork tag、二进制 Release 和多架构 Docker 发布全部成功后，通过仓库 Secrets `TELEGRAM_BOT_TOKEN`、`TELEGRAM_CHAT_ID` 发送一次非阻塞中文 Telegram 通知，并关闭 GitHub 英文链接预览；仓库、release 和 issues URL 可由构建环境覆盖且默认指向 `shay-wong/axonhub`；fork 自有模型增量由后端内嵌 `catalogdata/models.json` 承载，不再依赖浏览器直连仓库目录；定时模型目录同步从 fork 的 `beta` 分支运行，并将更新 PR 提交到 `beta`，不能依赖 fork 中不存在的 `unstable` 分支。
- 代码锚点：`.github/workflows/stable-fork-release.yml`、`.github/workflows/docker-publish.yml`、`.github/workflows/helm-chart.yml`、`.github/workflows/sync-model-developers.yml`、`.goreleaser.yml`、`deploy/helm/values.yaml`、`scripts/sync/sync-model-developers.js`、`internal/build/info.go`、`internal/server/biz/version.go`、`internal/server/biz/catalog.go`、`internal/server/biz/catalogdata/models.json`、`frontend/src/config/external-urls.ts`。
- 提交锚点：`fc687607`、`de2aa90a`、`6b147cbb`、`cee45b4b`、`b77f8e5f`、`47ffb508`、`ce626e28`、`923482f8`、`a94eb373`。
- 合并审核：重点检查 workflow 中 repository owner、tag、`latest` manifest、Chart image、Telegram 通知依赖与 Secrets 名称、模型目录同步的 checkout/base 分支、`AXONHUB_UPDATE_CHANNEL`、前端外链和后端内嵌模型增量；不要接受重新硬编码 upstream 仓库、把同步目标改回 `unstable`，或丢失 fork `catalogdata/models.json` 的变更。
- 吸收/删除条件：只有在 upstream 提供完全仓库无关的发布、更新和目录来源机制，且本 fork 不再需要本地默认值时才能删除。
- 验证：`go test ./internal/server/biz -run 'TestSelectLatestGitHubRelease|Test.*Version'`；`cd frontend && node --test src/config/external-urls.test.mjs`；静态检查 workflow、模型目录同步的 checkout/base 分支、Telegram 通知依赖/Secrets 和 Helm 默认镜像。

### F02 API Key 稳定身份、别名、权重和路由

- 生命周期：`长期保留`
- 原始意图：一个 channel 可以管理多个 upstream API Key，并在不泄露完整 secret 的前提下识别、测试和按权重路由每个 Key。
- 必须保持：优先读取 `apiKeyConfigs`，兼容旧 `apiKey`/`apiKeys`；Key 去重且非正权重归一为 `100`；编辑、导入、删除或重排 Key 时按 Key 身份保留对应别名和权重；支持 `trace_sticky`、`weighted_sticky` 和 `failover`；API-key quota 预检与 checker 使用同一归一化 Key 集合；日志和 UI 只显示别名及安全后缀；失败重试优先排除当前 Key 并轮换同一 channel 的其他可用 Key；模型发现允许选择一个可用 Key，并仅在该 Key 失败时按顺序尝试其他 Key，首个成功后立即停止，自动同步同样跳过已禁用 Key 且不遍历成功后的 Key。
- 代码锚点：`internal/objects/channel.go`、`internal/server/biz/channel_apikey_identity.go`、`internal/server/biz/channel_apikey.go`、`internal/server/biz/channel_apikey_provider.go`、`internal/server/biz/model_fetcher.go`、`internal/server/biz/model_fetcher_test.go`、`internal/server/biz/provider_quota.go`、`internal/server/biz/provider_quota_url_test.go`、`internal/server/orchestrator/retry.go`、`frontend/src/features/channels/data/api-key-display.ts`、`frontend/src/features/channels/data/channel-input.ts`、`frontend/src/features/channels/data/channel-config.test.mjs`、`frontend/src/features/channels/components/channels-action-dialog.tsx`、`frontend/src/features/channels/components/channels-api-key-management-dialog.tsx`。
- 提交锚点：`d6e092ba`、`2909ddaa`、`88980c6e`、`1a69f0c4`、`31b3ad18`、`d53787b1`。
- 合并审核：区分“Key 路由能力”和下文等待 upstream 吸收的“禁用/恢复修复”；upstream `1823ec34` 的统一密钥管理弹窗必须优先读取 `apiKeyConfigs`，导入或删除 Key 时保留已有别名和权重；`dfbe2259` 增加 `modelProtocols` 和增量 channel settings 更新时，必须让 `apiKeySelectionStrategy` 与协议配置并存并进入 settings patch；`6742293a` 新增的 ZenMux `managementApiKey` 只用于服务端配额查询，必须与结构化 inference Key 并存且不能代替或清空别名、权重和选择策略；`d3132241` 新增 Command Code 的 `providerQuota` 设置时，必须让该字段与 `apiKeySelectionStrategy` 共用同一增量 settings patch，不能互相覆盖；不得把结构化配置降级回无权重字符串数组，也不得把完整 Key 加入日志或 GraphQL 非敏感字段。
- 吸收/删除条件：只有 fork 明确放弃多 Key 权重策略，或 upstream 提供等价的稳定身份、路由算法、兼容迁移和脱敏展示时才能删除。
- 验证：`go test ./internal/server/biz ./internal/server/orchestrator -run 'APIKey|ProviderQuota|Weighted|Failover|Retry'`；`cd frontend && node --test src/features/channels/data/api-key-display.test.mjs src/features/channels/data/channel-input.test.mjs`。

### F03 按渠道启用 Codex 风格 Responses 转换

- 生命周期：`长期保留`
- 原始意图：运营人员可以只对选定的官方 Codex 或兼容 channel 启用 Codex 风格的 Responses 默认值，而不是全局改变所有 OpenAI-compatible 请求。
- 必须保持：`TransformOptions.CodexStyleResponses` 是 `codex` 和 `fenno` channel 的显式开关且默认关闭；关闭时保留调用方原始语义；开启时补充 Codex 原生字段和缺失默认值，包括缺失 `service_tier` 时使用 `priority`，但不能覆盖显式值，也不能影响图像请求。
- 代码锚点：`internal/objects/channel.go`、`internal/server/biz/channel.go`、`internal/server/biz/channel_test.go`、`internal/server/orchestrator/transform_options.go`、`llm/transformer/openai/codex/outbound.go`、`frontend/src/features/channels/components/channels-transform-options-dialog.tsx`、`frontend/src/features/channels/data/config_channels.ts`、`frontend/src/features/channels/data/channel-config.test.mjs`、`frontend/src/features/channels/data/schema.ts`。
- 用户文档：`docs/en/guides/channel-management.md`、`docs/zh/guides/channel-management.md`；当前用户影响记录在 `CHANGELOG.md` 的 `Unreleased`。
- 提交锚点：`f5cdeb74`；相关 merge resolution：`543e25b7`、`cf45f92e`；本次 Fenno 支持可用 `git log -S'supportsCodexStyleResponses' -- frontend/src/features/channels/data/config_channels.ts` 定位。
- 合并审核：upstream `9dfd6ac0`、`fc1d27da`、`c0233704` 和 `6f9bfc5f` 已补充 Claude Code cache compatibility、Responses 限制、Codex headers 与缺失 reasoning context 的默认值，`f2f80a9f` 又让 multipart 图像请求跳过 JSON body override 并保留实际图像格式，`dfbe2259` 将 reasoning effort 映射统一到跨客户端和跨协议的 outbound 前处理，但都没有提供按 channel 显式启用的完整 Codex 默认值开关。仍须逐项比较配置粒度、默认字段、显式值优先级、session ID 和图像请求例外。
- 吸收/删除条件：只有 upstream 提供相同配置粒度和请求语义时，才可用 upstream 实现替换；能力本身长期保留。
- 验证：`go test ./internal/server/biz -run 'TestChannelService_(Create|Update)ChannelNormalizesCodexStyleResponsesByType'`；`go test ./internal/server/orchestrator -run 'TestApplyTransformOptions_CodexStyleResponses|TestOverrideBodySkipsNonJSONBody'`；`cd llm && go test ./transformer/openai/codex -run CodexStyleResponses`；`cd frontend && node --test src/features/channels/data/channel-config.test.mjs`。

### F04 Fast/Ultrafast tier 识别、计费、价格表和展示

- 生命周期：`长期保留`
- 原始意图：准确识别 Codex/OpenAI/Anthropic 的 Fast 与 Ultrafast 请求意图和 provider 实际应用的 tier，并用正确价格计费、持久化和展示。
- 必须保持：request intent、provider-applied tier 和 request-derived pricing override 分开建模；Codex `priority`、`ultrafast` 只在 provider tier 为空或 default 时覆盖计费，且分别使用独立价格 key；明确的非 default provider tier 优先；Anthropic `speed=fast` 使用 Fast price key；显式 `ultrafast` 不被 Codex 风格默认值覆盖；Ultrafast 独立价格优先，未配置时按当前有效 Fast 价格的 2 倍计费，Fast 也未配置时等价于当前有效基础价格的 2 倍；已接收完整终态响应时，即使客户端随后取消或超时，也必须持久化 usage 和费用，且 completed execution 不得残留取消错误或失败状态码；定时价格表保留 prompt cache variants；请求列表分别展示 Fast 与 Ultrafast，并在移动端默认隐藏高密度列；列偏好迁移保留 v2 拆分列到 v3 合并列的既有语义，再把 v3 `usage` 等价展开为当前 `tokens`、`readCache` 和 `writeCache`。
- 代码锚点：`llm/service_tier.go`、`internal/server/orchestrator/service_tier.go`、`internal/server/orchestrator/request.go`、`internal/server/orchestrator/outbound.go`、`internal/server/orchestrator/orchestrator_streaming_test.go`、`internal/objects/price.go`、`internal/ent/schema/request_execution.go`、`internal/ent/schema/usage_log.go`、`internal/server/biz/cost_calc.go`、`internal/server/biz/cost_calc_test.go`、`internal/server/biz/usage_log.go`、`frontend/src/features/channels/data/model-price-form.ts`、`frontend/src/features/channels/components/channels-model-price-dialog.tsx`、`frontend/src/features/requests/utils/service-tier.ts`、`frontend/src/features/requests/utils/column-visibility.ts`、`frontend/src/features/requests/components/requests-columns.tsx`、`frontend/src/features/requests/components/requests-table.tsx`、`frontend/src/features/requests-mobile-columns.test.mjs`、`frontend/src/locales/en/requests.json`、`frontend/src/locales/zh-CN/requests.json`。
- 用户文档：`docs/en/guides/cost-tracking.md`、`docs/zh/guides/cost-tracking.md`；当前用户影响记录在 `CHANGELOG.md` 的 `Unreleased`。
- 提交锚点：`d4793bf7`、`7c45f58e`、`251b8770`、`f155d3d8`、`822da75c`、`8c64997f`；相关 merge resolution：`864c15a3`、`ee209fd8`；本次 Ultrafast 支持可用 `git log -S'ServiceTierUltrafast' -- llm/service_tier.go` 定位；本次 v6 列偏好迁移可用 `git log -S'REQUEST_COLUMN_VISIBILITY_STORAGE_VERSION = 6' -- frontend/src/features/requests/utils/column-visibility.ts` 定位；本次完整流取消后的 usage 持久化修复可用 `git log -S'CanceledAfterResponsesCompletionPersistsUsage' -- internal/server/orchestrator/orchestrator_streaming_test.go` 定位；upstream 价格导入/导出与 service-tier 映射的整合可用 `git log -S'mapSaveInputsToFormData' -- frontend/src/features/channels/data/model-price-form.ts` 定位。
- 合并审核：schema、Ent、GraphQL、backup、billing 和 UI 必须作为一个行为链审核；禁止让 request-side Fast 意图覆盖 provider 明确返回的其他 tier，也不能让完整终态响应后的客户端取消撤销 usage eligibility；生成文件冲突应修改 schema 后重新生成。Upstream `5ac75028` 新增跨 API 格式的 `usage.cost` 注入，`0210b151` 又允许 object-shaped cost；两者都必须复用落库时相同的 requested/applied/override/policy 决策，不能退回无 tier 的基础价格。Upstream `359cf840` 已重构请求表布局，`d6ed9c62` 已虚拟化价格卡片，`d1140628` 和 `24145b38` 又加入 cache rate 与列排序，`48e8b714` 新增价格 JSON 导入/导出；合并后仍须保留 Fast 列、拆分 cache 列与旧列偏好迁移，并让虚拟化卡片及导入/导出完整承载 service-tier、prompt-cache 和 schedule。
- 吸收/删除条件：只有 upstream 的字段、价格键、计费优先级、备份兼容和 UI 含义全部等价时才能替换本地实现。
- 验证：`cd llm && go test ./transformer/openai ./transformer/openai/responses ./transformer/anthropic`；`go test ./internal/server/biz -run 'TestComputeUsageCostForServiceTier'`；`go test ./internal/server/orchestrator -run 'TestChatCompletionOrchestrator_Process_CanceledAfterResponsesCompletionPersistsUsage'`；`go test ./internal/server/orchestrator ./internal/server/biz ./internal/server/backup ./internal/server/gql`；`cd frontend && node --test src/features/channels/data/model-price-catalog.test.mjs src/features/channels/data/model-price-form.test.mjs src/features/requests/utils/service-tier.test.mjs src/features/requests-mobile-columns.test.mjs`。

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
- 代码锚点：`internal/server/biz/thread.go`、`internal/server/biz/thread_test.go`、`internal/server/gc/gc.go`、`internal/server/gc/gc_test.go`、`internal/server/gc/gc_bodies.go`、`internal/server/gc/gc_bodies_test.go`。
- 提交锚点：merge resolution `3356f5bd`；本次载荷 GC 保护修复可用 `git log -S'PreservesActiveTraceUnderRetainedThread' -- internal/server/gc/gc_bodies_test.go` 定位。
- 合并审核：upstream 当前的级联状态写入语义与本地策略不同；解决冲突时不能恢复级联 retain/unretain，也不能只保护 trace 表而删除关联 request/usage 数据。请求、响应和 chunks 的独立载荷 GC 也必须复用 `retainedTracePredicate()`，不能剥离 retained thread 下 active trace 的 payload。
- 吸收/删除条件：只有本地明确改回 upstream 的级联策略，或 upstream 接受相同的父级保护和子级状态独立语义时才能删除。
- 验证：`go test ./internal/server/biz ./internal/server/gc -run 'TestThreadService_RetainPreservesTraceStatuses|TestWorker_cleanupTracesPreservesActiveTraceUnderRetainedThread|TestCleanupBodyPayloads_PreservesActiveTraceUnderRetainedThread'`。

## 等待 Upstream 吸收

### U01 API Key/渠道禁用、恢复和状态操作

- 生命周期：`等待上游吸收`
- 原始意图：单个坏 Key 不应误伤整个 channel，自动禁用和人工恢复必须有可解释、可授权且稳定的状态转换。
- 必须保持：Key-scoped provider 错误只累计和禁用对应 Key；transport/credential-agnostic failure 不禁用 Key；API Key policy 已处理的错误不再触发 channel 级禁用；临时禁用到期可恢复；状态图标只暴露当前状态允许且当前用户有权限的动作；倒计时、原因和安全身份显示一致。
- 代码锚点：`internal/server/biz/channel_auto_disable.go`、`internal/server/biz/channel_apikey.go`、`internal/server/biz/channel_metrics.go`、`internal/server/orchestrator/performance.go`、`frontend/src/features/channels/data/channel-status-policy.ts`、`frontend/src/features/channels/data/disabled-api-key-status.ts`、`frontend/src/features/channels/components/channels-columns.tsx`、`frontend/src/features/channels/components/channels-availability-dialog.tsx`。
- 提交锚点：`2909ddaa`、`fe7809e8`、`c9f85a35`、`fda1159c`；相关 merge resolution：`94d4f989`。
- 合并审核：upstream `783611df` 已吸收按凭据禁用、OAuth 固定身份、cron 恢复、保留凭据的永久禁用和 availability UI；`657db7f6` 仅抑制有稳定数据时的轮询报错，`7e706c0d` 仅让空字符串 error 仍可恢复，`6742293a` 仅增加 ZenMux 配额账户，`3f4f37b2` 仅增加渠道/API Key 自动禁用 webhook。这些变更不替代 fork 的 transport/credential-agnostic 错误分类、API Key policy 计数所有权、测试流量隔离、多 Key 别名/权重身份、channel 临时禁用状态和 permission gating；接入 webhook 时仍只在本地状态机实际禁用整条 channel 后通知。逐一验证这些额外语义，并让空 error 进入同一状态策略；不要把完整 Key 暴露给只读用户。
- 上游吸收条件：upstream 具备相同状态机、错误分类、权限和前端回归测试。
- 验证：`go test ./internal/server/biz ./internal/server/orchestrator -run 'DisableAPIKey|AutoDisable|CredentialAgnostic|TransportFailure'`；`cd frontend && node --test src/features/channels/data/channel-status-policy.test.mjs src/features/channels/data/disabled-api-key-status.test.mjs src/features/channels/data/disabled-api-key-dialog-contract.test.mjs`。

### U02 Codex Responses Lite 字段和约束保真

- 生命周期：`等待上游吸收`
- 原始意图：Codex Responses Lite 的 provider-private 字段不能在 inbound -> common model -> outbound 往返中丢失。
- 必须保持：Lite header 与 `reasoning.context=all_turns` 成对保留；`parallel_tool_calls` 约束不丢失；provider-private 数据保存在现有 `ProviderExtensions` sidecar，不污染通用 `llm.Request`；clone 和 retry 后仍存在。
- 代码锚点：`llm/model.go`、`llm/provider_extensions.go`、`llm/transformer/openai/responses/request_extensions.go`、`llm/transformer/openai/responses/model.go`、`llm/transformer/openai/responses/inbound.go`、`llm/transformer/openai/responses/outbound_convert.go`、`llm/transformer/openai/codex/outbound_executor_test.go`。
- 提交锚点：`f60fb767`、`753b2f26`。
- 合并审核：upstream `a6bfffa8` 已吸收 polymorphic `reasoning_content` 和 Responses body pass-through 时的 Codex metadata header allowlist，`c0233704` 补充 Codex identity headers，`6f9bfc5f` 又补齐缺失的 Lite reasoning context 并覆盖真实 inbound-to-Codex-outbound 路径；`49ade6f2` 支持兼容 relay 返回 completed JSON，但会删除 relay 上调用方明确发送的 Lite header；`e2b726eb` 扩展 raw request fields、HTTP/WebSocket continuation 和 pass-through 契约，但仍未覆盖 Lite header/context 对、`parallel_tool_calls` 与 clone/retry。保留 JSON fallback，同时确保显式 Lite 在官方和兼容 relay 都不丢失、缺失 context 补为 `all_turns`。必须同时比较 headers 和 JSON body；只保留 Lite header 而丢失 context 会形成 upstream 拒绝的非法组合。
- 上游吸收条件：upstream 有真实 inbound-to-Codex-outbound 测试，覆盖 context、parallel tool calls、clone 和 retry。
- 验证：`cd llm && go test ./transformer/openai/codex ./transformer/openai/responses -run 'ResponsesLiteRequirements|ConvertReasoning'`。

### U03 Tool Search 和跨协议工具调用语义

- 生命周期：`等待上游吸收`
- 原始意图：OpenAI Responses、Anthropic 和 Chat 转换之间必须完整传递 Tool Search、deferred tools 和工具调用参数。
- 必须保持：Tool Search 定义、调用和 output 可往返；`tool_search_output` 回放满足 upstream 必填字段；流式空参数不会生成错误调用；Responses 并行调用转 Chat 时正确聚合；Anthropic bridge 保留 deferred tools；仅在 done 事件出现的函数参数仍被保存；不同 namespace tool 不混淆。
- 代码锚点：`llm/tools.go`、`llm/metadata.go`、`llm/transformer/anthropic/`、`llm/transformer/openai/responses/`。
- 提交锚点：`e1d68898`、`8bf41241`、`5c201803`、`62edb839`、`41ba05ab`、`8a2a02c6`；相关 merge resolution：`24d949cd`。
- 合并审核：按 tool definition、call、delta、done、output 和 replay 六个阶段检查；不能只验证非流式 happy path。Upstream `2c6efdb1` 已吸收 done-only 参数、等价 JSON 和迟到 identity 处理，`a0850956` 补齐普通文件输入和更多跨格式字段，但仍没有 Tool Search 的结构化 definition/call/output、跨协议 round-trip、terminal/refusal 行为和大整数精度；这些本地语义必须独立保留并验证。
- 上游吸收条件：upstream 在 OpenAI Responses、Anthropic 和 Chat 三条转换链提供等价 round-trip 测试。
- 验证：`cd llm && go test ./transformer/openai/responses ./transformer/anthropic -run 'ToolSearch|tool_search|FunctionCall|NamespaceTool|Deferred'`。

### U04 流式响应完整性和终态错误保真

- 生命周期：`等待上游吸收`
- 原始意图：转换器不能丢失混合内容、usage、reasoning 顺序或 upstream 错误，也不能让遥测空 chunk 污染客户端流。
- 必须保持：Responses 混合流式内容保持分段和顺序；Cline 空 `choices` 遥测不向客户端透出但最终 usage 保留；interleaved reasoning 按 item 顺序输出；空或不可解析的 upstream error 回退到 status/raw status/通用消息，不返回空字符串；`response.completed`、`response.failed`、`response.incomplete`、`response.cancelled` 和 `response.canceled` 在 SSE metadata 或 JSON data 中都被识别为终态；direct stream 和 aggregation 语义一致。
- 代码锚点：`internal/server/orchestrator/inbound.go`、`llm/transformer/openai/responses/inbound_stream.go`、`llm/transformer/openai/responses/outbound.go`、`llm/transformer/openai/responses/aggregator.go`、`llm/transformer/anthropic/inbound_stream.go`、`llm/transformer/cline/outbound.go`。
- 提交锚点：`9909a8cc`、`a686efc3`、`042d41ec`；相关 merge resolution：`94d4f989`；本次终态识别变更可用 `git log -S'response.canceled' -- internal/server/orchestrator/inbound.go` 定位。
- 合并审核：upstream `4495aa3c` 已吸收 Chat `finish_reason` 与 Responses abnormal terminal status 的双向映射；`889bc8ee`、`35133b6e`、`3b7e8618` 又吸收 clean EOF 检测、pre-content retry、单次 `[DONE]`、资源上限和客户端 incomplete 报告，`a0b37424` 将上游连接中断分类为稳定的 502 错误并保留失败 execution 延迟。本轮 `c2cf9818` 验证 Anthropic clean EOF 的 tool arguments，`1908ca28` 保留转换后的 Responses session events，`e47fed9d` 给 SSE 写入增加 deadline，`a0850956` 识别 WebSocket error 的嵌套 detail/status；这些都应保留，但仍未替代 fork 的精确 `response.incomplete`、`response.failed`、`response.cancelled` 终态事件、read-only HTTP status metadata、混合分段、reasoning 顺序、空 error、request ID、Cline usage 及 direct/aggregate 一致性。终态事件后若仍出现非取消 transport error，必须按失败记录，不能因过早标记成功而吞掉健康计数。分别检查 direct stream、aggregate、normal completion、incomplete、provider error 和 transport error；不要把 empty-success retry 与 HTTP error formatting 混为一谈。
- 上游吸收条件：upstream 覆盖混合分段、reasoning 顺序、最终 usage、空 error 和 direct/aggregate 一致性。
- 验证：`go test ./internal/server/orchestrator -run 'TestIsTerminalStreamEvent_ResponsesTerminalEvents'`；`cd llm && go test ./transformer/openai/responses ./transformer/anthropic ./transformer/cline`。

### U05 失败执行响应体和终态诊断持久化

- 生命周期：`等待上游吸收`
- 原始意图：失败 request/execution 必须保留足够的 provider 证据，避免详情页只能显示 `{}`、`0 items` 或被包装后的空错误。
- 必须保持：当 `StoreResponseBody` 开启时，普通 HTTP error、流式 terminal event 和可聚合失败响应都保存原始/转换后的 body；当 `StoreChunks` 开启时，失败、取消和不完整流也保留已接收 chunks；外部存储和 inline 存储行为一致；原始 status/code/message 不被后续包装覆盖；进行中的部分响应仍隐藏；重试之间清除陈旧 raw response，避免串到下一次 execution。
- 代码锚点：`internal/server/orchestrator/inbound.go`、`internal/server/orchestrator/outbound.go`、`internal/server/orchestrator/request_execution.go`、`internal/server/biz/request.go`、`internal/server/biz/trace.go`、`frontend/src/features/requests/components/request-detail-content.tsx`。
- 提交锚点：`ea7371e1`、`bb119f4e`、`94133eee`、`098800e6`、`d926881f`。
- 合并审核：upstream `86f9829e` 已吸收失败、取消和不完整流的 DB inline chunks 持久化，但没有覆盖普通 `httpclient.Error.Body`、原始错误保真、重试清理和外部存储矩阵。按 request 与每个 execution 分开验证；检查 JSON、plain text、429/5xx、terminal SSE、外部存储、`StoreResponseBody=false` 和 `StoreChunks`；不能只测 Responses-specific error。
- 上游吸收条件：upstream 覆盖普通 `httpclient.Error.Body` 和流式终态的完整持久化矩阵，并保持 active body 隐藏。
- 验证：`go test ./internal/server/orchestrator ./internal/server/biz -run 'Terminal|ResponseBody|RequestExecution|Failed'`。

### U06 GPT-5.6、GPT-6 Astra 和 Claude Opus 5 默认模型

- 生命周期：`等待上游吸收`
- 原始意图：让 GPT-5.6、GPT-6 Astra 和 Claude Opus 5 不仅出现在 developer catalog，还能被相关渠道和 transformer 作为默认可用模型。
- 必须保持：OpenAI Chat Completions、OpenAI Responses 和 Codex 渠道的快速添加模型包含 `gpt-6-astra`；Codex transformer default models 包含 `gpt-5.6` 和 `gpt-6-astra`，缺省 `Version` 使用首个包含 Astra catalog 的 `0.153.1`；Anthropic 和 Claude Code 渠道默认模型包含 `claude-opus-5`；Claude Code transformer default models 同样包含 `claude-opus-5`。
- 代码锚点：`frontend/src/features/channels/data/config_channels.ts`、`llm/transformer/openai/codex/constants.go`、`llm/transformer/anthropic/claudecode/constants.go`。
- 提交锚点：`ab752d4b`、`0e91096d`、`44463a10`、`4eadf589`。
- 合并审核：upstream `ac70e652` 已吸收 GPT-5.6 等 developer catalog 数据，`067fff2f` 又同步了 GPT-6 Astra 的模型、价格和能力；后续仍须核对渠道快速添加模型、Codex transformer 默认模型与客户端版本，不能因展示数据存在就删除本条目。
- 上游吸收条件：upstream 的 Codex、Anthropic channel 和 Claude Code 默认模型全部等价后删除。
- 验证：`cd llm && go test ./transformer/openai/codex ./transformer/anthropic/claudecode`；`cd frontend && node --test src/features/channels/data/channel-config.test.mjs`。

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
- 必须保持：个人 API Key 始终只对创建者可见，系统 scope 不能绕过；API Key、disabled Key 和 provider quota identity 在事务提交后使缓存失效；quota checker 必须识别 `APIKeyConfigs` 等结构化凭据且不能记录或返回完整 secret；内部 quota routing 可最小化 bypass，但 GraphQL 配置读取要求 `read_settings`。
- 代码锚点：`internal/scopes/rule_personal_apikey.go`、`internal/server/gql/dashboard.resolvers.go`、`internal/server/gql/axonhub.graphql`、`internal/server/gql/tracer.go`、`internal/server/gql/tracer_test.go`、`internal/server/biz/channel_provider_quota_hook.go`、`internal/server/biz/provider_quota.go`、`frontend/src/features/channels/data/channel-input.ts`。
- 提交锚点：`b5bc14d2`、`e8656e4b`、`ccb025f8`；相关 merge resolution：`32d0699e`；本次结构化 quota 凭据预检可用 `git log -S'TestHasCredentialsForProvider_OpenCodeGoAPIKeyConfigs' -- internal/server/biz/provider_quota_url_test.go` 定位。
- 合并审核：upstream `6027d959` 已用官方 API Key usage endpoint 替代 OpenCode Go cookie scraper，并通过 beta9 migration 清除旧 cookie 配置；接受删除旧 `authCookie` schema 和 UI。`d3132241` 为 Command Code 新增 quota cookie 和基础 GraphQL 变量脱敏；合并时必须保留 fork 对嵌套 secret descriptor/path、key 名和 payload 的更完整脱敏，并让 type/base URL/credentials 变化继续触发 provider quota cache 失效。其余路径仍须分别检查 secret read/log、duplicate channel、结构化 Key、cache cold/hot path 和事务 rollback；不能用前端隐藏代替服务端权限。
- 上游吸收条件：upstream 有个人 Key ownership、结构化 quota credential、quota identity invalidation 和 `read_settings` 测试。
- 验证：`go test ./internal/scopes ./internal/server/biz ./internal/server/gql -run 'Personal|APIKeyConfigs|ProviderQuota|ReadSettings'`；`cd frontend && node --test src/features/channels/data/channel-input.test.mjs`。

### U10 反向代理来源 IP 信任边界

- 生命周期：`等待上游吸收`
- 原始意图：客户端不能通过伪造 forwarded headers 绕过 IP blocklist/rate limit 或污染访问日志。
- 必须保持：`trusted_proxies` 默认为空；只有显式代理 IP/CIDR 可提供 `X-Forwarded-For`/`X-Real-IP`；中间件统一使用 Gin 验证后的 `ClientIP()`；非法 proxy 配置启动即失败。
- 代码锚点：`internal/server/config.go`、`internal/server/server.go`、`internal/server/middleware/ip_blocklist.go`、`config.example.yml`、`docs/en/getting-started/quick-start.md`、`docs/zh/getting-started/quick-start.md`。
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
- 合并审核：upstream `2d7d7c86` 重构 SSE writer 并加入默认关闭的 keep-alive；`3b7e8618` 加入 32 MiB SSE 单事件上限，`7a5a2927` 改进 secret-safe logging，`e47fed9d` 增加 SSE 写 deadline，`a0850956` 保留 WebSocket error status/detail，但都没有吸收 HTTP error body 的可靠截断证据。合并时可复用这些上游边界，不能丢掉 `limit+1` 检测以及 `Truncated` 到 Responses 非流式错误的传播。检查 JSON error、plain text、无 heartbeat/有 heartbeat streaming 和 policy-masked error。
- 上游吸收条件：upstream 同时具备上限、可靠检测和全链路传播测试。
- 验证：`cd llm && go test ./httpclient ./pipeline -run 'LimitsErrorResponseBody|Truncat'`；`go test ./internal/server/api -run Truncat`。

### U12 Codex session header 最终规范化

- 生命周期：`等待上游吸收`
- 原始意图：前置转换确定的 Codex session ID 不能在后续 inbound header merge、retry 或 WebSocket executor 中被旧别名覆盖。
- 必须保持：resolved session ID 写入 transformer metadata；真正发送前统一写入 `Session_id` 并删除 `session_id`；普通、流式、non-stream aggregation 和 WebSocket 复用使用同一 canonical identity；header merge 不修改源 map。
- 代码锚点：`llm/transformer/openai/codex/outbound.go`、`llm/transformer/openai/codex/outbound_executor_test.go`、`llm/httpclient/utils.go`、`llm/transformer/openai/responses/websocket_executor.go`。
- 提交锚点：`5f3eeb83`；相关 merge resolution：`cf45f92e`。
- 合并审核：upstream `c0233704` 补充了 Codex identity headers，`16f08fed` 新增 Responses WebSocket 与 HTTP transport finalization，`e2b726eb` 调整了 continuation 与跨 transport 请求保真；本地 canonical session 规范化必须在 HTTP transport 删除 WebSocket-only 字段之前执行，WebSocket 路径也必须使用同一身份。追踪最终 executor 收到的 header，不要只看 `TransformRequest` 的中间结果；同时测试两种 header spelling、HTTP 字段清理、continuation 和 connection reuse。
- 上游吸收条件：upstream 在普通、流式和 WebSocket 路径都有 canonical session 测试。
- 验证：`cd llm && go test ./transformer/openai/codex ./transformer/openai/responses ./httpclient -run 'Session|Header|WebSocketExecutorReusesConnection'`。

### U13 Cline 混合渠道的 ClinePass 耗尽阻断

- 生命周期：`等待上游吸收`
- 原始意图：provider quota 当前按 channel/token limit 执行；混合渠道的 ClinePass 已耗尽时，不能降级为 `warning` 后继续把 `cline-pass/*` 请求路由到该渠道。
- 必须保持：混合渠道任一有效 ClinePass 窗口耗尽时，渠道状态为 `exhausted`、`Ready=false`，token limit 同样保留耗尽状态，并在 `exhausted_only` 模式下过滤整条渠道；仅包含 Cline usage-billing 模型的渠道，其 credits 余额仍只作展示，不参与路由阻断。
- 代码锚点：`internal/server/biz/provider_quota/cline_checker.go`、`internal/server/biz/provider_quota/cline_checker_test.go`、`internal/server/orchestrator/candidates_quota.go`、`internal/server/orchestrator/candidates_quota_test.go`。
- 用户提示：`frontend/src/locales/en/system.json`、`frontend/src/locales/zh-CN/system.json`；这是已发布行为的维护记录补漏，不新增当前 changelog 条目。
- 提交锚点：upstream 引入 `ad1176c19`；本地人工 merge resolution `42f7bd7a`。
- 合并审核：upstream `8915be26` 仍把混合渠道的 ClinePass 耗尽降级为 `warning` 并保持可路由；冲突处理必须同时核对 channel status、`Ready`、limit status、候选过滤和中英文提示，不能只看配额窗口计算是否更新。
- 上游吸收条件：upstream 提供等价的整渠道 fail-closed 行为，或实现按模型/配额池过滤并确保 `cline-pass/*` 请求不会命中已耗尽池，同时具备回归测试。
- 验证：`go test ./internal/server/biz/provider_quota -run 'TestCline_CheckQuota_(MixedScopeExhaustsWholeChannelFromPassPool|DirectOnlyUsesBalanceInformationally)$'`；`go test ./internal/server/orchestrator -run 'TestProviderQuotaSelector_(ExhaustedOnlyMode|ChannelExhaustedOverridesPerLimitAvailable)$'`。

### U14 测试流量与生产渠道健康状态隔离

- 生命周期：`等待上游吸收`
- 原始意图：channel test/probe 是诊断行为，不能改变生产路由、自动禁用或成功率统计。
- 必须保持：test source 不进入 channel/API Key failure counters、EWMA、load balancer、auto-disable、dashboard/channel metrics；测试成功也不能清空生产已累计的失败状态；测试 request/execution 本身仍可保存和查看。
- 代码锚点：`internal/ent/schema/request_execution.go`、`internal/server/orchestrator/health_state.go`、`internal/server/orchestrator/performance.go`、`internal/server/biz/channel_metrics.go`、`internal/server/gql/channel_performance_helpers.go`、`internal/server/gql/qb/throughput.go`。
- 提交锚点：`c9080fa2`。
- 合并审核：upstream `783611df` 的凭据恢复实现没有覆盖测试流量隔离；同时检查实时内存状态、应用重启后的数据库重建、dashboard raw query 和 auto-disable，只在一层过滤不够。
- 上游吸收条件：upstream 在上述四层统一排除 test source，并保留诊断记录。
- 验证：`go test ./internal/server/biz ./internal/server/orchestrator ./internal/server/gql ./internal/server/db -run 'TestSource|SkipHealthStateTracking|Probe|BackfillRequestExecutionSource'`。

### U15 前端交互和权限回归修复

- 生命周期：`等待上游吸收`
- 原始意图：小型前端回归不应在切换视图时重置用户输入、暴露无权限操作或拒绝后端合法 ID。
- 必须保持：data storage 表格数据引用稳定，编辑输入不因 render 重置；更新检查的 **包含 Beta 版本**在系统设置标签切换期间保持、页面刷新后重置；禁用 Key 提示有可读对比度；thread/trace/detail 状态动作要求 `write_requests` 并携带当前 project header；brand settings 可公开读取，但受保护 system settings 仍要求 `read_settings`。
- 代码锚点：`frontend/src/features/data-storages/index.tsx`、`frontend/src/features/channels/components/channels-columns.tsx`、`frontend/src/features/request-status-management.test.mjs`、`frontend/src/features/threads/components/thread-detail-page.tsx`、`frontend/src/features/traces/components/trace-detail-page.tsx`、`frontend/src/features/prompts/data/schema.test.mjs`、`frontend/src/features/system/components/about-settings.tsx`、`frontend/src/features/system/components/tabs.tsx`、`frontend/src/features/system/data/system-permissions.test.mjs`、`frontend/src/features/system/data/update-contract.test.mjs`。
- 用户文档：`docs/en/getting-started/quick-start.md`、`docs/zh/getting-started/quick-start.md`；当前用户影响记录在 `CHANGELOG.md` 的 `Unreleased`。
- 提交锚点：`b5d19692`、`151e1d4e`、`374cbd81`；相关 merge resolution：`3356f5bd`、`864c15a3`；本次 Beta 选择状态修复可用 `git log -S'onIncludeBetaChange' -- frontend/src/features/system/components/about-settings.tsx frontend/src/features/system/components/tabs.tsx` 定位。
- 合并审核：upstream `7cd1aee9` 已吸收部分 project role UI/effective scope 行为，`d232d343` 已等价吸收 prompt GUID 实现，`800bb72f` 已吸收 auto-refresh 控件；`131dc03d` 引入的 Beta 选择仍位于会随标签卸载的 About 子组件。本地仍保留 prompt regression test，data storage、Beta 选择的父级状态、状态动作 permission/project header 和 system settings 边界仍需独立验证。以行为测试逐项判断，不要因为 upstream 重构组件就删除测试覆盖，也不要用 `forceMount` 让所有设置页常驻。
- 上游吸收条件：对应 UI 行为和 permission test 已在 upstream 等价存在；允许逐项删除已吸收的小修复。
- 验证：`cd frontend && node --test src/features/request-status-management.test.mjs src/features/prompts/data/schema.test.mjs src/features/system/data/system-permissions.test.mjs src/features/system/data/update-contract.test.mjs`；data storage 输入需做组件交互检查。

### U16 多项目权限和邀请生命周期安全

- 生命周期：`等待上游吸收`
- 原始意图：system scope、project membership 和 project role 不能跨项目拼接；公开邀请入口不能成为权限绕过、竞争条件或 token 泄露点。
- 必须保持：effective project scopes 只在所属项目内计算；登录后只跳转到用户真实可访问的 dashboard/playground/profile；邀请绑定 active project，正确处理过期、max uses、并发注册/删除项目和 deleted row；token 使用 32-byte randomness；公开 get/register endpoint 分别限流；access log 使用 route template，不能记录真实 invitation token；错误使用结构化 4xx code。
- 代码锚点：`frontend/src/config/route-permission.ts`、`frontend/src/features/auth/data/auth-redirect.ts`、`internal/server/biz/invitation.go`、`internal/server/biz/project.go`、`internal/server/api/invitation.go`、`internal/server/routes.go`、`internal/server/middleware/ip_rate_limit.go`、`internal/server/middleware/access_log.go`。
- 提交锚点：merge resolution `94d4f989`。
- 合并审核：upstream `7cd1aee9` 已吸收角色绑定与部分 effective scope 行为，但 fork 仍保留 active project 串行化、删除竞争、过期/max-use、结构化错误、token logging 和 rate limit 约束；前端 route guard 不是服务端授权替代品。
- 上游吸收条件：upstream 服务端和前端都提供等价边界及并发/权限测试。
- 验证：`go test ./internal/authz ./internal/server/biz ./internal/server/api ./internal/server/middleware -run 'Invitation|ProjectPermission|IPRateLimit|RouteTemplate|SystemScope'`；`cd frontend && node --test src/features/auth/data/auth-redirect.test.mjs`。

### U17 Analytics 查询正确性和权限隔离

- 生命周期：`等待上游吸收`
- 原始意图：analytics 结果必须可复现、受权限约束，并明确展示失败或截断，不能把测试流量或越权 identity 维度混入统计。
- 必须保持：只统计 production usage；日期范围有上限并按配置 timezone 正确跨越 DST；project/channel/API Key/user/model 维度分别校验 system scope，project membership scope 不能冒充 system scope；保留 deleted channel attribution；用户维度和 personal Key 过滤正确；前端只请求和展示获授权维度，错误与 truncation 明示。
- 代码锚点：`internal/authz/scope.go`、`internal/server/gql/analytics.graphql`、`internal/server/gql/analytics.resolvers.go`、`internal/server/gql/analytics_helpers.go`、`frontend/src/features/analytics/`、`frontend/src/stores/analyticsStore.ts`。
- 提交锚点：merge resolution `ee209fd8`。
- 合并审核：检查 SQL/Ent predicate、timezone segmentation、limit/truncation、deleted relation fallback 和前端 query enablement；不能只比较图表外观。Upstream `ce8d6e7d` 已吸收 faceted filter 交互，但未替代 fork 的权限 gating、服务端搜索、日期感知 model options、错误重试和 user/API Key 联合权限约束。
- 上游吸收条件：upstream 有等价 resolver、authorization、DST、production-only、deleted attribution 和前端 error/truncation 测试。
- 验证：`go test ./internal/authz ./internal/server/gql -run 'Analytics|SystemScope'`；运行 frontend analytics unit tests（若新增统一入口，应在本文同步命令）。

### U18 渠道候选去重和重试预算语义

- 生命周期：`等待上游吸收`
- 原始意图：`MaxChannelRetries` 表示可尝试的不同 channel 数量，不能被同一 channel 的多条模型关联消耗完。
- 必须保持：每个 priority group 先按 channel ID 去重；同一 channel 的 model fallback 可在选中后合并；跨 priority 仍只占一个 channel budget；不同 `APIFormat` 的候选绝不合并；selection tracking 每个实际候选 channel 只记一次。
- 代码锚点：`internal/server/orchestrator/candidates.go`、`internal/server/orchestrator/candidates_loadbalance_test.go`。
- 提交锚点：merge resolution `94d4f989`。
- 合并审核：upstream `c095efbf` 已吸收可配置 load-balancer strategy，`b4d1fd04` 修复了 conditional association fallback，但两者都未等价替代 fork 的不同 channel 重试预算和 API format 隔离；不要按 association candidate 数量截断，用“同 channel 同 priority”“同 channel 跨 priority”“同 channel 不同 API format”三个矩阵检查，并同时保留 upstream 的条件 fallback 测试。
- 上游吸收条件：upstream 提供相同 retry budget 含义和三类回归测试。
- 验证：`go test ./internal/server/orchestrator -run 'CountsDistinctChannels|DoesNotMergeModelsAcrossAPIFormats'`。

### U19 内置模型目录 Schema 兼容

- 生命周期：`等待上游吸收`
- 原始意图：内置模型目录必须始终符合自身解析 schema，不能因单条上游模型数据格式错误而让整个目录加载失败。
- 必须保持：`experimental` 继续表示结构化实验模式及其价格，不接受同名布尔值；后端在远程目录和内嵌快照的输入边界丢弃非对象值，前端 schema 仍只接受结构化对象；预览状态使用现有 `status` 和 `metadata.lifecycle` 字段；同步目录后必须通过后端归一化和前端整表解析测试。
- 代码锚点：`internal/server/biz/catalog.go`、`internal/server/biz/catalog_filter.go`、`internal/server/biz/catalog_filter_test.go`、`internal/server/biz/catalogdata/providers.json`、`frontend/src/features/models/data/providers.schema.ts`、`frontend/src/features/models/data/providers.schema.test.mjs`、`frontend/src/features/models/data/providers-schema.test.mjs`、`scripts/sync/sync-model-developers.js`。
- 用户文档：维护者内部数据兼容修复，不新增配置或 API，无独立用户文档和 changelog 条目。
- 提交锚点：本次修复可用 `git log -S'normalizeCatalogExperimental' -- internal/server/biz/catalog_filter.go` 定位。
- 合并审核：upstream `4483c2e4` 已把目录拉取迁移到后端，`6f729f7c` 则用布尔/对象联合类型接受 `deepseek-v4-flash-vision-exp` 的 `experimental: true`；`dbeed3e6` 新增完整内置目录的 schema 解析测试，`0d85ba60` 补齐腾讯 HY 4 系列模型筛选，这些变更可以保留，但不能替代 fork 对布尔 `experimental` 的拒绝断言和扩展价格结构覆盖。必须在后端信任边界清洗无效值，并保持前端结构化 schema，避免价格目录调用方增加布尔分支。
- 上游吸收条件：upstream 删除或迁移该无效布尔值，并保留覆盖完整内置目录的 schema 解析测试。
- 验证：`go test ./internal/server/biz -run 'TestNormalizeCatalogExperimental|TestCatalogService'`；`cd frontend && node --test src/features/models/data/providers.schema.test.mjs src/features/models/data/providers-schema.test.mjs`。

### U20 Release 二进制应用内更新

- 生命周期：`长期保留`
- 原始意图：Docker 和“可执行文件目录由运行用户拥有”的进程管理器托管 Release 部署，应能直接从管理界面完成版本升级和历史版本回滚，不再要求操作者手工进入服务器替换二进制。
- 必须保持：复用现有 stable/beta 版本选择；只在受支持的非 Windows Release 构建显示安装入口；只接受 AxonHub 版本标签；历史列表最多展示严格早于当前版本的最近 3 个 Release，并保持 stable/beta 和 fork 类型一致；回滚目标必须重新通过服务端历史列表校验，不能信任前端传入任意 tag；升级与回滚均按当前平台下载 GoReleaser ZIP，必须校验同一 Release 的 `checksums.txt`；限制下载和解压大小；在可执行文件同目录保留 `.backup` 并原子替换；只有 `write_settings` 系统权限可查询历史、安装、回滚或重启；重启只退出进程并依赖 Docker/systemd 等 supervisor 拉起；fork 自动发布同时产生 Docker 镜像、ZIP 和 checksum，且 fork 发布不写 upstream Homebrew tap。
- 代码锚点：`internal/server/biz/update.go`、`internal/server/api/system.go`、`internal/server/routes.go`、`frontend/src/features/system/components/about-settings.tsx`、`.github/workflows/release.yml`、`.github/workflows/stable-fork-release.yml`、`Dockerfile`、`docker-compose.yml`。
- 用户文档：`docs/en/getting-started/quick-start.md`、`docs/zh/getting-started/quick-start.md`、`docs/en/index.md`、`docs/zh/index.md`、`README.md`；发布说明：`CHANGELOG.md`。
- 提交锚点：本次变更可用 `git log -S'InstallVersion' -- internal/server/biz/update.go` 定位。
- 合并审核：不能把“检测到 tag”等同于“可安装”；fork tag 必须有公开 Release 资产。历史回滚也不能接受任意用户输入的 tag，必须保持同渠道、同 fork 类型、严格旧于当前版本并重新校验服务端白名单。保留 SHA-256 校验、可信 GitHub 下载域、大小限制、同文件系统替换、权限检查和 supervisor 重启边界。Docker 必须让非 root 运行用户能写可执行文件目录；不要引入 Docker socket。
- 上游吸收条件：产品能力长期保留；仅在 fork 不再维护自有 Release，或 upstream 提供等价的二进制安装、发布资产和回归测试时重新评估。
- 验证：`go test ./internal/server/biz -run '^TestUpdate'`；`go test ./internal/server/api -run '^TestSystemUpdateHandlersRequireWriteSettings$'`；`cd frontend && node --test src/features/system/data/update-contract.test.mjs`；检查 fork release 同时包含平台 ZIP、`checksums.txt` 和 Docker tag。

### U21 Codex 定时任务启动项兼容

- 生命周期：`等待上游吸收`
- 原始意图：Codex App 以缺少 `call_id` 的 `automation_update` function output 启动定时任务时，不能让所有 Responses 上游以 400 拒绝请求。
- 必须保持：仅将缺少 `call_id` 且名称为 `automation_update` 的启动项转换为普通 user message；带 `call_id` 的真实工具结果继续使用既有工具续链语义；转换后发往上游的请求不再包含无锚点的 `function_call_output`。
- 代码锚点：`llm/transformer/openai/responses/inbound.go`、`llm/transformer/openai/responses/integration_test.go`。
- 用户文档：`docs/en/guides/codex-integration.md`、`docs/zh/guides/codex-integration.md`；当前用户影响记录在 `CHANGELOG.md` 的 `Unreleased`。
- 提交锚点：本次修复可用 `git log -S'Codex cron bootstraps' -- llm/transformer/openai/responses/inbound.go` 定位。
- 合并审核：当前 upstream 基线 `fd4158ef`（含 `a0850956`）仍把该项转换为缺少 `call_id` 的 tool message；`a0850956` 新增的 raw request preservation 和跨格式转换也没有识别该启动项。审核时必须检查完整 inbound-to-outbound 请求，不能只确认 `id` 或 `name` 被保留；最终上游载荷必须是合法 user message。
- 上游吸收条件：upstream 能识别 Codex 定时任务启动项，并有真实请求形状的 inbound-to-outbound 回归测试。
- 验证：`cd llm && go test ./transformer/openai/responses -run '^TestTransformRequest_NormalizesCodexAutomationBootstrap$' -count=1`。

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
