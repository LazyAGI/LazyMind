# 阶段 3：数据历史与富媒体支持

> 在已落地的 Plugin 执行管道基础上，补齐 artifact 的版本历史、富媒体输入输出、跨步骤上下文携带与前端展示增强能力。前置依赖：SubAgent 基础设施（`01-subagents/plan.md`）与 Plugin 机制（`02-plugin/plan.md`）**必须已落地**。

## 阅读顺序

1. 先读「一、设计原则与约束」了解边界。
2. 「二、数据层变更」是承重墙，所有功能依赖它。
3. 「三～六」各功能模块可按需阅读。
4. 「七、对外接口」和「八、实施顺序」是收尾。

---

## 一、设计原则与约束

### 1.1 最小化对已有表的改动

阶段 1/2 已建立的六张表（`sub_agent_tasks` / `sub_agent_steps` / `sub_agent_artifacts` / `plugin_sessions` / `plugin_session_steps` / `plugin_slot_revisions`）**结构不修改**。本阶段新增两张表，并在 `plugin.yaml` / `plugin_slot_revisions` 上扩展字段。

### 1.2 版本历史粒度：(slot_id, list_index)

版本历史挂在 **`plugin_slot_revisions`** 的每一行上。`plugin_slot_revisions` 的一行代表 slot 内一个具体内容项（`cardinality=single` 时 `list_index=NULL`，`cardinality=list` 时 `list_index=N`）。每次 AI 重新写入或用户手动修改，都在同一 `(session_id, slot_id, list_index)` 上追加一条新 revision 记录，`selected=TRUE` 始终指向最新。

这样版本历史直接复用已有的 `revision` 字段语义，无需引入独立版本表。

### 1.3 Seq 不可复用，逻辑删除

`sub_agent_artifacts.seq` 由框架单调递增分配，删除 artifact 只做逻辑隐藏（新增 `hidden` 字段），seq 值永不复用。前端展示时过滤 `hidden=TRUE` 的行；ChatAgent 引用"第N个"时以可见序列的第 N 个为准。

### 1.4 大内容工作空间文件已有兜底

`save_artifact` 工具已实现：text/json 超过 `LARGE_ARTIFACT_THRESHOLD` 时写入 workspace 文件，`value` 存 `{"type":"file","path":"...","size":...}`，对上层透明。本阶段沿用此机制，不引入 OSS，OSS 支持留到后续阶段。

---

## 二、数据层变更

### 2.1 `sub_agent_artifacts` 新增字段

```sql
ALTER TABLE sub_agent_artifacts
  ADD COLUMN hidden     BOOLEAN   NOT NULL DEFAULT FALSE,
  ADD COLUMN caption    TEXT,            -- 图片/文件的文字描述，供 ChatAgent 上下文使用
  ADD COLUMN sort_order INT;             -- 有序 slot 下的显示顺序（NULL 表示使用 seq 自然顺序）

CREATE INDEX idx_saa_task_visible ON sub_agent_artifacts(task_id, artifact_key, hidden, seq);
```

- `hidden`：逻辑删除标志，前端不展示，seq 不变。
- `caption`：图片或文件类型 artifact 的文字描述，用于在 ChatAgent 上下文摘要中代替 URL/路径。
- `sort_order`：有序 slot 支持前端拖拽调序后写回；`NULL` 时按 seq 排序。

### 2.2 `plugin_slot_revisions` 新增字段

```sql
ALTER TABLE plugin_slot_revisions
  ADD COLUMN content_snapshot JSONB,    -- 本次写入时的 artifact value 快照（用于版本回溯）
  ADD COLUMN change_source VARCHAR(16)  -- 'ai' | 'human'
    NOT NULL DEFAULT 'ai';
```

- `content_snapshot`：AI 写入时从 `sub_agent_artifacts.value` 复制；用户人工编辑时存编辑后内容。版本回溯时直接读此字段，不需要跨表 JOIN。
- `change_source`：区分 AI 自动写入和用户手工编辑，用于前端版本历史展示。

> `plugin_slot_revisions.revision` 在同一 `(session_id, slot_id, list_index)` 内单调递增，即线性版本号。回退时将旧 revision 的 `content_snapshot` 写为新 revision（`change_source='human'`），不修改历史记录。

### 2.3 `plugin.yaml` schema 扩展

