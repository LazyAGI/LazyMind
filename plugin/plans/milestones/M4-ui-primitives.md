# M4 — UI 原语补全

## 概述

**里程碑目标**：补全写作插件和 PPT 插件所需的全部 UI 原语，完善框架基础能力（大文本 OSS 存储、Go 上下文注入、多插件共存检测），使图片插件成为完整的框架验证载体，为 M5/M6 的业务插件开发扫清障碍。

**前置依赖**：M3 完成（多步骤 AsyncJob、human/auto 模式、StepProgress）。

**验收标准（一句话）**：图片标注后发送对话，AI 能感知标注内容；通过 scenario.md 路由，用户说「没问题继续」触发正确工具调用；拖拽排序后刷新数据不丢失；大文本正确切换为 OSS 存储。

---

## 一、需要实现的功能

### 1.1 `ImageAnnotator` 组件

**功能需求**

- 在图片上绘制圈点（圆圈/箭头/矩形框）
- 在标注上添加文字说明
- 「发送标注给 AI」按钮：将标注信息序列化后附加到 `plugin_context.payload.annotations`
- 标注数据持久化（作为 artifact 存储）

**标注数据格式**

```ts
interface Annotation {
  id: string
  type: 'circle' | 'arrow' | 'rect'
  x: number          // 相对图片宽度的百分比
  y: number          // 相对图片高度的百分比
  width?: number
  height?: number
  label?: string     // 文字标注
  color: string
}
```

**`plugin_context.payload` 格式扩展**

```json
{
  "artifact_id": "image_url",
  "cited_image_url": "https://...",
  "annotations": [
    { "id": "a1", "type": "circle", "x": 0.3, "y": 0.4, "label": "把这里的帽子改成皇冠" }
  ]
}
```

**技术选型**：使用 `fabric.js` 或 `konva` 实现画布交互；支持撤销/重做（本地状态，不创建版本）；导出为 base64 或坐标 JSON 二选一（推荐坐标 JSON，更易传给 AI）。

### 1.2 `RichEditor` + `SidebarTOC` 组件

**`RichEditor` 功能需求**

- Markdown 编辑器，支持实时预览（split 或 live preview 模式）
- 支持选中文本 → 「引用到对话框」操作
  - 选中文本后出现浮动工具条，点击「引用」
  - 将选中内容附加到 `plugin_context.payload.cited_text`
- 支持 inplace 编辑（直接在预览模式下点击编辑，防抖 5s 触发 PATCH）
- 大文本（≥64KB）分片懒加载展示

**`SidebarTOC` 功能需求**

- 解析 Markdown heading，生成目录
- 点击目录项滚动到对应位置
- 高亮当前视口中的章节
- 与 `RichEditor` 组合使用（通常作为 `PluginShell` 侧栏内容）

**技术选型**：`@uiw/react-md-editor` 或 `codemirror`；选中引用使用浏览器 `Selection API`；目录滚动使用 `IntersectionObserver`。

**选中引用 `plugin_context` 格式**

```json
{
  "artifact_id": "draft",
  "cited_text": "用户选中的段落文字...",
  "cited_range": { "start": 120, "end": 280 }   // 字符偏移，可选
}
```

### 1.3 `OutlineTree` 组件

**功能需求**

- 展示树形大纲结构（父节点 / 子节点层级）
- 每个节点可折叠/展开
- 支持 `dnd-kit` 拖拽排序（同层级节点间）
- 拖拽排序完成后自动 PATCH artifact（防抖 300ms）
- 节点支持行内编辑（双击进入编辑状态）

**大纲数据格式**（artifact `outline` 的内容）

```ts
interface OutlineNode {
  id: string
  title: string
  level: number       // 1=一级标题, 2=二级标题
  children: OutlineNode[]
  summary?: string    // 该节点的内容摘要
}
```

**拖拽排序持久化**：排序变更后，前端立即更新本地状态（乐观更新），同时 PATCH `/artifacts` 写库；若 PATCH 失败则回滚本地状态并提示错误。

### 1.4 `PageCarousel` 组件

**功能需求**

- 分页展示（适用于 PPT 每一页）
- 支持上一页/下一页导航按钮
- 缩略图条（底部或侧边），点击可跳转到指定页
- 支持 `dnd-kit` 拖拽调整页序（在缩略图条中拖拽）
- 页序变更后自动 PATCH artifact 的 pages 数组顺序
- 支持全屏展示模式

