# 供应商优先的模型设置 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 以稳定供应商身份为顶层，每个协议仅保留一份地址配置和多个 Key，取消产品中的连接分组，并提供清爽的模型配置界面。

**Architecture:** 复用现有供应商、连接、模型和凭据实现，先增加显式供应商标识及协议字段，再通过兼容 API 和可重复数据回填切换引用。连接的既有 ID、加密 AAD 和模型 ID 优先保留；旧表名暂作持久化细节，避免与产品重构同时进行全库重命名。模型目录提供基线，用户修改保存为覆盖值。

**Tech Stack:** Go、GORM、PostgreSQL/SQLite、现有 credentialvault、React 18、TypeScript、Ant Design、Vitest、现有 OpenAPI 生成流程。

## Global Constraints

- 状态：**下一阶段待执行；本次只编写计划，未实施下述实体、迁移、API 或界面改造。** 文内路径相对于此 worktree。
- Phase 1 范围仅为 models.dev/OpenRouter 目录、模态彩标、默认停用硬编码模型名单及其验证；本计划接续其最终产物，不扩大 Phase 1 范围。
- 当前关系是 `DefaultModelProvider → UserModelProvider → UserModelProviderGroup → GroupModel`；没有独立模型账号表，分组实际承担地址、加密 Key 池及验证状态。
- `UserSelectedModel`、共享配置、会话模型选择、OCR/搜索服务以及凭据备份恢复均引用现有连接或模型身份，必须保留行为。
- 稳定供应商键不取显示名称或域名；运行时协议/适配器不再从可修改的 `ProviderName` 推导。聚合平台提供的模型保留平台命名空间。
- Key 池只在同一用户、供应商实例、协议和地址内共享；不同地址不得直接合并 Key。先复用现有整体加密格式，不引入新的密钥保险库或调度框架。
- 遵守 `backend/core/migrations/AGENTS.md`：新增 dev up/down、支持两个数据库、更新已有 v0_3 aggregate；不改已合并 dev SQL，不创建新 aggregate。
- `/Users/chenghao1/.codex/worktrees/settings-models-redesign/LazyMind` 仅作参考：可复用 `SettingsNavigationGuard`、`QuickModelSettings`、默认模型行式布局及 `settings-workspace.scss` 设计；**不合并该分支实现**。

## 文件与接口边界

- 实体/目录：`backend/core/common/orm/model_provider.go`、`modelprovider/seed.go`、`list.go`、`upstream_catalog.go`、`config/model_catalog.yaml`。
- 连接/API：复用 `modelprovider/group.go`、`group_model.go`、`remote_models.go`；新增 `modelprovider/protocol_config.go`、`provider_migration.go` 及同名测试，各自负责协议投影与迁移回填。
- 运行时/凭据：`modelprovider/runtime_source.go`、`runtime_base_url.go`、`selection.go`、`selected_provider.go`、`credential_crypto.go`、`credential_backup_source.go`、`credential_restore_contract.go`、`modelconfig/model_config.go`、`chat/chat_model_selection.go`。
- 契约/前端：`backend/core/routes.go`、`openapi_registry.go`、`frontend/scripts/openapi/specs/core.yaml`、`frontend/src/api/generated/core-client/`、`frontend/src/modules/modelProvider/api.ts`、`pages/ModelProvidersPage.tsx`、`hooks/useDefaultModelConfig.ts`、`components/DefaultModelConfigPanel.tsx`、`index.scss`、`frontend/src/modules/settings/QuickModelSettings.tsx`、`SettingsNavigationGuard.tsx`。

### Task 1: 固定供应商、协议和目录覆盖契约

**Files:** Modify 上述 ORM、seed/list/upstream 文件；Create `backend/core/modelprovider/protocol_config.go`、`protocol_config_test.go`；Test `seed_test.go`、`upstream_catalog_test.go`。

