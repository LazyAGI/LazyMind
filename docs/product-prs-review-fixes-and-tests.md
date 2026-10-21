# cst/product_prs：问题、修复记录与测试总表

更新日期：2026-09-30。后续发现的问题和修复继续追加在此文档，保留问题编号、原因、代码修改、验证结果和未完成验收；不按严重程度分级。Review 在前，测试在后。

## Review 范围和当前状态

比较基线为 `upstream/main@9cbc57c5deb3b5fdded71c6b051041c1bff3aa30`；导入来源为 `origin/workflow_dev@5b586402a21a638b29f37bec21ed5b0f0491466d`。历史行为修复在 `4b26e452a`、`b0962fbbc`，本次继续在 `cst/product_prs` 修改。原问题不是单纯重复文件：产品业务实际改变了 manager、runner、图推进、产物事务、公共 Repository 和共享 UI 的控制规则。

R1–R7 的行为修复保留；本次处理 A01–A12 的新增控制层特例，以及错误目录和重构中发现的契约遗漏。代码修改和本地回归不等于真实模型、浏览器或供应商端测完成，未执行的验收列在末尾。

### 必须保留的原有行为

- `MarkdownArtifactEditor.tsx/.scss`、`SlotComponents.tsx`、`WorkflowPanel.scss`、现有 Writer 工作流和 `algorithm/lazymind/document_tools` 与上述 main 基线无差异；没有替换原 Markdown 编辑/保存/冲突恢复逻辑。
- 公共面板只新增可选声明和通用扩展挂载。未声明新能力的包继续使用原工具、任务展示和编辑方式，不因包名被自动套用产品策略。
- `launch_user_input/current_user_input` 仅增加上下文；manager 原样传递本轮 `user_input`。PPT 对“继续”的解释留在 PPT 提示词。
- 图片 V1 供应商路径、搜索校验和字幕仍保留；V2 复用公共实现，KB/Web 的工具错误不直接升级为流程失败。
- 原子产物仍使用既有 `artifactfile`、`artifactgraph`、Attempt 事务和 revision 表；未再建一套产品存储表。鉴权、用户来源、CAS、幂等仍在可信 Host。

## 已有行为问题及修复

### R1：上传图片编辑缺少必需源图产出

已用 `publish_edit_contract` 从当前 attempt 的 `remote_inputs.source_image` 读取实际绑定，校验图片和四项编辑约束后发布 `authoritative_image` 与 `edit_contract`。上传绑定优先于模型传入的 URL；多图、缺图、不完整约束均拒绝执行，不能通过模型猜路径或读取另一次运行的最新产物补齐。

两个必需输出共同约束步骤完成。验证使用真实 SubAgent context 和实际图片文件，检查发布图片字节与上传图一致；不是只检查提示词包含字段名。

### R2：产品正文转换损坏代码、URL 与证据事实

已去掉对整个 Markdown 的状态翻译、`null` 替换和外部证据否认。正文原样交给已有 Writer 发布机制，代码块、业务枚举、链接和来源编号不改写。产品提示词也明确要求保留这些内容，定向修订不能顺带重构未选中正文。

产品仍复用现有 Writer 能力；没有新增第二套共享 Markdown 编辑器，也没有改动 Writer 文档保存契约。

### R3：阶段导航替用户批准决定

已删除自动接受逻辑。结束当前阶段和切换到当前阶段修订不会接受任何决定；带未处理硬阻断项进入其他阶段会被拦截。显式暂缓可作为草稿携带，但决定仍为 proposed，不得作为 accepted 基线。

产品界面展示决定原文、依据和完整结构，提供逐项确认/暂缓。请求绑定用户实际看到的 session state version、decision ID 和 proposal hash；版本变化返回冲突后需重新查看，网络重试复用原命令，不能刷新 hash 后偷偷确认新内容。后端保留 owner 检查和幂等处理。

旧 `product-workflow-default` 自动确认不会被重放成用户确认；读取历史 workspace 或继承 seed 时重新开放待确认项，保留审计历史。真实 SQLite repository 回归验证了 finish 持久化、重复命令、旧 hash/错误 owner 拒绝、刷新恢复及提案变化后重新确认。

### R4：普通图片路线忽略搜索和知识库

普通准备步骤已接通已有图片搜索/校验及 KB 查询。实际检索结果进入参考摘要和生成提示词，通过校验的图片发布为 `material_images`。各来源独立执行，KB 失败或无命中不影响 Web 检索，也不丢弃其他来源的成功结果。工具异常、空结果和成功分别记录在 `retrieval_results` 及材料摘要中，不直接升级为准备步骤或整个流程失败。

例如 KB 服务不可用、Web 找到有效图片时，继续使用 Web 图片，摘要如实标明 KB 未取得资料。即使所有来源都失败，仍保留各工具的失败记录，明确没有取得经过验证的外部参考，不把失败改写成“无需外部资料”，也不伪造知识库事实。产物发布/存储本身的错误仍正常上报，不被检索容错吞掉。用户明确取消参考要求时才跳过检索尝试。

V1 的图片校验、搜索和字幕实现提取到安装包公共模块 `image_workflow_support.py`，V1 通过原函数名转出，V2 复用该模块。这样不需要安装包或不可变工作流包里存在另一个工作流源码目录。V1 通用供应商路线保留；V2 是明确的 Seedream 路线，不替代 V1 的既有供应商能力。

### R5：启动需求与本轮指令混用

启动输入与本轮输入分别传递。图片 V2 的纯“继续/确认/重试”不再替代需求；明确修改可覆盖冲突的启动条件。普通图片比例及表情包数量/字幕读取有效需求，准备阶段保存的比例修订在后续确认时继续有效。编辑约束同时保存当时的用户需求，供下一步精确绑定使用。

修改后仅验证图片 V2 使用新增字段；Writer/PPT 等既有 `user_input` 协议不改写。

### R6：Mermaid 分支条件被丢弃

支持的普通流程图保留边标签与回边，独立图使用独立 SVG marker。子图、特殊箭头及 sequence/state 等超出简化渲染器支持的语法，展示明确说明和完整转义源码，不能画出缺失分支的“成功”图。移除了原有几类会丢失结构的替代渲染器。

当前不承诺这些复杂图形都有图形化预览；保证不静默遗漏原文。若必须完整图形化，应另行接入完整 Mermaid 实现并验证。

### R7：独立钳制宽高导致源图变形

改为按同一缩放系数计算宽高，并在供应商尺寸约束和取整后检查比例误差。标准横图、竖图、方图和可表示宽图保持比例；极端比例无法同时满足像素和边长约束时，报错要求用户选择支持的画幅，不静默拉伸或裁剪。

## 公共控制层问题及本次修改

### A01：manager 为 PPT 单独改写“继续”

原 `_ppt_step_user_input` 按 `ppt-workflow` 名称把控制输入清空。已移除 helper 及其普通/hand-off 调用；两种推进原样传递用户文本。PPT `scenario/state.yml` 在各阶段提示中说明从启动上下文和已绑定产物继承原需求，不把“继续”当成新主题。已有附加上下文字段继续复用。

### A02：manager/SDK 持有产品工具和隐式 session 重定向

产品阶段查询、读取和 relay 工具移到 `algorithm/lazymind/chat/host_extensions/product_project.py`，HTTP 客户端移到同目录的 `product_client.py`。公共 manager、SDK 删除产品方法、`product_tools` 开关及 `_relayed_session_id` 字典重定向。

