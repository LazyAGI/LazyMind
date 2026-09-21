# LazyMind：面向可控 LLM Agent 工作流的有界控制委托

> 中文论文草稿 v0.1
>
> 本文档用于确定论文论点、方法边界和实验计划。所有尚未获得数据支持的结论均使用“待补”标记，不应在投稿版本中写成既成事实。

## 候选标题

主标题建议：

**LazyMind：面向可控 LLM Agent 工作流的有界控制委托**

英文对应：

**LazyMind: Bounded Delegation for Controllable LLM Agent Workflows**

其他候选：

- **委托而不失控：LLM Agent 工作流中的人机协同决策**
- **谁来驱动工作流？LLM Agent 系统中的有界委托与用户接管**
- **LazyMind：基于场景协议与类型化制品的可控 Agent 工作流**

## 一句话主张

LazyMind 允许用户将常规的工作流验收与推进决策委托给 Driver Agent，同时通过显式状态图、类型化且版本化的 Artifact、可审计的验收契约和强制人工边界，保证委托不会演化为不受约束的自主执行。

## 摘要

大语言模型 Agent 正被用于研究、写作、方案设计和内容生产等长周期知识工作。然而，现有 Agent 系统通常在两种不理想的控制方式之间取舍：完全自主的 Agent 能减少人工操作，却容易偏离流程、传播中间错误或越过用户意图；固定工作流虽然限制了执行顺序，却难以理解自然语言干预，也往往要求用户频繁确认。现有的人在环方法通常提供暂停、批准或恢复能力，但“能够暂停”并不等同于用户在长流程中持续拥有有效控制。

本文提出 LazyMind，一种面向长周期 LLM Agent 任务的可控工作流运行时。LazyMind 将系统分为三个相互约束的层次：Scenario 以自然语言描述任务场景和协作协议，将开放式用户意图映射到工作流允许的交互；可执行状态图通过类型化 Artifact、显式依赖、工具权限和状态转移定义合法执行空间；Driver Agent 则在用户授予的范围内，根据步骤验收契约代理用户评估中间产物并决定继续、重试或升级给用户。用户可以在关键边界检查和编辑 Artifact、撤销自动决策、接管后续步骤或回退到历史版本。工作流运行时根据 Artifact 依赖传播失效状态，使修改和故障只触发必要的下游重算。

我们计划在研究写作、产品方案、技术投标、演示文稿和通用文档生产等多阶段任务上评估 LazyMind，并将其与自主 Agent、固定工作流、普通 checkpoint 工作流以及全人工审批模式比较。评估将同时覆盖最终任务质量、约束违反、错误传播、故障恢复、重复计算、人工干预成本和用户控制感。我们还将比较 Human-only、Driver-only、无判断自动推进和 Human–Driver hybrid 等控制策略。

**待补结果：** 摘要最终一段必须替换为 3–4 个关键量化结果，例如：Hybrid 在任务成功率与 Human-only 无显著下降的情况下减少多少人工审批；相较 checkpoint baseline 降低多少错误传播；局部恢复减少多少重复执行与 token 成本。

## 1 引言

### 1.1 背景

LLM Agent 已经能够搜索资料、调用工具、生成代码和文档，并在多个步骤之间自主规划。随着任务从一次问答扩展到持续数十分钟乃至数小时的知识工作，系统面临的核心问题不再只是模型能否生成一个正确答案，而是任务能否在可理解、可干预、可恢复的过程中可靠完成。

长周期任务中的错误具有累积性。一个不完整的调研结果可能导致错误的大纲，错误大纲又可能传播到正文、图表和最终交付物。用户通常只能在最终结果中发现问题，此时修复往往意味着重新执行整个任务。另一方面，如果系统在每一步都要求用户确认，Agent 带来的自动化收益又会被频繁审批抵消。

因此，实际系统需要解决一个比“自主或人工”更细致的问题：用户应当能够把低风险、标准明确的判断委托给系统，同时保留对关键决策、异常情况和不可逆操作的控制权。本文将这一需求称为**有界控制委托（bounded delegation of control）**。

### 1.2 现有方法的不足

现有 Agent workflow 框架通常已经支持图编排、持久化 checkpoint、interrupt、resume 和 human-in-the-loop。这些机制解决了“在哪里暂停”和“如何恢复”，但仍留下四个问题：

1. **控制边界不明确。** Agent 或自动评审器能够做出哪些决定，常由 prompt 隐式描述，缺少运行时可执行的授权边界。
2. **中间状态缺少稳定语义。** 许多系统主要持久化消息和通用图状态，用户难以直接检查、编辑和比较任务产物。
3. **自然语言干预与执行图脱节。** 用户可以通过聊天表达“采用第二个方案”或“重做上一阶段”，但系统难以判断该请求对应哪个合法状态转换。
4. **修改后的失效范围不清楚。** 当用户修改中间结果或回退历史版本时，系统可能全部重跑，也可能错误复用已经不再有效的下游结果。

