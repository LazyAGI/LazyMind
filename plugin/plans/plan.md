# 对话插件（Chat Plugin）系统设计方案

---

## 一、项目背景

### 1.1 原始需求

计划在对话框中支持两个富交互工具模块：

**AI 写作**
- 能力：从零写作 + 在线编辑
- 流程：用户需求 → 大纲 → Draft → 完稿
- 前端：分 Tab 展示大纲、参考文献、Draft、完稿；Draft/完稿有侧栏目录辅助跳转
- 记忆：「全局信息」在整个生成过程中持续注入上下文
- 子问题：生成过程中的子问题及其答案可见
- 编辑方式：直接在线编辑 + 通过对话指导大模型修改（支持引用特定段落）
- 版本管理：每次修改自动打快照，可查两版本间 diff，可回退

**AI PPT**
- 能力：从零生成 + 在线编辑
- 流程：用户需求 → 大纲 → 每页设计（文字）→ 完稿（图片）
- 前端：分页展示，支持前后翻页、拖拽调整页序
- 记忆：「全局信息」在整个生成过程中持续注入上下文
- 子问题：生成过程中的子问题及其答案可见
- 每页包含：设计文字（可直接编辑 / 对话修改）+ 完稿图片（可对话修改）
- 图片交互：可在图片上圈点、添加文字标注，随对话发给 AI

两个工具均支持「自动进行」和「人工干预」两种模式。

### 1.2 设计意图

这两个工具并不是本方案要实现的目标，而是用来**标定「对话插件（Chat Plugin）框架」所需支持的能力边界**。设计框架时，以这两个工具为参照，确保框架足够通用，同时不把插件特有的逻辑混入框架。

---

## 二、设计原则

1. **算法工具不感知插件**：`generate_image(prompt) → url`、`search_kb(query) → results` 等工具是纯函数，不引用任何插件类，不 emit 任何 PluginEvent。事件由插件的 `run()` 负责 emit，工具只负责计算结果。工具依赖方向是单向的：插件 → 工具，工具不知道插件的存在。

2. **插件文件集中存放**：每个插件涉及的所有层（算法编排、前端视图、可选后端导出）的代码放在同一目录下（`plugins/{id}/`），不散落在 `algorithm/`、`frontend/`、`backend/` 各处。

3. **框架最大化，插件最小化**：持久化、版本管理、任务调度、SSE 解析、UI 原语、上下文注入、Scenario 加载与校验——所有可复用的能力全部由框架提供。插件只需关注：工作流步骤、内容生产逻辑（调纯函数工具）、界面组合、交互定义。

---

## 三、架构概览

### 3.1 Plugin vs Tool 职责边界

```
Tool（算法工具）    纯函数，位于 algorithm/lazymind/chat/engine/tools/
                    generate_image(prompt) → url
                    search_kb(query) → results
                    call_llm(prompt) → text
                    不引用任何插件类，不 emit 事件

Plugin（插件）      工作流编排者，位于 plugins/{id}/algorithm.py
                    import 并调用纯函数工具
                    在 run(ctx) 中 emit(PluginEvent)

Plugin Manager      把每个插件的操作包装成 ReactAgent 可调用的工具：
                      trigger_plugin()         LLM 首次识别意图时调用，创建 session、启动 Job
                      plugin_proceed(id)       推进下一步（内部校验 state.yml 硬约束）
                      plugin_edit(id, ...)     对某个 artifact 发起修改请求
                    首次触发后，步骤执行由代码驱动（不再依赖 LLM 多次 call）
```

ReactAgent 通过消费 `scenario.md`（框架自动注入 `environment_context`）来理解插件的当前状态和用户意图，决定调用哪个工具。无需独立的 PluginRouter 组件。

**与 Skill 的区别**：

| | Skill | Plugin Scenario |
|---|---|---|
| 驱动方式 | LLM 多步主动 call tools | LLM 触发一次，代码执行工作流 |
| 元数据入口 | SKILL.md，LLM 运行时主动调 `get_skill()` 工具读取 | `scenario.md`，框架自动注入 `environment_context`，LLM 被动感知 |
| 内容可见性 | 默认只可见名称 + 描述，全文需主动拉取 | `scenario.md` 全文始终在 context 里，无需主动发现 |
| 注入时机 | 任何对话均可触发 | 仅当该插件 session 活跃时才有意义 |
| 硬约束 | 无 | `state.yml` 的边界约束在 `plugin_proceed()` 工具层强制执行 |
| 适合场景 | 灵活探索型任务 | 固定流程 + 富交互 UI 的任务 |
| lazyllm 改动 | 无需改 | **无需改**（scenario 走两条已有通道：environment_context + 工具注册） |

