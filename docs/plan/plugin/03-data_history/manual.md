---
name: 人工编辑附件能力
overview: 在现有 Phase 3 基础上，增加人工创建/上传内容、两层草稿存储（localStorage + 后端版本）、大文件整体编辑、独立 caption 编辑（不产生版本）、用户上传文件持久化到 session 五项能力。
todos:
  - id: draft-store
    content: 新增 draftStore：setDraft(写localStorage+重置60s timer) / flushDraft / flushAllDrafts / cancelDraft；图片/文件不经过 draftStore，直接 patchSlotItemValue
    status: pending
  - id: slot-text-refactor
    content: SlotText mount时静默恢复localStorage草稿；onChange→setDraft；Ctrl+S/Save按钮/关闭编辑态→写localStorage（行为完全一致，不产生版本）；Cancel→cancelDraft。大文件走虚拟滚动
    status: pending
  - id: chat-layout-flush
    content: chatLayout onOpenSSE 发送前 await flushAllDrafts(sessionId)
    status: pending
  - id: backend-caption-api
    content: 后端新增 PATCH .../caption API，不写 revisions 只改 artifacts.caption
    status: pending
  - id: frontend-caption-ui
    content: SlotComponents 卡片底部增加 caption inline 编辑（单行 input，失焦立即落盘）
    status: pending
  - id: backend-create-api
    content: 后端新增 POST items API，支持追加和 insert_before；大文件先 upload 再关联 artifact path
    status: pending
  - id: frontend-add-btn
    content: list slot 底部增加 + 按钮和创建 Modal
    status: pending
  - id: user-attachments-slot
    content: dialog 级 __user_attachments__ 隐式 slot，附件按 session 存储；Go core 聚合同 dialog 所有 session 附件注入 system prompt（普通对话+SubAgent 均生效）；SubAgent 额外注册 read_user_attachment(filename) 工具（无 session_id 参数，上下文自动绑定）
    status: pending
isProject: false
---

# 人工编辑附件能力方案

## 背景

现有基础（Phase 3 已落地）：
- `SlotText` 有 textarea 编辑态，`Save` 按钮直接调 `patchSlotItemValue` → 后端立即写新 `human` revision（本方案将改掉这个行为）
- `sub_agent_artifacts.caption` 字段已有，但无前端编辑入口
- `plugin_slot_order` 乐观锁调序已实现
- `chatLayout` 发送时不感知 `PluginPanel` 内部编辑状态

---

## 能力一：人工创建/上传新内容

**后端新增 API**（[`backend/core/plugin/handlers.go`](backend/core/plugin/handlers.go)）

```
POST /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items
Body: { value: {...}, caption?: string, insert_before?: number }
```

- `insert_before` 省略 → 追加到末尾
- `insert_before = N` → 插入到第 N 位之前，`order_list` 更新顺序，`list_index` 不变（稳定身份）
- 调用 `WriteSlotRevisionWithSnapshot`，`change_source = 'human'`
- 图片/文件类型：前端先 `POST /api/core/temp/uploads` 上传，拿到 `stored_path`，再填入 `value.path`

**前端 UI**（[`SlotComponents.tsx`](frontend/src/modules/chat/components/PluginPanel/SlotComponents.tsx)）

- list slot 底部增加 `+` 按钮
- 点击后弹出 Modal：文字 slot 输入文本，图片/文件 slot 上传文件，可选填 caption
- 提交调用 `createSlotItem(sessionId, slotId, value, caption?, insertBefore?)` store 方法

---

## 能力二：文字编辑的草稿与版本控制

### 核心设计：两层存储

| 层 | 存储位置 | 触发条件 | 作用 |
|---|---|---|---|
| 草稿层 | `localStorage` | onChange / Ctrl+S / Save按钮 / 关闭编辑态 | 防丢失，页面崩溃可静默恢复 |
| 版本层 | 后端 `plugin_slot_revisions` | 60s 无新输入 / Chat 发送 | 产生正式版本记录 |

**Save 按钮 / Ctrl+S / 关闭编辑态 三者行为完全一致**：只写 localStorage，不产生后端版本。

**版本触发条件（仅两个）**：
- 60s 无新输入（debounce 自动，用户停止编辑后静默落版本）
- Chat 发送前（`flushAllDrafts`，强制 flush 所有 pending 草稿）

### draftStore 设计

新增到 [`frontend/src/modules/chat/store/pluginPanel.ts`](frontend/src/modules/chat/store/pluginPanel.ts)：

```typescript
interface DraftEntry {
  value: Record<string, unknown>;
  timer: ReturnType<typeof setTimeout> | null;
}
// key = `${sessionId}:${slotId}:${sortOrder}`
drafts: Map<string, DraftEntry>
```

四个方法：