可信扩展由 `runtime.host_extensions` 声明选择，`product-project-v1` 是安装能力名，复制包无需使用特定 workflow ID。ChatService 是扩展组合入口；未知扩展明确报错。扩展注册不是 workflow ID 白名单。

为保留同一轮“切换后继续执行”，公共工具提供显式 `bind_successor` Host 回调：只接受当前 source session、同 conversation、同 workflow 和固定 revision 的服务端结果，再通过 SDK 确认目标 session 可读。成功后，普通推进和 hand-off 工具共用更新后的绑定；这个回调不暴露给模型。产品扩展调用它后要求读取真实 Ready frontier；finish 才返回通用终止控制。没有让公共工具读取产品专用重定向字典。

### A03：输入白名单依赖产品名称

新增 `runtime.trigger_inputs`，编译器验证所有名称都是已声明 external material。触发工具只暴露并接受该列表；未声明沿用既有输入规则，显式空数组表示不允许模型提供任何材料。使用可空列表保留“未声明”和“空列表”的差别。可信 `workspace_seed/stage_approval/upstream_*` 不由模型自由伪造。

### A04：runner 按产品步骤名设置预算、工具和失败策略

删除 `product_policy.py`。轮数、超时和工具调用配额从固定 revision 的 `runtime.execution_limits` 读取，通用实现位于 `workflow/execution_policy.py`。编译器校验步骤存在、轮数 1–64、超时 1–3600 秒、配额 1–100；改工作流 ID 不改变行为。产品每个步骤的预算在包内声明。

删除 runner 的产品 builtin tools 开关、硬编码工具过滤和产品工具失败即终止逻辑；继续使用既有工具声明和普通工具错误处理。工具错误是否可恢复不再由产品工具名表决定。

### A05：远程执行器提前解释业务输入

execution depth、参考样例、字数的别名和业务校验下沉到产品 `scripts/tools.py` 的路线发布工具。远程执行器只传输通用输入，不在调用工作流工具前把产品参数错误直接升级为执行失败。未修改其他工作流的输入解释。

### A06：产品覆盖工作目录和错误协议

移除产品 workspace 子目录覆盖；统一使用 execution spec 的 workspace。HTTP 错误按共同 envelope 解码；仅声明了预算的步骤使用通用超时包装。没有“产品才读错误消息、其他包丢失错误详情”的分支。

### A07：ArtifactSink/Attempt 执行产品发布语义

`productstate` 改为通用 `workflow/publication`，`attempt/product_outputs.go` 改为 `transactional_outputs.go`。是否延迟选择、失败撤回、成功统一选择及使消费者失效，来自 `runtime.transactional_outputs`。`runtime.publication` 明确发布步骤与必需输出；编译器要求这些输出由发布步骤产生。

Core 只核对冻结输入、有效 revision、必需产物及内容摘要并记录 `workflow.published`。产品的阶段、Workspace/Manifest 对应关系、正文/HTML/Markdown 配对、assessment 和描述摘要一致性移到包内 `_validate_product_publication`，在发送任何产物之前校验；没有因移走 Go 产品代码而放弃原校验。

### A08：图求值完成后暗改 workspace_seed witness

移除 transition handler 和远程读取器里的产品步骤/slot 特判。`runtime.published_input_aliases` 声明继承来源，编译器检查来源属于已发布输出、目标为 external input、两端类型相同。读取 runtime snapshot 时先投影别名，再进行图求值与冻结，因此 Ready witness、持久化 binding 和远程读取都指向同一 revision。

Snapshot 输入使用通用读取与内容摘要；JSON 返回 JSON，文本返回文本。发布前及远程读取时检查冻结摘要，发现内容变化会拒绝。已发布版本不被后来的 working selection 替换。

### A09：人工编辑的快照/COW 依赖产品 ID

两个编辑入口改读固定 revision 的 `transactional_outputs` 能力，再复用 `artifactfile.Snapshot` 和已有 COW 路径。普通 Writer 不声明该能力，保持原保存、草稿与冲突恢复逻辑。没有根据 workflow ID 决定文件生命周期。

### A10：公共 Repository 重复实现产品领域模型与默认值

阶段导航、项目聚合、决定及历史移到 `backend/core/product` 独立领域服务；领域 handler 也从 workflow facade 移出，由 routes 注册。公共 Repository 只提供 `AtomicCommand` 事务原语；owner/CAS/幂等及原有 session/input/artifact API 保留。

删除 Go `productStageDefaults` 及自动导入默认资源。原差异为 direction 1400/1800、design 3500/5000、PRD 4500/6000、review 1800/3000、handoff 3500/5000（Python/Go）。现在没有显式偏好时由包内路线验证器决定默认值；Go 只恢复用户已存在的偏好。

七阶段及其文档格式仍是 `product-project-v1` 领域协议的一部分，由业务服务、包和业务组件共同遵守；它不是支持任意阶段的通用引擎。新增领域阶段需要升级该扩展协议，不能向通用 Store/manager 添加分支。

### A11：relay 自动升级包并返回固定 Ready 步骤

创建下一阶段使用 source session 的 `WorkflowRevisionID`。只在摘要中只读查询最新包以提示更新，不在 relay 时切版本。新 session 创建后重新读取实际持久化输入绑定，通过该 revision 的 graph projection 得到 Ready，删除 `route_product_stage` 常量。

服务端返回 source/session/conversation/workflow/revision 身份供显式 Host 绑定校验。测试构造新 head 与旧 pinned revision、改名入口，验证没有隐式升级和错误 Ready，并检查未注入字数默认值。

### A12：共享 UI 依据产品 ID 覆盖包声明

移除 WorkflowPanel、TaskCenter 和 chatLayout 的产品 ID 判断。包内 `ui.task_presentation` 声明任务分组、隐藏步骤与最终文件展示；`ui.group_downloads` 选择通用下载聚合；`ui.extensions` 请求已安装控件。`WorkflowExtensions` 注册业务组件，`WorkflowTaskProgress` 不包含产品步骤名。

原来在面板中删除 `_outline_report` 和重设 layout 的做法，改为产品包自身 slots/tabs 的声明；同时把 PRD/review/handoff 检查报告的 `exposed` 改为 false，确保编译契约一致。产品决定控件内部保留业务 API，下载聚合保留每个原操作的保存/禁用回调。

## 本轮补充发现与修复

### C01：Core error constructors 没有登记

用户提供的 `TestCoreErrorConstructorsAreCatalogued` 日志涉及六种不同产品错误。`common/error_catalog_workflow_publication.go` 登记这些旧业务错误及新的通用发布错误；非法数据映射为 400，冻结/内容变化/发布不完整映射为 409。没有删除测试或给扫描器加忽略规则。该用例单独 `-count=1 -v` 运行通过。

### C02：拆分后不能丢失校验或冻结内容

补充包内完整发布预检；manifest 摘要与绑定正文不一致时零产物发布。补充 ContentSnapshot 的绑定摘要和远程读取/发布时比对，防止移除产品读取分支后丢失冻结约束。

### C03：声明的空值与 UI 可见性不一致

`trigger_inputs: []` 不能被序列化为未声明；改用可空字段，并增加复制包/空白名单测试。隐藏 outline report 时同步修改 slot 的 exposed 标记，实际包编译检查已覆盖。只调整 UI tabs 而不调整公开声明会导致安装失败，现已修正。

### C04：阶段切换不能只创建新 session 后结束