**页面数据格式**（artifact `pages` 的内容）

```ts
interface PPTPage {
  id: string
  index: number       // 页序（由 pages 数组顺序决定）
  title: string
  content: string     // 设计文字（Markdown）
  imageUrl?: string   // 完稿图片 URL
  notes?: string      // 备注
}
```

### 1.5 `SubQuestionList` 组件

**功能需求**

- 展示工作流执行过程中产生的子问题及其答案
- 子问题来源：框架从 SSE `tool_results` 自动收集，写入 `sub_questions` artifact
- 支持折叠/展开单个问题的答案
- 只读展示，无需编辑交互

**子问题数据格式**

```ts
interface SubQuestion {
  id: string
  question: string
  answer: string
  timestamp: string
  step: string       // 在哪个步骤产生
}
```

**框架自动收集逻辑**：在 SSE Handler（Python 侧）中，监听 `tool_results` 类型事件，提取 RAG 搜索、LLM 问答等工具调用的 Q/A，emit `PluginEvent(type='patch', artifact_id='sub_questions', op='append', value={...})`。

### 1.6 `ReferencePanel` 组件

**功能需求**

- 展示工作流中 RAG 检索到的参考文献
- 来源：框架从 SSE `sources` 字段自动收集，写入 `references` artifact
- 每条参考展示：标题、摘要片段、来源文档名
- 支持点击跳转到知识库文档详情

**框架自动收集逻辑**：与 `SubQuestionList` 类似，监听 SSE `sources` 字段，自动 append 到 `references` artifact。

### 1.7 多插件共存检测

**规则**：同一 `plugin_id` 在同一 conversation 内只允许一个活跃实例；不同 `plugin_id` 可并存。

**Go 层实现**

`POST /plugin-sessions` 创建时检查：
```sql
SELECT COUNT(*) FROM plugin_sessions
WHERE conversation_id = ? AND plugin_id = ? AND current_step_status != 'done'
```
若存在活跃实例，返回 `409 Conflict`：
```json
{
  "code": "PLUGIN_ALREADY_ACTIVE",
  "existing_session_id": "ps-xxx",
  "message": "该插件在当前会话中已有活跃实例"
}
```

**前端处理**：接收到 409 时，弹出 Toast「写作插件已在运行，是否跳转？」，点击跳转到现有 session。

### 1.8 大文本（>64KB）OSS 存储

**Go 层实现**

在写入 `plugin_session_versions.content` 时判断：
```go
func storeContent(content []byte) (jsonb []byte, err error) {
    if len(content) < 64*1024 {
        // 直接存 JSONB
        return json.Marshal(map[string]any{"type": "inline", "data": string(content)})
    }
    // 上传到 OSS/S3
    url, err := ossClient.Put(content)
    if err != nil { return nil, err }
    return json.Marshal(map[string]any{"type": "ref", "url": url})
}
```

读取时同样做反向处理（`type == "ref"` 时从 OSS 下载）。

前端 `RichEditor` 按需分片加载：获取 artifact 时，若 content 为 `ref` 类型，先展示前 8KB 内容，滚动到底部时懒加载更多。

### 1.9 Go 上下文注入

当 plugin session 活跃时，Go 层向 `environment_context` 注入：
- 当前步骤名（`current_step`）
- 每个 artifact 的摘要（前 200 字或 JSON 节点数）

**注入格式**

```json
{
  "plugin_session": {
    "session_id": "ps-xxx",
    "plugin_id": "writing-plugin",
    "current_step": "draft",
    "artifacts": {
      "outline": { "summary": "第一章: 背景介绍...", "type": "json" },
      "draft": { "summary": "（前 200 字）本文探讨...", "type": "text" }
    }
  }
}
```

Python `chat_service.py` 在 Go 注入的基础上再注入 `scenario.md` 和可达出边（M1 已实现）。

### 1.10 `/dev/plugin-demo` 更新

更新开发者演示页面（仅 dev 环境可见），展示所有新增原语：
- `ImageAnnotator` 演示
- `RichEditor` + `SidebarTOC` 演示
- `OutlineTree` 拖拽排序演示
- `PageCarousel` 翻页演示
- `SubQuestionList` / `ReferencePanel` 演示
- 大文本 OSS 存储触发演示

