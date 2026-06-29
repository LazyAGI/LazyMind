# 阶段 3：数据历史与富媒体支持

> 在已落地的 Plugin 执行管道基础上，补齐 artifact 的版本历史、富媒体输入输出、跨步骤上下文携带与前端展示增强能力。前置依赖：SubAgent 基础设施（`01-subagents/plan.md`）与 Plugin 机制（`02-plugin/plan.md`）**必须已落地**。

## 阅读顺序

1. 先读「一、设计原则与约束」了解边界。
2. 「二、数据层变更」是承重墙，所有功能依赖它。
3. 「三、Slot 操作」描述写入、调序、删除的完整语义。
4. 「四～六」各功能模块可按需阅读。
5. 「七、对外接口」和「八、实施顺序」是收尾。
6. 人工编辑附件能力（draftStore / caption / 文件持久化）详见 [manual.md](./manual.md)，本文档在相关位置以 `→ manual.md` 注标。

---

## 一、设计原则与约束

### 1.1 最小化对已有表的改动

阶段 1/2 已建立的六张表（`sub_agent_tasks` / `sub_agent_steps` / `sub_agent_artifacts` / `plugin_sessions` / `plugin_session_steps` / `plugin_slot_revisions`）**结构不修改**。本阶段新增两张表（`plugin_slot_order`），并在 `sub_agent_artifacts` / `plugin_slot_revisions` / `plugin.yaml` 上扩展字段。

### 1.2 `list_index`：列表项的稳定身份标识

`list_index` 是 slot 内一个列表项的**内部稳定身份**，工具层和 DB 内部使用，**不对外暴露**。对外接口和前端统一使用 `sort_order`（展示顺序）定位列表项。

- **新增**（`cardinality=list` 且未指定 `sort_order`）：分配 `list_index = MAX(当前 slot 所有 list_index) + 1`（从 0 开始），已删除的项也参与 MAX 计算，不复用。
- **覆盖指定项**（`cardinality=list` 且指定 `sort_order`）：根据 `sort_order` 查出对应 `list_index`，在同一 `(session_id, slot_id, list_index)` 上追加新 revision；`list_index` 不变，`order_list` 位置不变。
- **覆盖单值**（`cardinality=single`，`sort_order` 无论是否指定均忽略）：在固定 `list_index=NULL` 上追加新 revision，旧 revision 保留，`selected` 指向最新。
- **删除**：只做逻辑隐藏（`hidden=TRUE`），被删除的 `list_index` 不再分配给后续新增项，同步从 `plugin_slot_order.order_list` 中移除。

版本历史挂在 `plugin_slot_revisions` 的每一行上，每次 AI 写入或用户手动修改都在同一 `(session_id, slot_id, list_index)` 上追加一条新 revision，`selected=TRUE` 始终指向最新。前端展示时过滤 `hidden=TRUE` 的行；ChatAgent 引用"第N个"时以 `sort_order` 排列的可见序列第 N 个为准。

### 1.3 大内容落盘（已有机制，沿用）

`save_artifact` 工具已实现大内容自动落盘机制：`text`/`json` 内容超过 `LARGE_ARTIFACT_THRESHOLD` 时自动写入 workspace 文件，`value` 存 `{"type":"file","path":"...","size":N}`，对上层透明。本阶段沿用此机制，不引入 OSS。

---

## 二、数据层变更

### 2.1 `sub_agent_artifacts` 新增字段

```sql
ALTER TABLE sub_agent_artifacts
  ADD COLUMN hidden     BOOLEAN   NOT NULL DEFAULT FALSE,
  ADD COLUMN caption    TEXT;            -- 图片/文件的文字描述，供 ChatAgent 上下文使用

CREATE INDEX idx_saa_task_visible ON sub_agent_artifacts(task_id, artifact_key, hidden, seq);
```

- `hidden`：逻辑删除标志，前端不展示，`list_index` 不变、不复用。
- `caption`：图片或文件类型 artifact 的文字描述，用于在 ChatAgent 上下文摘要中代替 URL/路径。

> `sort_order` **不存在 `sub_agent_artifacts` 上**，由专表 `plugin_slot_order` 统一管理（见 §2.2）。查询时动态 JOIN 展开，`sub_agent_artifacts` 保持职责单一。

### 2.2 `plugin_slot_order` 表（新增）

```sql
CREATE TABLE plugin_slot_order (
  session_id     VARCHAR     NOT NULL,
  slot_id        VARCHAR     NOT NULL,
  order_list     JSONB       NOT NULL,   -- [list_index, ...] 按展示顺序排列，仅含可见项
  order_version  INT         NOT NULL DEFAULT 0,
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (session_id, slot_id)
);
```