拆分工具时发现，如果 relay 一律作为 stop tool，聊天发出的“继续”会只准备下一阶段而不执行。已改为 A02 的显式绑定回调，普通工具可在同一轮读取新 session；只结束操作停止模型。测试同时拒绝错误来源和意外 revision 升级。

### C05：风格预览使用固定版式，不能完整反映所选风格（新增发现，未修复）

核查“先看三套方案”时确认：模型返回 A/B/C 三组风格配置；每组样张由 `runtime/scripts/run_stage.py::_render_sample_deck_svg` 拼成三页 SVG（标题、三个要点、角色/受众/场景/格式）。三套共用固定排版，文字字体写死为 Inter/Arial，底色写死为 `#111827`；design_style/color_tone 主要显示为名称，预览没有完整执行配置中的 typography 和 rendering recipe。因此用户看到的是配色与固定样张，不能据此验证实际浅色、极简等风格的最终呈现。

该样张渲染器在对比基线 main 已存在；本分支的 preview_choice 流程将其作为用户选风格的正式入口。当前只核查并记录，没有运行真实模型/浏览器验证，也未修改实现。后续应让样张复用正式页面的风格渲染契约，并验证所选字体、明暗及版式确实反映到样张上；三套内容保持一致以便比较。

### C06：SubAgent 执行包内测试，污染 Chat 进程（已修复）

2026-09-30 16:45 的真实 Chat 日志中，文档动作先报 `AttributeError: module 'lazyllm' has no attribute 'globals'`，后续 `plan_background_prompts` 在模型配置注入时又报 `ImportError: cannot import name 'LOG' from 'lazyllm'`，外层显示“上游错误”。原因是 SubAgent 的 `load_workflow_tools` 遍历并执行包内所有 `scripts/**/*.py`，连 `scripts/tests/test_style_flow.py` 也被执行；该测试顶层向 `sys.modules` 写入缺少 globals/LOG 的替身，覆盖在线进程的真实模块。不是模型供应商返回的错误。公共 toolkit 本已有另一套带声明过滤的加载器，但 SubAgent 没有复用；此前只给加载测试传入 tools.py，遗漏了完整脚本包场景。

修复将 runner 接入 `workflow_toolkit.load_workflow_package_tools`，继续先校验 revision、tree hash 和可信执行权限，再物化完整包；公共加载器支持真实 `package_root`，保留依赖 `__file__` 的相邻运行资源。按 `tool_scripts` 加载本轮请求的函数及其所属脚本；没有声明的历史包仍走兼容加载，但排除 tests/__tests__ 目录、test_*.py、*_test.py、conftest.py，兼容 Core 的空文件 null 编码。未声明的函数不会被额外暴露。没有添加任何 PPT ID 特判，也未改 Writer 的编辑或业务逻辑。

同时将 PPT 测试的全局替身限定在 `mock.patch.dict` 的加载作用域，结束立即还原。新增 runner 回归覆盖完整 PPT scripts 目录及 workflow.yaml、任意重命名的 workflow ID、真实模块身份保持、未声明/未请求脚本不执行、运行资源路径；公共 loader 回归覆盖历史包的测试路径及空脚本。此修复对原来的固定 revision 生效，无需修改旧包或自动升级会话。恢复运行必须重启已经污染的 Chat 进程。

### C07：底图提示词步骤绑定了两个单值 style_flow 来源（已修复）

2026-09-30 17:00:33、17:00:42、17:00:52，session `ce2180c9d5824e03a83999fd31c1becf` 的 `plan_background_prompts` 连续三次在获取 Attempt Context 时失败，Core 返回 503，尚未执行模型或 PPT 工具。只读打开实际 Core SQLite，并直接调用 `DBContextLoader.LoadAttemptContext` 复现原始错误：`single-cardinality material "style_flow" has multiple distinct input bindings`。

失败 Attempt 同时绑定启动时的 `input_resource`（binding `9ab2b806-73c3-42be-b4d5-e8f71eccfa73`）和 `analyze_requirements` 后生成的同名 artifact（revision `aec4a4cc-826b-4a08-86c5-8e8df74ebf65`）；`style_flow` 声明为 single，下游执行器正确拒绝两个不同来源。实际原始输入为“直接生成一套｜AI 根据需求确定一套整体风格并继续”，步骤产物为 `auto`，语义相同但来源和内容表示不同。现将固定 revision 的编译图传入 `loadRuntimeSnapshot`：已声明 `MaterialProducers.kind=step` 的材料只由步骤产物提供 witness，启动问答仍保留在会话里，但不再参与该产物的图求值和 Attempt 输入冻结。产物不存在或已失效时也不会用原始问答冒充产物；声明为外部来源的输入/列表保持原行为。普通任务展示、Ready 求值、路由冻结、步骤启动/回退后重算均复用这个快照规则。没有增加 PPT ID/字段名特判，没有改 Writer/图片业务，也没有放松执行器的单值冲突校验或改写旧 Attempt 绑定。

同时修正 `RemoteExecutorClient.data` 的诊断信息丢失：Core 返回结构化 error 时，将 code/message 保留到 HTTPStatusError，继续保留原 HTTP 状态和 response；非 JSON 或非协议错误继续抛原 HTTP 错误，成功响应行为不变。未更改用户会话、历史失败记录或重试计数；新重试使用修正后的快照。

### C08：任务产物列表泄漏声明为隐藏的流程标志（已修复）

同一真实会话的第一步保存了 5 条 SubAgent artifacts：requirement_analysis、ppt_capability_requirements、style_flow、generate_background_images、skip_style_choices；后四者都是包内显式 `exposed: false` 的内部材料，但记录的 hidden 均为 false。基线 main 已用 capability/skip 标志驱动流程；本分支又增加风格模式、背景开关规范化结果和跳过风格标志。它们不是用户分析成果，不应出现在任务的产物列表中。

`projectOrdinarySession` 现从当前会话固定 revision 的 workflow.yaml 读取显式隐藏声明，同时过滤 SubAgent 原始产物（包括还未关联 slot revision 的记录）及 Workflow slot 产物，最终交付列表也复用同一过滤结果。未声明 exposed 的历史产物继续可见；声明 true 的产物继续可见。内部数据仍保留给路由/下游，不修改历史记录的 hidden 字段，不按 PPT 名称或字段名列黑名单，不改变 Writer 编辑逻辑。已有固定 revision 会话刷新即可采用该展示规则。实际会话还存在人工编辑后的同一分析产物两版被重复展示：现按 slot/list_index 选择当前选中版（尚未选定时保留该条目的最新版本），并去除单值产物的旧 SubAgent 副本；各列表条目仍独立展示，历史修订继续保存在数据库。兼容旧数据中同 slot/index、同 revision 的独立记录：只有明确的新版本才替代旧版本，不按 ID 任意隐藏同版本记录。

### C09：完成评审默认 20 秒超时导致任务失败（已移除限制）

用户报告 `Could not verify task completion before the evaluation deadline`。该错误来自 SubAgent 收尾时的额外模型评审，默认 20 秒并非任务总耗时 5 分钟。Git blame 指向 upstream main 已有的 `d8eb8f1a9` / PR #750。按用户要求删除 `subagent_completion_evaluation_timeout` 配置注册及 `_evaluate_completion_async` 的 deadline/超时判失败分支；等待评审正常返回，不再因这一独立计时器把任务判失败。保留非阻塞线程调用、取消轮询、取消后的迟到结果丢弃、真实评审结论和必需产物检查。供应商请求自身错误仍按原逻辑处理。

