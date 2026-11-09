# 分阶段工作流与人工介入对论文写作 Agent 的影响：证据一致性、修订可靠性与质量—成本权衡

> 仿真代码已实际运行；结果来自所述仿真假设，不是现实实测数据。


<a id="block-sec-001"></a>
## 引言
<!-- writer:outline {"node_id":"sec-001","outline_description":"从自动化科学写作、分阶段取证与人在回路机制的背景出发，提出核心研究问题、现有证据缺口及本文的研究目标（约6字）。","target_chars":6,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-001-001"></a>
### 研究背景与问题提出
<!-- writer:outline {"node_id":"sec-001-001","outline_description":"说明论文写作 Agent 已从一次性生成转向跨阶段协作，但结构改善不能替代主张级证据核验，由此引出分阶段与人工介入的联合评估需求（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-001-002"></a>
### 研究问题与研究目标
<!-- writer:outline {"node_id":"sec-001-002","outline_description":"明确在固定模型、论文任务和总预算下识别分阶段工作流、风险触发人工及其交互效应的研究问题，并说明质量与成本双重目标（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-001-003"></a>
### 研究假设与贡献
<!-- writer:outline {"node_id":"sec-001-003","outline_description":"陈述阶段结构、人工门控及交互与边际收益假设，并逐项给出不支持条件，避免把待验证机制表述为既成结论（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-002"></a>
## 文献综述
<!-- writer:outline {"node_id":"sec-002","outline_description":"综合分阶段生成、自动审查与修订、人工介入和评价方法的相关研究，归纳质量收益来源、成本结构、冲突证据及研究空白（约12字）。","target_chars":12,"context_relations":[{"target_node_id":"sec-001","relation":"continuity","guidance":"承接前一节已建立的信息和叙事进展。"}],"subtasks":[]} -->


<a id="block-sec-002-001"></a>
### 分阶段工作流及其质量收益来源
<!-- writer:outline {"node_id":"sec-002-001","outline_description":"比较 STORM、AutoSurvey2、STIG、OUTLINEFORGE 与 The AI Scientist 的阶段设计，解释结构、覆盖和连贯性收益分别来自哪些模块（约3字）。","target_chars":3,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-002-002"></a>
### 自动审查、反馈修订与可靠性风险
<!-- writer:outline {"node_id":"sec-002-002","outline_description":"评述多代理审查对幻觉修复的潜力与错误评审造成的退化，说明独立证据核验、全局记忆和异构评审的重要性（约3字）。","target_chars":3,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-002-003"></a>
### 人工介入的时机、方式与资源分配
<!-- writer:outline {"node_id":"sec-002-003","outline_description":"依据 IDRBench、CoAuthorAI、When Should Humans Step In? 及人机协作评价研究，讨论早期门控、风险触发、介入预算与人工负担（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-002-004"></a>
### 证据一致性、引用核验与修订轨迹
<!-- writer:outline {"node_id":"sec-002-004","outline_description":"梳理主张级引用核验、文档不一致检测和真实科学修订轨迹的数据与方法基础，说明为何应同时评估证据支持与修订回归（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-002-005"></a>
### 研究空白与证据边界
<!-- writer:outline {"node_id":"sec-002-005","outline_description":"归纳四级消融、交互估计、统一成本核算和真实修订评价方面的研究空白，并交代当前材料存在摘要覆盖、截断和外部效度限制（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-003"></a>
## 研究设计
<!-- writer:outline {"node_id":"sec-003","outline_description":"给出拟开展的配对任务消融、模拟人工流程、质量与成本指标、评审器扰动检查及统计分析方案，明确哪些环节已经执行、哪些仍为计划（约12字）。","target_chars":12,"context_relations":[{"target_node_id":"sec-002","relation":"continuity","guidance":"承接前一节已建立的信息和叙事进展。"}],"subtasks":[]} -->


<a id="block-sec-003-001"></a>
### 研究对象与实验臂
<!-- writer:outline {"node_id":"sec-003-001","outline_description":"界定具备选题—检索—证据提取—大纲—写作—审查—修订阶段的论文写作 Agent，并定义一次性生成、仅分阶段、仅风险触发人工和联合条件（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-003-002"></a>
### 任务、模拟人工与控制变量
<!-- writer:outline {"node_id":"sec-003-002","outline_description":"说明综述型、方法型和实证型论文的配对抽样方式，以及模拟专家的评分量表、噪声模型、采纳决定和一致性报告方案（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-003-003"></a>
### 质量与修订可靠性评价
<!-- writer:outline {"node_id":"sec-003-003","outline_description":"定义引文存在性、语义支持、来源相关性、跨章节矛盾、结构覆盖、严重问题闭环、回归错误和运行方差等指标及核验流程（约3字）。","target_chars":3,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-003-004"></a>
### 成本分解与收益—成本指标
<!-- writer:outline {"node_id":"sec-003-004","outline_description":"规划逐阶段记录模型调用、检索、重试、延迟、人工分钟、返工与失败风险，并以质量增益/增量成本和质量/总成本比较方案（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-003-005"></a>
### 统计分析与验证条件
<!-- writer:outline {"node_id":"sec-003-005","outline_description":"说明配对检验、bootstrap 区间、混合效应模型和二因子交互估计如何区分主效应、交互与论文类型异质性，并设置异构评审与扰动稳定性检查（约3字）。","target_chars":3,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-004"></a>
## 模拟分析结果
<!-- writer:outline {"node_id":"sec-004","outline_description":"报告已执行参数化模拟的四级质量结果、相对基线效应、探索性交互、成本增量与单位资源收益，并明确这些结果不是现实系统实测（约12字）。","target_chars":12,"context_relations":[{"target_node_id":"sec-003","relation":"continuity","guidance":"承接前一节已建立的信息和叙事进展。"}],"subtasks":[]} -->


<a id="block-sec-004-001"></a>
### 四级工作臂的质量表现
<!-- writer:outline {"node_id":"sec-004-001","outline_description":"结合各臂三次独立模拟重复的均值和可用自助区间，描述主张支持率、修订回归率与严重问题闭环率的方向性差异（约12字）。","target_chars":12,"context_relations":[],"subtasks":[]} -->

<!-- paper-charts: ["fig1"] -->

Quality outcomes by workflow arm

<!-- paper-charts: ["fig2"] -->

Treatment effects relative to one-shot generation

<!-- paper-charts: ["fig3"] -->

Factorial interaction between staging and risk-triggered human review

<!-- paper-charts: ["fig4"] -->

Incremental simulated cost and human time

<!-- paper-charts: ["fig5"] -->

Quality benefit per unit of resource

<a id="block-sec-005"></a>
## 讨论
<!-- writer:outline {"node_id":"sec-005","outline_description":"综合文献与模拟结果解释分阶段、风险触发人工、交互和成本收益的来源，并判断 H1—H3 在当前证据下获得何种程度的支持（约10字）。","target_chars":10,"context_relations":[{"target_node_id":"sec-004","relation":"continuity","guidance":"承接前一节已建立的信息和叙事进展。"}],"subtasks":[]} -->


<a id="block-sec-005-001"></a>
### 分阶段收益的机制与适用边界
<!-- writer:outline {"node_id":"sec-005-001","outline_description":"将分阶段带来的支持率与闭环率改善联系到显式规划、证据检索、状态保存和回归检查，同时解释闭环指标方向不一致的可能原因（约3字）。","target_chars":3,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-005-002"></a>
### 风险触发人工的收益与失效条件
<!-- writer:outline {"node_id":"sec-005-002","outline_description":"解释人工单独介入为何支持率上升但闭环率下降，并讨论错误或过度批判评审、触发策略和模拟者差异造成的潜在失效（约3字）。","target_chars":3,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-005-003"></a>
### 交互效应与质量—成本权衡
<!-- writer:outline {"node_id":"sec-005-003","outline_description":"解释联合条件在主张支持率、回归率和闭环率上的相反交互信号，讨论按决策指标配置方案以及边际人工投入递减尚待验证的问题（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-005-004"></a>
### 对论文写作 Agent 设计的启示
<!-- writer:outline {"node_id":"sec-005-004","outline_description":"据现有证据提出可落地的流程建议，包括保留证据状态、设置阶段门控、记录修订处置和实施异构评审，同时避免把模拟优势包装为产品优势（约2字）。","target_chars":2,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-006"></a>
## 局限性与后续研究
<!-- writer:outline {"node_id":"sec-006","outline_description":"系统陈述模拟假设、重复次数、统计不确定性、裁判偏差和材料覆盖等限制，并提出真实专家、更多任务与完整复现所需的验证计划（约8字）。","target_chars":8,"context_relations":[{"target_node_id":"sec-005","relation":"continuity","guidance":"承接前一节已建立的信息和叙事进展。"}],"subtasks":[]} -->


<a id="block-sec-006-001"></a>
### 模拟与材料限制
<!-- writer:outline {"node_id":"sec-006-001","outline_description":"集中说明参数化模拟、每臂三次重复、未配对保留、探索性区间、摘要或截断材料及单源自动评价对结论强度的限制（约4字）。","target_chars":4,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-006-002"></a>
### 后续验证计划
<!-- writer:outline {"node_id":"sec-006-002","outline_description":"提出在固定模型和真实论文任务上开展四级配对消融、真实专家盲评、异构裁判扰动测试和成本复测的验证路线（约4字）。","target_chars":4,"context_relations":[],"subtasks":[]} -->


<a id="block-sec-007"></a>
## 结论
<!-- writer:outline {"node_id":"sec-007","outline_description":"直接回答核心研究问题，概括模拟条件下的条件性发现、尚不能确认的协同效应及后续实证验证要求（约6字）。","target_chars":6,"context_relations":[{"target_node_id":"sec-006","relation":"continuity","guidance":"承接前一节已建立的信息和叙事进展。"}],"subtasks":[]} -->