**待补相关工作证据：** 系统性检查 LangGraph、AutoGen/AG2、AgentScope、CrewAI、Dify 等公开实现和论文，制作功能与语义对比表。特别区分“支持某项功能”和“为该功能提供显式语义/实验验证”，避免不准确的新颖性声明。

### 1.3 本文方法

LazyMind 使用三层结构解决上述问题：

- **Scenario 协作协议层**连接开放式对话与结构化执行，描述工作流适用条件、任务阶段、可接受的用户意图和升级规则；
- **Artifact-centric 状态图层**使用类型化输入输出、单生产者依赖、显式路由和工具能力定义合法执行空间；
- **Driver 委托决策层**依据显式 acceptance criteria 评估步骤结果，只能在状态图和用户授权允许的范围内推进，并在不确定或高风险时升级给用户。

与自由行动的 critic/reviewer Agent 不同，Driver 不是另一个拥有完整执行权限的自治 Agent。它是一个被工作流约束的代理监督者：可观察对象、可做出的决定、可选择的后继状态和必须升级的边界均由工作流定义。

### 1.4 贡献

本文计划做出以下贡献：

1. 提出**有界控制委托**，将 Agent workflow 的用户控制定义为可授权、可审计、可撤销和可接管的运行时能力，而不只是暂停按钮。
2. 提出由 Scenario、Artifact-centric 状态图和 Driver Agent 构成的三层架构，在保留自然语言交互灵活性的同时限制合法执行空间。
3. 设计基于类型化、版本化 Artifact 的局部失效与恢复机制，使用户修改、失败重试和历史回退仅重算受影响的下游步骤。
4. 构建面向长周期知识工作的 controllability benchmark，从最终质量、约束遵循、错误传播、恢复效率和人工负担等多个维度评估 Agent workflow。
5. 在多个真实工作流和多种基础模型上验证 Human–Driver hybrid 是否能在保持质量的同时显著降低人工审批成本。

**待补证据：** 如果实验没有覆盖新 benchmark 或没有开放数据，贡献 4 应改成“提出评测协议”，不能声称构建 benchmark。

### 待补图 1：论文首页总览图

建议制作一张横向四栏架构图：

1. 左侧是用户请求和 Scenario，展示自然语言意图“生成报告；关键结论由我确认”；
2. 中间是状态图，节点之间传递不同颜色和类型的 Artifact；
3. 状态图上方是 Driver，对普通节点执行 accept/retry/escalate；用户位于关键 approval gate；
4. 右侧展示用户修改某个中间 Artifact 后，受影响下游节点变成 stale 并局部重算，其他分支保持有效。

该图必须一眼展示论文的三个关键词：**delegation、bounded control、localized recovery**，不建议使用普通产品架构截图代替。

## 2 问题定义与设计目标

### 2.1 Workflow 模型

将一个工作流定义为：

\[
W=(S, A, E, C, P, R)
\]

其中：

- \(S\) 为步骤集合；
- \(A\) 为 Artifact slot 集合；
- \(E\) 为步骤间控制边；
- \(C\) 为步骤输入、输出和验收契约；
- \(P\) 为每个步骤允许使用的模型、工具和操作权限；
- \(R\) 为控制责任分配，将步骤映射为 human、driver 或 hybrid。

每个 Artifact 至少包含逻辑类型、cardinality、版本、producer、有效性和 provenance。非外部 Artifact 由唯一步骤生产；消费步骤只有在 required input 表达式满足时才可进入 ready 状态。

### 2.2 控制事件

控制事件包括：

- `accept(s, v)`：接受步骤 \(s\) 产生的 Artifact 版本 \(v\)；
- `retry(s, h)`：以可选修复提示 \(h\) 重试步骤；
- `edit(a, v')`：用户创建 Artifact \(a\) 的新版本；
- `rewind(s)`：回退到步骤 \(s\) 的有效边界；
- `route(s, t)`：从候选后继中选择 \(t\)；
- `escalate(s, q)`：Driver 将决定升级给用户，并给出问题或证据缺口；
- `takeover(k)`：用户撤销从边界 \(k\) 开始的 Driver 授权。

### 2.3 可控性属性

本文不将 controllability 当作主观口号，而将其拆成以下可测属性：