### C10：素材图片链接失效后应重新搜索替代图（已修改提示词）

按用户要求修改 PPT collect_materials 提示词：候选图片链接过期、403/404、下载到非图片内容时，丢弃该候选，按同一页主题重新调用图片搜索，选不同 URL/来源并下载注册；不重复请求坏链接，不重启整个素材搜集步骤，保留已成功的摘要证据和图片。先尝试新候选再判断是否省略可选素材；替代搜索计入已有检索预算并预留恢复次数。搜索服务本身不可用或重新搜索仍无可用图时记录缺口，明确 AI 底图不能替代用户必需的真实参考图。

此变更属于工作流包的新 revision；已固定旧 revision 的会话不会被偷偷升级，重启同步包后新建任务使用新提示词。

### C11：相对 upstream main 的跨工作流兼容范围复核（仍需端测）

fetch 后 upstream/main 仍为 `9cbc57c5d`，当前提交为 `eb1178be6`。本分支共 113 个差异文件，包含工作流资源、公共运行时、前端、测试和文档；不能视为仅增加独立工作流。Writer/Markdown 核心和 writer-workflow 路径无差异，但公共任务运行/产物展示变化仍会影响它们。

- C08 对所有工作流的显式 exposed=false 生效。基线的 image-workflow `generated_base_image`、bid_tech_proposal_writer `outline`，以及 academic_research_pipeline 的 writing_context 等都会从普通任务产物列表隐藏。数据与下游读取不被删除，但旧用户若从任务列表查看/下载这些中间结果，入口会发生变化；尚未完成这些旧工作流的真实界面验收，不能据此声称原行为完全保留。
- C07 对所有 producer=step 的材料排除同名外部输入。符合声明语义，但依赖以启动输入预填步骤产物的旧自定义包会改变就绪状态，需验证旧包与人工修订/重试场景。
- 公共脚本加载器统一后不再执行未声明或测试命名的脚本。旧包无 manifest 时仍有兼容路径；如果旧自定义包把实际工具放进 test_*.py，或依赖未声明模块的顶层副作用，行为会改变。尚未发现仓库内真实包复现这种依赖，不将其描述成已确认故障。
- C09 删除的是公共 SubAgent 评审 deadline，普通子任务也生效。慢评审不再被 20 秒判失败；若供应商调用长期不结束，则需等待其请求错误或用户取消。这是用户明确要求的全局行为变化。

新增发布、执行预算和业务 UI 扩展由包声明启用，未声明时保留原分支；这降低影响范围，但不抵消以上全局改动。38 个 Go package 和 125 项近期 Python 测试通过，之前前端回归也通过；不等同于全部旧工作流端到端兼容。合主仓前仍需旧 Writer/投标/学术流程的编辑恢复、旧图片流程中间图下载、自定义工作流包加载/预填输入的验证。

### C12：公共 Python 模块格式检查失败（已修复）

修正 product_client、product_project、registry、execution_policy、workflow_manager 的 15 条 E302/E501/E128/Q000：补齐顶层定义空行、拆分长调用/条件/字典、对齐续行、统一单引号。没有更改运行逻辑；5 个文件与修改前的 AST 完全一致。

### C13：Algorithm CI 的 PPT 回归测试停留在旧风格协议（已修复）

