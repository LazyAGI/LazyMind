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
      - web_search_tool
      - image_search_tool

steps:                               # 步骤 UI 声明（仅供前端展示，执行逻辑在 state.yml）
  - id: step_one                     # 步骤 ID，需与 state.yml 中 steps key 一致
    label: Step One                  # 步骤显示名称

slots:                               # artifact 数据槽完整定义（list 格式）
  - id: my_text_slot                 # slot 唯一标识，供 state.yml 的 inputs/outputs 引用
    label: My Text                   # slot 显示名称（用户可见）
    type: text                       # artifact 类型：text | image | file | json
    cardinality: single              # single（单值，重写覆盖）| list（追加或按 index 更新）

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
| `ui.tabs[].id` | 条件 | tab 唯一标识（有 ui 时必填）|
| `ui.tabs[].label` | 条件 | tab 显示标签 |
| `ui.tabs[].layout` | 条件 | `list` / `grid` / `horizontal` |
| `ui.tabs[].slots[].id` | 条件 | 引用 slots 中的 id |
| `i18n` | 否 | 国际化翻译，有就写，AI 自动生成时不产出此字段 |

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
      condition: 'Always enter step_one first.'   # 供 LLM 阅读的自然语言条件
  step_one:

  step_two:
    - to: step_three
      condition: 'step_two complete — proceed to step_three.'
    - to: step_one          # 条件路由：需配合 route: choice
      condition: 'step_two failed — retry from step_one.'

  step_three:
    - to: __end__
      condition: 'Pipeline complete.'

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
      - web_search_tool           # 框架工具（save_artifact 等）始终自动注入，无需声明
    inputs:                       # 可选：前置依赖 slot 列表
      - slot: prior_slot          # 引用 plugin.yaml 中 slots 的 id
        required: true            # true（缺失时拒绝触发）| false（可选，缺失时仍执行）
    outputs:                      # 可选：本步骤应产出的 slot 列表
      - slot: my_output
    acceptance_criteria: |        # 可选：步骤质量标准，供 DriverAgent 评判时参考
      my_output artifact must be saved and contain at least 20 words.
    skipif: 'condition to skip'   # 可选：满足时 StateMachine 生成 bypass 边允许跳过
    route: choice                 # 可选，仅在有多个出边时有意义：
                                  #   all（默认）：同时触发所有满足条件的出边（并行）
                                  #   choice：只走第一个满足条件的出边（条件路由，互斥选一）
```

### 字段速查表

| 字段 | 必填 | 说明 |
|---|---|---|
| `initial` | 是 | 固定为 `__start__` |
| `transitions` | 是 | 转移规则 map，key 为源步骤 id；`__start__` key 定义从起始节点出发的入口转移 |
| `transitions.__start__[].to` | 是 | 第一个目标步骤 |
| `transitions.__start__[].condition` | 推荐 | 起始转移条件描述 |
| `transitions[src][].to` | 是 | 目标状态 |
| `transitions[src][].condition` | 推荐 | 供 LLM 阅读的转移条件描述 |
| `steps[step_id].label` | 否 | 步骤显示名称 |
| `steps[step_id].mode` | 否 | `auto`（DriverAgent 推进）/ `human`（等用户）；**即将支持，当前不生效** |
| `steps[step_id].prompt` | 是 | SubAgent 执行指令，支持 `{{...}}` 占位符 |
| `steps[step_id].tools` | 否 | 自定义工具名列表（框架工具自动注入，无需写）|
| `steps[step_id].inputs[].slot` | 条件 | 依赖的 slot id |
| `steps[step_id].inputs[].required` | 否 | 默认 true；false 表示可选 |
| `steps[step_id].outputs[].slot` | 条件 | 产出的 slot id |
| `steps[step_id].acceptance_criteria` | 否 | 步骤质量标准，供 DriverAgent 评判参考 |
| `steps[step_id].route` | 否 | `all`（默认）/ `choice`（条件路由）|
| `steps[step_id].skipif` | 否 | 满足时允许 LLM 跳过此步骤 |

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

**约束**：`{{<slot_id>}}` 只能引用该步骤 `inputs` 里声明的 slot。引用了不在 inputs 里的 slot 时，运行时该占位符会被替换为空字符串，导致 SubAgent 获取不到预期内容。

---

## 四、scenario.md 格式说明

`scenario.md` 是插件的**使用场景介绍**，描述插件能做什么、适合什么场景、各步骤的作用、以及用户需要了解的注意事项。它在运行时被注入 ChatAgent 的 system prompt，帮助 ChatAgent 理解插件的上下文。

**不要**在 scenario.md 里描述冷启动触发逻辑、`advance_step` 工具调用规则等框架机制——那些由框架自动处理，与具体插件无关。

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

## 六、重试语义

重试通过 `transitions` 自环或回退边表达，无需额外字段。

```yaml
transitions:
  step_one:
    - to: step_two
      condition: 'step_one artifact meets quality standard.'
    - to: step_one               # 自环 = 重试本步骤
      condition: 'step_one artifact is missing or below quality standard.'
```

配合 `route: choice` 使用：ChatAgent 根据 DriverAgent 的评估消息，选择走推进边还是重试边。

---

## 七、常见模式

### 7.1 串行流水线（最简单）

```yaml
transitions:
  __start__: [{to: step_a, condition: 'Always.'}]
  step_a: [{to: step_b, condition: 'step_a done.'}]
  step_b: [{to: __end__, condition: 'Pipeline complete.'}]
```

### 7.2 条件路由

```yaml
transitions:
  step_a:
    - to: step_b
      condition: 'User provided outline — skip to writing.'
    - to: step_outline
      condition: 'No outline provided — generate outline first.'
steps:
  step_a:
    route: choice    # 必须配合 choice，否则两条边都触发
```

### 7.3 可选步骤（skipif）

```yaml
steps:
  step_optional:
    skipif: 'User already provided reference materials.'
    # StateMachine 自动生成 bypass 边，LLM 可选择跳过
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