1. **授权安全性（authorization safety）**：任何控制事件都由被授权的主体产生；Driver 不得跨过 human-only 边界。
2. **契约可执行性（contract enforceability）**：缺失 required input、required output 或未满足结构检查时，步骤不能被错误推进。
3. **干预局部性（intervention locality）**：编辑或回退只使因果依赖于目标版本的结果失效。
4. **可恢复性（recoverability）**：故障后系统能够复用仍然有效的 Artifact 和成功步骤。
5. **决策可审计性（decision auditability）**：每次推进、重试和升级都能关联到观察到的 Artifact、验收标准及控制主体。
6. **可接管性（takeoverability）**：用户能够撤销委托并从稳定边界继续任务。

### 2.4 设计目标与非目标

LazyMind 的目标是在长周期、Artifact 密集的知识任务中减少错误传播和不必要的人工审批。它不保证 Driver 判断永远正确，也不试图解决所有 Agent 安全或模型对齐问题。系统提供的是可执行的控制边界和失败后的恢复能力，而非对模型内部行为的完全控制。

**待补形式化内容：**

- 给出 execution fact、projection、Artifact version 和 stale dependency 的严格定义；
- 给出至少两个 invariant，并说明编译期或运行时如何保证；
- 最好给出局部失效算法及复杂度；
- 用一个反例说明仅有 checkpoint 为什么不能保证 Artifact 级局部有效性。

## 3 系统设计

### 3.1 系统总览

LazyMind 将工作流定义编译成不可变执行图，并将运行期间发生的 attempt、Artifact revision、route decision 和 approval 记录为持久化事实。当前状态由编译图和这些事实投影得到，而不是由 Agent 自报“已经完成到哪一步”。

一次典型执行如下：

1. ChatAgent 根据用户请求判断是否应进入某个 Workflow；
2. Scenario 被加载，提供特定任务的协作协议；
3. 工作流运行时投影当前可达和 ready 的步骤；
4. SubAgent 在步骤限定的上下文、输入和工具权限内执行；
5. 生成结果被保存为声明过的 Artifact；
6. human 或 Driver 根据步骤模式评估结果；
7. 运行时冻结路由决定并激活合法后继；
8. 用户可在后续任意稳定边界编辑、重试或回退。

### 待补图 2：运行时组件与信任边界图

图中区分：

- 不可信/概率性组件：ChatAgent、SubAgent、Driver；
- 确定性控制组件：compiler、graph projector、authorization checker、Artifact store；
- 外部工具和数据源；
- 用户。

用实线表示确定性验证，用虚线表示 LLM 建议。重点展示“LLM 提出决策，运行时验证决策是否合法”。

### 3.2 Scenario：自然语言协作协议

Scenario 描述工作流的适用任务、各步骤的业务含义、用户可能表达的干预意图，以及何时应请求额外信息。它不会直接决定某个节点是否合法；它帮助 ChatAgent 把自然语言表达映射为运行时能够验证的控制动作。

例如，用户说“背景图不合适，换成更克制的风格，但不要重做大纲”。Scenario 可以帮助 ChatAgent 将请求解析为：编辑背景提示词或回退到背景生成步骤，而不是重新启动整个演示文稿工作流。运行时随后检查目标步骤是否可回退、哪些 Artifact 版本需要失效，以及哪些下游步骤必须重算。

Scenario 的关键作用是把开放式自然语言交互限制在一个特定任务协议中。它与状态图形成“语义解释—合法性验证”的分工：Scenario 解释用户想做什么，状态图决定该动作现在是否允许。

**待补方法增强：** 当前 Scenario 如果主要是自由文本，投稿前建议定义一个最小结构化协议，例如 supported intents、required information、escalation conditions、protected stages。即便仍以 Markdown 表达，也应能静态抽取或验证这些字段。

**待补实验：** 比较无 Scenario、通用系统 prompt、完整 Scenario 三种条件下的意图识别准确率、错误 Workflow 触发率、非法回退请求率和多轮干预成功率。

### 待补图 3：Scenario 将自然语言映射到控制事件

展示 4–6 个典型用户表达，例如“继续”“只重做第三页”“使用第二版大纲”“剩下的你决定”“下一步必须让我确认”，经过 Scenario-aware ChatAgent 后映射为 accept、retry、edit、rewind、delegate 或 takeover，再由 graph runtime 验证。

### 3.3 Artifact-centric 状态图

LazyMind Workflow 将中间结果视为一等 Artifact，而不是临时消息。每个 slot 声明类型、单值或列表 cardinality、是否为外部输入，以及 UI 和传输属性。步骤显式声明 required/optional inputs 和 required outputs。

该设计带来三个作用：

1. **就绪性判断**不依赖 Agent 的语言声明，而依赖 Artifact 是否存在并满足输入表达式；
2. **中间结果可干预**，用户能够直接检查、编辑和比较真实产物；
3. **依赖可追踪**，运行时能够判断修改影响哪些下游节点。

