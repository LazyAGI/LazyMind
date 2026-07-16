# LazyMind 插件格式规范

插件是 LazyMind 的可扩展工作流单元，由最多五个文件组成。本文档是插件格式的**唯一 source of truth**，供开发者查阅和大模型生成时参考。

---

## 一、文件结构

```
plugins/<plugin-id>/
  plugin.yaml              # 插件注册元数据（必须）
  scenario/
    scenario.md            # ChatAgent 意图识别指南（必须）
    state.yml              # 状态机 + 步骤执行规范（必须）
    driver.md              # DriverAgent 评判 system prompt（auto 模式必须，dynamic 模式可省略）
  scripts/
    tools.py               # 插件自定义工具函数（可选，可有多个文件）
```

**运行时职责**：

| 文件 | 读取方 | 用途 |
|---|---|---|
| `plugin.yaml` | Python PluginLoader、前端 | 插件注册、触发条件、slot 定义、UI 布局 |
| `scenario.md` | ChatAgent（注入 system prompt） | 插件使用场景介绍、各步骤作用说明 |
| `state.yml` | Python SubAgent runner、StateMachine | 步骤执行规范（prompt/tools/inputs/outputs/transitions）|
| `driver.md` | DriverAgent（auto 模式，每步完成后调用） | 评判 SubAgent 执行结果是否符合质量标准 |
| `scripts/*.py` | Python PluginLoader（动态 import） | 插件自定义工具函数，供 SubAgent 调用 |

---

## 二、plugin.yaml 字段说明

### 完整模板

```yaml
id: my-plugin                        # 插件唯一标识，需与目录名一致
name: My Plugin                      # 插件显示名称（用户可见）
description: >                       # 功能描述，注入冷启动 system prompt
  一段描述插件能做什么的文字。

when_to_use: >                       # 【最关键】触发判断依据，拼入 trigger 工具的 docstring
  ONLY call this tool when ...       # 告知 ChatAgent 何时（且仅在何时）触发此插件
  Do NOT trigger if ...              # 同时明确写出不应触发的场景

tool_scripts:                        # 可选：插件自定义工具脚本列表
  - path: scripts/tools.py           # 相对于插件目录的 Python 文件路径
    functions:                       # 要从该文件导入并注册为工具的函数名列表
      - web_search
      - image_search_tool

steps:                               # 步骤 UI 声明（仅供前端展示，执行逻辑在 state.yml）
  - id: step_one                     # 步骤 ID，需与 state.yml 中 steps key 一致
    label: Step One                  # 步骤显示名称

slots:                               # artifact 数据槽完整定义（list 格式）
  - id: my_text_slot                 # slot 唯一标识，供 state.yml 的 inputs/outputs 引用
    label: My Text                   # slot 显示名称（用户可见）
    type: text                       # artifact 类型：text | image | file | json
    cardinality: single              # single（单值，重写覆盖）| list（追加或按 index 更新）
    external: true                   # 由用户/session 提供；否则必须有且仅有一个 step producer

  - id: my_image_list
    label: Image Collection
    type: image
    cardinality: list
    ordered: true                    # list 时可选：支持用户拖拽重排序
    allow_manual_add: false          # list 时可选：用户是否可手动添加项（默认 true）

  - id: my_summary
    label: Summary
    type: text
    cardinality: single
    summary_max_chars: 300           # 可选：slot 值注入 prompt 时截断的最大字符数

ui:                                  # 可选：插件面板 UI 布局定义
  tabs:
    - id: result_tab                 # tab 唯一标识
      label: Result                  # tab 显示标签
      layout: grid                   # 布局方式：list（纵向列表）| grid（网格）| horizontal（横向）
      slots:
        - id: my_image_list          # 引用 slots 中的 id

i18n:                                # 可选：国际化翻译，有就写，没有就省略整个块
  zh-CN:
    name: 我的插件
    steps:
      step_one: {label: 第一步}
    slots:
      my_text_slot: {label: 我的文本}
    tabs:
      result_tab: {label: 结果}
```

### 字段速查表