---

## 二、实施计划

### 阶段划分

**Week 1 — Go 基础能力**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| 大文本 OSS 存储（写入 + 读取） | Backend (Go) | 1.5d |
| 多插件共存检测（POST /plugin-sessions 检查 + 409） | Backend (Go) | 0.5d |
| Go 上下文注入（步骤名 + artifact 摘要） | Backend (Go) | 1d |
| 框架 RAG sources / 子问题自动收集（Python） | Algorithm (Python) | 1d |

**Week 2 — 文本编辑类组件**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `RichEditor`（Markdown 编辑 + 选中引用） | Frontend (TS) | 1.5d |
| `SidebarTOC`（目录 + 滚动高亮） | Frontend (TS) | 1d |
| `SubQuestionList` / `ReferencePanel` | Frontend (TS) | 1d |
| 选中引用 `plugin_context` 注入逻辑 | Frontend (TS) | 0.5d |

**Week 3 — 结构类组件**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `OutlineTree`（折叠/展开 + dnd-kit 排序） | Frontend (TS) | 2d |
| `PageCarousel`（翻页 + 缩略图 + dnd-kit 排序） | Frontend (TS) | 2d |

**Week 4 — 标注 + 演示页 + 联调**

| 任务 | 负责层 | 工作量 |
|------|--------|--------|
| `ImageAnnotator`（画布标注 + 发送给 AI） | Frontend (TS) | 2d |
| 多插件共存 Toast 跳转（前端） | Frontend (TS) | 0.5d |
| `/dev/plugin-demo` 更新 | Frontend (TS) | 1d |
| 端到端联调 | All | 0.5d |

---

## 三、测试方案

### 3.1 单元测试

**大文本 OSS 存储**

```go
func TestStoreContent_SmallContent_StoredAsJSONB(t *testing.T) {
    // 10KB 内容，验证 content.type == "inline"
}

func TestStoreContent_LargeContent_StoredAsRef(t *testing.T) {
    // 100KB 内容，mock OSS client
    // 验证 content.type == "ref"，url 非空
}

func TestReadContent_RefType_FetchesFromOSS(t *testing.T) {
    // content.type == "ref"，验证从 OSS 下载内容
}
```

**多插件共存检测**

```go
func TestCreateSession_RejectsIfAlreadyActive(t *testing.T) {
    // 先创建一个 writing-plugin session（非 done）
    // 再创建同 plugin_id 的 session
    // 期望：409，code='PLUGIN_ALREADY_ACTIVE'
}

func TestCreateSession_AllowsDifferentPluginId(t *testing.T) {
    // writing-plugin + ppt-plugin 可并存
    // 期望：两次创建都成功
}

func TestCreateSession_AllowsAfterPreviousDone(t *testing.T) {
    // 上一个 session 已 done，允许创建新的
    // 期望：201
}
```

**Go 上下文注入**

```go
func TestContextInjection_ContainsCurrentStep(t *testing.T) {
    // 活跃 session，验证 environment_context.plugin_session.current_step 正确
}

func TestContextInjection_ArtifactSummaryTruncated(t *testing.T) {
    // artifact 内容超过 200 字，验证 summary 被截断
}
```

### 3.2 前端组件测试

**`OutlineTree` 拖拽**

```ts
describe('OutlineTree', () => {
  it('renders nested outline correctly', () => {})
  it('collapses child nodes on click', () => {})
  it('patches artifact after drag reorder', async () => {
    // 模拟 dnd-kit 拖拽事件
    // 验证 PATCH /artifacts 被调用，content 包含新顺序
  })
  it('reverts order on PATCH failure', async () => {
    // mock PATCH 失败，验证顺序回滚
  })
})
```

**`PageCarousel` 拖拽**

```ts
describe('PageCarousel', () => {
  it('renders correct page count', () => {})
  it('navigates prev/next', () => {})
  it('jumps to page on thumbnail click', () => {})
  it('patches artifact after thumbnail drag reorder', async () => {})
})
```

**`ImageAnnotator`**

```ts
describe('ImageAnnotator', () => {
  it('adds annotation on canvas click', () => {})
  it('serializes annotations to plugin_context on send', () => {
    // 点击「发送标注」，验证请求体包含 annotations 数组
  })
})
```