编译器检查重复 slot、未知引用、步骤不一致、非法路由、无生产者、多生产者、producer/consumer 控制关系和 UI 引用等问题。编译后的图包含控制边、输入表达式、material producer、类型、cardinality、步骤模式和验收条件。

**待补证据：** 整理编译器实际实现的全部诊断码，按 type、dependency、control、permission、UI/runtime contract 分类；报告内置工作流和自动生成工作流中静态检查发现过多少真实问题。

### 待补图 4：Artifact 数据流与控制流双图

使用一个 5–7 步示例，同时画出：

- 上半部分的控制边；
- 下半部分的 Artifact producer–consumer 边；
- human/driver 节点；
- 修改一个 Artifact 后的 stale 传播范围。

图应明确说明控制可达不等于输入 ready。

### 3.4 Driver：受限的代理监督者

Driver 在自动模式下评估步骤输出是否达到 acceptance criteria，并返回有限集合中的决定，例如 accept、retry 或 escalate。Driver 可以读取当前步骤、声明的输入输出、Artifact 内容、验收条件和必要的执行证据，但不能任意选择图外步骤、增加未授权工具或越过 human-only gate。

这与一般 critic Agent 有两个区别。第一，Driver 的权限由运行时限定，而不是依赖 prompt 自律；第二，Driver 的决定影响控制流，因此必须被持久化和审计。每个 Driver decision 应至少记录：被评估的 Artifact 版本、使用的 acceptance criteria、结论、理由、置信度或不确定性、最终激活的控制边。

对于主观偏好、高风险操作、不可逆外部动作和 Driver 无法获得足够证据的情况，工作流应要求 human decision 或触发 escalate。用户还可以在会话级或步骤级撤销自动批准偏好。

**待补实现或澄清：**

- Driver 输出是否已有严格 schema；
- Driver 决策是否合法由哪一层验证；
- 是否支持显式 escalate 和 uncertainty；
- 是否区分“内容质量未达标”和“缺少证据无法判断”；
- 同一模型执行与评审时如何避免相关错误；
- high-risk/human-only 边界是否能由运行时强制。

如果这些能力尚未实现，应在投稿前补齐其中至少前三项。

### 3.5 Human–Driver 混合控制

LazyMind 支持在不同步骤分配不同的控制主体：

- `human`：完成后必须等待用户确认；
- `driver` 或当前系统中的自动模式：由 Driver 按标准判断；
- `hybrid`：Driver 处理明确通过或明确失败的结果，将模糊情况升级给用户；
- `takeover`：用户从指定边界撤销委托。

这种设计允许用户表达“常规步骤自动继续，但大纲、最终版本和所有外部发布必须由我确认”。控制策略因此是工作流的一部分，而不是每次运行中的临时约定。

**待补设计决定：** 如果当前实现只有 `auto` 和 `human`，论文中不要提前声称原生支持 `hybrid`。可以将 hybrid 定义为“Driver escalate + human step”的组合语义，或在实现中增加显式模式。

### 待补图 5：控制权状态机

画一个小型状态机：User-controlled → Delegated → Driver evaluating → Accepted/Retry/Escalated → User takeover。标注授权、撤销、强制审批和异常升级事件。

### 3.6 Retry、Rewind 与局部失效

当某一步失败时，Retry 创建新的 attempt，并复用仍有效的上游 Artifact。当用户编辑一个 Artifact 或 rewind 到更早步骤时，运行时将依赖旧版本的相关 attempt 和 route fact 标记为 stale，然后从新的事实集合重新投影状态。与重启整个工作流不同，这一机制保留不受影响的分支和已验证结果。

局部失效机制需要满足：

- 不复用依赖已失效输入的结果；
- 不重算与修改无依赖关系的结果；
- 路由选择依赖的 Artifact 改变后，旧 route fact 同样失效；
- Artifact 历史版本继续可审计，但不被当作当前有效输入。

**待补算法：** 给出 invalidation frontier 的伪代码。输入是被替换的 Artifact version 或 rewind step，输出是 stale attempts、stale route facts 和重新变为 reachable/ready 的节点集合。

### 待补图 6：局部恢复示例

使用包含并行分支的 DAG。第一次执行中两个分支均完成；用户修改左分支的中间 Artifact 后，只把左分支后继和汇合节点标为 stale，右分支保持有效。用数字标明全量重跑与局部重算的步骤数、token 和时间差异。

### 3.7 工作流生命周期与可复现性

