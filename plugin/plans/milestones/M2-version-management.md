# M2 — 版本管理 + 引用 + 组件内对话修改

## 概述

**里程碑目标**：在 M1 打通的生图管道基础上，叠加持久化版本历史、图片引用到对话框、组件内继续修改能力，完善插件框架的核心数据层。

**前置依赖**：M1 完成（plugin_sessions 表存在，SSE 管道跑通，ImageCard 可展示）。

**验收标准（一句话）**：生图 → 对话修改两次 → 版本回退 → 制造分叉 → 版本树结构正确，所有操作刷新后数据不丢失。

---

## 一、需要实现的功能

### 1.1 数据层扩展（三张新表）

**`plugin_session_steps` 表**

```sql
plugin_session_steps
  id               VARCHAR(36) PK
  session_id       VARCHAR(36) INDEX     -- FK → plugin_sessions.id
  step             VARCHAR(64)           -- 如 'generate'
  step_mode        VARCHAR(16)           -- 'human' | 'auto'
  step_status      VARCHAR(16)           -- 'running' | 'waiting' | 'done'
  created_at       TIMESTAMP
  updated_at       TIMESTAMP
```

**`plugin_session_artifacts` 表**

```sql
plugin_session_artifacts
  id               VARCHAR(36) PK
  session_id       VARCHAR(36) INDEX
  step_id          VARCHAR(36) INDEX     -- FK → plugin_session_steps.id
  artifact_id      VARCHAR(64)           -- 如 'image_url'、'prompt_used'
  head_version_id  VARCHAR(36)           -- 可移动指针，FK → plugin_session_versions.id
  created_at       TIMESTAMP
  updated_at       TIMESTAMP
  UNIQUE (session_id, step_id, artifact_id)
```

**`plugin_session_versions` 表**

```sql
plugin_session_versions
  id                VARCHAR(36) PK
  session_id        VARCHAR(36) INDEX
  artifact_id       VARCHAR(36) INDEX    -- FK → plugin_session_artifacts.id
  parent_version_id VARCHAR(36)          -- NULL 表示根节点
  content           JSONB                -- 内容；≥64KB 时存 OSS URL ref
  change_source     VARCHAR(16)          -- 'ai' | 'human'
  change_summary    VARCHAR(512)         -- 可选描述
  created_at        TIMESTAMP
```

**HEAD 指针语义**：`head_version_id` 是可移动指针。回退 = 移动指针，历史版本链不删除，支持任意节点跳转与分叉（分叉后新 patch 以当前 head 为 parent，形成新分支）。

**内容存储策略**

| artifact 类型 | 存储方式 |
|-------------|---------|
| JSON 结构 | 全量 JSONB |
| 纯文本 < 64KB | 全量 JSONB |
| 纯文本 ≥ 64KB | OSS/S3，content 改为 `{"type":"ref","url":"..."}` |
| 图片 / 视频 | 始终存 URL |

M2 先实现前两种（JSONB 存储），大文本 OSS 放至 M4。

**M1 meta 数据迁移**

M1 将 artifact 临时存在 `plugin_sessions.meta`，M2 需要：
1. 提供迁移脚本：将现有 meta 中的 artifact 数据迁移到 artifacts/versions 表
2. Go SSE Proxy 中原来写 meta 的路径改为写 artifacts 表

### 1.2 版本快照逻辑

**AI 路径（SSE patch → 自动快照）**

```
SSE patch 事件 (type='patch')
  → Go SSE Handler 接收
  → 更新 plugin_session_artifacts 对应记录
  → 创建新的 plugin_session_versions 记录（change_source='ai'）
  → 更新 head_version_id 指向新版本
```

每批 SSE patch 完成后（`finish_reason` 出现时）自动打一个快照。流式过程中不逐 token 打快照，只在流完成时打。

**人工路径（PATCH /artifacts → 自动快照）**

```
PATCH /plugin-sessions/:id/artifacts
  → 验证 artifact_id 属于该 session
  → 防抖 5 秒（同一 artifact 5 秒内多次编辑合并为一个版本）
  → 创建新的 plugin_session_versions 记录（change_source='human'）
  → 更新 head_version_id
```

**发送对话前强制快照**：前端在发送含 `plugin_context` 的对话请求前，先调 `PATCH /artifacts` 确保当前编辑内容已落库，保证版本连续。

### 1.3 版本管理 REST API

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/plugin-sessions/:id/artifacts/:artifact_id` | 获取 HEAD 版本内容 |
| `PATCH` | `/plugin-sessions/:id/artifacts` | 人工编辑，自动打快照 |
| `GET` | `/plugin-sessions/:id/artifacts/:artifact_id/versions` | 获取版本树（含 parent 关系） |
| `POST` | `/plugin-sessions/:id/artifacts/:artifact_id/rollback` | 回退：移动 head_version_id 到指定版本 |

**版本树响应格式**

```json
{
  "artifact_id": "image_url",
  "head_version_id": "v-xxx",
  "versions": [
    {
      "id": "v-001",
      "parent_version_id": null,
      "change_source": "ai",
      "change_summary": "initial generation",
      "created_at": "..."
    },
    {
      "id": "v-002",
      "parent_version_id": "v-001",
      "change_source": "ai",
      "change_summary": "revised with better prompt",
      "created_at": "..."
    }
  ]
}
```

### 1.4 `PATCH /artifacts` 人工编辑接口

```json
// 请求体
{
  "artifact_id": "prompt_used",
  "content": "a cat wearing a top hat, photorealistic, 8k",
  "change_summary": "用户手动修改 prompt"
}