**设计理由**：

- `sort_order` 是对整个 slot 的原子操作，挂在 `sub_agent_artifacts` 单行上需要批量 UPDATE N 行，且乐观锁版本号没有自然的 slot 级挂载点。
- 专表将顺序状态收敛为一行一个 slot，乐观锁语义清晰，调序只写一行，查询时 `jsonb_array_elements WITH ORDINALITY` 动态展开为 `sort_order`。
- `order_list` 只含 `hidden=FALSE` 的 `list_index`；删除操作需同步从 `order_list` 中移除对应值。

**`sort_order` 查询（动态展开）**：

```sql
WITH ordered AS (
  SELECT value::int AS list_index,
         ordinality::int AS sort_order
  FROM plugin_slot_order,
       jsonb_array_elements(order_list) WITH ORDINALITY
  WHERE session_id = $1 AND slot_id = $2
)
SELECT a.*, COALESCE(o.sort_order, a.list_index + 1) AS sort_order
FROM sub_agent_artifacts a
LEFT JOIN ordered o USING (list_index)
WHERE a.session_id = $1 AND a.slot_id = $2 AND a.hidden = FALSE
ORDER BY sort_order, a.list_index;
```

> 代码示例（DDL、Go 乐观锁 handler、前端 debounce）见 [code.md](./code.md)。

### 2.3 `plugin_slot_revisions` 新增字段

```sql
ALTER TABLE plugin_slot_revisions
  ADD COLUMN content_snapshot JSONB,    -- 本次写入时的 artifact value 快照（用于版本回溯）
  ADD COLUMN change_source VARCHAR(16)  -- 'ai' | 'human'
    NOT NULL DEFAULT 'ai';
```

- `content_snapshot`：AI 写入时从 `sub_agent_artifacts.value` 复制；用户人工编辑时存编辑后内容。版本回溯时直接读此字段，不需要跨表 JOIN。
- `change_source`：区分 AI 自动写入和用户手工编辑，用于前端版本历史展示。

> `plugin_slot_revisions.revision` 在同一 `(session_id, slot_id, list_index)` 内单调递增，即线性版本号。回退时将旧 revision 的 `content_snapshot` 写为新 revision（`change_source='human'`），不修改历史记录。`list_index` 为内部字段，对外接口不暴露。

### 2.4 `plugin.yaml` schema 扩展

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

- `layout`：Tab 布局模式。`list` 为垂直堆叠（默认），`grid` 为网格，`composite` 为跨 slot 联合渲染（见 §7.4）。
- `ordered`：声明该 slot 的顺序是否有意义；`true` 时前端渲染拖拽手柄，调序结果写回 `sort_order`。
- `caption_key`：~~与图片/文件 slot 配对的描述 artifact key；Go 在处理 artifact 事件时将两者关联写入。~~ **已废弃**：caption 直接通过 `save_artifact(caption=...)` 写入主 artifact 的 value JSON，Go 的 `extractCaption` 从 value JSON 中读取并写入 `sub_agent_artifacts.caption` 列，无需额外 artifact key。
- `i18n`：多语言覆盖字段，详见 §7.5。

### 2.5 Artifact 类型系统

#### 2.5.1 存储格式：统一 TEXT

`sub_agent_artifacts.value` 统一存 `TEXT`（字符串）。不同内容类型的序列化约定：

| content_type | value 格式 |
| --- | --- |
| `text` | 原始字符串 |
| `image` | URL 或本地路径字符串 |
| `html` | HTML 字符串 |
| `json` | JSON 字符串 |
| `file`（大内容落盘） | `{"type":"file","path":"...","size":N}` |

#### 2.5.2 类型推断优先级

前端渲染时按以下优先级确定实际类型：

```
1. value 本身为 {"type":"file",...} 格式
     → 加载文件，根据文件扩展名 / MIME 决定渲染方式
2. plugin.yaml slot 声明了静态 type（text / image / html / json）
     → 按静态 type 渲染
3. （后续阶段）save_artifact 传入显式 type 字段，存入 DB
     → 按显式 type 渲染，覆盖 slot 静态声明
```

**当前阶段约定**：需要动态类型的内容（如 SubAgent 写入未知格式）一律通过文件落盘传递（走第 1 条），前端加载后按 MIME 渲染。显式 `type` 字段留到后续阶段实现。

> `plugin.yaml` slot 的 `type` 字段仍为必填，用于声明该 slot 期望的内容类型；动态类型推断是对静态声明的**补充**，而不是替代。

---

## 三、Slot 操作

### 3.1 `list_index` 分配与逻辑删除

示例场景（`cardinality=list`）：