**Interfaces:** `DefaultModelProvider.SupplierKey` 是不随名称变化的目录键；`UserModelProvider.ID` 仍为用户供应商实例 ID，增加 `IsCustom` 支持同目录供应商的自定义实例。现有连接增加 `Protocol`、`RuntimeSource`，模型仍通过连接 ID 路由。模型增加可空 `CatalogModelKey`、`NameOverride`（显示名）、`MaxInputTokensOverride`；现有 `Name` 保持实际 API Model ID。

- [ ] **Step 1:** 在 `protocol_config_test.go` 写以下协议契约用例；补充“供应商改名后适配器不变”和“OpenRouter 同名模型不串到原厂”回归。
```go
func TestProtocolConfigIdentity(t *testing.T) {
    provider := orm.UserModelProvider{ID: "p", Name: "用户改过的名字"}
    config := orm.UserModelProviderGroup{ID: "existing-connection", UserModelProviderID: "p", Protocol: "openai-chat", RuntimeSource: "deepseek"}
    got, err := resolveProtocolConfig(provider, config)
    if err != nil || got.ID != config.ID || got.RuntimeSource != "deepseek" { t.Fatal(got, err) }
}
```
- [ ] **Step 2:** 在 `backend/core` 执行 `go test ./modelprovider -run 'TestProtocolConfigIdentity|Test.*Supplier|Test.*Upstream' -count=1`；先确认新类型缺失导致失败。
- [ ] **Step 3:** 定义以下最小 DTO 和 `resolveProtocolConfig(provider orm.UserModelProvider, config orm.UserModelProviderGroup) (protocolConfigItem, error)`；只接受连接所属供应商匹配且协议已注册的配置。协议值为 `openai-chat`、`openai-responses`、`anthropic-messages`、`gemini`、`native`；`native` 使用已有 `RuntimeSource` 专用适配器，不据 URL 猜测调用格式。
```go
type protocolConfigItem struct {
    ID string `json:"id"`
    Protocol string `json:"protocol"`
    RuntimeSource string `json:"runtime_source"`
}
```
- [ ] **Step 4:** 为目录供应商补显式 `supplier_key`，保留现有数据库 ID；自定义名称仅影响显示。目录模型读取顺序固定为“用户非空覆盖 > 当前上游基线”，自定义模型保存完整配置；显式清空和继承使用不同 JSON 语义。
- [ ] **Step 5:** 重跑 Step 2；新增目录更新时保留用户名称/上下文覆盖、无覆盖字段获得新值的测试。提交本任务的实体与契约变更，不夹带 UI。

### Task 2: 先扩展表结构，再安全回填连接和模型

**Files:** Create `backend/core/migrations/dev_mode/v0_3/20261022034023_provider_first_models.up.sql`、对应 `.down.sql`、`backend/core/migrations/provider_first_models_test.go`、`backend/core/modelprovider/provider_migration.go`、`provider_migration_test.go`；Modify 既有 `version_mode/v0_3/20260805000000_workflow_runtime_release.up.sql` 及 `.down.sql`。执行时若该时间戳已被占用，使用 `date -u +%Y%m%d%H%M%S` 生成新版本，不覆盖文件。

**Interfaces:** `MigrateProviderProtocolConfigs(ctx context.Context, db *gorm.DB) error` 按用户执行事务回填；局部迁移映射保存 `kind, old_id, new_id`，仅用于旧连接/模型 ID 的兼容解析。