### 3.2 插件目录结构

```
repo-root/
  algorithm/        ← 不变（纯函数工具在此）
  frontend/         ← 不变（框架 UI 原语在此）
  backend/          ← 不变（框架 Go 代码在此）
  plugins/          ← 与各层平级，每个插件的所有文件集中于此
    base.py             框架：BasePlugin、PluginEvent、PluginContext
    manager.py          框架：@register_plugin、build_plugin_tools()
    loader.py           框架：启动时扫描 plugins/*/algorithm.py，动态 import
    validator.py        框架：Scenario 文件校验
    image-plugin/
      plugin.yaml
      scenario/
        scenario.md     概述：场景、能力、状态转移说明、用户意图识别指引
        state.yml       状态机：步骤有向图 + 转移条件
        driver.md       auto 模式：步骤完成时代 agent 代替用户决策的策略
        prompts/
          step1.md      该步骤的任务指令 + 输出格式规范
          ...
      algorithm.py
      frontend/
        config.ts
        types.ts
        index.tsx
      backend/
        export.go       可选，仅文件导出（Word/PDF/PPTX）时创建
    writing-plugin/
      ...
    ppt-plugin/
      ...
```

**`plugin.yaml` 示例**：

```yaml
id: writing-plugin
name: AI 写作
scenario: scenario/          # 显式指向 scenario 目录，loader 按此路径加载
trigger_description: |
  Launch an interactive writing panel when the user asks to write a
  long-form article, essay, report, blog post, or any structured document.
steps:
  # default_mode 含义：该步骤执行完成后的默认行为
  # human（默认）= 暂停，等待用户通过对话或「继续」按钮确认后再推进
  # auto = 步骤完成后由 agent 读取 driver.md，结合上下文和当前产物自动决策下一步
  - id: outline
    label: 大纲
    default_mode: human
  - id: draft
    label: Draft
    default_mode: human
  - id: final
    label: 完稿
    default_mode: auto
artifacts:
  global_info:   { type: text }
  outline:       { type: json }
  references:    { type: json }
  sub_questions: { type: json }
  draft:         { type: text }
  final:         { type: text }
```

### 3.3 各层加载机制

| 层 | 加载方式 | 新增插件是否需要改动 |
|---|---|---|
| 算法层（Python） | 启动时 `loader.py` 扫描 `plugins/*/algorithm.py`，动态 import，`@register_plugin` 自动注册；同时解析 `scenario/`（缓存 scenario.md、state.yml、driver.md、prompts/） | ❌ 不需要 |
| 前端（TypeScript） | `registry.ts` 加一行 import | ✅ 加一行 |
| 后端（Go） | 有 `export.go` 时 `loader.go` 加一行 blank import；无导出插件无需动 | 🔶 按需 |

---

## 四、框架 vs 插件职责

### 4.1 持久化存储

**框架提供**：四张通用表（见 §五），所有插件共用。写入逻辑也由框架负责：
- AI 路径：SSE `plugin_event.patch` → Go SSE Handler → `plugin_session_artifacts`，自动生成版本快照
- 人工路径：`PATCH /plugin-sessions/:id/artifacts` → Go Handler → `plugin_session_artifacts`，自动生成版本快照

**插件提供**：仅需在 `plugin.yaml` 声明 artifact 列表及类型（`text` / `image` / `json`）。

### 4.2 多轮对话路由与上下文

当 plugin session 活跃时，以下内容被注入 `environment_context`：

| 注入方 | 注入内容 | 数据来源 |
|---|---|---|
| Go | `plugin_sessions.meta`、当前 artifact 摘要、当前步骤名 | 数据库 |
| Python `chat_service.py` | `scenario.md` 全文、当前步骤的 `state.yml` 可达出边 | `loader.py` 启动缓存 |

ReactAgent 读取这些上下文后，自行决策调用哪个工具：
- 用户意图为「推进下一步」→ 调 `plugin_proceed(session_id, target_step)`
- 用户意图为「修改当前产物」→ 调 `plugin_edit(session_id, artifact_id, instruction)`
- 用户一般性提问 → 正常回复，不调插件工具

`plugin_proceed()` 内部从 `loader.py` 缓存读取 `state.yml`，做硬约束校验——即便 Agent 决策有误，非法的步骤跳转也会被工具层拦截。

**全局对话上下文**：请求体携带 `plugin_context` 字段（前端框架自动填充），插件在 `run()` 中通过 `ctx.plugin_context` 读取。

**插件内局部引用**：框架负责 `RichEditor` 的「选中引用到对话」和 `ImageAnnotator` 的「发送标注给 AI」，自动把内容序列化进 `plugin_context.payload`；插件在 `run()` 中读取 `ctx.payload.cited_text` / `ctx.payload.annotations` 决定如何使用。