```
初始生成（未指定 sort_order × 3）：
  order_list=[0,1,2]，sort_order=1→list_index=0，2→1，3→2

用户对 sort_order=2 重新生成（指定 sort_order=2）：
  → 查出 list_index=1，追加新 revision；order_list 不变，sort_order 不变

用户删除 sort_order=2：
  → list_index=1 hidden=TRUE，从 order_list 移除 → [0,2]
  → sort_order 重新展开：1→list_index=0，2→list_index=2

后续新增（未指定 sort_order）：
  → list_index=3（MAX(0,1,2)+1，已删除的 1 也参与计算），追加到 order_list → [0,2,3]
  → sort_order=3→list_index=3
```

示例场景（`cardinality=single`）：

```
AI 第一次写入（sort_order 不传或传了都忽略）：
  → list_index=NULL，revision=1，selected=TRUE

AI 重新生成同一 slot：
  → list_index=NULL，revision=2，selected=TRUE；revision=1 保留，selected=FALSE

用户版本回退到 revision=1：
  → 以 revision=1 的 content_snapshot 写入 revision=3，selected=TRUE
  → revision=1/2 保留，支持再次回退
```

**逻辑删除接口**（用 `list_index` 定位，前端从 `/slots` 响应直接取得）：

```
DELETE /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}
  → sub_agent_artifacts WHERE session_id=? AND slot_id=? AND list_index=? → hidden=TRUE
  → plugin_slot_revisions 对应 list_index 的所有 revision → selected=FALSE（历史保留，仅取消选中）
  → plugin_slot_order.order_list 中移除该 list_index，order_version+1
```

> **composite 场景（同行多 slot）**：如果 Tab 是 `layout=composite`，同一 `sort_order` 代表一行中多个 slot 各自的 artifact。前端删除时应同时删除该行所有参与 slot 的对应 `sort_order` 项（批量调用上述接口，或后端提供 composite 行级删除接口）。该扩展留到 composite 布局实现阶段再确定。

前端在图片/内容卡片**右上角**直接展示 `×` 按钮（`cardinality=list` 时显示）：
- 点击 `×` → 弹出二次确认弹窗（"确认删除该项？此操作不可撤销"）
- 确认后调用删除接口
- 调用成功后前端触发本地刷新，从渲染列表中移除对应项
- 版本历史记录完整保留，保证可追溯

### 3.2 有序 Slot 调序

```
PATCH /api/core/plugin-sessions/{session_id}/slots/{slot_id}/order
  body: { "order": [3, 1, 2], "version": 5 }
    ← order：新的 list_index 排列（前端直接传 list_index 序列，无需经 sort_order 翻译）
    ← version：前端从上次 GET /slots 或 GET /order 拿到的 order_version，用于乐观锁

  → Go 在事务内：
      1. SELECT ... FOR UPDATE plugin_slot_order WHERE session_id=? AND slot_id=?
      2. 校验 order_version == req.version，不匹配 → 409 Conflict
      3. 校验所有 list_index 均为 hidden=FALSE，否则 400
      4. UPDATE order_list=?, order_version=order_version+1
```

**并发安全性**：

| 场景 | 行为 |
| --- | --- |
| 两个并发请求携带相同 version | `FOR UPDATE` 行锁串行化；第二个 version 校验失败 → 409 |
| 用户连续拖拽（前端有 debounce） | 500ms 内只发最后一次，通常不触发 409 |
| 用户连续拖拽（请求乱序到达） | 旧请求 version 落后 → 409，前端回滚 UI 到最新状态 |
| DELETE 与 PATCH /order 并发 | 删除同步从 order_list 移除对应 list_index；PATCH 校验时发现 list_index 已不在可见集合 → 400 |

前端在 `ordered=true` 的 slot 上渲染拖拽手柄（`DragHandle`），拖拽完成后 debounce 500ms 再调此接口持久化；收到 409 时重新 GET 刷新 `order_version`，UI 回滚到最新顺序。`GET /slots` 响应需在 slot 级别携带 `order_version` 字段。

> 详细代码示例见 [code.md](./code.md)。

### 3.3 `save_artifact` 写入语义

`save_artifact` 工具签名（对 AI 可见的参数不含 `list_index`）：

```python
def save_artifact(key, value, content_type='text',
                  source_tool=None, sort_order=None,
                  caption=None) -> Dict:
```

`sort_order` 与 slot 的 `cardinality` 共同决定写入行为：