- [ ] **Step 1:** 用现有 migration 测试的 `openDatabase`/`applySQL` 写双数据库 fixture：同供应商的两个同地址连接、一个不同地址连接、重复及自定义模型、默认/共享选择、会话固定模型、备份绑定和待处理 outbox。
- [ ] **Step 2:** 执行 `go test ./migrations -run TestProviderFirst -count=1 -v`，确认 fixture 在新字段/迁移缺失时失败；PostgreSQL 必须配置 `MIGRATION_TEST_POSTGRES_DSN`，出现 SKIP 不算通过。
- [ ] **Step 3:** 先增加字段、覆盖列及迁移映射；内置实例唯一性改为“用户 + 目录供应商，且非自定义、未删除”，协议唯一性为“用户 + 供应商实例 + 协议”。复用旧连接 ID 作为协议配置 ID；合并完成后再建立唯一索引。
```go
// 在持有用户事务的回填中调用；不会读取或修改其他用户的数据。
func MigrateProviderProtocolConfigs(ctx context.Context, db *gorm.DB) error
```
- [ ] **Step 4:** 合并顺序为：显式映射供应商/协议 → 同协议同规范地址选择最早连接作保留项 → Key 去重 → 模型合并 → 引用迁移。地址规范化仅使用已验证的等价规则，保留路径/版本/固定地址语义；不同地址迁移到“供应商 · 原连接名”的自定义实例，保留其连接及模型 ID。
- [ ] **Step 5:** 有新增 Key 时，解密原池、按保留连接 ID 和递增 revision 重加密；没有修改池时保持原密文与 revision。**不能把原密文复制给新 ID**：当前 AAD 包含用户 scope、`local_provider_group_id` 和 revision；安全存储不可用则回滚，启动继续旧读路径并呈现可重试状态。
- [ ] **Step 6:** 重复模型优先保留被引用项；建立所有被合并模型/连接的 ID 映射，更新默认/共享选择及当前会话引用；历史显示快照保持原文，运行时路由通过映射解析。迁移绑定/outbox 不重置云端记录身份或修订号；保留原记录用于回滚，禁止静默丢弃两份不同云端备份。
- [ ] **Step 7:** 双数据库分别执行 up → 回填两次 → down，比较 dev 全路径与 aggregate 最终状态；验证模型/Key/选择/备份未丢失、无跨用户关联。执行 `go test ./modelprovider ./migrations -run 'TestProviderFirst|Test.*Migration' -count=1 -v` 后提交。

### Task 3: 增加供应商 API，并保留旧分组客户端

**Files:** Modify routes、OpenAPI、`group.go`、`group_model.go`、`remote_models.go`、`frontend/src/modules/modelProvider/api.ts`；Test `protocol_config_test.go`、`group_key_test.go`、`backend/core/openapi_gen_test.go`。

**Interfaces:** 在现有 `/model_providers` 下增加 `GET /{id}`、`PUT /{id}/protocols/{protocol}`、`POST|DELETE /{id}/protocols/{protocol}/keys`、`POST /{id}/protocols/{protocol}:check`、`GET /{id}/protocols/{protocol}/models`；模型 CRUD/同步沿用现有处理器，通过协议配置解析连接 ID。

- [ ] **Step 1:** 写 HTTP 契约测试：供应商详情返回配置和模型；重复协议配置更新原行；两个 Key 可添加/独立删除；旧 group URL 和旧 key ID 经映射仍能操作正确池；跨用户 ID 返回 404。
- [ ] **Step 2:** 执行 `go test ./modelprovider -run 'TestProtocol|TestGroupKey|TestAddKey|TestRemoveKey' -count=1`，先确认新路由/字段尚未实现导致失败。
- [ ] **Step 3:** 详情响应复用 `keys: [{id, masked}]` 和 `has_api_key`，绝不增加明文读取接口；旧 `/groups` 继续返回兼容结构，写操作转到同一协议配置，删除最后一个 Key 后撤销该池验证状态。旧客户端创建第二个不同地址时创建自定义实例，响应明确返回其实际供应商 ID。
```json
{"id":"provider-existing","supplier_key":"deepseek","protocols":[{"id":"connection-existing","protocol":"openai-chat","runtime_source":"deepseek","keys":[{"id":"opaque-key-id","masked":"********...1234"}]}]}
```
- [ ] **Step 4:** 同时更新 `openapi_registry.go` 与 `frontend/scripts/openapi/specs/core.yaml`；在 `frontend` 执行 `node scripts/openapi/generate-api.mjs core`、`pnpm gen:openapi:check`，不得手改生成客户端。
- [ ] **Step 5:** 在 `backend/core` 执行 `go test . -run 'TestOpenAPI|Test.*ModelProvider' -count=1` 并重跑 Step 2；提交新 API、兼容处理与生成产物。