### 4.3 前端展示、inplace 编辑、版本管理

**框架提供**：所有通用 UI 原语（见 §八）、inplace 编辑保存、版本快照/diff/rollback、拖拽排序持久化。

**插件提供**：`index.tsx` 选用哪些原语、如何排布；`types.ts` 定义 artifact 数据结构。

### 4.4 步骤模式（human / auto）

**默认是 human 模式**。每个步骤在 `plugin.yaml` 中通过 `default_mode` 声明该步完成后的默认行为；用户也可在 `StepProgress` 组件中逐步切换 `step_mode`。

| 模式 | 步骤完成后的行为 |
|---|---|
| **human**（默认） | 步骤进入 `waiting` 状态，暂停工作流。用户通过对话表达意图（ReactAgent 调 `plugin_proceed` / `plugin_edit`）或点击「继续」按钮（`POST /proceed`）推进 |
| **auto** | 框架在步骤完成后触发一次 agent 决策：读取 `scenario/driver.md` + 当前上下文 + 当前步骤产物，由 agent 代替用户判断并调用 `plugin_proceed` 推进下一步（或 `plugin_edit` 要求修改） |

auto 模式不走独立的「自动驾驶子进程」，而是在步骤边界由框架发起一次正常的 ReactAgent 调用，只是决策依据来自 `driver.md` 而非用户输入。`driver.md` 在步骤完成时注入 agent 的 system prompt。

### 4.5 其他可复用能力

| 能力 | 框架/插件 | 说明 |
|---|---|---|
| 步骤进度条（human/auto 切换） | **框架** | `StepProgress` 组件 + `plugin_session_steps.step_mode` 存储 |
| 步骤暂停与恢复 | **框架** | human 模式 `POST /proceed` + Redis 信号；auto 模式步骤边界 agent 决策 |
| AsyncJob 任务调度 | **框架** | 每个 session 对应一个 long-lived Job，per-job LockTTL |
| 大文本 OSS 存储（>64KB） | **框架** | 自动切换 `content: {"type":"ref","url":"..."}` |
| 同一会话多插件共存检测 | **框架** | 创建前检查同 plugin_id 活跃实例，已存在则 toast 跳转 |
| RAG sources / 子问题自动收集 | **框架** | 从 SSE `sources` / `tool_results` 自动写入对应 artifact |
| Scenario 加载与校验 | **框架** | 启动时加载缓存，`PluginValidator` 校验，session 活跃时注入 context |
| LLM 内容生产 / prompt 设计 | **插件** | `run()` 内调用纯函数工具，`scenario/prompts/{step}.md` 提供 prompt 模板 |
| 文件导出（Word/PDF/PPTX） | **插件** | `backend/export.go` 实现，`init()` 注册路由 |

---

## 五、数据模型

### 5.1 四张核心表

```sql
-- 一个 plugin 会话的容器，一次触发对应一行
plugin_sessions
  id               VARCHAR(36) PK          -- 'ps-' 前缀，全局唯一
  conversation_id  VARCHAR(36) INDEX
  history_id       VARCHAR(36) INDEX       -- 触发该插件的消息
  plugin_id        VARCHAR(64)
  current_step_id  VARCHAR(36)             -- FK → plugin_session_steps.id
  meta             JSONB                   -- 全局信息（global_info 等）
  create_user_id   VARCHAR(255) INDEX
  created_at, updated_at

plugin_session_steps
  id               VARCHAR(36) PK
  session_id       VARCHAR(36) INDEX
  step             VARCHAR(64)             -- 如 'outline' / 'draft' / 'final'
  step_mode        VARCHAR(16)             -- 'human' | 'auto'，默认 human
  step_status      VARCHAR(16)             -- 'running' | 'waiting' | 'done'
  created_at, updated_at

plugin_session_artifacts
  id               VARCHAR(36) PK
  session_id       VARCHAR(36) INDEX
  step_id          VARCHAR(36) INDEX
  artifact_id      VARCHAR(64)
  head_version_id  VARCHAR(36)
  created_at, updated_at
  UNIQUE (session_id, step_id, artifact_id)

plugin_session_versions
  id               VARCHAR(36) PK
  session_id       VARCHAR(36) INDEX
  artifact_id      VARCHAR(36) INDEX
  parent_version_id VARCHAR(36)
  content          JSONB
  change_source    VARCHAR(16)             -- 'ai' | 'human'
  change_summary   VARCHAR(512)
  created_at
```

**`step_status` 含义**：
- `running`：步骤正在执行
- `waiting`：步骤执行完毕，human 模式下等待用户确认
- `done`：步骤已推进至下一步