| 字段 | 必填 | 说明 |
|---|---|---|
| `id` | 是 | 唯一标识，需与目录名一致 |
| `name` | 是 | 插件显示名称 |
| `description` | 是 | 功能描述，用于冷启动 system prompt |
| `when_to_use` | 推荐 | 触发判断依据，拼入 trigger 工具 docstring |
| `tool_scripts[].path` | 条件 | 自定义工具脚本路径（有自定义工具时必填）|
| `tool_scripts[].functions` | 条件 | 要注册为工具的函数名列表 |
| `steps[].id` | 是 | 步骤 ID（需与 state.yml 一致）|
| `steps[].label` | 是 | 步骤显示名称 |
| `slots[].id` | 是 | slot 标识 |
| `slots[].label` | 是 | slot 显示名称 |
| `slots[].type` | 是 | `text` / `image` / `file` / `json` |
| `slots[].cardinality` | 是 | `single` / `list` |
| `slots[].ordered` | 否 | list 时是否支持拖拽重排，默认 false |
| `slots[].allow_manual_add` | 否 | list 时用户是否可手动添加，默认 true |
| `slots[].summary_max_chars` | 否 | 注入 prompt 时的摘要字符上限 |
| `slots[].external` | 否 | true 表示外部输入素材，不需要 step producer |
| `ui.tabs[].id` | 条件 | tab 唯一标识（有 ui 时必填）|
| `ui.tabs[].label` | 条件 | tab 显示标签 |
| `ui.tabs[].layout` | 条件 | `list` / `grid` / `horizontal` |
| `ui.tabs[].slots[].id` | 条件 | 引用 slots 中的 id |
| `i18n` | 否 | 国际化翻译，有就写，AI 自动生成时不产出此字段 |

素材表示可持久化的数据或制品，只允许来自两类来源：用户在任务描述之外额外提供的输入（例如上传文件、参考图片、表单字段、数据集），或者某个前序步骤的产出。用户 query、任务描述、意图、指令、prompt 文本和对话上下文不是素材，不得为了向步骤传递原始请求而创建 `user_query`、`search_query`、`request`、`topic`、`task_description`、`instructions` 等伪素材；步骤可直接使用任务和对话上下文。只有确实要求用户单独填写或上传的数据才声明为 `external: true`。

素材 ID 必须遵守单 producer：每个非 external 素材恰好由一个步骤产出，可以被任意多个后继步骤读取；同一步骤不能读取并重新产出同一个素材。多阶段加工必须使用不同 ID（如 `outline`、`revised_outline`）。素材 producer 必须是 consumer 的控制祖先，系统不会自动补控制边。

---

## 三、state.yml 字段说明

`state.yml` 是状态机执行规范。注意：**slot 定义在 `plugin.yaml`，`state.yml` 里只引用 slot id**。

### 完整模板

```yaml
initial: __start__    # 状态机起始状态，固定为 __start__

# 占位符说明（Python SubAgent runner 在执行时将 prompt 中的占位符替换）：
#   {{user_input}}           — 用户原始请求文本
#   {{runtime_instruction}}  — 运行时临时指令（重试 hint 等，由 Go 注入），无时为空字符串
#   {{<slot_id>}}            — 指定 slot 的 artifact 值（text → 文本内容；image → URL）

transitions:
  __start__:
    - to: step_one
  step_one:
    - to: step_two
      when: 用户希望继续完善结果                 # 可选，自然语言提示，由 ChatAgent 判断
    - to: step_three
      when: 用户希望直接使用当前结果

  step_two:
    - to: step_three

  step_three:
    - to: __end__

steps:
  step_one:
    label: Step One               # 步骤显示名称（可与 plugin.yaml 保持一致）
    mode: auto                    # 即将支持。auto（DriverAgent 自动推进）| human（等待用户确认）
    prompt: |
      You are an expert for this task.

      User request: {{user_input}}
      {{runtime_instruction}}

      Your task:
      1. Do something useful.
      2. Save the result:
           save_artifact(key='my_output', content_type='text', value=<result>)

      Stop after saving.
    tools:                        # 可选：该步骤 SubAgent 可用的工具名列表
      - web_search                # ToolConfig 名；Toolkit 会作为整体注册并按需展开
    inputs:                       # 有序输入列表；required 决定是否阻塞 Ready
      - material: revised_outline
        required: true
        alternatives:
          - material: outline
      - material: references
        required: true
      - material: style_guide
        required: false
    outputs:                      # 可选：本步骤应产出的 slot 列表
      - material: my_output
    acceptance_criteria: |        # 可选：步骤质量标准，供 DriverAgent 评判时参考
      my_output artifact must be saved and contain at least 20 words.
    skip_if:                      # 可选：满足素材表达式时 bypass，不创建 attempt
      any:
        - material: existing_result
        - material: imported_result
    route: choice                 # 可选，仅在有多个出边时有意义：
                                  #   all（默认）：可并行推进适用的出边
                                  #   choice：由 ChatAgent 根据 when 选择适用出边
```

### 字段速查表