| cardinality | sort_order | 行为 |
| --- | --- | --- |
| `list` | `None`（未指定） | **追加新项**：分配 `list_index = MAX + 1`，追加到 `plugin_slot_order.order_list` 末尾 |
| `list` | `N`（已指定） | **覆盖指定项**：根据 `sort_order=N` 查出对应 `list_index`，在该 `list_index` 上追加新 revision；`order_list` 位置不变 |
| `single` | `None`（未指定） | **覆盖**：在固定 `list_index=NULL` 上追加新 revision；旧 revision 保留，`selected` 指向最新 |
| `single` | `N`（已指定） | **忽略 sort_order**，等同于未指定，执行覆盖逻辑 |

工具层内部始终通过 `sort_order → list_index` 的查找完成写入，`list_index` 不暴露给 AI。

当 `caption` 非空时，工具层将 `caption` 写入 `sub_agent_artifacts` 的同一行（`caption` 字段，见 §2.1）；Go 的 `OnArtifactEvent` 从 artifact value 中读取 `caption` 字段并写入 DB 行。

> **新增工具（已落地）**：除 `save_artifact` 外，工具层还新增了以下工具供 SubAgent 使用：
> - `patch_artifact(key, patch, ...)` — 局部编辑 artifact 草稿（不直接提交），支持 str_replace / json_merge / json_patch；配合 `save_artifact` 两步提交
> - `discard_draft(key, sort_order?)` — 丢弃 `patch_artifact` 写入的草稿
> - `find_artifact(artifact_key, sort_order?)` — 按 key 和 sort_order 查找当前选中版本的 URL/path
> - `find_user_attachment(filename, turn?)` — 查找用户上传附件，返回 url 和 path
>
> Python 工具层内部做 `sort_order → list_index` 查找（通过 `GET /order` 接口），`list_index` 仍不对外暴露给 AI。

---

## 四、富媒体输入

### 4.1 用户上传附件传递给 SubAgent

现有链路（`chat_service.py` 中 `files` 参数 → `validate_and_resolve_files`）已支持将文件路径注入 ChatAgent 上下文。本阶段扩展到 Plugin Step 场景：

1. 前端发送消息时携带 `files`（已有字段），Go 将文件路径列表写入 `sub_agent_tasks.params["user_files"]`。
2. Go 构造 Step objective 时，将 `user_files` 拼入 prompt（`state.yml` 中用 `{{user_files}}` 占位符声明接收）。
3. SubAgent 通过 `get_artifact` 读取后，可用 `save_artifact` 将用户文件存入对应 slot。

### 4.2 联网搜索结果入库

SubAgent 调用 `web_search_tool` / `image_search_tool` 后，通过现有 `save_artifact(key, url, content_type='image')` 即可存入。本阶段无需修改工具层，只需在调用 `save_artifact` 时额外传入 `caption` 参数（`save_artifact(key, url, content_type='image', caption=description)`），caption 会自动写入 `sub_agent_artifacts.caption`，并在 `artifact_summary` 中使用。`caption_key` 字段已废弃（见 §2.4）。

### 4.3 知识库检索

现有 `kb.py` 工具已支持按 `kb_id` 检索，ChatAgent 已能调用。本阶段补充：

1. 新增工具 `list_knowledge_bases()`：返回当前用户有权访问的知识库列表（id / name / type / tags），让 SubAgent 能在不预知 kb_id 的情况下发现可用知识库。
2. 此工具注册到 SubAgent 工具集（与 `save_artifact` 等框架工具同级）；SubAgent 先调 `list_knowledge_bases()` 选库，再调已有的 `kb_search()` 检索，结果通过 `save_artifact` 入库。

---

## 五、上下文携带与意图理解

### 5.1 Artifact 摘要注入 ChatAgent

`artifact_summary` 和 `visible_sort_order_map` 由 **Python 层**在处理请求时直接读 DB 生成（`db.py` 的 `format_artifact_summary`），不经 Go 层构造。Go 层的职责是：将前端携带的 `plugin_ui_state`（`focused_tab` / `focused_sort_order`）合并进 `plugin_context` 后转发给 Python。Python 侧汇总完整的 `plugin_context`：

```json
"plugin_context": {
  "session_id": "...",
  "plugin_id": "image-plugin",
  "current_step": "generate_image",
  "focused_tab": "materials",
  "focused_sort_order": 2,
  "artifact_summary": {
    "subject_analysis": "赛博朋克城市，霓虹灯，夜景，高密度建筑",
    "material_image": ["ref1.jpg（街景）", "ref2.jpg（灯光）"],
    "optimized_prompt": "cyberpunk city at night, neon lights..."
  },
  "visible_sort_order_map": {
    "material_image": [1, 2]
  }
}
```

新增字段说明：

- `focused_tab`：前端随 chat 请求携带，值为用户当前停留的 Tab id（如 `materials` / `slides`）；Plugin Panel 收起时为 `null`。
- `focused_sort_order`：前端携带，值为用户当前聚焦行的 `sort_order`（composite / ordered slot 场景，如 PPT 当前编辑第几页）；非 ordered slot 或无聚焦时为 `null`。