- `setDraft(sessionId, slotId, sortOrder, value)` — 写 localStorage（`slotDraft:${key}`）+ 更新内存草稿 + 重置 60s timer
- `flushDraft(sessionId, slotId, sortOrder)` — 清 timer，调 `patchSlotItemValue`，产生 human revision，**不**清除 localStorage（localStorage 在 `setDraft` 时维护，版本落盘不清除）
- `flushAllDrafts(sessionId)` — 遍历所有 `sessionId:` 前缀的条目，并行 `flushDraft`，返回 `Promise<void>`
- `cancelDraft(sessionId, slotId, sortOrder)` — 清 timer，清除 localStorage，丢弃草稿，不产生版本

### SlotText 改造

- **mount 时**：检查 `localStorage.getItem('slotDraft:...')`, 若有草稿，静默替换显示内容，不弹提示，不自动进入编辑态；用户看到的是最新内容
- **onChange**：调 `setDraft`（不再直接调 `patchSlotItemValue`）
- **Ctrl+S / Save 按钮 / 关闭编辑态**：调 `setDraft`（含 localStorage 写入），即"确认当前草稿已保存到本地"，不产生版本
- **Cancel 按钮**：调 `cancelDraft`，恢复到 `artifact_value` 原始内容
- **图片/文件替换**：不经过 draftStore，上传完成直接调 `patchSlotItemValue`，立即产生新版本

数据流：

```mermaid
sequenceDiagram
    participant User
    participant SlotText
    participant DraftStore
    participant LocalStorage
    participant ChatLayout
    participant Backend

    User->>SlotText: 持续输入 / Ctrl+S / 点Save
    SlotText->>DraftStore: setDraft()
    DraftStore->>LocalStorage: 写 slotDraft:key
    Note over DraftStore: 重置 60s timer

    Note over DraftStore: 60s 无新输入
    DraftStore->>Backend: patchSlotItemValue() — 静默落版本

    User->>ChatLayout: 点击发送
    ChatLayout->>DraftStore: await flushAllDrafts(sessionId)
    DraftStore->>Backend: patchSlotItemValue() — 强制落版本
    ChatLayout->>Backend: POST /conversations:chat
```

### 大文件文本的整体版本

大文本 artifact value 格式：`{"type":"text","path":"artifacts/xxx.txt","size":N}`。

- `SlotText` 检测到 `artifact_value.path` 时，异步 `GET /api/core/static-files/{path}` 拉取完整内容
- 使用虚拟滚动渲染，避免 DOM 爆炸；编辑时全量覆盖 draftStore 的同一 key
- `flushDraft` 触发时：先 `POST /temp/uploads` 上传草稿内容为新文件，取回 `stored_path`，再 `PATCH items` 更新 `value.path`，产生 1 个 human revision
- 分页是纯 UI 表现，无论用户修改哪行，版本始终对应整个文件

---

## 能力三：编辑 caption（不产生新版本）

**后端新增 API**（[`backend/core/plugin/handlers.go`](backend/core/plugin/handlers.go)）

```
PATCH /api/core/plugin-sessions/{session_id}/slots/{slot_id}/items/{sort_order}/caption
Body: { caption: string }
```

逻辑：`SortOrderToListIndex` → 找 selected revision 的 `artifact_key` → `UPDATE sub_agent_artifacts SET caption=? WHERE artifact_key=?`。**不触碰 `plugin_slot_revisions`**。

**前端 UI**（[`SlotComponents.tsx`](frontend/src/modules/chat/components/PluginPanel/SlotComponents.tsx)）

- slot 卡片下方显示 caption（灰色小字），点击进入 inline 编辑态（单行 input）
- 失焦或按 Enter 立即调 `patchSlotCaption`，不经过 draftStore，立即落盘
- 文字/图片/文件三种 slot 均适用

---

## 能力四：用户上传文件持久化（对话阶段）

**问题**：对话中上传的文件目前只是 temp 路径传入 chat，没有持久化到 session，AI 后续无法再次引用。

**方案**：dialog 级别的隐式 `__user_attachments__` list slot，普通对话与 SubAgent 均可感知文件，SubAgent 额外支持读取文件内容。

```mermaid
flowchart LR
    Upload["用户上传文件\n（对话框 / PluginPanel）"] --> Temp["POST /temp/uploads\n返回 stored_path"]
    Temp --> SlotAPI["POST .../slots/__user_attachments__/items\nvalue={filename,path,size}, caption=用户描述\nchange_source='human'"]
    SlotAPI --> DB["持久化为 artifact + revision\n（归属当前 session）"]

    DB --> SystemPrompt["每次 chat\nGo core 聚合同 dialog 所有 session 的附件\n注入 system prompt 文件列表（普通对话 + SubAgent 均生效）"]
    DB --> SubAgentTool["SubAgent 额外注册\nread_user_attachment(filename) 工具\n可读取文件内容"]
    DB --> Panel["PluginPanel 可选展示\n已上传文件列表（按 session 分组）"]
```