| 字段 | 必填 | 说明 |
|---|---|---|
| `initial` | 是 | 固定为 `__start__` |
| `transitions` | 是 | 转移规则 map，key 为源步骤 id；`__start__` key 定义从起始节点出发的入口转移 |
| `transitions.__start__[].to` | 是 | 第一个目标步骤 |
| `transitions.__start__[].when` | 否 | 给 ChatAgent 的自然语言选择提示；候选节点仍由 Go 标记为 Reachable |
| `transitions[src][].to` | 是 | 目标状态 |
| `transitions[src][].when` | 否 | 自然语言路由提示，不参与 Go 素材求值，也不要求无条件 fallback |
| `steps[step_id].label` | 否 | 步骤显示名称 |
| `steps[step_id].mode` | 否 | `auto`（DriverAgent 推进）/ `human`（等用户）；**即将支持，当前不生效** |
| `steps[step_id].prompt` | 是 | SubAgent 执行指令，支持 `{{...}}` 占位符 |
| `steps[step_id].tools` | 否 | 自定义工具名列表（框架工具自动注入，无需写）|
| `steps[step_id].inputs` | 否 | 有序输入列表；每项通过 `required` 区分必须/可选，必须输入可声明一层 `alternatives` |
| `steps[step_id].outputs[].material` | 条件 | 产出的素材 id |
| `steps[step_id].acceptance_criteria` | 否 | 步骤质量标准，供 DriverAgent 评判参考 |
| `steps[step_id].route` | 否 | `all`（默认）/ `choice`（条件路由）|
| `steps[step_id].skip_if` | 否 | 一层 `all(materials)` 或 `any(materials)`；为真时由 Go bypass，不支持嵌套和自然语言 |

### 保留关键字

| 关键字 | 说明 |
|---|---|
| `__start__` | 虚拟起始节点，不可作为真实 step |
| `__end__` | 虚拟终止节点，不可作为真实 step |

### prompt 中的占位符

| 占位符 | 替换内容 |
|---|---|
| `{{user_input}}` | 用户原始请求文本 |
| `{{runtime_instruction}}` | Go 注入的运行时指令（重试 hint 等），无时为空字符串 |
| `{{<slot_id>}}` | 指定 slot 的 artifact 值（text → 文本；image → URL）|

**约束**：`{{<slot_id>}}` 只能引用该步骤 `inputs` 中声明的主素材或替代素材。

输入固定为有序列表。`required: true` 的各项之间是 AND；该项的主素材与 `alternatives` 之间是 OR；`required: false` 不参与 Ready 判断且不能配置替代素材。素材 ID 全局唯一，因此不提供 `bind_as`。步骤声明的所有 outputs 均视为必产，前端不提供可选产出配置。

---

## 四、scenario.md 格式说明

`scenario.md` 是插件的**使用场景介绍**，描述插件能做什么、适合什么场景、各步骤的作用、以及用户需要了解的注意事项。它在运行时被注入 ChatAgent 的 system prompt，帮助 ChatAgent 理解插件的上下文。

**不要**在 scenario.md 里描述冷启动触发逻辑、`advance_step` 工具调用规则等框架机制——那些由框架自动处理，与具体插件无关。

### 冷启动预检与首次步骤

冷启动时，ChatAgent 初始只看到插件的 `name`、`description` 和 `when_to_use`。
调用 `trigger_<plugin>(request_context)` 后，框架才加载完整插件并执行启动预检；trigger
本身不会创建 PluginSession 或任务。预检结果可能为 `need_information`、
`not_applicable`、`preflight_failed` 或带有 `launch_plan` 的 `ready`。

`ready` 表示首步、规范化后的完整用户意图以及是否 hand-off 均已确定并通过状态机校验。
ChatAgent 必须在同一回合调用对应 advance 工具；若模型未调用，执行层会先强制继续一次
ReAct，再按已校验的 `launch_plan` 确定性提交。只有首次 advance 被后端接受时才创建
PluginSession，并原子消费对话中持久化的 preflight。

`auto` 模式不会注册 `ask_user`。预检缺少必需信息时返回阻塞结果，等待用户下一轮主动补充。

### 推荐结构

```markdown
# <插件名>

## 场景描述

一段对插件的整体介绍：这个插件的目的是什么，适合什么样的用户需求，能完成什么类型的任务。

## 工作流程

简要说明各步骤的作用和顺序：

1. **step_one** — 做什么
2. **step_two** — 做什么，基于第一步的结果
3. **step_three** — 做什么，产出最终结果

如果某些步骤支持独立重跑，可以在这里说明（例如：用户可以单独重做步骤二来调整中间结果）。

## 注意事项

- 这个插件适合 XX 类任务，不适合 YY 类任务
- 某步骤可能耗时较长，属于正常情况
- 其他需要用户了解的约束或说明
```