### Task 4: 切换运行时、默认模型和备份恢复

**Files:** Modify 运行时/凭据边界所列文件及 `backend/core/credentialvault/repository.go`；Test `runtime_source_test.go`、`runtime_base_url_test.go`、`selection_test.go`、`credential_crypto_d2_test.go`、`credential_restore_d4_test.go`、`backend/core/modelconfig/evo_model_config_test.go`、`backend/core/chat/chat_model_selection_test.go`。

**Interfaces:** 运行时以模型 ID 解析 `provider instance + protocol config + runtime_source + base_url + decrypted key pool`；兼容阶段旧 ID 先经过 Task 2 映射，云端 `source=cloud` 仍走独立现有路径。

- [ ] **Step 1:** 新增回归：显示名称修改不改 source；代理地址不将原厂变成 OpenAI；每个协议只使用自己的 Key 池；默认模型、自进化、管理员共享 embedding、固定会话和 fork 仍解析原模型。
- [ ] **Step 2:** 执行 `go test ./modelprovider ./modelconfig ./chat -run 'Test.*(Protocol|Runtime|Selection|Fork|Credential|Restore)' -count=1`，确认新增场景先失败。
- [ ] **Step 3:** 将 `LazyLLMSource(row.ProviderName)` 调用点改为连接的显式 `RuntimeSource`；保留旧记录映射兜底直到回填完成，地址继续走现有适配器的归一化规则。在现有调用边界验证/补齐顺序轮换，以 mock transport 断言 `key1 → key2 → key1`，不得将整个换行 Key 池作为 Authorization 发出；逐个检查新 Key，检测失败不使已有可用 Key 失效。
```json
{"source":"deepseek","model":"deepseek-chat","base_url":"https://api.deepseek.com/","api_key":"<仅存在于服务端运行时内存>"}
```
- [ ] **Step 4:** 备份/恢复继续以稳定连接 ID 绑定；恢复旧 catalog key 到新 supplier key，通过显式别名映射完成。覆盖同机备份恢复、第二设备恢复、修订冲突、临时凭据清理、回填失败回滚，不打印 Key。
- [ ] **Step 5:** 重跑 Step 2 与 `go test ./credentialvault -count=1`；验证 OCR/搜索未随模型页改造改变选择，云端登录、额度和本地回退沿用现有契约；提交。

### Task 5: 改成供应商双栏配置，移除产品分组

**Files:** Modify 前端边界所列文件及 `frontend/src/i18n/locales/zh-CN.ts`、`en-US.ts`；Create `frontend/src/modules/modelProvider/pages/ModelProvidersPage.providerFirst.test.tsx`。新增 UI 仅按需要从原大页面抽出 `components/ProviderProtocolPanel.tsx`，不建设通用设置框架。

**Interfaces:** `api.ts` 使用 Task 3 生成客户端；路由保存供应商实例 ID 和协议标识，表单草稿保留在现有导航保护中。旧 `editor=provider&item=...&group=...` URL 经兼容映射定位新详情。