```yaml
ui:
  tabs:
    - id: materials
      label: Materials
      layout: grid          # NEW: 'list'（默认）| 'grid' | 'composite'
      slots:
        - id: material_images
          type: image
          cardinality: list
          artifact_key: material_image
          ordered: true     # NEW: true 时支持前端拖拽调序（写回 sort_order）
          caption_key: material_image_caption  # NEW: 同步写入描述的 artifact key（可选）
```

新增字段：
- `layout`：Tab 布局模式。`list` 为垂直堆叠（默认），`grid` 为网格，`composite` 为跨 slot 联合渲染（见 §5.2）。
- `ordered`：声明该 slot 的顺序是否有意义；`true` 时前端渲染拖拽手柄，调序结果写回 `sort_order`。
- `caption_key`：与图片/文件 slot 配对的描述 artifact key；Go 在处理 artifact 事件时将两者关联写入。

---

## 三、Artifact 生命周期管理

### 3.1 Seq 分配与逻辑删除

**Seq 分配**：`SubAgentDB._next_seq()` 当前实现已是 `MAX(seq)+1`，满足单调递增。本阶段无需修改 Python 侧逻辑。Go 侧 `SaveArtifact` 同理，seq 由调用方传入，已经单调。

**逻辑删除接口**：

```
DELETE /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/{list_index}
  → sub_agent_artifacts WHERE task_id=? AND artifact_key=? AND seq=? → hidden=TRUE
  → plugin_slot_revisions WHERE ... AND list_index=? → selected=FALSE
  → 向前端发 Conversation Events SSE: {type: 'slot_item_deleted', slot_id, list_index}
```

前端收到事件后从渲染列表中移除对应项，不刷新整个 Panel。

### 3.2 有序 Slot 调序

```
PATCH /api/core/plugin-sessions/{session_id}/slots/{slot_id}/order
  body: { "order": [0, 2, 1, 3] }   ← list_index 的新排列顺序
  → 批量更新 sub_agent_artifacts.sort_order
```

前端在 `ordered=true` 的 slot 上渲染拖拽手柄（`DragHandle`），拖拽完成后调此接口持久化。

---

## 四、富媒体输入

### 4.1 用户上传附件传递给 SubAgent

现有链路（`chat_service.py` 中 `files` 参数 → `validate_and_resolve_files`）已支持将文件路径注入 ChatAgent 上下文。本阶段扩展到 Plugin Step 场景：

1. 前端发送消息时携带 `files`（已有字段），Go 将文件路径列表写入 `sub_agent_tasks.params["user_files"]`。
2. Go 构造 Step objective 时，将 `user_files` 拼入 prompt（`state.yml` 中用 `{{user_files}}` 占位符声明接收）。
3. SubAgent 通过 `get_artifact` 读取后，可用 `save_artifact` 将用户文件存入对应 slot。

### 4.2 联网搜索结果入库

SubAgent 调用 `web_search_tool` / `image_search_tool` 后，通过现有 `save_artifact(key, url, content_type='image')` 即可存入。本阶段无需修改工具层，只需在 `plugin.yaml` 的对应 slot 加 `caption_key` 字段，使搜索结果的描述文本同步写入（SubAgent 在 `save_artifact` 时额外调一次 `save_artifact(caption_key, description, content_type='text')`）。

### 4.3 知识库检索

现有 `kb.py` 工具已支持按 `kb_id` 检索，ChatAgent 已能调用。本阶段补充：

1. 新增工具 `list_knowledge_bases()`：返回当前用户有权访问的知识库列表（id / name / type / tags），让 SubAgent 能在不预知 kb_id 的情况下发现可用知识库。
2. 此工具注册到 SubAgent 工具集（与 `save_artifact` 等框架工具同级）；SubAgent 先调 `list_knowledge_bases()` 选库，再调已有的 `kb_search()` 检索，结果通过 `save_artifact` 入库。

---

## 五、上下文携带与意图理解

### 5.1 Artifact 摘要注入 ChatAgent

Go 在每次构造 `/api/chat/stream` 请求体时，将当前 Plugin Session 的 artifact 摘要附加到 `plugin_context` 字段：

```json
"plugin_context": {
  "session_id": "...",
  "plugin_id": "image-plugin",
  "current_step": "generate_image",
  "artifact_summary": {
    "subject_analysis": "赛博朋克城市，霓虹灯，夜景，高密度建筑",
    "material_image": ["ref1.jpg（街景）", "ref2.jpg（灯光）"],
    "optimized_prompt": "cyberpunk city at night, neon lights..."
  },
  "visible_index_map": {
    "material_image": [0, 1]   // 可见 list_index，已排除 hidden 项
  }
}
```