// 响应
{
  "version_id": "v-003",
  "head_version_id": "v-003"
}
```

### 1.5 前端：`VersionHistory` 组件

**功能需求**

- 展示当前 artifact 的版本树（树形或列表形式）
- 每个版本节点显示：时间戳、来源（AI/人工）、变更摘要
- 点击某个版本可预览该版本内容（不立即回退）
- 「回退到此版本」按钮触发 `POST /rollback`
- 显示当前 HEAD 版本（高亮标识）

**状态管理（pluginSessionStore 扩展）**

```ts
interface ArtifactState {
  content: any
  headVersionId: string
  versions: VersionNode[]
}

interface VersionNode {
  id: string
  parentVersionId: string | null
  changeSource: 'ai' | 'human'
  changeSummary: string
  createdAt: string
}
```

### 1.6 前端：`DiffViewer` 组件

**功能需求**

- 接受两个版本 ID，从 API 拉取内容并做 diff 展示
- 支持 `text` 类型 artifact 的行级 diff（使用 `diff` 库）
- 支持 `image` 类型的并排对比（两张图左右显示）
- `DiffViewer` 集成在 `VersionHistory` 的版本对比模式中

### 1.7 前端：图片引用到对话框（`plugin_context` 注入）

**`ImageCard` 新增「引用到对话框」交互**

- `ImageCard` 底部添加「引用此图片」按钮
- 点击后将图片 URL 和当前 `plugin_context`（session_id、plugin_id、step、event='user_edit'）序列化到对话框的 input context
- 用户发送消息时，请求体自动携带 `plugin_context.payload.cited_image_url`

**请求体格式**

```json
{
  "input": ["在这张图片基础上，把帽子改成皇冠"],
  "plugin_context": {
    "plugin_session_id": "ps-xxx",
    "plugin_id": "image-plugin",
    "step": "generate",
    "event": "user_edit",
    "payload": {
      "artifact_id": "image_url",
      "cited_image_url": "https://..."
    }
  }
}
```

**前端框架自动填充**

`PluginShell` 维护当前活跃的 `plugin_context`，在用户发送对话时自动附加，无需插件开发者手动处理。

---

## 二、实施计划

### 阶段划分

**Week 1 — 数据层**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| 创建三张新表的 migration 文件 | Backend (Go) | 0.5d |
| Go ORM：三个结构体 + CRUD 方法 | Backend (Go) | 1d |
| M1 meta 数据迁移脚本 | Backend (Go) | 0.5d |
| SSE Proxy：patch 事件改为写 artifacts 表 + 打快照 | Backend (Go) | 1d |

**Week 2 — 版本管理 API**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `GET /artifacts/:id`（HEAD 内容） | Backend (Go) | 0.5d |
| `PATCH /artifacts`（人工编辑 + 快照） | Backend (Go) | 1d |
| `GET /versions`（版本树） | Backend (Go) | 0.5d |
| `POST /rollback`（移动 head） | Backend (Go) | 0.5d |
| 版本树单元测试 | Backend (Go) | 0.5d |

**Week 3 — 前端组件**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `pluginSessionStore` 扩展（artifacts + versions 状态） | Frontend (TS) | 0.5d |
| `VersionHistory` 组件 | Frontend (TS) | 1d |
| `DiffViewer` 组件（text + image 两种模式） | Frontend (TS) | 1d |
| `ImageCard`「引用到对话框」按钮 | Frontend (TS) | 0.5d |
| `PluginShell` 自动填充 `plugin_context` | Frontend (TS) | 0.5d |

**Week 4 — 联调 + 迁移验证**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| M1→M2 数据迁移验证（staging 环境） | All | 0.5d |
| 防抖快照逻辑联调 | Backend + Frontend | 0.5d |
| 版本分叉场景联调（回退后再修改） | All | 0.5d |
| E2E 测试脚本编写 | All | 0.5d |

---

## 三、测试方案

### 3.1 单元测试

**版本树逻辑（Go）**

```go
func TestCreateVersion_AISource(t *testing.T) {
    // SSE patch 完成后，验证新版本被创建，change_source='ai'
}

func TestCreateVersion_HumanSource(t *testing.T) {
    // PATCH /artifacts 后，验证新版本 change_source='human'
}

func TestRollback_MovesHeadPointer(t *testing.T) {
    // 创建 v1→v2→v3，rollback 到 v1
    // 验证 head_version_id == v1，v2/v3 记录仍存在
}

func TestRollback_ThenPatch_CreatesFork(t *testing.T) {
    // 回退到 v1，再 AI patch
    // 验证新 v4.parent_version_id == v1（分叉）
    // 验证 v2/v3 仍存在（未删除）
}

