# LazyMind 插件格式规范

插件是 LazyMind 的可扩展工作流单元，由注册文件、步骤规范、Skill 和可选工具组成。本文档是插件格式的**唯一 source of truth**，供开发者查阅和大模型生成时参考。

---

## 一、文件结构

```
workflows/<workflow-id>/
  workflow.yaml              # 插件注册元数据（必须）
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
| `workflow.yaml` | Python PluginLoader、前端 | 插件注册、触发条件、slot 定义、UI 布局 |
| `scenario.md` | ChatAgent（注入 system prompt） | 插件使用场景介绍、各步骤作用说明 |
| `state.yml` | Python SubAgent runner、StateMachine | 步骤执行规范（prompt/tools/inputs/outputs/transitions）|
| `driver.md` | DriverAgent（auto 模式，每步完成后调用） | 评判 SubAgent 执行结果是否符合质量标准 |
| `scripts/*.py` | Python PluginLoader（动态 import） | 插件自定义工具函数，供 SubAgent 调用 |

---

## 二、workflow.yaml 字段说明

### 外部 Agent 执行

同一工作流由原生 LazyMind 和任意外部 Agent 共用一套业务契约：步骤提示词、输入、输出与验收标准只定义在 `state.yml`，`runtime.execution` 提供双方共用的 Skill、约束与可选工具。原生执行器保留本地工具绑定和已配置的模型；外部宿主创建自己的 subagent，负责生成和验证。LazyMind 保存步骤、输入、产物版本、状态和调用回执，不为外部执行启动文本/视觉模型，也不要求服务端语义验证回执。

```yaml
runtime:
  execution:
    version: '1'
    skills: [scenario/execution.md]
    constraints:
      - Validate the actual result and preserve the requested scope.
    steps:
      step_one:
        skills: [scenario/step-one.md]
        tools:
          - {name: ppt_export_html, source: package}
          - {name: 'builtin:image_generator', source: global}
```

声明必须覆盖图中所有步骤；每一步的 `state.yml` 有非空 `prompt`。`execution.steps` 只接受 `skills`、`constraints` 和 `tools`，禁止重新定义 prompt、acceptance_criteria 或 required_outputs，避免执行器产生两套业务规则。全局与步骤级 Skill、约束合并；Skill 路径必须是包内相对路径。Core 从固定 revision 读取 UTF-8 Skill 正文，在两端同形的 `step_contract.execution.skill_files` 返回路径、内容和 SHA-256；单文件上限 256 KiB，总上限 512 KiB。原生执行器把这些正文传入 SubAgent，外部宿主也须原样传给 worker，不能替换为本机其他版本。

契约同时返回 `launch_request`、本次 `user_request` 和固定输入。兼容字段 `external_execution` 从共享契约投影，`prompt` 和 `task_specification` 与原生节点提示词相同。`adapter_instructions` 说明原生 wrapper、工具专用模式、模型选择和自动发布规则属于本地执行绑定；外部适配器用宿主能力或已声明工具实现相同业务目标，不能据此改变输出含义、页数、格式或证据要求。

`acceptance_criteria`、`required_outputs`、槽类型、单值/列表、控制边和输入版本均直接使用共同节点的数据契约，可选与条件输出不会因执行器不同变成强制输出。人工审核、提问和停止/重试规则不因使用外部执行器而改变。

`tools` 是允许请求的可选能力，不决定 executor 的归属。`source: global` 指现有 capability gateway 中用户已授权的工具；推荐使用完整工具 ID。`source: package` 需要显式服务适配器，不能仅通过 YAML 声明就远程执行任意包脚本。PPT 提供 `ppt_export_html`、`ppt_render_html`、`ppt_inspect_pptx` 三个无模型适配器；脚本绑定与固定包内容须匹配服务实现。

外部宿主有能力时直接使用宿主能力。能力缺失时先检查已声明的 LazyMind 工具；仍不可用则向用户说明应在 LazyMind 配置哪项功能，保存已完成工作并等待重试。不得偷偷切到 LazyMind 文本模型、伪造产物，或把导出成功当作质量验证成功。

历史 `runtime.external_execution.version: '1'` 仍按已发布 revision 的原有规则读取，保留其中的独立提示词和覆盖语义；新包使用 `runtime.execution`，两者不得同时声明。更早的 revision 保留旧 executor 选择规则。修改声明要重新发布，已有运行继续绑定原 revision。接入流程见 [外部工作流开发接入](dev/external-workflow.md)。

### 启动前字段与 Ask User

`runtime.clarification_fields` 声明用户决策，不等同于文件或产物槽。启动时先复用本次请求、已提交的结构化答案和已批准默认值；适用且 `required: true` 的字段仍缺少，或请求中的明确取值存在冲突、无法确定时才提问。默认值不能覆盖用户明确排除的选项或未解决的冲突。Python 执行本地解析，Core 在 preparation 和直接启动入口校验；缺少必需信息时不创建 Session。没有新增串行模型解析阶段。

```yaml
runtime:
  clarification_fields:
    - id: output_format
      label: 导出格式
      question: 需要哪种交付格式？
      type: single
      choice_policy: fixed
      choices: [docx, md]
      default: docx
      allow_other: false
    - id: word_target
      label: 目标篇幅
      question: 正文目标字数是多少？
      reason: 篇幅决定章节分配和写作工作量。
      type: text
      required: true
      resolution: explicit
      patterns: ['([0-9]+)\s*字']
      validation: {format: positive_integer, min: 1000, max: 500000, example: '例如：8000'}
    - id: use_default_docx_template
      label: 文档样式
      question: 是否使用默认投标样式？
      type: boolean
      default: 'true'
      boolean_labels: {'yes': 使用默认投标样式, 'no': 使用基础样式}
      applies_when: {field: output_format, equals: docx}
```

字段级别由规则组成，不增加一套独立状态：

| 级别 | 表达方式 | 行为 |
|---|---|---|
| blocking | `required: true`，没有可用值或默认值 | 阻止启动，只询问未解决项 |
| defaultable | 声明 `default`，或由既有执行器自动规划 | 不为收集偏好而阻塞；用户明确值优先 |
| conditional | `applies_when` | 仅条件成立才解析；可与 blocking/defaultable 组合 |

条件只能引用更早声明的字段，并使用 `equals` 或 `not_equals` 之一。`false` 和 `0` 是已提供的值，是否有效由校验规则决定。`value_aliases` 将展示选项映射为执行参数；`patterns` 是包维护者定义的单捕获组正则，不接受模型生成的正则。`resolution: explicit` 禁止使用模型填写的启动参数作为用户选择，必须来自请求中的可验证表达或已提交答案；主题/写作目标等开放语义字段可由现有主模型依据上下文提取。上传问题的 `attachment.material_id` 等于字段 ID 时，以 Core 的真实输入资源绑定为准；点击“上传”或填写文件名不能满足必需文件校验。

本地别名匹配优先使用完整短语，识别紧邻的否定表达；仅对已声明的开关值互相取反，不从“不要英文”推断“使用中文”。篇幅规则通过不参与捕获的分支排除摘要字数；多个不同的正文数量需要确认，相同数量的不同写法不会重复询问。`validation.max_length: 0` 或省略该字段均表示不限制长度。

每张普通澄清卡包含 1–3 个问题，一个问题对应一个决策维度，说明缺少该信息的影响。开放建议单选最多 3 项、多选最多 4 项，按需附加“其他”并在选中后输入；多选允许只选一项。固定枚举不截断合法取值：不超过 6 项平铺，超过 6 项使用可搜索选择器，列表显示区域最多 6 行。Boolean 支持语义完整的 `boolean_labels`；text 支持示例、长度、整数及数值范围校验。业务专用审批、环境输入和词汇复习不套用普通澄清问题数量限制。

卡片持久化 `ask_id`、字段 `id`、工作流引用、固定 revision 和本次启动标识。Core 根据会话内已有卡片验证提交，不接受浏览器替换启动身份；后续批次复用已提交字段。已明确、可采用安全默认值或不适用的字段不应重复提问。简单且信息完整的普通问答不注册通用 Ask User 工具。

内置工作流的默认策略：PPT 主题/页数缺少时阻塞，无模板时 AI 底图必须明确选择，已有模板不询问底图；Writer 复用现有结构/配图规划，不要求用户填写一张参数表；Image 复用原有数量默认和表情包审阅，不因未要求文案而提问；Academic 的主题/类型/篇幅/正文语言/引用格式无安全默认，导出默认 DOCX；Bid 文件与目标字数必需，默认 DOCX 和默认投标样式，效果图未指定则沿用原有自动数量；Product 复用现有路由、阶段篇幅和样例结构，不用旧版严格参数表覆盖当前执行行为。

### 完整模板

```yaml
id: my-workflow                        # 插件唯一标识，需与目录名一致
name: My Workflow                      # 插件显示名称（用户可见）
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
    required: false                  # external 输入是否在创建 Session 前必填；默认 true

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

`state.yml` 是状态机执行规范。注意：**slot 定义在 `workflow.yaml`，`state.yml` 里只引用 slot id**。

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
    label: Step One               # 步骤显示名称（可与 workflow.yaml 保持一致）
    mode: auto                    # 即将支持。auto（DriverAgent 自动推进）| human（等待用户确认）
    prompt: |
      You are an expert for this task.

      User request: {{user_input}}
      {{runtime_instruction}}

      Your task:
      1. Do something useful.
      2. Save the result:
           save_artifacts(artifacts=[
             {'key': 'my_output', 'content_type': 'text', 'value': <result>}
           ])

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
| `steps[step_id].inputs[].transport` | 否 | `auto`（默认）/ `value` / `path` / `reference`，控制该步骤收到素材时的物理传输形态 |
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

`transport` 与 slot 的逻辑 `type` 相互独立，并且只作用于当前消费步骤：

- `auto` 保持宿主默认行为：`text/json` 传值，公共图片保留引用，二进制图片和 `file` 物化为路径；
- `value` 将标量素材解包为文本或 JSON 值；
- `path` 将素材物化到当前 Attempt 的隔离输入目录并传递本地路径；
- `reference` 保留图片的 durable reference 对象。

同一输入声明了 `alternatives` 时，主素材与所有替代素材使用相同的 `transport`。

---

## 四、scenario.md 格式说明

`scenario.md` 是插件的**使用场景介绍**，描述插件能做什么、适合什么场景、各步骤的作用、以及用户需要了解的注意事项。它在运行时被注入 ChatAgent 的 system prompt，帮助 ChatAgent 理解插件的上下文。

**不要**在 scenario.md 里描述冷启动触发逻辑、`advance_step` 工具调用规则等框架机制——那些由框架自动处理，与具体插件无关。

### 冷启动预检与首次步骤

冷启动时，ChatAgent 初始只看到插件的 `name`、`description` 和 `when_to_use`。
调用 `trigger_<workflow>(request_context)` 后，框架才加载完整插件并执行启动预检；trigger
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

`driver.md` 是 **DriverAgent 的 system prompt**。DriverAgent 仅在 **auto 模式**下生效：每当一个 step 的 SubAgent 执行完成后，Go 调用 `/api/workflow/driver`，将 `driver.md` + 该 step 的 `acceptance_criteria` 作为 system prompt，让 DriverAgent 对执行结果输出 1-2 句自然语言评估。Go 把这段评估作为合成用户消息，触发 ChatAgent 下一轮推理，由 ChatAgent 决定推进、重试还是结束。

### 推荐结构

```markdown
You are the DriverAgent for the <Workflow Name> workflow.
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

**跨 composite 页码联动**

同一组逐页素材的 composite tab 可声明相同的 `page_group`（例如
`page_group: presentation`）。组内共享当前页；拖动页序时，所有已生成的
`cardinality: list`、`ordered: true` 素材使用一次事务同步排序。空槽跳过，
页数不一致或版本冲突则拒绝整个排序。不要把不对应页面的素材库放进这一组。
`sort_order` 是从 1 开始的当前显示位置；`list_index` 是稳定身份，拖动后不能
用“页码减一”代替它。已有下载文件不会随 UI 排序自动重写，需要重新导出。

**声明式 HTML 页面与导出 action**

通用渲染器不会根据 slot id 或素材内容猜测控件类型。完整 HTML 页面必须声明
`ui.slots.<slot>.widgetType: html-slide`。导出能力声明在对应 tab 上：

```yaml
ui:
  slots:
    deck_pages:
      widgetType: html-slide
  tabs:
    - id: deck
      layout: composite
      composite_tab_position: left
      slots: [{id: deck_pages}, {id: speaker_notes}]
      actions:
        - id: export_deck
          type: export
          provider: html-presentation
          inputs: {pages: deck_pages, notes: speaker_notes}
          formats: [raster-pptx, pdf, editable-pptx]
          alignment: sort_order
```

`alignment: sort_order` 表示所有映射的素材必须是 `ordered: true` 的 list slot，
并按相同 `sort_order` 对齐。通用 composite 只负责布局、分页和联动重排；格式选择、
依赖检测、文件命名和转换由 exporter provider 负责。


```yaml
slots:                    # 在 workflow.yaml 中定义
  - id: image_list
    type: image
    cardinality: list
    ordered: true

# SubAgent 调用 save_artifacts(artifacts=[{'key': 'image_list', ...}]) 追加内容
```