`artifact_summary` 内容：
- `text` 类型：截取前 200 字符。
- `image` / `file` 类型：优先用 `caption`，无 caption 则用文件名。
- `json` 类型：`str(value)[:200]`。

`plugin.yaml` 的 `summary_max_chars` 字段可覆盖 200 的默认截断长度。

`visible_index_map` 提供可见项的 list_index 列表，ChatAgent 工具层根据它将用户的"第N个"解析为实际 list_index（第 N 个可见项的 list_index 值）。

### 5.2 "第N个"意图解析

`plugin_manager.py` 中的 `_trigger_plugin_step()` 和 `advance_step` 工具在解析 `runtime_instruction` 时，如含有"第N个"类表达，通过 `visible_index_map` 将 N 映射为 `list_index`，并写入 `step_exec.params["target_list_index"]`；Go 在构造 Step objective 时将 `target_list_index` 注入 `{{target_index}}` 占位符，SubAgent 据此调用 `save_artifact(list_index=target_list_index)` 做部分重试。

### 5.3 图片 Caption

`save_artifact` 工具新增可选参数 `caption: str = None`：

```python
def save_artifact(key, value, content_type='text',
                  source_tool=None, list_index=None,
                  caption=None) -> Dict:
```

当 `caption` 非空时，工具层将 `caption` 写入 `sub_agent_artifacts` 的同一行（`caption` 字段，见 §2.1）；Go 的 `OnArtifactEvent` 从 artifact value 中读取 `caption` 字段并写入 DB 行。

---

## 六、版本历史

### 6.1 写入时机

| 场景 | 触发时机 | `change_source` |
| --- | --- | --- |
| AI 步骤完成（`done` 事件） | Go 在 `routeToTaskSSE` 的 `done` 分支，读取该步骤所有 artifact，对每个 `(slot_id, list_index)` 写一条新 `plugin_slot_revisions`（`content_snapshot` = artifact value，`selected=TRUE`，旧行 `selected=FALSE`） | `'ai'` |
| 用户在 Panel 内人工编辑 | 前端防抖 3s 后调 `PATCH /plugin-sessions/{id}/slots/{slot_id}/items/{list_index}`，Go 写新 revision 行 | `'human'` |
| 发送对话消息前强制快照 | 前端在调 `POST /conversations:chat` 前，若有未提交的编辑，先发 PATCH 强制写版本，再发消息 | `'human'` |

### 6.2 版本回退

```
POST /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/{list_index}/rollback
  body: { "revision": 3 }
  → 读 plugin_slot_revisions WHERE session_id=? AND slot_id=? AND list_index=? AND revision=3
  → content_snapshot 作为新 revision 写入（revision = MAX+1, change_source='human', selected=TRUE）
  → 旧 selected=TRUE 的行置 FALSE
  → 更新 sub_agent_artifacts 对应行的 value（保证 /slots 接口返回值与版本一致）
  → SSE: {type: 'slot_updated', slot_id, list_index, revision: MAX+1}
```

回退不删除历史，只追加新 revision，保持线性。

### 6.3 版本历史接口

```
GET /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/{list_index}/versions
  → plugin_slot_revisions WHERE session_id=? AND slot_id=? AND list_index=?
    ORDER BY revision ASC
  → [{revision, change_source, created_at, content_snapshot (truncated for image)}]
```

---

## 七、前端扩展

### 7.1 Panel 扩展点（已有组件增量修改）

| 组件 | 新增能力 |
| --- | --- |
| `SlotImage` | 右键菜单：删除（逻辑隐藏）、「引用到对话框」、查看版本历史；`ordered=true` 时渲染拖拽手柄 |
| `SlotText` | 支持内联编辑（contentEditable），失焦后防抖触发 PATCH；右键「查看版本历史」 |
| `PluginPanel` | 发送消息前拦截：若有未提交编辑先强制快照再发；接收 `slot_item_deleted` / `slot_updated` SSE 事件局部刷新 |

### 7.2 图片引用到对话框

`SlotImage` 新增「引用」按钮，点击后将图片 URL 注入父组件 `ChatInput` 的 `files` 列表（复用已有的文件附件 UI），随下一条消息作为 `files` 字段发送；Go 将其写入 `params["user_files"]` 并注入 Step objective。

### 7.3 版本历史面板（`SlotVersionDrawer`）