### 存储粒度

- `__user_attachments__` 是普通 slot（`slot_type='list'`），前端按 `slot_id` 前缀 `__` 过滤，不显示在 tab 中
- slot 归属于具体的 `plugin_session_id`，天然隔离不同轮次（session）的上传文件
- PluginPanel 展示时按 session 分组，保留历史可读性

### 注入策略：system prompt 层（普通对话 + SubAgent 均生效）

Go core 在 `buildChatRequestBody` 阶段（早于 `applyChatRuntimeConfigs`）新增注入逻辑：

1. 根据 `conversation_id` 查出同一 dialog 下所有 session 的 `__user_attachments__` slot 的 selected revisions
2. 将文件列表拼入 `system_prompt` 末尾的固定片段：

```
[用户已上传的文件]
- report.pdf (2 MB) — 上传于 2026-06-18
- data.xlsx (500 KB)
如需读取文件内容，请使用工具（仅 SubAgent 模式支持）。
```

3. 注入写入 `reqBody["user_attachments_context"]` 字段，Python chat 侧在拼 system prompt 时消费此字段
4. 文件列表只写入 system prompt，**不写入消息历史**，每次请求重建，历史消息保持干净

### SubAgent 工具扩展（SubAgent 独有）

Python `tools.py` 新增 `read_user_attachment(filename)` 工具：

- 工具**不接受 session_id 参数**，`session_id` 通过执行上下文（`lazyllm.globals['agentic_config']['plugin_session_id']`）自动绑定，AI 不感知
- 查找范围：同 dialog 下所有 session 的 `__user_attachments__`，按 filename 匹配
- 返回文件内容（文本）或文件路径（二进制），AI 可进一步处理

> `list_user_attachments` 工具不再单独提供——文件列表已通过 system prompt 注入，AI 无需主动调工具查询。

### 上传触发时机

- 用户对话框上传文件后，在原有 `artifact_refs` 上传流程后追加一步 `POST items`
- 若当前 conversation 无活跃 session，Go core 自动为该 dialog 创建一个隐式 `__attachments_session__`，专门承载附件 slot

---

## 实施顺序

1. `draftStore` 核心逻辑（setDraft / flushDraft / flushAllDrafts / cancelDraft）
2. `SlotText` 改造（onChange/Ctrl+S/Save/关闭→setDraft，Cancel→cancelDraft，mount 静默恢复，大文件虚拟滚动）
3. `chatLayout` 发送前 `await flushAllDrafts(sessionId)`
4. 后端 PATCH caption API
5. 前端 caption inline 编辑组件
6. 后端 POST items API（追加/insert_before，支持大文件 path 写入）
7. 前端 + 按钮 + 创建 Modal
8. `__user_attachments__` slot 机制：
   a. Go core `buildChatRequestBody` 聚合同 dialog 所有 session 附件 → 注入 `user_attachments_context`
   b. Python chat 侧消费 `user_attachments_context`，拼入 system prompt
   c. Python `tools.py` 新增 `read_user_attachment(filename)` 工具（无 session_id 参数，上下文自动绑定）
   d. 前端上传文件后追加 `POST items` 到 `__user_attachments__` slot

## 需要修改的关键文件

- [`frontend/src/modules/chat/store/pluginPanel.ts`](frontend/src/modules/chat/store/pluginPanel.ts) — draftStore（含 localStorage）
- [`frontend/src/modules/chat/components/PluginPanel/SlotComponents.tsx`](frontend/src/modules/chat/components/PluginPanel/SlotComponents.tsx) — SlotText 改造 + 大文件编辑器 + caption UI + + 按钮
- [`frontend/src/modules/chat/pages/chatLayout/index.tsx`](frontend/src/modules/chat/pages/chatLayout/index.tsx) — flushAllDrafts + 上传后关联 user_attachments
- [`backend/core/plugin/handlers.go`](backend/core/plugin/handlers.go) — PATCH caption、POST items 两个新 API
- [`backend/core/plugin/routes.go`](backend/core/plugin/routes.go) — 注册新路由
- [`backend/core/chat/conversation.go`](backend/core/chat/conversation.go) — `buildChatRequestBody` 聚合 dialog 内所有 session 的 `__user_attachments__`，注入 `user_attachments_context`
- [`algorithm/lazymind/chat/engine/chat.py`](algorithm/lazymind/chat/engine/chat.py)（或 system prompt 拼接处）— 消费 `user_attachments_context`，追加到 system prompt
- [`algorithm/lazymind/chat/engine/subagent/tools.py`](algorithm/lazymind/chat/engine/subagent/tools.py) — `read_user_attachment(filename)` 工具（上下文绑定 session_id，无外露参数）