2026-10-04 通过浏览器查看 [CI job 109836899207](https://github.com/LazyAGI/LazyMind/actions/runs/36699741037/job/109836899207)：4 failed、4031 passed。四项失败均来自 `tests/algorithm/chat/workflows` 中的旧测试与当前包内风格协议不一致；本次只修改测试及本文档，没有修改运行时代码，也没有跳过测试或放宽 CI 门槛。

- 路由测试补齐 `choose_style`，并检查预览路线要求 preview_choice，直接底图/大纲路线要求 auto。
- 两项大纲重试测试的 stage 替身接受显式 `sample` 参数，生成包含 design_style、color_tone、primary_color、palette 的有效风格合同。保留失败重试、发布失败后复用内容检查点、跨工作流隔离与原图片保留断言；额外确认人工修改的有效风格不会被重新生成覆盖。
- 旧 standard deck 测试移除新版本才有的 `params.style_flow`，真实模拟旧数据。显式传入 standard 的新 deck 仍表示 preview_choice；缺少 style_flow、样例和选择记录的旧 deck 按现有迁移规则恢复为 auto，不重建目录或图片。

### C14：PR #793 批量产物保存与本分支的兼容 review（2026-10-08，已在 #793 修复，尚未合入本分支）

对比 [PR #793](https://github.com/LazyAGI/LazyMind/pull/793) 的 `56f7260a6` 与本分支 `57f1b0f94`，共同基线为 `9cbc57c5d`。#793 仅改 runner 提示词、subagent/tools.py 和新增批量保存测试，共 3 个文件。与本分支、最新 upstream/main `ecb769c7d` 分别进行 merge-tree 检查，均无文本冲突；另在隔离 worktree 中将 #793 与本分支实际试合并并执行测试。

兼容结论：没有恢复已回退的 Writer/Markdown 编辑器改动，没有重新加入按工作流名称分支的上层控制逻辑。`_save_artifact` 及其 `internal_publish`、`publisher_list_index` 接口保留，publisher-owned 校验仍在；Core 的 transactional_outputs、冻结/发布逻辑和内部产物隐藏逻辑未被修改。批量预校验与 Core 发布事务属于不同层，不是重复实现，前者不能替代后者。没有发现直接覆盖之前修复的冲突，但下列批量保存问题需要处理。

**不同来源的同名文件在发布前被覆盖。** `tools.py:578–590` 先准备整批文件，再逐项发布；底层 `copy_artifact_input` 仍按 basename 复制到同一 workspace。复现：把 `a/report.txt=FIRST` 和 `b/report.txt=SECOND` 放入一个 save_artifacts 调用，发布时读取对应文件内容。当前分支得到 FIRST、SECOND；试合并后得到 SECOND、SECOND。图片同样使用该复制函数。原来可变路径已有覆盖/消费时序风险，这次预复制整批使该场景在第一次发布前就确定丢失前一份内容，后续 Core 冻结快照也无法恢复。修复方向：为每个不同来源生成不会碰撞的目标路径，并同步文件授权解析的目标声明；或在有副作用前拒绝同一批中目标路径碰撞。补充同名文件/图片及失败批次不改写已有文件的回归测试。

**列表顺序查询失败被缓存为空列表，使整批覆盖变为追加。** `tools.py:698–701` 把异常缓存成 `[]`，后续同 key 条目不再查询，发布时均缺少 list_index，返回值却为 ok。复现：现有 order_list=[7,3]，同批覆盖第 1、2 项，首次查询短暂失败、第二次查询可成功。当前分支发出的 list_index 为 [None,3]，合并后为 [None,None] 且仅查询一次。首次失败静默追加是基线已有问题；#793 将其扩散到同批全部后续覆盖项。修复方向：区分“已确认空列表”和“查询失败”，仅缓存成功结果；明确请求覆盖而无法确定索引时，在预校验阶段报错，不静默追加。新增测试目前将失败缓存行为视为成功，需要一起修正。

后续修复：已在 #793 的 cst/opt_save 分支修正两个问题，修复提交为 `27f22d8cf`。文件复制使用任务目录内新建的独立子目录，保留原文件名并独占创建；文件授权解析同步声明目标父目录。受限源文件读取和 POSIX 固定父目录句柄校验保留。查询异常/响应格式错误会使整批预校验报错，不发产物事件、不消耗序号；重试重新查询，确认空列表的原行为保留。相关修复没有混入 cst/product_prs 的业务提交；只在隔离 worktree 合并验证两者兼容。

新增回归覆盖同名文件/图片/file_list、后续保存不改写旧副本、失败批次保留旧文件且可重试，以及顺序查询失败后的整批拒绝与恢复。详细记录随 #793 的 `docs/pr-793-artifact-batch-fixes.md` 提交。

### C15：底图提示词步骤的设计要求过重（已简化）

`plan_background_prompts` 原先强制每页 5–12 个元素、精确留白比例、逐字重复风格锚点、视觉母题演变及分别描述材质/光照/配色。auto 路线还要求先生成完整 style_spec。两页简单主题也因此被扩展为多轮设计权衡。

现改为每页一段简短英文提示词，通常 2–4 句；仅保留 16:9、已有风格与配色一致、正文留白、背景无文字/标识/图表等要求。未知布局默认左侧留白、右侧装饰，直接发布，不反复探索布局或自审。验收条件同步简化。auto 在此阶段通过已有 ppt_init_deck 初始化元数据，不额外调用风格模型；preview_choice 仍复用用户选定 A/B/C 的现有风格。保留指定页替换、整页插入及后续发布/图片生成接口；正式页面风格仍由后续大纲/页面阶段处理。

这是工作流包的新 revision。已运行的会话固定旧 revision，不自动替换；服务同步后新建 PPT 任务使用简化提示词。此前 SiliconFlow 流式响应缺少 finish_reason 的直接错误仍属于供应商响应完整性问题，此修改减少额外任务负担，不声称修复供应商协议错误。

### C16：页面生成长时间无反馈、重复生成与串行调试（已修复，需真实供应商复测）

2026-10-08 的两页任务在供应商协议错误、传输超时后仍长时间显示“开始执行”。工具返回失败后，外层 Agent 再次调用 `ppt_generate_pages`；第 2 页 HTML 已落盘但没有 `.page_retry_002.json`，因此后续调用没有复用。重试、检查点与缺乏内部进度反馈的机制在 upstream/main 已存在，不归为本 PR 新增。检查发现并行页面使用 `redirect_stdout` 捕获 JSON 返回值，会修改进程共享的 stdout：不同页面及日志可以互相串台。此并发缺陷已通过回归测试覆盖；不能仅凭现有日志断言它是历史运行检查点缺失的唯一原因。

本次修改：
- runtime `_capture_cmd` 改为 ContextVar 隔离的结构化状态接收器；命令行直接执行仍输出 JSON。并行、嵌套、异常退出不修改全局 stdout。
- 成功页面的输入/输出哈希检查点使用临时文件替换写入，全部发布成功后仍保留。再次调用可复用未修改页面；内容变化仍使缓存失效。历史裸 HTML 缺少来源检查点时不冒充可验证缓存。
- 页面批次失败后，当前 SubAgent Attempt 记录失败。Agent 再次调用整套或低层页面生成入口会被阻止，不再发送供应商请求；提示词要求报告失败并停止。用户开启新 Attempt 后可以重试。工具内部原有有界重试仍保留，超时仍不自动重试。
- 通过已有 `ctx.emit(progress)` 实时报告开始、每页成功/失败、重试和发布结果；等待当前页时每 15 秒报告页码与等待时间。这是状态更新间隔，不是新的任务截止时间；未修改公共 manager/runner、完成评审或 Writer。
- 按用户澄清，`ppt_generate_pages`/底层 batch-page-html 工具改成逐页串行生成，旧参数即使传 concurrency=3/8 也只同时生成一页；单独运行 runtime CLI 同样串行。保留全部请求页数，两页任务仍生成两页，不截断为第 1 页。提示词和验收同步要求完整页数。

使用限制：新代码需要新的工作流 revision；已有会话固定旧包，不热替换。此修改不承诺 SiliconFlow 的超时/协议错误消失。成功页仍按原页序发布，前页未完成时通过进度说明后页已生成，未改变产物排序语义。

### C18：产品工作流中文参数导致父 Router 发布失败（已修复）

会话 `0d69ccff-299b-4683-8674-49a5e6ad8aa0` 的绑定参数包含 `execution_depth="全流程"` 和 `reference_sample_choice="无"`。父 Router 发布前的别名表不接受它们，触发 `PRODUCT_INPUT_INVALID`；由于用户绑定值优先于模型参数，模型改传空值也无法恢复。

补齐“全流程”→`full`、“无”→`none-confirmed`，绑定输入校验复用产品工作流已有的标准化函数，移除重复别名表，避免预检和发布入口再次出现规则漂移。保留空值/default 的原有默认规则、未知值拒绝规则、用户输入优先级及原始资源不可变性。变更只在产品工作流内，不增加上层 manager 特例，不增加模型调用。

本次提交不包含 HTTP 504 分类、错误展示或供应商超时处理；未重启服务。已有会话绑定不可变工作流包，不能将源代码修复视为已运行会话自动恢复；真实供应商全流程仍需在加载新工作流 revision 后复测。

### C19：模型把工作流输入对象编码为字符串，初始化连续失败（已修复代码）

会话 `4425573e-e797-4fba-b7b9-0768572a7ae3` 在 16:57:42、16:57:44、16:57:49 调用触发工具时，把 `input_bindings` 传成包含 JSON 的字符串，而非对象，工具类型校验返回 `Input should be a valid dictionary`，未进入工作流初始化逻辑。16:57:46 的调用省略绑定，因此必填产品目标没有进入准备请求，返回 `waiting_for_input`。该会话没有创建 Session；终态记录为 `cancelled/user_cancelled`，不能归因为首个 subagent 失败。

通用工作流触发入口兼容字典及一次 JSON 编码的字典，解析后仍严格要求“字符串 material ID → 字符串值”。坏 JSON、数组、JSON null、嵌套对象、非字符串值均拒绝；包未暴露的字段仍按原规则拒绝。兼容覆盖文本绑定和附件绑定，附件仍走原解析与导入流程；工具说明明确推荐传对象。没有按产品工作流名称增加分支，没有额外模型调用，也没有从自由文本猜测绑定值或放宽必填校验。

2026-10-08 17:01 按用户要求执行 `make local-down` 后，以 `LAZYMIND_DYNAMIC_PROMPT_MODULES=false make local-up` 重启。运行时 ready，前端与 Core 健康检查均为 HTTP 200，Chat PID 为 31407，确认意图识别仍禁用。真实模型端测尚未执行。

## 边界与兼容说明

main 已有的 `workflow/ppt_incremental_pages.go` 仍按 PPT 身份处理旧页插入；这是基线已有逻辑，本次未新增或扩展。它需要单独设计旧会话迁移才能安全改成声明式能力，不能直接删除后破坏现有 PPT 页面保留行为。本次 A01 处理的是增量新加的 manager 输入改写，不代表整个主仓库已经不存在任何历史特例。

本分支的产品能力尚处于这次 PR 的迭代中。测试新实现应同步新的产品包 revision 并新建会话；不要只替换 Host 二进制后用旧的、没有声明新能力的产品 session 验收。运行中 session 不被自动升级。既有 main Writer/图片/PPT 未声明新能力时按原协议运行。

历史详细探针保留在 [架构 Review](product-prs-architecture-review-20260930.md)，早期行为交接在 [原 Review](product-prs-review-and-tests-20260930.md)。二者是对应提交的历史记录，当前状态以本文为准。以后新增问题继续追加编号和证据，修复后更新同一条记录，不把未测写成通过。

## 同事从已有本地仓库接手

在同事现有仓库中执行，`origin` 应指向他自己的 fork；若目标本地分支已存在，先换一个未使用的分支名，不覆盖未提交修改：

```bash
git fetch https://github.com/CarlosShaoting/LazyRAG.git cst/product_prs
git switch -c cst/product_prs FETCH_HEAD
git push -u origin cst/product_prs
gh pr create --repo LazyAGI/LazyMind --base main --head '他的GitHub用户名:cst/product_prs' --title 'Fix workflow boundaries and preserve Writer behavior' --body-file docs/product-prs-review-fixes-and-tests.md
```

将示例用户名替换为实际 fork owner。后续修复在他自己的同一分支提交、push，即可更新提交到主仓库的 PR；不需要重新 clone，也不从整个 dev 覆盖 main。

## 测试：本次自动化验证

2026-09-30 本地执行，所有下列命令退出码为 0；Go 最终一轮禁用结果缓存。结果没有依赖 GitHub CI 状态。

| 范围 | 结果 |
|---|---|
| `TestCoreErrorConstructorsAreCatalogued` | 单独 `-count=1 -v`：PASS；没有修改该测试 |
| Go `workflow/... product common doc skillv2/...` | 38 个有测试的 package 通过，另有 5 个无测试 package；覆盖真实 SQLite 的发布、产物选择、决定、固定 revision relay |
| Core 顶层编译 | `go test . -run '^$'` 通过，包含领域路由注册 |
| Python 产品/图片/PPT/公共执行器/SDK/Writer | 22 个独立测试进程，623 项通过；另有已有参数化子测试通过 |
| 新声明及显式 session 绑定 | 7 项通过；与 selection/runner 再联合执行为 142 项通过，未重复计入前一行 |
| WorkflowPanel 原有面板/store | 18 文件，147 项通过 |
| Writer、任务中心、taskCenter store | 25 文件，270 项通过 |
| Markdown 编辑器、共享修订弹窗、PPT Slot | 5 文件，107 项通过 |
| 产品决定控件与通用扩展挂载 | 2 文件，6 项通过 |
| 前端生产构建 | Vite build 通过；已有 bundle 大小警告 |
| 实际内置工作流编译 | Go `TestBundledWorkflowsCompileForRuntime` 通过，包括产品、图片 V2、PPT |
| Writer 保真与补丁检查 | 核心 Writer/Markdown 文件对 main 无差异；`git diff --check` 通过 |

新增回归不是仅检索源代码字符串：包含 ArtifactSink 真实写库后不提前选择、Attempt 成功/失败/取消/幂等、发布别名的图 witness、缺产物与 stale 输入拒绝、改名包仍启用声明、旧 head 存在新版本仍固定 revision、默认字数不由 Go 注入、manifest 与正文不一致时零发布、Host session 错误来源/版本拒绝、无声明时不挂载业务 UI。

Python 使用仓库 Python 3.11 环境；测试依赖从本机临时目录补充，业务代码仍来自当前工作树。PPT 老测试清理时有 lazyllm stub 的退出日志，但测试主体 87 项及 10 个子测试通过、进程退出码为 0。Go 存在 macOS Keychain SDK 弃用警告。以上均未当作真实端测成功。

### C06 运行故障补充验证

加载器及完整 PPT 脚本包 26 项、可信执行/工作区授权 15 项、SubAgent/远程执行器 120 项、PPT 脚本 87 项通过，共 248 项，另有 10 个子测试通过；1 项真实 Core HTTP 联测按其环境开关跳过。首次运行新增测试时修正了测试仓库路径，并将依赖不同 conftest 的测试拆开运行；以下结果为修正后的执行结果。PPT 测试结束不再出现 lazyllm 替身的退出清理错误。2026-09-30 16:53 已执行 `make local-down` / `make local-up`，清理旧进程后服务恢复 ready，前端 HTTP 200；文档动作接口用无效 reference 探针验证已能完成模型配置注入并返回预期 422（`DOCUMENT_ACTION_REFERENCE_INVALID`），没有再次在注入阶段报 globals/LOG 缺失，也未触发模型调用。真实模型生成和完整 PPT 导出尚未因此重新验收。

```bash
python -m pytest -q tests/algorithm/chat/subagent/test_workflow_package_materialization.py tests/algorithm/test_workflow_toolkit.py
python -m pytest -q tests/algorithm/chat/test_workflow_full_trust.py tests/algorithm/chat/test_workspace_authorization_contract.py
python -m pytest -q tests/algorithm/chat/test_subagent_runner.py tests/algorithm/chat/test_remote_executor.py
python -m pytest -q workflows/ppt-workflow/scripts/tests
```

### C07 输入来源交接补充验证

新增真实 SQLite 快照回归覆盖：同名启动输入与步骤产物同时存在时仅绑定产物；产物缺失/失效时不能被启动输入替代；外部列表保留两个 witness；启动输入历史记录不被删除。字段和 workflow 使用通用名称，避免 PPT 特判。远程客户端与执行器 30 项 Python 测试通过，包含结构化错误消息、非 JSON/非协议错误及成功响应兼容。

```bash
cd backend/core
go test ./workflow -run '^TestRuntimeSnapshotUsesDeclaredProducerForStartupNameCollision$' -count=1
go test ./workflow/... ./product ./common ./doc ./skillv2/... -count=1
# 仓库根目录
python -m pytest -q tests/algorithm/chat/test_remote_executor_client_errors.py tests/algorithm/chat/test_remote_executor.py
```

### C08 内部产物可见性补充验证

SQLite 回归同时覆盖有 SubAgent 适配记录和纯 Host 两条路径：显式隐藏材料不进入阶段/最终产物；没有 exposed 的产物及 exposed=true 产物保持可见；未产生 slot revision 的内部原始产物不泄漏；数据库内部材料仍然存在。新增测试 `TestOrdinaryWorkflowHidesInternalSlotsFromPinnedPackage` 通过，同时验证编辑后的单值产物只显示新版、列表的两个条目都保留。最初完整回归发现 `TestOrdinaryWorkflowFinalArtifactsSurviveStagePagination` 失败，原因是同 slot/index、同版本号的旧产物被误合并。推送前已补充兼容规则，保留没有明确版本替代关系的独立记录；原分页测试未改动，最终完整 Go 回归通过。2026-09-30 17:19 已完成 `make local-down` / `make local-up`，状态 ready、前端 HTTP 200。对真实会话只读投影验证：第一步仅显示一份当前分析结果，底图提示词只绑定一份规范化风格产物。临时真实数据库探针未提交进仓库。

```bash
cd backend/core
go test ./workflow -run '^TestOrdinaryWorkflowHidesInternalSlotsFromPinnedPackage$' -count=1
```

### C09 完成评审补充验证

`tests/algorithm/chat/test_subagent_runner.py` 与 `tests/algorithm/chat/test_remote_executor.py` 共 120 项通过。原“评审超时必须失败”测试替换为“超过旧配置截止时间仍等待真实成功结果”；已有主动取消、迟到结果隔离、事件循环不阻塞、产物完整性检查均通过。推送前另将远程错误透传测试合并执行，共 125 项通过。C08 分页回归已在最终 Go 测试中通过。随后已完成 local-down/local-up，状态 ready、前端 HTTP 200；C10 提示词 YAML 解析验证通过。

### C07–C10 推送前最终验证与变更说明

- 输入来源：按固定编译图的 producer 声明绑定步骤产物，消除启动问答与规范化结果重复绑定；保持原始输入历史和外部列表。
- 产物展示：遵守固定包的显式 exposed=false，隐藏内部控制标志；展示当前修订并兼容同版本旧记录，保留列表条目和最终产物分页。
- 错误诊断：保留 Core 的结构化错误 code/message 及 HTTP 状态。
- 完成评审：移除 20 秒独立截止时间及其配置，保留评审结论、产物检查和主动取消。
- 素材提示词：失效图片链接重新搜索不同候选后下载注册，保留成功素材；新提示词用于新 revision，旧会话不自动升级。

最终 `go test ./workflow/... ./product ./common ./doc ./skillv2/... -count=1` 的 38 个有测试 package 通过（另有 5 个无测试 package），包括原最终产物分页回归及 Core 错误目录测试；Python SubAgent、远程执行器和错误透传共 125 项通过；PPT YAML 解析及 git diff --check 通过。最后一项分页兼容修改发生在上一次重启之后，需下次重启 Core 才加载该项修正。完整真实供应商生成/导出尚未重新端测。

### C12 Python lint 验证

使用仓库 Python 3.11 环境及本机临时目录中的 lint 依赖，执行完整 `make lint-python`，algorithm/backend/evo 全范围退出码为 0；5 个修改文件的 AST 对比一致，`git diff --check` 通过。未禁用规则或增加忽略项。

```bash
PYTHONPATH=/tmp/lazymind-installer-review-pytest make lint-python PYTHON=local/build/deps/python/algorithm/bin/python
```

### C13 PPT CI 验证

- Algorithm 全量：4035 passed、17 skipped、21 subtests passed（47.74 秒）；原 CI 的 4 项失败全部通过。
- CI 中原先失败的文件及 speaker notes：19 passed。
- `workflows/ppt-workflow/scripts/tests` 和 `workflows/ppt-workflow/runtime/scripts/tests`：125 passed，12 subtests passed。
- `make lint-python`：通过；`git diff --check`：通过。
- 本机首次全量测试缺少 Pandoc，4 项文档转换失败；CI 配置已有 Pandoc 安装步骤。通过官方发行包补齐与 CI 一致的 Pandoc 3.11 后重新全量执行，不调整生产依赖和 CI 配置。

复现命令（仓库根目录，Python 已安装测试依赖；Pandoc 位于 PATH 或通过 LAZYMIND_PANDOC_PATH 指定）：

```bash
LAZYLLM_INIT_DOC=1 PYTHONPATH=algorithm:algorithm/lazyllm local/build/deps/python/algorithm/bin/python -m pytest tests/algorithm/ -q
PYTHONPATH=algorithm:algorithm/lazyllm local/build/deps/python/algorithm/bin/python -m pytest -q workflows/ppt-workflow/scripts/tests workflows/ppt-workflow/runtime/scripts/tests
make lint-python PYTHON=local/build/deps/python/algorithm/bin/python
```

### C14 PR #793 合并验证

修复后的串行全量验证：#793 分支 3954 passed、17 skipped、21 subtests passed；与本分支合并后 4054 passed、17 skipped、21 subtests passed。Python lint 和 git diff --check 均通过。验证过程先补齐隔离 worktree 的 LazyLLM 子模块，并将并行全量运行改为串行，避免聊天流/并发测试之间的干扰。下述 218 项及两个对照探针为修复前 review 记录。

- 与本分支、最新 upstream/main 分别试算合并：均无文本冲突。
- 隔离 worktree 实际合并 #793 + 本分支后：218 passed（subagent 全目录、runner、workflow review regressions、product solution delivery）。
- 两个额外对照探针确认同名文件覆盖和查询异常缓存问题；现有 218 项测试通过不能覆盖上述风险。
- 没有执行浏览器完整端测或真实 Core/供应商链路。本次探针在 artifact emit 边界读取文件，验证的是发布前内容已被覆盖，不依赖消费者速度。

```bash
python -m pytest -q tests/algorithm/chat/subagent tests/algorithm/chat/test_subagent_runner.py tests/algorithm/chat/test_workflow_review_regressions.py tests/algorithm/chat/workflows/test_product_solution_delivery.py
```

### C15 底图提示词简化验证

136 passed、12 subtests passed：PPT 持久化回归、工作流包脚本测试和 runtime 脚本测试。新增行为测试直接执行 auto 初始化 → 发布两段提示词 → 同 deck 重试 → 消费原始提示词生成两张图片，确认不调用 style/outline 阶段、不要求 style_spec.json 或 outline.json。既有风格选择、指定页替换与插页测试继续通过。供应商调用使用测试替身，真实模型输出长度仍需端测观察。

```bash
PYTHONPATH=algorithm:algorithm/lazyllm local/build/deps/python/algorithm/bin/python -m pytest -q tests/algorithm/chat/workflows/test_ppt_persistent_storage.py workflows/ppt-workflow/scripts/tests workflows/ppt-workflow/runtime/scripts/tests
```

### C16 页面生成恢复与串行调试验证

- PPT 工具、runtime 与持久化相关测试：140 passed，12 subtests passed。`make lint-python` 和 `git diff --check` 通过。
- 新增测试覆盖并发命令状态隔离且 stdout 不被替换、异常后状态恢复；同 Attempt 的整套/低层重入被阻止而新 Attempt 允许重试；等待模型期间发出实时 progress；传入旧 concurrency=8 时仍逐页生成完整两页、保留完整大纲。
- 原跨调用恢复测试增加全部成功后再次执行，确认保留检查点且不再次调用页面模型。
- 真实供应商串行生成尚未验证；未将 mock 测试记作端测成功。

### C18 产品工作流中文参数验证

- 产品工作流与交付契约测试：54 passed。新增发布入口回归，覆盖真实中文绑定、资源包装/JSON 字符串、300 字目标和完整阶段计划，以及模型占位值不能覆盖用户输入。
- 覆盖预检入口接受相同中文选项、空值/default 继续推断样例是否存在、原始绑定不被改写、未知选项在发布任何产物前失败。
- `make lint-python PYTHON=local/build/deps/python/algorithm/bin/python` 通过；`git diff --check` 通过。发布出口使用测试替身，未执行真实模型端测。

复测命令：

```bash
PYTHONPATH=algorithm:algorithm/lazyllm local/build/deps/python/algorithm/bin/python -m pytest tests/algorithm/chat/workflows/test_product_solution_delivery.py tests/algorithm/chat/test_product_workflow_delivery_contract.py -q
```

### C19 工作流触发参数验证

- `test_workflow_selection.py`、`test_product_solution_delivery.py`、`test_product_workflow_delivery_contract.py`：103 passed。
- 回归经过真实 `ModuleTool` 参数校验，再调用触发函数，覆盖原生对象/JSON 字符串两种形式的文本和附件绑定；覆盖无效 JSON、错误类型、未暴露字段在导入或准备会话前被拒绝。
- `make lint-python PYTHON=local/build/deps/python/algorithm/bin/python` 和 `git diff --check` 通过。
- 手动复测：加载修改后的 Chat，使用本次完整企业会议知识助手需求发起新会话；确认初始化工具接收到合法绑定后创建 Session，进入首个 Router，且不再因字符串包装反复报字典类型错误。缺少真正的必填输入时仍应返回等待补充。

### 可重复执行的命令

在 `backend/core`：

```bash
go test ./common -run '^TestCoreErrorConstructorsAreCatalogued$' -count=1 -v
go test ./workflow/... ./product ./common ./doc ./skillv2/... -count=1
go test . -run '^$'
```

在仓库根目录，使用安装好仓库依赖及 pytest 的 Python。模块替身测试分进程，避免互相污染：

```bash
export PYTHONPATH=algorithm:algorithm/lazyllm
python -m pytest -q tests/algorithm/chat/test_workflow_declared_capabilities.py tests/algorithm/chat/test_workflow_selection.py tests/algorithm/chat/test_subagent_runner.py
python -m pytest -q tests/algorithm/chat/workflows/test_product_solution_delivery.py
python -m pytest -q tests/algorithm/chat/test_product_workflow_delivery_contract.py
python -m pytest -q tests/algorithm/chat/test_workflow_review_regressions.py
python -m pytest -q tests/algorithm/chat/test_remote_executor.py
python -m pytest -q tests/algorithm/test_workflow_sdk.py
python -m pytest -q tests/algorithm/test_workflow_toolkit.py
python -m pytest -q tests/algorithm/chat/test_workflow_full_trust.py
python -m pytest -q tests/algorithm/chat/test_image_workflow_v2_tools.py
python -m pytest -q tests/algorithm/chat/test_image_workflow_v2_baoyu.py
python -m pytest -q tests/algorithm/chat/workflows/test_image_workflow_v2_contract.py
python -m pytest -q tests/algorithm/chat/workflows/test_image_workflow_meme_modes.py
python -m pytest -q tests/algorithm/chat/workflows/test_image_workflow_search_validation.py
python -m pytest -q tests/algorithm/chat/workflows/test_image_workflow_caption_renderer.py
python -m pytest -q tests/algorithm/chat/test_image_workflow_prompt_contract.py
python -m pytest -q workflows/ppt-workflow/scripts/tests
python -m pytest -q workflows/ppt-workflow/runtime/scripts/tests
python -m pytest -q tests/algorithm/chat/test_writer_workflow_runtime.py
python -m pytest -q tests/algorithm/chat/test_writer_plugin_draft_stream.py
python -m pytest -q tests/algorithm/chat/test_writer_stream_recovery.py
python -m pytest -q tests/algorithm/chat/test_writer_source_contract.py
```

在 `frontend`：

```bash
node node_modules/vitest/vitest.mjs run --config vitest.workflow-panel.config.ts --maxWorkers=2 --minWorkers=1
node node_modules/vitest/vitest.mjs run src/modules/chat/components/WorkflowPanel/Writer src/modules/chat/components/WorkflowPanel/writer src/modules/chat/components/WorkflowPanel/SlotWriterDocument.test.tsx src/modules/chat/components/TaskCenter src/modules/chat/store/taskCenter.test.ts --maxWorkers=2 --minWorkers=1
node node_modules/vitest/vitest.mjs run src/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor.test.tsx src/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor.real.test.tsx src/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor.table.test.tsx src/modules/chat/components/WorkflowPanel/ArtifactRewriteDialog.test.tsx src/modules/chat/components/WorkflowPanel/ppt/SlotHtmlSlide.test.ts --maxWorkers=2 --minWorkers=1
node node_modules/vitest/vitest.mjs run src/modules/chat/extensions/WorkflowExtensions.test.tsx src/modules/chat/components/WorkflowPanel/ProductProject.test.tsx --maxWorkers=2 --minWorkers=1
node node_modules/vite/bin/vite.js build
```

## 测试：合入前必须执行的真实服务端测

准备配置好 LLM、Seedream `ARK_API_KEY`、图片搜索及含品牌资料的知识库的开发实例；构建本分支并同步这三个工作流的新 revision。保留一份升级前 Writer 文档/会话用于恢复验证。记录每项 session、revision、操作步骤、前后文件和预期/实际结果。

| 场景 | 操作 | 通过条件 |
|---|---|---|
| Writer 旧文档 | 打开已有文档，切换可视/源码模式，编辑表格、代码块、链接、引用，再保存刷新 | 正文和格式保真；原先的历史版本、焦点、展开与恢复行为一致 |
| Writer 定向修订 | 选中文字修订；模拟网络失败/版本冲突后重试；在弹窗外点击 | 只改所选范围；原关闭规则和冲突提示保留；未保存修改不丢失 |
| Writer 流式生成 | 从原入口新建文档，生成中打开编辑区，结束后保存并重开 | 原来的流式展示、草稿恢复及完成状态一致；没有产品导航工具干扰 |
| 产品正文 | 生成/定向修订含 `accepted`、`null`、URL `/accepted`、WEB 来源和 Mermaid 的正文 | 业务值、URL、证据事实不被展示层改写；定向修改不破坏未选中内容 |
| 产品决定 | 留一个硬阻断提案未处理，尝试进入下一阶段；确认/暂缓后刷新；结束项目 | 未处理时拦截跨阶段；暂缓仍是草稿；finish 不自动批准；刷新保留明确操作 |
| 产品并发 | 两个页面打开同一提案，另一页修改提案后在旧页确认；重复提交同一命令 | 旧版本明确冲突；不自动批准新提案；重复命令不重复接受/创建阶段 |
| 图片上传编辑 | 上传一张带唯一标记的横图，只要求改帽子颜色 | 编辑使用该图；身份/背景/画幅保留；下载源图不被覆盖；缺图/多图明确失败 |
| 搜索与 KB | 分别制造 KB 失败/Web 成功、KB 成功/Web 失败、双方无结果或异常 | 保留并使用成功来源；失败/空结果分别记录，不直接使准备步骤失败；双方失败时明确无有效参考，不虚构来源事实 |
| 图片需求延续 | 初始 16:9，先“继续”；准备时改 9:16 后再“继续”；修改表情包数量及逐字字幕 | 不回到默认方图；已保存修订仍生效；最终数量/字幕与修改一致 |
| 编辑极端比例 | 普通横/竖/方图和 8:1 源图分别局部编辑 | 普通比例不变形；不支持比例明确拒绝；用户指定支持的比例后可运行 |
| Mermaid | 有是/否分支和回边的流程图；另测 subgraph、sequence loop、虚线箭头 | 支持图保留条件；不支持语法完整源码可见，无缺边假图 |
| PPT | 选风格生成，取消/重新选择多元素后局部修订，预览并导出 | 不误关 PPT 选择弹窗；修改范围正确；未选页/元素保留；导出可打开 |
| 固定包与复制包 | 改 workflow ID 后重复输入、阶段切换、发布、编辑、下载；在阶段间发布一个新 head | 行为来自声明；下一阶段仍使用 source revision，Ready 为实际图结果；无重复新 session |
| 发布失败与重试 | 让 finalizer 缺一个双格式文件或让存储中断，再修复重试 | 失败不替换已发布产物；成功一次性切换；旧版本可读且没有重复发布 |
| 旧图片工作流 | 明确选择原 `image-workflow`，用原配置生成并添加字幕 | 原供应商路线和字幕逻辑仍可用，未被 V2 自动替代 |

真实供应商调用、完整浏览器端到端操作和真实导出文件的人工验收尚未执行。上述项目全部完成并记录结果后，再判断是否可合入主仓库。
