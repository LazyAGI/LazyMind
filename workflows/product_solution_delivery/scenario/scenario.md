# 产品方案与交付

## 入口与路由

本工作流在同一会话内持续维护一个产品项目，详细合同和边界见 `SOURCE.md`。
`route_product_stage` 按显式阶段 > 高置信度确定性规则 > 模型推荐选择唯一 selected_stage；
新项目不必从方向开始，已有同阶段正文直接修订。连续任务只登记计划，每阶段后等待用户继续。
仅 design 进入 `route_design_scope`：按六个设计领域分配主责/联动范围，逐决定判断 light/heavy；
隐私、身份、权限、静默写入、跨租户和不可逆高损失强制 heavy。阶段深度不覆盖该判断。
Router 只整理已绑定证据；`collect_design_light_evidence` / `collect_design_heavy_evidence` 按范围取证，轻量研究可升级子决策。

## 输入与产物

只要求产品目标；深度默认 auto，篇幅按阶段，样例存在则复用，否则沿用默认结构，不重复问卷。
用户可覆盖偏好；材料保持文件输入，标量保持值输入。缺少必要依据时记录缺口，不编造确认。
五个文本阶段复用原生 Writer，自动生成大纲，保留编辑能力，正文仍有人工确认边界。
七阶段交付同一版本的 HTML 与 Markdown；方向/方案/PRD/原型等是独立共享视图，不互相覆盖。
每阶段保存内部 assessment；真实检查、依赖版本、决定与缺口共同约束质量，文件生成不等于完成。

## 持续迭代与边界

finalize_product_delivery 登记内部 Manifest/Workspace，停在 awaiting-stage-confirmation。
用户明确继续/切换后，由宿主幂等创建新 Session，携带 workspace_seed、stage_approval 和精确 upstream_* 版本。
保留旧 Session 和产物；修订复用同阶段基线，新要求来自 stage_approval.request_context，项目目标保持稳定。
产物接受是独立授权；衔接时默认接受待确认决定必须记录系统来源，不能冒充用户逐项批准。
内容与质量评估分别版本化，旧通过结论不能掩盖新失败。查看阶段成果不授权修改或运行，也不中断当前执行。
轮次、截止时间、重复调用及单工具限制由 Host 决定；Writer 有单章/整篇截止时间，不允许失败后漫游文件。
不声称登录操作、付费资料、浏览器视觉 QA、生产代码、技术架构、研发估时、发布日期或组织审批。
内部 Session 不要求另开窗口；内部步骤、评估、历史阶段产物列表、Manifest/Workspace 和路径不向用户展示。