前端在 `POST /conversations:chat` 请求 body 中新增 `plugin_ui_state` 字段（与 `query` 并列）：

```json
"plugin_ui_state": {
  "session_id": "...",
  "focused_tab": "materials",
  "focused_sort_order": 2
}
```

Go 在 `applyChatRuntimeConfigs` 阶段读取 `plugin_ui_state`，合并进 `plugin_context` 下发给 Python。

`artifact_summary` 摘要截断规则：

- `text` 类型：截取前 200 字符。
- `image` / `file` 类型：优先用 `caption`，无 caption 则用文件名。
- `json` 类型：`str(value)[:200]`。

`plugin.yaml` 的 `summary_max_chars` 字段可覆盖 200 的默认截断长度。

`visible_sort_order_map` 按实际展示顺序（`sort_order ASC NULLS LAST`，再按 `list_index` 兜底）列出可见项的 `sort_order` 值列表。`list_index` 不对外暴露，仅在工具层内部使用。

### 5.2 "第N个"意图解析

`plugin_manager.py` 中的 `_trigger_plugin_step()` 和 `advance_step` 工具在解析 `runtime_instruction` 时，如含有"第N个"类表达，从 `visible_sort_order_map` 中取第 N 个 `sort_order` 值，写入 `step_exec.params["target_sort_order"]`；Go 在构造 Step objective 时将 `target_sort_order` 注入 `{{target_sort_order}}` 占位符，SubAgent 据此调用 `save_artifact(sort_order=target_sort_order)` 做部分重试。

工具层（Python）在执行 `save_artifact(sort_order=N)` 时，因 `cardinality=list` 且已指定 `sort_order`，走「覆盖指定项」分支：根据 `sort_order` 查找对应 `list_index`，在该 `list_index` 上追加新 revision；`order_list` 不变。**`list_index` 全程不出现在 prompt 或工具参数的对外文档中。**

---

## 六、版本历史

### 6.1 写入时机

| 场景 | 触发时机 | `change_source` |
| --- | --- | --- |
| AI 步骤完成（`done` 事件） | Go 在 `routeToTaskSSE` 的 `done` 分支收到 done 信号后，**异步（goroutine）**读取该步骤所有 artifact，对每个 `(slot_id, list_index)` 写一条新 `plugin_slot_revisions`（`content_snapshot` = artifact value，`selected=TRUE`，旧行 `selected=FALSE`）。**注意**：快照为异步写入，前端在极短时间窗口内通过 `/versions` 接口可能拿到 `content_snapshot=null` 的最新版本，应展示 loading 状态直到有值。 | `'ai'` |
| 用户在 Panel 内人工编辑文字 | 前端不再直接调 `PATCH /items/idx/{list_index}`——改为**两层草稿机制**：编辑期间写 localStorage，60s 无新输入或 Chat 发送前自动 flush 为后端 revision（详见 [manual.md § 能力二](./manual.md)）。✅ 已落地 | `'human'` |
| 用户替换图片/文件 | 上传完成直接调 `PATCH /items/idx/{list_index}`，立即产生新版本，不经过 draftStore。✅ 已落地 | `'human'` |

### 6.2 版本回退

```
POST /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/rollback
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
GET /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/versions
  → plugin_slot_revisions WHERE session_id=? AND slot_id=? AND list_index=?
    ORDER BY revision ASC
  → [{revision, change_source, created_at, content_snapshot (truncated for image)}]
```

---

## 七、前端扩展

### 7.1 Panel 扩展点（已有组件增量修改）

| 组件 | 新增能力 | 状态 |
| --- | --- | --- |
| `SlotImage` | 右上角 `×` 按钮（二次确认后删除该 list_index 下所有 artifact）；左下角版本角标（多版本时显示，含快速切换箭头）；版本角标点击弹出 `SlotVersionPopover`；引用按钮（携带 artifact 元数据，见 §7.2）；caption inline 编辑；`ordered=true` 时渲染拖拽手柄 | ✅ 已落地 |
| `SlotText` | **两层草稿机制**（onChange→localStorage，60s/发送时 flush 为版本，详见 [manual.md](./manual.md)）；版本角标点击弹出 `SlotVersionPopover`；caption inline 编辑；`SlotEditingContext` 通知父组件禁用 Continue/Retry 按钮 | ✅ 已落地 |
| `SlotFile` | 版本角标；caption inline 编辑 | ❌ caption 编辑未实现 |
| `SlotVersionPopover` | 图片模式（缩略图条 + 上传新版）+ 文本模式（版本列表 + diff 对比）+ 「应用此版本」回退 | ✅ 已落地 |
| `AddSlotItemButton` | list slot 底部 `+` 按钮 + 创建 Modal（文字输入 + caption） | ✅ 已落地 |
| `PluginPanel` | `composite` 布局渲染；接收 `slot_updated` SSE 事件局部刷新；维护 `focused_tab` / `focused_sort_order` 状态并随 chat 请求携带 | 部分实现（composite 待） |