```
SlotVersionDrawer
  ├── 版本列表（revision / change_source badge / created_at）
  ├── 点击任意版本 → 预览 content_snapshot
  ├── 文本类型：内联展示 diff（当前版本 vs 选中版本，行级高亮）
  ├── 图片类型：并排对比（当前 | 历史）
  └── 「回退到此版本」按钮 → POST .../rollback
```

### 7.4 composite 布局（跨 Slot 联合渲染）

当 Tab `layout=composite` 时，前端按 `list_index` 对齐多个 slot 的内容，每个 `list_index` 为一行，行内各 slot 并排展示：

```
composite tab (PPT 场景)
  index 0: [页面描述 text] [图片/HTML slot] [讲稿 text]
  index 1: [页面描述 text] [图片/HTML slot] [讲稿 text]
  ...
```

行级拖拽调序同时更新所有参与 slot 的 `sort_order`（批量 PATCH）。

### 7.5 Plugin i18n

`plugin.yaml` 支持 `i18n` 字段覆盖展示文案：

```yaml
name: AI Image Generation
i18n:
  zh-CN:
    name: AI 图片生成
    steps:
      analyze_subject: {label: 主体分析}
      generate_image:  {label: 生图}
    tabs:
      result: {label: 结果}
    slots:
      image_output: {label: 生成图片}
```

`plugin_loader.py` 解析 `i18n` 字段，`GET /api/core/plugins/{plugin_id}` 接口新增 `accept-language` 响应字段，前端按当前语言选取对应 label。

---

## 八、对外接口（新增/变更汇总）

```
# Artifact 管理
DELETE /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/{list_index}
  → 逻辑删除，发 slot_item_deleted 事件

PATCH  /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/{list_index}
  → 人工编辑写版本，change_source='human'

PATCH  /api/core/plugin-sessions/{session_id}/slots/{slot_id}/order
  body: {order: [0,2,1]}  → 批量更新 sort_order

# 版本历史
GET  /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/{list_index}/versions
  → 线性版本列表

POST /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/{list_index}/rollback
  body: {revision: N}  → 追加回退版本，selected 指向新 revision

# 已有接口变更
GET /api/core/plugin-sessions/{session_id}/slots
  → 每条记录新增 content_snapshot / change_source / caption / sort_order 字段

GET /api/core/plugins/{plugin_id}
  → 新增 i18n 字段；根据 Accept-Language header 返回对应语言 label
```

---

## 九、实施顺序

1. **数据层**：
   - `sub_agent_artifacts` 新增 `hidden` / `caption` / `sort_order` 字段 + migration。
   - `plugin_slot_revisions` 新增 `content_snapshot` / `change_source` 字段 + migration。
   - `plugin.yaml` schema 扩展（`ordered` / `caption_key` / `layout` / `i18n`），`plugin_loader.py` 同步解析。

2. **Go 层**：
   - `routeToTaskSSE` 的 `done` 分支：读 artifact 写 `content_snapshot` 到 `plugin_slot_revisions`。
   - `OnArtifactEvent`：从 artifact value 中读取 `caption` 字段写入 DB。
   - 逻辑删除、调序、人工编辑版本、回退四个新接口。
   - `plugin_context` 构造：增加 `artifact_summary` + `visible_index_map`。

3. **Python 层**：
   - `save_artifact` 工具新增 `caption` 参数。
   - `list_knowledge_bases()` 工具注册到 SubAgent 工具集。
   - `plugin_manager.py`：`plugin_context` 中的 `visible_index_map` 解析 "第N个" → `target_list_index`。
   - `plugin.yaml` i18n 解析，`GET /plugins/{id}` 接口返回多语言 label。

4. **前端**：
   - `SlotImage` / `SlotText` 增量：删除、引用、内联编辑、拖拽手柄、版本历史入口。
   - `SlotVersionDrawer` 组件：版本列表 + diff/并排对比 + 回退。
   - `PluginPanel`：`composite` 布局、发消息前强制快照、`slot_item_deleted` / `slot_updated` 局部刷新。
   - `ChatInput`：「引用图片」注入 files 列表。

5. **端到端验证**（image-plugin 扩展）：
   - 验证图片删除后 "第N个" 正确映射。
   - 验证用户上传参考图 → SubAgent 读取并 save_artifact 入库。
   - 验证 AI 完成步骤后 `content_snapshot` 写入，版本列表可查，回退后 Panel 正确刷新。