func TestPatchDebounce_MergesWithin5s(t *testing.T) {
    // 5 秒内两次 PATCH，验证只创建一个版本
}

func TestPatchDebounce_SeparatesAfter5s(t *testing.T) {
    // 间隔 >5s 的两次 PATCH，验证创建两个版本
}
```

**内容存储（Go）**

```go
func TestSmallContent_StoredAsJSONB(t *testing.T) {
    // < 64KB 内容直接存 JSONB
}

func TestOSSRef_Format(t *testing.T) {
    // 大文本存 OSS 后，content 格式为 {"type":"ref","url":"..."}
    // M2 可先 skip，M4 实现大文本 OSS 后补充
}
```

### 3.2 API 接口测试

**版本树 API**

```
# 获取版本树
GET /api/v1/plugin-sessions/:id/artifacts/image_url/versions
期望：200，versions 数组，head_version_id 正确

# 回退
POST /api/v1/plugin-sessions/:id/artifacts/image_url/rollback
Body: { "version_id": "v-001" }
期望：200，head_version_id 变为 v-001

# 再次获取，验证 HEAD 已移动
GET /api/v1/plugin-sessions/:id/artifacts/image_url
期望：内容为 v-001 对应的内容
```

**人工编辑**

```
PATCH /api/v1/plugin-sessions/:id/artifacts
Body: { "artifact_id": "prompt_used", "content": "new prompt" }
期望：200，version_id 非空

# 发送 10 次，验证防抖后版本数 <= 2
```

### 3.3 前端测试

**`VersionHistory` 组件**

```ts
describe('VersionHistory', () => {
  it('renders version list with correct count', () => {
    // mock API 返回 3 个版本，验证渲染 3 个节点
  })
  it('highlights current HEAD', () => {
    // head 版本节点有特殊样式
  })
  it('rollback triggers API call', async () => {
    // 点击「回退到此版本」，验证 POST /rollback 被调用
  })
})
```

**`DiffViewer` 组件**

```ts
describe('DiffViewer', () => {
  it('shows added/removed lines for text diff', () => {
    // 两段文本 diff，验证变更行高亮
  })
  it('shows two images side by side for image diff', () => {
    // image 类型，验证两个 img 元素并排
  })
})
```

**`plugin_context` 自动填充**

```ts
it('attaches plugin_context when sending message with cited image', () => {
  // 点击「引用此图片」后发送消息
  // 验证请求体包含 plugin_context.payload.cited_image_url
})
```

### 3.4 端到端（E2E）验收测试

**完整版本管理流程**

```
1. 触发图片生成（版本 v1 自动创建）
2. 引用图片到对话框，发送「改成皇冠」
   → 等待生成完成（版本 v2 自动创建）
3. 再次修改「改为斗笠」（版本 v3）
4. 打开 VersionHistory，验证显示 3 个版本
5. 点击回退到 v1
   → 验证图片变回 v1 的内容
   → 验证 DB head_version_id == v1 的 ID
6. 在 v1 基础上再次修改（制造分叉）
   → 验证版本树中 v4.parent == v1
   → 验证 v2、v3 仍在历史中可查
7. DiffViewer：选择 v1 和 v3 对比
   → 验证展示 diff
8. 刷新页面
   → 验证当前 HEAD 内容正确（v4）
   → 验证版本树完整
```

---

## 四、验收标准

| 验收项 | 验收方式 | 通过标准 |
|--------|----------|----------|
| 三张新表正确创建 | DB 检查 | migration 执行成功，表结构符合设计 |
| AI patch 自动打快照 | 单元测试 + E2E | 每次 AI 生成完成后 versions 表有新记录 |
| 人工编辑防抖快照 | 单元测试 | 5s 内多次编辑合并为 1 个版本 |
| 版本回退正确移动 HEAD | 单元测试 + E2E | rollback 后 GET artifact 返回目标版本内容 |
| 版本分叉历史不丢失 | 单元测试 | 回退后新建版本，旧分支仍可查 |
| VersionHistory 展示正确 | E2E | 版本列表数量、顺序、HEAD 标识正确 |
| DiffViewer 展示正确 | 前端测试 | text/image 两种类型均能正确对比 |
| 图片引用携带 plugin_context | 前端测试 + E2E | 请求体包含正确的 plugin_context |
| 刷新后数据不丢失 | E2E | 所有版本历史刷新后仍在 |

---

## 五、注意事项与风险

1. **M1 meta 迁移**：M2 上线前需要对 M1 存入 meta 的数据做兼容处理，建议先在测试环境验证迁移脚本。
2. **防抖实现位置**：防抖在 Go Handler 层实现（而非前端），保证多端操作时只打一次快照。
3. **大文本 OSS**：M2 暂不实现 64KB 以上的 OSS 存储，超大内容暂时全量存 JSONB（会有性能风险）；M4 补全。
4. **版本树深度**：长期使用后版本树可能很深，`GET /versions` 需要考虑分页或最大深度限制，M2 先做简单实现，后续优化。
5. **并发快照竞争**：同一 artifact 并发 SSE patch 可能导致版本乱序，需在 Go 层加行级锁或使用乐观锁。