### 5.2 async_jobs 扩展

```
conversation_id   VARCHAR(36) NOT NULL DEFAULT '' INDEX
lock_ttl_seconds  INT NOT NULL DEFAULT 0   -- 0 = Runner 全局默认（10min）
```

Plugin session Job 使用 `lock_ttl_seconds = 86400`（1 天），支持 human 模式长时间等待。

### 5.3 版本树与 HEAD 指针

`head_version_id` 是可移动指针，回退 = 移动指针，历史不删除，支持任意节点跳转和分叉。

### 5.4 内容存储策略

| artifact 类型 | 策略 |
|---|---|
| JSON 结构 | 全量 JSONB |
| 纯文本 < 64KB | 全量 JSONB |
| 纯文本 ≥ 64KB | OSS/S3，`content` 改为 `{"type":"ref","url":"..."}` |
| 图片 / 视频 | 始终存 URL |

版本快照：AI 侧每批 SSE patch 完成后自动打快照；人工侧防抖 5 秒或显式保存，发送对话前强制打快照确保版本连续。

### 5.5 多插件共存规则

同一 `plugin_id` 在同一 conversation 内只允许一个活跃实例；不同 `plugin_id` 可并存。创建前检查，已存在则 toast 跳转。

---

## 六、Scenario 机制

Scenario 是插件框架的**核心基建**（M1 即落地），解决两个问题：
1. **交互路由**：有活跃 plugin session 时，ReactAgent 如何理解用户意图、决定调哪个工具
2. **auto 模式决策**：步骤完成时，agent 按什么策略代替用户推进流程

Scenario 文件由框架在启动时加载并缓存，不需要每次对话动态读取。

### 6.1 文件结构与职责

```
scenario/
  scenario.md     ← 框架注入 environment_context；ReactAgent 被动感知
  state.yml       ← 硬约束由 plugin_proceed() 校验；可达出边注入 context
  driver.md       ← auto 模式步骤完成时注入 agent system prompt，指导自动决策
  prompts/
    {step_id}.md  ← algorithm.py 通过 BasePlugin.load_prompt(step) 加载
```

| 文件 | 谁读 | 何时 |
|---|---|---|
| `scenario.md` | 框架注入 environment_context | plugin session 活跃时每次对话 |
| `state.yml` 可达出边 | 框架注入 environment_context | plugin session 活跃时每次对话 |
| `state.yml` 硬约束 | `plugin_proceed()` 工具内部校验 | agent 调用 proceed 时 |
| `driver.md` | 注入 agent system prompt | auto 模式步骤完成时 |
| `prompts/{step}.md` | `BasePlugin.load_prompt(step)` | 执行对应 step 时 |

### 6.2 scenario.md 规范

```markdown
# AI 写作插件

## 场景描述
帮助用户完成长篇文章写作，流程为：大纲 → Draft → 完稿。

## 各步骤能力
- outline：根据用户需求生成结构化大纲，支持修改和重新生成
- draft：基于确认的大纲流式生成全文，支持引用段落进行局部修改
- final：对 draft 进行排版优化，输出完稿

## 用户意图识别
当用户表达"满意/继续/没问题/开始写/下一步"等意图时，调用 plugin_proceed()
当用户指向具体内容修改时，调用 plugin_edit()
当用户提出一般性问题时，直接回答，不调用插件工具
```

### 6.3 state.yml 规范

硬约束（哪些步骤之间有边）由框架强制执行；软条件（边上的转移判断）由 LLM 依据描述决策：

```yaml
initial: outline
transitions:
  outline:
    - to: draft
      condition: "用户明确确认大纲，或表示满意并要求继续写作"
    - to: outline
      condition: "用户要求修改大纲方向或重新生成大纲"
  draft:
    - to: final
      condition: "用户确认 draft 没有大问题，可以生成完稿"
    - to: draft
      condition: "用户要求修改 draft 的局部或整体内容"
  final:
    - to: draft
      condition: "用户对完稿不满意，需要回退重新修改 draft"
```

`state.yml` 不存在时，框架回退到 `plugin.yaml` 的 steps 线性顺序。

### 6.4 state.yml 处理链

全部在 Python 侧，Go 不参与：

```
启动时  loader.py → 解析 state.yml → 缓存 {plugin_id: StateMachine}

每次对话  chat_service.py
  → 注入 scenario.md + 可达出边至 environment_context
  → ReactAgent prompt 包含当前步骤 + 合法后继及转移条件

plugin_proceed() 调用时  manager.py
  → StateMachine.is_valid_transition(current, target)
  → 非法 → 返回错误；合法 → 写 Redis 信号，推进 AsyncJob
```

### 6.5 driver.md 规范