---

## 五、driver.md 格式说明

`driver.md` 是 **DriverAgent 的 system prompt**。DriverAgent 仅在 **auto 模式**下生效：每当一个 step 的 SubAgent 执行完成后，Go 调用 `/api/plugin/driver`，将 `driver.md` + 该 step 的 `acceptance_criteria` 作为 system prompt，让 DriverAgent 对执行结果输出 1-2 句自然语言评估。Go 把这段评估作为合成用户消息，触发 ChatAgent 下一轮推理，由 ChatAgent 决定推进、重试还是结束。

### 推荐结构

```markdown
You are the DriverAgent for the <Plugin Name> plugin.
Your job is to describe, in plain natural language, whether the current step result is complete and acceptable.

## Step completion criteria

### <step_id>
- Complete: <描述何种情况视为完成，例如 artifact 已存且满足某条件>
- Incomplete: <描述何种情况视为未完成，以及应描述什么问题>

### <step_id_2>
- Complete: ...
- Incomplete: ...

## Output rules

Write 1-2 plain sentences describing what happened.
- If complete: state what was saved and that it looks good.
- If incomplete: state what is missing or wrong, and what likely caused it.
- Do NOT output PASS, RETRY, DONE, FAIL, or any verdict codes.
- Do NOT output bullet lists, tags, or preamble.
- When the root cause lies in a prior step, name that step in your description.
- Keep the message under 60 words.
```

---

## 六、重试与回溯

控制图必须是 DAG，不允许自环或回退边。`retry` 与 `rewind` 是 Go 运行时命令：重试失效当前 attempt；回溯则按实际素材 witness 和 route provenance 递归标记 Stale，再重新计算 Ready。

---

## 七、常见模式

### 7.1 串行流水线（最简单）

```yaml
transitions:
  __start__: [{to: step_a}]
  step_a: [{to: step_b}]
  step_b: [{to: __end__}]
```

### 7.2 条件路由

```yaml
transitions:
  step_a:
    - to: step_b
      when: 用户认可当前大纲
    - to: step_outline
      when: 用户希望继续修改大纲
steps:
  step_a:
    route: choice    # Go 暴露候选 Reachable 节点，ChatAgent 根据 when 选择；不需要 fallback
```

### 7.3 可选步骤（skip_if）

```yaml
steps:
  step_optional:
    skip_if:
      any:
        - material: existing_references
        - material: imported_references
    # 条件为真时由 Go 编译后的 bypass 路径绕过，节点自身不会执行
```

### 7.5 Composite 布局（新格式 C）

Composite 布局用于将多个 slot 以任意横纵分割方式并排展示，支持嵌套分块和分块内 Tab 切换。

**ui.slots — 全局控件配置**

widget 配置从 `tabs[].slots[].widget` 移至顶层 `ui.slots`，以 slot id 为 key：

```yaml
ui:
  slots:
    page_design:
      widgetType: image-gallery
      itemLayout: grid
      itemWidth: 200
    content:
      widgetType: text-markdown
    notes:
      widgetType: text-single
      readOnly: true

  tabs:
    - id: slides
      layout: composite
      composite_tab_position: left   # 全局 Tab 条位置：top/bottom/left/right
      slots:
        - id: page_design
        - id: content
        - id: notes
      composite_layout:
        direction: row
        children:
          - slot: page_design
            weight: 2
          - direction: column
            weight: 1
            children:
              - slot: content
                weight: 1
              - tabs:
                  - notes
                weight: 1
```

**`CompositePanelNode` 字段说明**

| 字段 | 类型 | 说明 |
|---|---|---|
| `slot` | string | 叶子节点：绑定单个 slot id |
| `tabs` | string[] | 叶子节点：Tab 切换区，各项为 slot id，Tab 标题取 slot label |
| `direction` | `'row'` / `'column'` | 容器节点：横向或纵向分割 |
| `children` | CompositePanelNode[] | 容器节点的子节点列表 |
| `weight` | number | 该节点在父容器中所占比例（默认 1） |

约束：`slot` / `tabs` / `direction+children` 三者互斥。

**兼容性**

旧格式（数组 `[{slot, weight}]` 或 `[[{slot, weight}]]`）在解析时自动迁移为格式 C，无需手动转换。


```yaml
slots:                    # 在 plugin.yaml 中定义
  - id: image_list
    type: image
    cardinality: list
    ordered: true

# SubAgent 多次调用 save_artifact(key='image_list', ...)，每次追加一项
```