### 7.2 Artifact 引用到对话框

`SlotImage`（及后续支持引用的 slot 组件）新增「引用」按钮，点击后将该 artifact 的引用对象注入父组件 `ChatInput` 的 `files` 列表，随下一条消息发送。

引用对象包含两部分：

**文件本体**（用于多模态输入）：
- 图片类型：图片 URL / 本地路径，复用已有 `files` 字段

**artifact 元数据**（帮助大模型决策，单独字段携带）：

```json
{
  "type": "artifact_ref",
  "session_id": "...",
  "slot_id": "material_image",
  "sort_order": 2,
  "artifact_key": "material_image",
  "caption": "ref2.jpg（灯光参考）",
  "content_type": "image"
}
```

前端在 `ChatInput` 的 `files` 列表中展示预览缩略图，同时在消息 body 中携带 `artifact_refs` 数组（与 `files` 并列）。Go 在构造 Step objective 时将 `artifact_refs` 拼入 prompt，格式为：

```
[引用的素材] slot: material_image, sort_order: 2, 描述: ref2.jpg（灯光参考）
```

这样 SubAgent 在重新生成时能理解"用户引用的是哪个素材、在哪个位置"，可据此调用 `save_artifact(sort_order=2)` 精确覆盖目标项。

### 7.3 版本历史 UI（`SlotVersionPopover`）

**设计原则**：主界面不引入额外视觉元素，版本信息以角标形式内联在图片/内容上。

#### 主界面：版本角标

- 有多版本（`revision > 1`）时，在图片卡片**左下角**显示版本角标（如 `V3`），并在角标两侧显示 `‹` / `›` 快速切换箭头。
- 当前只有一个版本时，不渲染角标，保持主界面干净。
- 点击 `‹` / `›`：直接在当前卡片内切换预览版本（仅前端状态，不触发 rollback）；切换后角标高亮提示"非当前版本"，显示「应用」按钮。
- 点击版本号（`V3`）：弹出版本对比浮层（见下）。
- UI图参考docs/plan/plugin/03-data_history/image_version_ui.png

#### 版本对比浮层

```
┌──────────────────────────────────────────────┐
│  版本历史                               [×]  │
├─────────────────┬────────────────────────────┤
│  版本列表        │  对比区                    │
│  ┌──────────┐  │  ┌──────────┬───────────┐  │
│  │ V1  ai   │  │  │ 当前版本  │ V1 预览   │  │
│  │ V2  ai   │  │  │  [图片]   │  [图片]   │  │
│  │ V3  human│◀─│  │           │           │  │
│  └──────────┘  │  └──────────┴───────────┘  │
│                │  change_source / created_at  │
│                │  [应用此版本]                │
└─────────────────┴────────────────────────────┘
```

- 版本列表：`revision` 倒序，`change_source` 用 badge 区分（`ai` / `human`），`created_at` 相对时间。
- 对比区：左侧固定显示当前选中版本（`selected=TRUE`），右侧显示用户点击的历史版本。
- 文本类型：右侧显示行级 diff（当前 vs 历史）。
- 图片类型：并排图片，可点击放大。
- 「应用此版本」→ `POST .../rollback`，成功后浮层关闭，主界面刷新，角标更新为新版本号。

### 7.4 composite 布局（跨 Slot 联合渲染）

当 Tab `layout=composite` 时，前端按 `sort_order` 对齐多个 slot 的内容，每个 `sort_order` 值对应一行。行内各列的排列方式通过 `composite_layout` 字段声明，支持并排与 Tab 切换的任意嵌套。

#### composite_layout 语法

布局节点有三种形式，可递归嵌套：

| 节点形式 | 语义 |
| --- | --- |
| `"slot_id"` | 单个 slot，独占一列 |
| `[a, b, c]` | 并排展示，等分列宽（默认行为） |
| `{tabs: [a, b, c]}` | Tab 切换，a/b/c 共享同一区域 |

列宽可用扩展形式按 `weight` 控制（省略时均为 1）：

```yaml
- slot: slide_html
  weight: 2     # 占 2/(1+2) 的宽度
```

#### 示例

**PPT 场景**：左侧文字描述，右侧 HTML 预览/讲稿可 Tab 切换：