- [ ] **Step 1:** 写界面测试：从 `ModelProvidersPage.cloudSystem.test.tsx` 复用 router、cloud、backup 和 request mock 初始化方式；将详情响应设为 Task 3 的 JSON，并扩充两 Key/两协议/空列表情形。使用真实中文 locale 初始化 i18n，渲染 `ModelProviderPage` 后验证以下用户行为结果。
```tsx
render(<ModelProviderPage />);
expect(await screen.findByRole("navigation", { name: "供应商" })).toBeVisible();
expect(screen.queryByRole("button", { name: "添加分组" })).not.toBeInTheDocument();
fireEvent.click(screen.getByRole("button", { name: "DeepSeek" }));
expect(screen.getByRole("button", { name: "添加 API Key" })).toBeVisible();
```
- [ ] **Step 2:** 在 `frontend` 执行 `pnpm test src/modules/modelProvider/pages/ModelProvidersPage.providerFirst.test.tsx`，确认旧分组界面导致失败。
- [ ] **Step 3:** 左栏显示搜索、供应商彩色图标、名称和启用状态；右栏显示标题、Key 列表、协议/地址、检查及模型列表。单协议直接展示，多协议用轻量切换；高级路径选项折叠。移除分组计数、创建/展开和 `source:` 标签；默认模型保持独立的紧凑任务行。
```css
.provider-settings { display: grid; grid-template-columns: 240px minmax(0, 1fr); }
@media (max-width: 800px) { .provider-settings { grid-template-columns: minmax(0, 1fr); } }
```
- [ ] **Step 4:** 窄屏采用“供应商选择 → 详情 → 返回”且保留选中供应商/协议，避免横向溢出；补键盘焦点、错误重试、同步空结果、未保存切换保护、旧深链及中英文文案。彩标只读 Phase 1 目录元数据，未知能力不臆造。
- [ ] **Step 5:** 执行 `pnpm test src/modules/modelProvider src/modules/settings/QuickModelSettings.visibility.test.tsx src/modules/settings/SettingsNavigationGuard.test.tsx`、`pnpm typecheck:all`；浏览器检查 1440/1024/390px、键盘路径和两种语言，保存截图后提交 UI 变更。

### Task 6: 验收、回滚演练和兼容收口

**Files:** Test `backend/core/migrations/provider_first_models_test.go`、`backend/core/modelprovider/provider_migration_test.go` 及上述端到端边界；Modify 本计划中的勾选状态与实际验证记录。

**Interfaces:** 一个版本内保留旧 API 和迁移映射；只有确认旧客户端窗口结束、数据回填完成后，才另开清理改动删除旧路由或物理旧表名。

- [ ] **Step 1:** 从已有数据库升级，分别演练同址合并、异址自定义实例、供应商改名、目录更新、默认与共享模型、固定旧会话、Key 增删、备份恢复和失败重试；确认升级前后均能发起真实模型请求。
- [ ] **Step 2:** 使用测试专用 PostgreSQL DSN 执行 `go test ./migrations ./modelprovider ./modelconfig ./credentialvault ./chat -count=1`；保留双数据库 up/down 及 aggregate 一致性证据，不以静态 SQL 字符串断言代替执行。
- [ ] **Step 3:** 在 `frontend` 执行 `pnpm gen:openapi:check`、`pnpm test src/modules/modelProvider src/modules/settings`、`pnpm typecheck:all`、`pnpm build`。失败归因后修复本次引入项；历史基线失败单独记录，不伪称全绿。
- [ ] **Step 4:** 审查 diff 确认 Phase 1 硬编码默认关闭、目录构建行为、未知能力显示、云端账户隔离均未退化；用迁移前备份演练回滚，检查被合并记录仍可还原；提交验收记录。

## 官方参考与设计依据

- [Cherry Studio 模型服务设置](https://www.cherryai.com/docs/en/pre-basic/settings/providers/)：供应商管理地址、Key 和模型，单供应商支持多 Key 轮询；借鉴交互，不照搬其 API Host 拼接规则。
- [Cherry 官方供应商/模型注册表架构](https://github.com/CherryHQ/cherry-studio/blob/main/docs/references/provider-model/provider-registry.md)：稳定供应商身份、协议 endpoint 配置，以及目录基线叠加用户差异；作为本计划实体边界参考。
- [models.dev 官方 API](https://models.dev/)：目录入口 `https://models.dev/api.json?type=all`，包含专用模型类型；供应商供给、输入/输出模态和限制作为目录基线，不能当作当前 Key 已获授权的证明。
- [OpenRouter Models API](https://openrouter.ai/docs/api/api-reference/models/list-all-models-and-their-properties)：以 API 返回的模型 ID、模态与上下文信息为准；保留 OpenRouter 命名空间，实际 Key 可用性由检查/请求确认。