auto 模式下，步骤完成后框架触发 agent 决策，将 `driver.md` 注入 system prompt。agent 结合当前产物和上下文，通过正常的 `plugin_proceed()` / `plugin_edit()` 路径推进，无需额外工具或 API。

```markdown
# Auto Driver - AI 写作插件

你是写作插件的自动决策助手。当前步骤已完成，请根据产物质量决定是否推进。

## 各步骤判断标准

### outline 步骤
- 大纲覆盖用户原始需求的主要方面，章节数 3-7 个
- 满足条件 → 调用 plugin_proceed(target_step="draft")
- 有明显缺失 → 调用 plugin_edit 要求补充，或保持当前步骤

### draft 步骤
- 所有章节均已生成，无明显空白
- 满足条件 → 调用 plugin_proceed(target_step="final")

## 原则
- 不主动提出修改意见，除非有明显错误
- 每步最多循环修改 2 次，超出则强制推进
```

### 6.6 prompts/{step}.md 规范

`run()` 通过 `BasePlugin.load_prompt(step)` 加载，包含任务描述和输出格式规范。不提供时 `run()` 自行管理 prompt。

### 6.7 Scenario 校验

| 文件 | 检查方式 |
|---|---|
| `state.yml` | 工具（语法/存在性/连通性/一致性） |
| `scenario.md` | 大模型（结构/步骤覆盖/意图识别） |
| `prompts/{step}.md` | 工具（结构）+ 大模型（内容质量） |
| 跨文件一致性 | 工具（步骤 ID 统一性） |

| 级别 | 行为 |
|---|---|
| `error` | `loader.py` 拒绝加载该插件 |
| `warning` | 正常加载，写入日志 |
| `info` | 仅在 `POST /plugins/:id/validate` 响应中返回 |

**运行时机**：启动时 `validate_all()`；`PUT /plugins/:id/state-machine` 保存前校验 state.yml；按需 `POST /plugins/:id/validate` 完整检查。

```python
class PluginValidator:
    def validate_state_yml(state_yml, plugin_yaml) -> ValidationResult      # 纯工具
    def validate_scenario_md(scenario_md, plugin_yaml) -> ValidationResult  # LLM
    def validate_prompts(prompts_dir, plugin_yaml) -> ValidationResult       # 工具 + LLM
    def validate_all(plugin_dir) -> ValidationResult
```

---

## 七、通信协议

### 7.1 PluginEvent（SSE 扩展字段）

现有 SSE `result` 对象新增 `plugin_event` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `type` | `'mount' \| 'patch' \| 'step_change' \| 'append_item'` | 事件类型 |
| `plugin_session_id` | string | 插件会话 ID |
| `plugin_id` | string | `mount` 时携带 |
| `initial_state` | object | `mount` 时的初始 artifacts 快照 |
| `artifact_id` | string | `patch` / `append_item` 时携带 |
| `op` | `'replace' \| 'merge' \| 'append' \| 'delete'` | patch 操作类型 |
| `path` | string | JSON Pointer |
| `value` | any | patch 时的新值 |
| `step` | string | `step_change` 时携带 |
| `step_status` | string | `'running' \| 'waiting' \| 'done'` |
| `step_mode` | string | `'human' \| 'auto'`，步骤完成时携带当前模式 |
| `item` | any | `append_item` 时，如 PPT 逐页追加 |

### 7.2 plugin_context（请求体扩展）

```json
{
  "input": ["..."],
  "plugin_context": {
    "plugin_session_id": "ps-xxx",
    "plugin_id": "writing-plugin",
    "step": "draft",
    "event": "user_edit",
    "payload": {
      "artifact_id": "draft",
      "cited_text": "用户在 RichEditor 中选中引用的段落",
      "annotations": []
    }
  }
}
```

---

## 八、框架提供的能力清单

### 8.1 前端 UI 原语

| 组件 | 用途 | 阶段 |
|---|---|---|
| `PluginShell` | 标题栏 + 主内容区 + 侧栏 + 对话入口 | M1 |
| `StepProgress` | 步骤进度条，含 human/auto 切换 | M3 |
| `RichEditor` | Markdown 编辑 + 选中引用 + `SidebarTOC` | M4 |
| `DiffViewer` | 两版本 diff 对比 | M2 |
| `OutlineTree` | 可折叠 + dnd-kit 拖拽排序 | M4 |
| `PageCarousel` | 分页浏览 + 缩略图 + 拖拽排序 | M4 |
| `ImageAnnotator` | 图片圈点 + 文字标注 | M4 |
| `SubQuestionList` | 子问题 + 答案列表 | M4 |
| `VersionHistory` | 版本树 + 跳转 + 回退 | M2 |
| `ReferencePanel` | RAG sources 展示 | M4 |
| `StateGraph` | state.yml 有向图可视化 + 运行态叠加；P1 支持拖拽编辑 | **M7（增强）** |