```yaml
ui:
  tabs:
    - id: slides
      layout: composite
      slots:
        - id: slide_desc
        - id: slide_html
        - id: slide_notes
      composite_layout:
        - [slide_desc, {tabs: [slide_html, slide_notes]}]
```

渲染效果（每行 sort_order=N）：

```
┌──────────────┬─────────────────────────┐
│  slide_desc  │ [HTML预览] [讲稿]        │
│              │─────────────────────────│
│  (页面描述)  │  当前 Tab 内容           │
└──────────────┴─────────────────────────┘
```

**图文对比场景**：素材图与生成图并排：

```yaml
composite_layout:
  - [material_image, generated_image]
```

**带列宽权重的复杂场景**：

```yaml
composite_layout:
  - - slot: subject_text
      weight: 1
    - slot: {tabs: [image_output, html_output]}
      weight: 2
```

#### 语义规则

- 顶层 `composite_layout` 是数组，每个元素描述**列布局**（所有行共用同一列布局）。
- `[...]` 并排节点内的子节点可以是字符串 slot_id 或 `{tabs: [...]}` 对象，不限层级。
- `{tabs: [...]}` 内的每个 Tab 标题从对应 slot 的 `label`（含 i18n）读取。
- 省略 `composite_layout` 时，退化为所有 slot 简单并排（等同于 `[slot1, slot2, ...]`）。
- 行级拖拽调序通过 `plugin_slot_order` 表的 `order_list` 统一管理，参与 composite 的所有 slot 共享同一 `sort_order` 空间（同一 `session_id + composite_tab_id` 维护一行）。