Workflow 定义、脚本和配置通过 revision 发布。运行中的 session 固定到特定 revision，避免工作流更新改变正在执行的任务语义。Artifact revision、attempt 和 decision trace 共同构成一次运行的审计记录。

**待补证据：** 验证运行中升级 Workflow revision 不会改变已绑定 session；验证历史任务可以重建当时的投影状态；说明模型版本、工具版本和外部网页变化会给完全复现带来什么限制。

## 4 实现

LazyMind 由 Workflow compiler、graph projector、session/attempt service、Artifact store、ChatAgent、SubAgent、Driver Agent 和前端工作台组成。工作流使用 `workflow.yaml` 声明注册信息和 Artifact slots，使用 `state.yml` 声明步骤、转移、输入输出和验收标准，并通过 `scenario.md` 与 `driver.md` 描述任务级协作和评审协议。自定义工具可以作为受限 capability 注册给特定步骤。

运行时将编译图视为不可变结构，将 attempts、materials 和 routes 视为持久化事实，并通过纯投影计算节点的 reachability、readiness、execution、validity 和 branch 状态。概率性 Agent 不直接写入最终投影状态，而是通过有限工具提交 Artifact 或控制请求。

**待补实现数据：**

- 后端语言和关键模块代码量；
- Workflow compiler 的诊断规则数量；
- 当前内置 Workflow 数量、节点数、Artifact 数和最长路径；
- 支持的 Artifact 类型和存储后端；
- session/attempt/Artifact/route 的数据库结构；
- Driver 和 SubAgent 默认使用的模型及 prompt 长度；
- Desktop 与服务端部署差异。

### 待补表 1：内置 Workflow 统计

每行一个真实 Workflow，列出：领域、步骤数、控制边数、Artifact slots、human gates、Driver gates、工具数、最长路径、是否有条件分支、是否支持 checkpoint resume。该表证明系统不是只在玩具图上实现。

## 5 评测

### 5.1 研究问题

- **RQ1：** 显式 Artifact contract 是否减少步骤遗漏、非法推进和错误传播？
- **RQ2：** Driver 委托能否在保持最终质量的同时降低人工审批负担？
- **RQ3：** Scenario 是否提高多轮自然语言干预和路由的准确性？
- **RQ4：** 局部失效与恢复是否比全量重启和普通 checkpoint 更高效且不复用陈旧结果？
- **RQ5：** 各机制的收益能否跨 Workflow、任务复杂度和基础模型保持？

### 5.2 Benchmark 与任务

建议构建五类任务：

1. 学术研究：检索、证据综合、大纲、草稿、审稿和修改；
2. 产品方案：市场证据、产品方向、PRD、设计和交接；
3. 技术投标：需求解析、合规矩阵、技术方案、审查和导出；
4. PPT：材料搜集、大纲、逐页内容、背景图和导出；
5. 通用长文或数据分析：用于验证结论不局限于已有复杂模板。

每类建议至少 50 个任务，总量 250–500。每个任务包括用户请求、必要输入、关键中间验收标准、最终评分 rubric 和至少一个可注入故障点。

如果构建成本过高，第一版可以使用 3 类 × 40 个任务，但每个任务应运行多个随机种子并覆盖多个模型。

**待补数据构建证据：** 说明任务来源、版权、去重、难度分层、专家标注过程和 inter-annotator agreement。不能只由同一个 LLM 生成任务和评判结果。

### 5.3 Baseline

在相同模型、工具、输入资料和最大预算下比较：

- **Autonomous Agent**：单 Agent 自主规划和执行；
- **Planner–Executor**：先生成计划，再逐步执行；
- **Static Chain/DAG**：固定执行顺序，结构检查通过即推进；
- **Checkpoint Workflow**：支持 pause/resume 和人工批准，但没有 Artifact dependency invalidation；
- **Human-only LazyMind**：每个关键步骤都由人判断；
- **Driver-only LazyMind**：除硬性 human gate 外由 Driver 判断；
- **Hybrid LazyMind**：Driver 处理常规决策，异常和不确定情况升级给用户；
- **LazyMind Full**：按实际 workflow policy 混合控制。

如资源允许，应实现一个基于 LangGraph 的强 baseline。重点比较语义，而不是故意使用功能不足的简化版本。

### 5.4 指标

#### 最终效果

- Task Success Rate；
- 专家盲评质量；
- Artifact completeness；
- 事实、引用或需求符合率；
- workflow-specific deterministic checks。

#### 控制与可靠性

- Constraint Violation Rate；
- Unauthorized Transition Rate；
- Stale Artifact Leakage Rate；
- Error Propagation Depth；
- Failure Recovery Rate；
- Intervention Success Rate；
- Driver false-accept 与 false-reject；
- Escalation precision、recall 和 coverage。