**`RichEditor` 选中引用**

```ts
describe('RichEditor', () => {
  it('shows quote toolbar on text selection', () => {
    // 模拟 Selection API
  })
  it('attaches cited_text to plugin_context', () => {
    // 点击「引用」，验证 plugin_context.payload.cited_text
  })
  it('auto-patches after 5s debounce', async () => {
    // 输入文字后等待 5s，验证 PATCH 被调用
  })
})
```

### 3.3 集成测试

**scenario.md 路由验证**

```
1. 图片插件 session 活跃
2. 用户发送「没问题继续」
3. 检查 agent 被调用的工具：
   → 必须调用 plugin_proceed()，不能是一般回复
4. 用户发送「把帽子改大一些」
   → 必须调用 plugin_edit()
5. 用户发送「今天天气怎么样」
   → 不调任何插件工具，直接回复
```

**RAG sources 自动收集**

```
1. 在知识库问答步骤中触发 RAG 搜索
2. SSE sources 字段返回参考文献
3. 验证 references artifact 中有新条目
4. ReferencePanel 展示正确数量
```

### 3.4 端到端（E2E）验收测试

**图片标注 + 对话修改**

```
1. 生成图片（ps-xxx）
2. 打开 ImageAnnotator，在图片上画圈并添加标注「把这里改成皇冠」
3. 点击「发送标注给 AI」并输入「按照标注修改图片」
4. 验证请求体包含 annotations 数组
5. 验证新图片生成，版本记录 +1
```

**拖拽排序持久化**

```
1. 写作插件大纲已生成（3 个节点）
2. 拖拽节点 3 到第 1 位
3. 验证 UI 顺序立即更新
4. 刷新页面
5. 验证顺序保持（从 DB 加载后顺序一致）
```

**大文本 OSS**

```
1. 生成一篇超过 64KB 的文章（可 mock）
2. 验证 DB 中 content.type == "ref"
3. 前端能正确加载并展示文章（OSS 获取透明）
4. 版本回退到历史版本（同样是 OSS 存储的），内容正确
```

---

## 四、验收标准

| 验收项 | 验收方式 | 通过标准 |
|--------|----------|----------|
| scenario.md 路由正确 | 集成测试 | 「没问题继续」→ plugin_proceed，修改意图 → plugin_edit |
| 图片标注发送 AI | E2E | 请求体包含正确格式 annotations |
| 选中引用发送 AI | E2E | 请求体包含 cited_text |
| 拖拽排序持久化 | E2E | 刷新后顺序不变 |
| 拖拽失败回滚 | 前端测试 | PATCH 失败时顺序回滚 |
| 多插件共存检测 | 单元测试 + E2E | 同 plugin_id 返回 409，前端 Toast 提示 |
| 大文本 OSS 存储 | 单元测试 | ≥64KB 时 content.type="ref" |
| 大文本前端加载 | E2E | OSS 内容正常展示，无报错 |
| Go 上下文注入 | 集成测试 | agent context 包含步骤名 + artifact 摘要 |
| SubQuestionList / ReferencePanel 自动填充 | 集成测试 | RAG 结果自动收集到 artifact |

---

## 五、注意事项与风险

1. **`ImageAnnotator` 库选型**：`fabric.js` 更成熟但包体大（~280KB gzip 前）；`konva` 更轻量。建议先评估打包体积，按需决定是否懒加载。
2. **`RichEditor` 大文本性能**：超过 64KB 的 Markdown 若全量渲染会卡顿，需实现虚拟化渲染（只渲染视口内容），这是 M4 的技术难点。
3. **拖拽排序乐观更新**：PATCH 失败回滚时，若用户已进行其他操作（如继续拖拽），回滚逻辑需处理并发修改场景。
4. **OSS 存储权限**：OSS URL 需要带签名或访问控制，不能是公开可访问的永久链接，避免内容泄露。
5. **`/dev/plugin-demo` 隔离**：确保 demo 页面仅在 `LAZYMIND_DEV_MODE=true` 时可见，避免暴露在生产环境。
6. **M4 是框架完整性验证节点**：M4 完成后所有框架原语均已就位，在推进 M5/M6 前需要用图片插件完整验证一遍所有原语的组合使用，发现问题及时修复。