> 前端解析器实现见 [code.md § composite_layout 解析](./code.md#composite_layout-解析)。

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

> **路由设计说明**：所有 slot item 级接口均使用 `items/idx/{list_index}` 定位，而非原设计的 `items/{sort_order}`。`sort_order` 是展示顺序，会随删除/调序动态变化；`list_index` 是稳定身份，前端从 `GET /slots` 响应中已拿到每项的 `list_index`，直接用于接口参数，避免服务端一次 sort_order→list_index 翻译，也消除并发下的歧义。

```
# Artifact 管理（✅ 已实现）
DELETE /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}
  → 逻辑删除（hidden=TRUE），同步从 order_list 移除；前端调用后通过本地刷新更新 UI

PATCH  /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}
  → 人工编辑写版本，change_source='human'（由 draftStore.flushDraft 触发，不再由 Save 按钮直接触发）

PATCH  /api/core/plugin-sessions/{session_id}/slots/{slot_id}/order
  body: {order: [list_index, ...], version: N}  → 乐观锁调序（version 不匹配返回 409）
  注意：order 数组传入 list_index 序列（已调整后的顺序），前端从本地排序结果直接映射

GET /api/core/plugin-sessions/{session_id}/slots/{slot_id}/order
  → 返回当前 {order_list: [...], order_version: N}（供工具层 sort_order→list_index 查询）

# 人工编辑附件（✅ 已实现，详见 manual.md）
POST /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items
  body: {value, caption?, insert_before?}  → 创建新 slot item，change_source='human'

PATCH /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/caption
  body: {caption}  → 只更新 caption，不写 revision，不触碰 plugin_slot_revisions

# 版本历史（✅ 已实现）
GET  /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/versions
  → plugin_slot_revisions WHERE list_index=? ORDER BY revision ASC
  → [{revision, change_source, created_at, content_snapshot}]

POST /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/idx/{list_index}/rollback
  body: {revision: N}  → 追加回退版本，selected 指向新 revision

# 已有接口变更
GET /api/core/plugin-sessions/{session_id}/slots
  → 每条记录新增 content_snapshot / change_source / caption / sort_order / revision_count / list_index 字段
  → slot 级别新增 order_version 字段（用于前端乐观锁）

POST /api/core/conversations:chat（前端新增字段）
  body 新增 plugin_ui_state: {focused_tab, focused_sort_order}  ✅ 已实现
  body 新增 artifact_refs: [{artifact_key, slot_id, sort_order}]  ✅ 已实现
  → Go 读取后合并进 plugin_context 下发 Python

GET /api/core/plugins/{plugin_id}
  → 新增 i18n 字段；根据 Accept-Language header 返回对应语言 label
```

---

## 九、实施顺序

1. ✅ **数据层**：
   - `sub_agent_artifacts` 新增 `hidden` / `caption` 字段 + migration。
   - 新建 `plugin_slot_order(session_id, slot_id, order_list JSONB, order_version INT)` 表 + migration。
   - `plugin_slot_revisions` 新增 `content_snapshot` / `change_source` 字段 + migration。
   - `plugin.yaml` schema 扩展（`ordered` / `layout` / `i18n`），`plugin_loader.py` 同步解析。

2. ✅ **Go 层**：
   - `routeToTaskSSE` 的 `done` 分支：读 artifact 写 `content_snapshot` 到 `plugin_slot_revisions`。
   - `OnArtifactEvent`：从 artifact value 中读取 `caption` 字段写入 DB。
   - 逻辑删除、调序、人工编辑版本、回退四个接口（均以 `list_index` 定位）；新增 POST items、PATCH caption 接口（→ manual.md 能力一/三）。
   - `plugin_context` 构造：`focused_tab` / `focused_sort_order` 从 `plugin_ui_state` 读取并合并。
   - `current_turn_seq` 字段：Go `filesPerTurnMap` 函数新增 `currentSeq` 参数，通过 `current_turn_seq` 字段透传给 Python，确保当前轮次附件标注准确。
   - `history_files_per_turn` 字段：从 `conversation_logic` 透传到 SubAgent runner 和 driver agent，供 `find_user_attachment` / `read_user_attachment` 使用。
   - **注意**：`artifact_summary` 和 `visible_sort_order_map` 的构造在 Go 层**尚未实现**——目前 `plugin_context` 只转发前端传入的字段，不包含 Go 主动汇总的 artifact 摘要。artifact 摘要能力由 Python 层的 `db.py`（`format_artifact_summary`）提供，直接在 Python 侧读 DB 生成，不经 Go 层构造。

3. ✅ **Python 层**：
   - `save_artifact` 工具新增 `caption` / `sort_order` 参数，内部做 `sort_order → list_index` 查找。
   - `patch_artifact` 工具：局部编辑 artifact 草稿（str_replace / json_merge / json_patch），配合 `save_artifact` 两步提交。已注册到 SubAgent 工具集。
   - `discard_draft` / `find_artifact` / `find_user_attachment` 工具：已实现并注册。
   - `list_knowledge_bases()` 工具：已注册到 SubAgent 工具集。
   - `read_user_attachment(filename)` 工具：已注册，当前绑定请求临时 files（待 §TODO 对齐 DB 查询）。
   - `chat_service.py` ToolGuard 修复：所有 `plugin_tools` 中的可调用对象统一注册到 allowlist；`plugin_stop_tools` 仅用于 `set_stop_tools`，两件事分离。
   - `plugin_manager.py`：`plugin_context` 中的 `visible_sort_order_map` 解析"第N个"→ `target_sort_order`；读取 `focused_tab` / `focused_sort_order` 注入 prompt。
   - `plugin.yaml` i18n 解析，`GET /plugins/{id}` 接口返回多语言 label。

4. ✅ **前端（已落地部分）**：
   - ✅ `SlotImage`：`×` 删除按钮（二次确认）；版本角标（含 `‹/›` 快切箭头）；`SlotVersionPopover`；引用按钮；caption 编辑；绝对路径异步 sign 渲染修复；`ordered=true` 时渲染拖拽手柄（`isDraggable` 条件渲染）。
   - ✅ `SlotText`：两层草稿机制（→ manual.md 能力二）；版本角标；`SlotVersionPopover`；caption 编辑；`SlotEditingContext`。
   - ✅ `SlotVersionPopover`：版本列表 + 图片/diff 对比（LCS 相似度矩阵算法，支持行数不等的块对齐）+ 「应用此版本」。
   - ✅ `AddSlotItemButton`：list slot 底部 `+` 按钮 + Modal（→ manual.md 能力一）。
   - ✅ `ChatInput` / `chatLayout`：引用注入 `artifact_refs`；`plugin_ui_state` 携带；发送前 `flushAllDrafts`。
   - ✅ `PluginPanel`：拖拽手柄及 drag-and-drop 调序已实现（`onDragStart/onDrop/onDragEnd`，调序后调 `reorderSlotItems`）。
   - ✅ `SlotFile`：caption inline 编辑已实现（与 SlotText/SlotImage 对齐）。
   - ✅ `PluginPanel`：`composite` 布局已实现（`CompositeSlotGrid`，支持 `composite_layout` 声明式解析，含嵌套 tabs）。

5. user_attachments（详见 manual.md）：
   - ✅ 注入逻辑已通过 `history_files_per_turn` 复用实现（Go → Python system prompt，ChatAgent + SubAgent 均生效）。

6. ✅ **端到端验证**（image-plugin 扩展）：
   - 验证图片删除后"第N个"正确映射。
   - 验证用户上传参考图 → SubAgent 读取并 save_artifact 入库。
   - 验证 AI 完成步骤后 `content_snapshot` 写入，版本列表可查，回退后 Panel 正确刷新。