#### 人工与系统成本

- 人工审批次数；
- 人工有效操作时间；
- 从发现问题到修复的时间；
- token、模型调用、工具调用；
- wall-clock latency；
- 重复执行步骤数；
- Artifact 存储和状态投影开销。

### 5.5 实验一：端到端质量与约束遵循

在所有 benchmark 任务上运行主要 baseline，比较最终成功率、Artifact 完整性和约束违反。每个模型—方法—任务至少运行 3 次；温度和预算保持一致。

**期望展示的结果：** LazyMind Full 应在最终质量上优于 autonomous/static baseline，并显著降低缺失步骤、缺失 Artifact 和非法提前完成。即使最终质量提升不大，约束违反和失败可诊断性也应有明显改善。

### 待补表 2：端到端主结果

行为方法，列为任务成功率、约束违反、Artifact 完整性、专家评分、token 和延迟。分领域报告，并给出 macro average、置信区间和显著性检验。

### 5.6 实验二：Human–Driver 控制策略

选择 60–100 个包含主观与客观验收节点的任务。由领域专家给出每个中间结果的 accept/retry/escalate 标注，比较 Human-only、Driver-only、Static auto-advance 和 Hybrid。

重点观察 Driver 能否承担明确、重复的验收，同时在不确定情况将决定交回用户。

**期望展示的结果：** Hybrid 的最终质量接近 Human-only，但人工确认数量和有效耗时明显下降；相比 Driver-only，Hybrid 显著降低错误放行；相比 Static automation，Driver 能阻止更多不合格中间结果传播。

### 待补图 7：质量—人工成本 Pareto 曲线

横轴为每任务人工分钟数或审批次数，纵轴为成功率/专家质量。每个控制策略是一个点，Hybrid 应位于较优 Pareto frontier。还可以通过调整 Driver escalation threshold 绘制一条曲线。

### 待补图 8：Driver 决策混淆矩阵

以专家决定为 gold label，展示 accept/retry/escalate 混淆矩阵；按客观验收、主观质量、高风险节点分别报告。必须特别报告 false accept，因为它最容易造成错误传播。

### 5.7 实验三：Scenario 对多轮干预的作用

为每个 Workflow 构造自然语言干预集，包括同义表达、隐式指代、局部修改、回退、版本选择、改变自动化偏好和非法请求。比较无 Scenario、通用 Workflow 描述和完整 Scenario。

指标包括 intent accuracy、目标步骤准确率、合法控制事件准确率、错误 Workflow 触发率以及完成干预所需对话轮数。

**期望展示的结果：** Scenario 主要提升歧义表达和多轮上下文中的映射准确性，并减少 Agent 提议图外操作。如果只提升触发准确率而不提升控制事件映射，应缩小论文中对 Scenario 的主张。

### 待补表 3：Scenario 消融结果

按 intervention type 分组，报告 intent、target step、action 和 end-to-end intervention success。

### 5.8 实验四：错误注入与局部恢复

在不同阶段注入以下故障：

- 模型未生成 required output；
- Artifact 格式错误；
- 工具超时或进程中断；
- Driver 错误接受不合格结果；
- 用户在下游完成后修改上游 Artifact；
- 路由条件所依赖的 Artifact 被替换；
- 并行分支中的一个分支失败。

比较全量重启、step checkpoint resume 和 Artifact-aware localized recovery。除效率外，必须检查恢复后的最终结果是否混入旧版本内容。

**期望展示的结果：** LazyMind 应具有接近零的 stale leakage，并随修改位置靠近末端而显著减少重算；普通 checkpoint 虽能从节点恢复，但在上游 Artifact 被编辑时更容易全部重跑或错误复用下游状态。

### 待补图 9：修改位置与恢复成本

横轴为修改发生在归一化工作流进度的百分位，纵轴分别为重算节点、token、时间。三条曲线对应 full restart、checkpoint 和 LazyMind localized recovery。

### 待补表 4：故障恢复正确性

报告 recovery success、stale leakage、重复步骤、恢复耗时和额外 token。按故障类型分组。

### 5.9 实验五：消融

从 LazyMind Full 分别移除：

- Scenario；
- typed Artifact validation；
- single-producer/ancestor constraints；
- acceptance criteria；
- Driver；
- human gates；
- Artifact revision；
- stale propagation；
- deterministic post-step checks。

**期望展示的结果：** 不同机制应对应不同收益：Scenario 影响干预解释；Artifact contracts 影响完整性和约束；Driver 影响自动化与质量平衡；stale propagation 影响恢复正确性和成本。

### 待补图 10：消融热力图

行为被移除的组件，列为 success、violation、stale leakage、human cost 和 token cost。用颜色展示相对 full system 的变化。