### 8.2 其他框架能力

| 能力 | 说明 |
|---|---|
| `pluginSessionStore`（Zustand） | SSE 事件 → store 更新 |
| 版本管理 API | `GET/POST /versions`、`POST /rollback` |
| AsyncJob 调度 | 创建 Job、Heartbeat、per-job LockTTL |
| human 模式恢复 | `POST /proceed` → Redis → Handler 继续 |
| auto 模式决策 | 步骤完成时框架触发 agent + `driver.md` |
| 上下文注入 | Go 注入 DB 数据；Python 注入 scenario.md + 可达出边 |
| plugin 工具三件套 | `trigger_plugin` / `plugin_proceed` / `plugin_edit` |
| RAG sources / 子问题收集 | 自动写入对应 artifact |

---

## 九、执行计划

### Milestone 依赖关系

```
M1（图片插件 POC + Scenario 基建）
  └── M2（版本管理 + 引用 + 组件内对话）
        └── M3（步骤条 + AsyncJob + human/auto 模式）
              └── M4（UI 原语补全）
                    ├── M5（AI 写作插件）
                    └── M6（AI PPT 插件）
                          └── M7（StateGraph 可视化/编辑 + 任务看板，增强功能）
```

以「图片生成插件」为 POC 贯穿 M1–M4，**Scenario 机制在 M1 即落地**，避免后期重构。M5/M6 在框架充分验证后开发，只需关注内容侧。M7 为不影响主流程的增强功能，放在写作/PPT 插件之后。

---

### M1 — 极简 POC + Scenario 基建

**目标**：打通 `算法层 emit → SSE → Go → 前端渲染` 核心管道，同时落地 Scenario 加载与注入基建，确保后续里程碑无需重构 Scenario 层。

**交付物**：用户发送「生成一只戴帽子的猫」→ AI 优化 prompt → 生成图片 → 聊天框内弹出图片卡片，刷新后仍在。Scenario 文件已就位并被框架正确加载。

**Scenario 基建**
- [ ] `plugins/validator.py`：`PluginValidator`，含 state.yml 工具侧检查（语法/存在性/连通性/一致性）
- [ ] `plugins/loader.py`：解析 `plugin.yaml` 的 `scenario:` 字段，加载并缓存 `scenario.md` / `state.yml` / `driver.md` / `prompts/`；启动时 `validate_all()`，error 阻止加载
- [ ] `BasePlugin.load_prompt(step)`：从缓存返回 prompt 模板
- [ ] `plugins/image-plugin/scenario/`：scenario.md / state.yml / driver.md / prompts/（即使单步流程也先建好结构）
- [ ] `chat_service.py`：注入 `scenario.md` + `state.yml` 可达出边至 `environment_context`（session 活跃时）
- [ ] `POST /plugins/:plugin_id/validate` 接口（Go → Python）

**数据 & 协议**
- [ ] 定义 `PluginEvent` 最小接口（`type: mount | patch`）
- [ ] 新建 `plugin_sessions` 表，暂不建 steps/artifacts/versions 表
- [ ] Go ORM：`PluginSession` 结构体 + migration

**算法层**
- [ ] `plugins/base.py`：`PluginEvent`、`PluginContext`、`BasePlugin`
- [ ] `plugins/manager.py`：`@register_plugin`、`build_plugin_tools()`（含 `trigger_plugin`）
- [ ] `plugins/image-plugin/plugin.yaml`：含 `scenario:` 字段
- [ ] `plugins/image-plugin/algorithm.py`：`run()` 通过 `load_prompt(step)` 加载 prompt
- [ ] `event_translator.py`：flush `plugin_event_queue` 作为 `extra={'plugin_event': pe}` 发出

**后端 & 前端**
- [ ] SSE proxy 识别 `plugin_event`，mount 创建 session，patch 更新 meta
- [ ] `POST/GET /plugin-sessions`
- [ ] `pluginSessionStore` 最小版 + `StreamManager` 扩展
- [ ] `PluginShell` 骨架 + `ImageCard` 组件
- [ ] `registry.ts` 注册 `image-plugin`

**验证**：生图端到端跑通；Scenario 文件启动时校验通过；session 活跃时 scenario.md 出现在 agent context

---

### M2 — 版本管理 + 引用 + 组件内对话修改

**目标**：叠加持久化版本历史、图片引用到对话框、组件内继续修改。