### 5.10 实验六：扩展性和系统开销

使用合成 DAG 改变节点数、边数、Artifact 数、并行度和历史 attempt 数，测量 compile、projection、invalidation 和恢复耗时。真实工作流用于验证合成测试的代表性。

**期望展示的结果：** 在论文目标规模内，确定性控制开销应显著小于 LLM 调用时间；随着历史事实增加，投影延迟仍保持可接受。

### 待补图 11：系统扩展性

四个子图分别展示节点数/历史事实数对编译、投影、失效计算和数据库读取延迟的影响，报告 p50/p95。

### 5.11 用户研究

招募 24–40 名参与者，让其在 Chat-only intervention、Checkpoint UI 和 LazyMind Artifact workspace 三种条件下完成发现并修复中间错误的任务。至少部分任务应由真实领域用户完成。

测量：

- 错误发现率；
- 修复成功率；
- 完成时间；
- 无效操作数；
- 对当前执行状态和修改影响范围的理解；
- NASA-TLX；
- perceived control、trust calibration，而非笼统满意度。

**期望展示的结果：** Artifact workspace 和显式状态应帮助用户更快找到错误、预测修改影响并完成局部修复。若只有 perceived control 提升而客观表现不变，需要诚实报告。

### 待补图 12：用户研究结果

主图展示错误发现率、修复时间和状态理解准确率；主观量表放在次图或附录。另补一张脱敏后的真实交互时间线，展示用户查看 diff、编辑 Artifact、接管并恢复执行。

## 6 预期结果的正确表述方式

在实验完成前不要预写具体提升百分比。结果出来后，建议按以下逻辑组织：

1. Full system 是否提高最终成功率；
2. 即便成功率接近，是否显著降低约束违反和错误传播；
3. Hybrid 是否提供更好的质量—人工成本 Pareto trade-off；
4. 局部恢复是否既节省计算又保证不泄漏 stale state；
5. Scenario 和 Driver 分别在哪些条件下有效、在哪些条件下失效。

一个理想但必须由数据支持的结果叙述示例：

> 在五类共 N 个长周期任务中，LazyMind 将约束违反率从 X% 降低到 Y%，并将上游修改后的重复执行成本降低 Z%。Hybrid 控制在与 Human-only 的最终质量无显著差异的情况下减少 H% 的人工审批时间。Driver 在客观验收节点上与专家判断达到 A 一致率，但在主观节点上 false-accept 明显更高，说明强制人工边界仍然必要。

最后一句很重要：论文不仅要报告系统成功，也要通过失败边界证明“bounded”的必要性。

## 7 相关工作

### 7.1 Agent orchestration 与 workflow runtime

讨论 LangGraph、AutoGen/AG2、AgentScope、CrewAI、Dify 等。比较图编排、checkpoint、human-in-the-loop、状态持久化和多 Agent 协作。本文不声称首创这些单项功能，而强调显式控制委托、Artifact 依赖语义和用户修改后的局部有效性。

### 7.2 Human–Agent collaboration

讨论 mixed-initiative interaction、adjustable autonomy、human-on-the-loop、approval gate 和 human takeover。将 LazyMind 定位为把 adjustable autonomy 落实到每个工作流步骤和 Artifact 边界。

### 7.3 LLM-as-a-Judge 与 critic Agent

讨论自动评审、自反思和 reviewer Agent。Driver 与它们的区别是：评审结果直接参与工作流控制，因此受显式权限、验收契约、合法边和升级机制约束。

### 7.4 Provenance、可恢复执行与数据流系统

联系 workflow provenance、build system incremental recomputation、dataflow、event sourcing 和数据库 materialized view。Artifact stale propagation 与增量构建存在相似性，应主动承认并说明 LLM Agent 场景中的新挑战：概率性步骤、人工编辑、自然语言路由和代理监督。

**待补证据：** 相关工作不能只覆盖 Agent 论文。需要补充至少一组来自 workflow/dataflow/build systems 和一组来自 adjustable autonomy/HCI 的经典文献，否则理论定位会显得狭窄。

### 待补表 5：相关系统能力与语义对比

列建议包括：structured workflow、natural-language intervention、typed artifacts、single-producer check、human editable intermediate output、delegated reviewer、enforced authority boundary、localized invalidation、decision audit、workflow revision pinning。每一格必须有公开文档或实验证据，不要凭印象填写。

## 8 讨论

### 8.1 Driver 不能等同于用户

Driver 能够减少常规审批，但无法完整代理用户的隐性偏好、责任判断和风险容忍度。当执行 Agent 与 Driver 使用同源模型时，两者还可能产生相关错误。因此 LazyMind 的目标不是消灭人工，而是将人工注意力集中到高风险、主观和不确定节点。

### 8.2 什么样的验收标准适合自动委托

结构完整性、格式、引用存在性和确定性测试更适合 Driver 或程序检查；创新性、商业判断、伦理风险和最终发布通常需要人工。未来可研究如何自动判断 acceptance criteria 的可判定性并推荐控制模式。

### 8.3 控制与自动化的代价

显式 Artifact、版本、验收和决策日志会增加工作流设计成本与存储开销。过多 approval gate 也会造成自动化疲劳。论文应报告在哪些任务规模下收益超过配置成本。

### 8.4 从 Skill 到 Workflow

将自然语言 Skill 编译成可验证 Workflow 是自然延伸，但不建议成为本文的第二条主线。本文可以将其作为工作流获取方式或未来工作；单独的编译方法、数据集和语义保持实验更适合后续论文。

## 9 局限性

- Driver 的判断依赖基础模型，不能保证语义正确；
- acceptance criteria 可能含糊、冲突或不可自动验证；
- 外部网页和工具状态变化限制完全重放；
- 当前 Workflow 主要集中在知识工作和内容生产，未必推广到机器人或实时控制；
- Artifact dependency 只能捕获声明过的依赖，隐藏在 prompt、外部状态或工具副作用中的依赖可能遗漏；
- 人工批准不天然意味着安全，用户可能快速点击继续或缺乏领域知识；
- 更多控制机制可能增加作者配置负担；
- 本文评估的是系统层可控性，而不是模型内部可解释性或对齐。

## 10 伦理、安全与可复现性

LazyMind 应避免用 Driver 自动批准医疗、法律、金融交易、公开发布、删除数据等高风险或不可逆动作。benchmark 中使用的用户资料和企业文档需要脱敏并取得授权。用户研究应经过适用的伦理审查并明确告知参与者哪些决定由模型生成。

为提高可复现性，投稿材料应包含：

- Workflow definitions 和 compiler；
- benchmark requests、输入资料和评分 rubric；
- baseline 配置；
- 模型、温度、token budget 和重试策略；
- 原始 execution trace 的脱敏版本；
- 自动评分脚本；
- 人工标注指南；
- 每个结果的随机种子和置信区间。

## 11 结论

本文提出 LazyMind，一个支持有界控制委托的 LLM Agent 工作流运行时。LazyMind 不要求用户在完全自主和逐步审批之间二选一，而是通过 Scenario 协作协议、Artifact-centric 状态图和受限 Driver Agent，使控制权能够按步骤委托、在异常时升级并由用户随时接管。类型化、版本化 Artifact 和显式依赖进一步使用户修改与故障恢复具有局部、可审计的语义。

**待补最终结论：** 用真实实验数字回答两个问题：委托节省了多少人力；系统约束避免了多少错误传播。不要在结论中加入实验未验证的“安全”“可信”或“保证正确”等宽泛主张。

## 附录 A：建议优先完成的最小投稿实验包

如果资源有限，优先完成以下组合：

1. 三个真实 Workflow，每个 40 个任务；
2. 三个基础模型，每项三次运行；
3. Autonomous、Checkpoint、Human-only、Driver-only、Hybrid 五组；
4. 端到端质量与约束违反主实验；
5. 6 类错误注入与局部恢复实验；
6. Driver 专家标注一致性和质量—人工成本曲线；
7. Scenario 消融；
8. 24 人以内的受控用户实验；
9. 编译器规则、真实工作流规模和系统开销统计。

这套实验足以支撑一篇完整系统论文。若缺少人类实验，应避免把“用户控制感”作为核心结论；若缺少强 baseline，应优先投系统展示或 industry track，而不是直接宣称通用方法优越。

## 附录 B：投稿前实现与论文一致性检查

- [ ] `Scenario` 是否有明确、可复现的输入输出或协议定义；
- [ ] `Driver` 是否使用结构化决策 schema；
- [ ] Driver 的 route/advance 是否经过确定性权限验证；
- [ ] 是否存在显式 `escalate`；
- [ ] human-only gate 是否不能被 Driver 绕过；
- [ ] Artifact version 与 attempt 输入是否能够建立精确依赖；
- [ ] 修改 Artifact 后是否会失效 route fact；
- [ ] retry 与 rewind 的语义是否有测试覆盖；
- [ ] Workflow session 是否固定到 revision；
- [ ] 文档中 `auto`/`human`/`dynamic` 等术语是否与实现一致；
- [ ] README、格式规范、数据库状态与论文描述是否一致；
- [ ] 所有主张是否能映射到代码、实验或形式化定义中的至少一种证据。