- [ ] 新增 `plugin_session_steps` / `plugin_session_artifacts` / `plugin_session_versions` 表
- [ ] SSE patch 落库 + 自动版本快照；`PATCH /artifacts` 人工编辑
- [ ] 版本 API：`GET versions`、`POST rollback`
- [ ] `VersionHistory` + `DiffViewer` 组件
- [ ] `ImageCard`「引用到对话框」+ `plugin_context` 注入逻辑

**验证**：生图 → 对话修改两次 → 版本回退 → 分叉 → 版本树正确

---

### M3 — 步骤条 + AsyncJob + human/auto 模式

**目标**：图片插件升级为多步骤可控流程。默认 human 模式暂停等待用户；auto 模式步骤完成后由 agent 读 `driver.md` 自动决策。

- [ ] `async_jobs` 新增 `conversation_id` + `lock_ttl_seconds`
- [ ] `plugin_sessions.current_step_id` FK
- [ ] AsyncJob Handler：多步骤 + human 模式 Redis poll 等待 + **auto 模式步骤边界触发 agent 读 driver.md 决策**
- [ ] `plugins/manager.py`：注册 `plugin_proceed` / `plugin_edit`，内部校验 state.yml 硬约束
- [ ] `POST /plugin-sessions/:id/proceed`（human 模式用户点「继续」）
- [ ] `step_change` SSE 携带 `step_status` + `step_mode`
- [ ] `StepProgress` 组件：步骤状态展示 + human/auto 切换开关
- [ ] human 模式 UI：步骤完成后显示结果 + 「继续」按钮

**验证**：human 模式 prompt 优化完 → 暂停（waiting）→ 用户点继续 → 生图；切换 auto 模式 → 步骤完成后 agent 自动推进；关闭重开状态正确恢复

---

### M4 — UI 原语补全

**目标**：补全写作/PPT 所需的全部 UI 原语。本步结束后框架能力在图片插件上充分验证，不含 StateGraph（属 M7 增强）。

- [ ] `ImageAnnotator`：圈点/标注 + 「发送标注给 AI」
- [ ] `RichEditor` + `SidebarTOC`：Markdown 编辑 + 选中引用
- [ ] `OutlineTree` / `PageCarousel`：dnd-kit 拖拽排序 + 自动 PATCH
- [ ] `SubQuestionList` / `ReferencePanel`
- [ ] 同一会话多插件共存检测
- [ ] 大文本（>64KB）OSS 存储
- [ ] Go 上下文注入：session 活跃时注入步骤名 + artifact 摘要
- [ ] 更新 `/dev/plugin-demo` 展示所有原语

**验证**：图片标注 + 对话修改；通过 scenario.md 路由，用户说「没问题继续」触发 `plugin_proceed`；拖拽排序正确写库

---

### M5 — AI 写作插件

**目标**：使用已验证的框架原语组装写作插件，重点只在内容侧。

- [ ] `plugins/writing-plugin/`：plugin.yaml + scenario/ + algorithm.py（outline/draft/final 三步，流式 draft）
- [ ] `plugin:writing-plugin` AsyncJob Handler
- [ ] 前端：四 Tab（大纲/Draft/参考文献/子问题）+ `GlobalInfoPanel` + 导出按钮
- [ ] `registry.ts` 注册

**验证**：human 模式大纲审核 → 继续 → draft 流式生成 → 引用段落修改 → 版本回退；auto 模式 driver.md 自动推进三步

---

### M6 — AI PPT 插件

**目标**：第二个插件验证「新增插件只需关注内容侧」。

- [ ] `plugins/ppt-plugin/`：plugin.yaml + scenario/ + algorithm.py（`append_item` 逐页追加）
- [ ] `plugin:ppt-plugin` AsyncJob Handler
- [ ] 前端：大纲 Tab + `PageCarousel` + `ImageAnnotator` + 导出 PPTX
- [ ] 双插件并存验证：写作 + PPT 上下文不串台

**验证**：PPT 全流程；与写作插件同时运行互不干扰

---

### M7 — StateGraph 可视化/编辑 + 任务看板（增强）

**目标**：不影响主流程的增强功能。StateGraph 帮助开发者和用户理解工作流状态；任务看板提供会话级 Job 监控。

**StateGraph 可视化（P0）**
- [ ] `GET /plugin-sessions/:id/state-graph`：合并 state.yml 图结构 + 运行态（步骤状态 + artifact 预览）
- [ ] `StateGraph` 组件（ReactFlow）：节点状态 badge + 边 condition label + 合法后继虚线高亮；`readonly` 模式
- [ ] `PluginShell` 侧栏集成（可折叠）

**StateGraph 可视化编辑（P1）**
- [ ] `GET/PUT /plugins/:plugin_id/state-machine`：读取/保存图结构，保存前 `validate_state_yml()`，通过后热重载
- [ ] `StateGraph` 新增 `editable` prop：节点/边增删改、condition 内联编辑、layout 持久化
- [ ] 权限：仅 admin 或 `LAZYMIND_DEV_MODE=true`

**任务看板**
- [ ] `GET /conversations/:id/jobs`：按 conversation_id 查全部关联 Job
- [ ] 前端任务监控面板：展示当前会话所有 plugin session 进度条

**收尾**
- [ ] 补全 `docs/plugin-protocol.md`

**验证**：StateGraph 正确展示运行态；编辑器修改 state.yml 热重载生效；任务看板展示多插件进度

---

## 十、REST API 汇总

所有接口挂载在 `/api/v1/` 下，需认证，强制校验 `create_user_id`。

**Plugin Session**

| 方法 | 路径 | 说明 | 阶段 |
|---|---|---|---|
| `POST` | `/plugin-sessions` | 创建 session（SSE mount 自动触发） | M1 |
| `GET` | `/plugin-sessions/:id` | session 详情 + 当前步骤 + job 进度 | M1 |
| `PATCH` | `/plugin-sessions/:id` | 更新 current_step_id / meta | M3 |
| `GET` | `/plugin-sessions` | 按 conversation_id 列举 | M1 |

**步骤 & 执行**

| 方法 | 路径 | 说明 | 阶段 |
|---|---|---|---|
| `GET` | `/plugin-sessions/:id/steps` | 列举步骤记录 | M3 |
| `POST` | `/plugin-sessions/:id/proceed` | human 模式推进（写 Redis 信号） | M3 |
| `GET` | `/plugin-sessions/:id/job` | Job 状态与进度 | M3 |

**Artifact & 版本**

| 方法 | 路径 | 说明 | 阶段 |
|---|---|---|---|
| `GET` | `/plugin-sessions/:id/artifacts/:artifact_id` | 获取 HEAD 内容 | M2 |
| `PATCH` | `/plugin-sessions/:id/artifacts` | 人工编辑 | M2 |
| `GET` | `/plugin-sessions/:id/artifacts/:artifact_id/versions` | 版本树 | M2 |
| `POST` | `/plugin-sessions/:id/artifacts/:artifact_id/rollback` | 回退 | M2 |

**Scenario 校验**

| 方法 | 路径 | 说明 | 阶段 |
|---|---|---|---|
| `POST` | `/plugins/:plugin_id/validate` | 完整校验（工具 + LLM） | M1 |

**增强功能（M7）**

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/plugin-sessions/:id/state-graph` | 图结构 + 运行态 |
| `GET` | `/plugins/:plugin_id/state-machine` | state.yml 图结构（编辑器初始化） |
| `PUT` | `/plugins/:plugin_id/state-machine` | 保存编辑后的状态机，校验后热重载 |
| `GET` | `/conversations/:id/jobs` | 按 conversation 查全部 Job |

**数据库变更**

| 变更 | 阶段 |
|---|---|
| 新增 `plugin_sessions` | M1 |
| 新增 `plugin_session_steps` / `artifacts` / `versions` | M2 |
| `async_jobs` 新增 `conversation_id` + `lock_ttl_seconds` | M3 |

---

## 十一、新增插件标准清单

### 插件目录

```
plugins/my-plugin/
  plugin.yaml           必须：id / trigger_description / scenario / steps / artifacts
  scenario/
    scenario.md         必须
    state.yml           推荐（不提供则线性执行）
    driver.md           推荐（auto 模式决策策略）
    prompts/{step}.md   推荐
  algorithm.py          必须：@register_plugin，只写 run()
  frontend/
    config.ts / types.ts / index.tsx   必须
  backend/export.go     可选
```

### 目录之外只需改两处

```
frontend/src/modules/chat/plugins/registry.ts   + 一行 import
backend/core/plugin/loader.go                   + blank import（仅有 export.go 时）
```

### 插件必须提供

- `plugin.yaml`：触发条件、步骤列表（含 `default_mode`，默认 human）、artifact schema、`scenario:` 路径
- `scenario/scenario.md`：场景描述、各步骤能力、用户意图识别指引
- `algorithm.py` 的 `run(ctx)`：调纯函数工具 → emit PluginEvent；`load_prompt(step)` 加载 prompt
- 前端 `index.tsx` + `types.ts`

### 插件不需要关心

- 工具注册、DB migration、版本写入、AsyncJob 调度、REST API、SSE 解析、store 设计
- 上下文注入（scenario.md + 可达出边 + meta/artifact 摘要，框架自动处理）
- state.yml 硬约束校验、`PluginValidator` 启动校验
- `plugin_proceed` / `plugin_edit` 工具注册、human/auto 模式调度逻辑
- lazyllm ReactAgent 改动
