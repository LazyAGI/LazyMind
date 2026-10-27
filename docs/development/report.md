# LazyMind Windows 问题清单核查

> **本机最新补测见文末“Windows 原生补测与配置编码修复”**：已切换至 `cst/ppt_template`。新增确认并修复 2 项主仓库问题：UTF-8 配置中文值解码、SQLite 认证迁移中断后的半完成状态；另移入已有 Windows doctor 诊断。历史表格不代表本轮 Windows 验证结果。

## 当前结论：归因、已解决项与剩余待办（2026-10-26）

本节及 41 项表的“当前归因与处理状态”列为汇总结论；后面的 upstream 初次复查、macOS 补测和 Windows 实测是不同时间、不同版本的证据，不能把历史“仍存在”当成当前未修复，也不能把单元测试通过当成整套部署通过。当前工作分支为 `cst/ppt_template`，基线 `7154e8451`，锁定 LazyLLM `abf630b8`；本轮新增修复、测试与报告随本次提交交付，未修改子模块指针。没有重新核对此后变化的 upstream main。

### 哪些是我们的问题，哪些不能归到项目源码

按本报告已经定位并实施修复的具体缺陷统计，**主仓库 6 项：原清单 #4、#7、#13、#21、#35，加本轮新增的认证库迁移中断问题；依赖 LazyLLM 2 项：#16、#32**。这不是“其余 36 项都没有问题”的结论：部分是设计行为，部分是环境问题，还有部分缺集成证据。原清单条目可能同时含代码与环境两个子问题，例如 #4 的破坏性重试已修，但 PE 安装错误不能一起记为已修。

**目前确认由用户自行修改代码导致的故障为 0 项。** 尚未取得对应用户修改的完整 diff、实际运行版本及同输入对照复现，不能直接写成“都是用户改坏的”。以下只指出归因边界及优先排查方向：

| 归因 | 对应问题 / 场景 | 判断与下一步 |
|---|---|---|
| 主仓库代码缺陷 | #4、#7、#13、#21、#35；新增认证迁移中断 | 已有具体源码和复现/回归证据，见下表；不能推给用户自改代码。 |
| 上游依赖缺陷 | #16 URL、#32 rerank 类型 | 位于 LazyLLM；当前分支已有修复 pin，本轮未修改子模块。 |
| 环境、依赖或部署配置 | #1 网络、#2 手动 GOTMPDIR、#5 安装、#6 重复分发信息、#14 TEMP、#15 混用配置、#20 改密未同步、#38 token/ACL；本轮内存耗尽及 WSL share 锁失败 | 不能直接认定业务代码 bug。结合实际环境、依赖版本、配置与原始日志排查；其中部分历史现象在本机未复现。 |
| 可能与用户自改启动链有关，尚未证实 | #9 自定义 worker/嵌入环境、#10 DSH PYTHONPATH、#15 自定义 .env 加载、#21 清单中的 start-local.ps1 | 清单中的 DSH、盘符路径和部分脚本不属于已核对启动链。需对比用户 diff、启动命令和实际导入路径。#21 仓库自己的 UTF-8 读取缺陷另有证据且已修，不能混为用户问题。 |
| 原描述过宽、已有实现或设计行为 | #3 isolated 必失败、#8 已有参数文件、#17 本机自动登录限制、#19 CORS 白名单、#22/#28 原生 Office/表格读取、#27 权限分工、#36 动态 URL 注入、#37 preparing 过滤、#40 会话隔离、#41 目录只剩旧型号 | 不能按原描述直接认定缺陷；仍须区分具体失败路径、模型权限和集成结果。 |
| 条件风险 / 尚待定位 | #11 软锁、#12 指定 Caddy 构建、#18 远程登录、#23–#26 Office/GUI、#29 PID、#30/#33 大表与事务、#31 源码 Milvus、#34 状态恢复、#39 真实写文件 | 没有足够证据全部关闭，也没有证据全部归因用户。按待测表补齐复现后再决定是否改代码。 |

若需确认“用户自改导致”，应固定主仓库和子模块 SHA，以相同配置、依赖和合成输入分别运行干净源码与用户修改版，逐步缩小 diff。仅用户修改版失败且可定位到具体改动时，才将该项改为“用户自改代码导致”；环境变量、安装包与源码版本不一致要单独记录。

### 我们已经解决了什么

| 问题 | 已做修复 / 当前交付状态 | 已验证范围与未覆盖边界 |
|---|---|---|
| #4 依赖安装失败清空旧 venv | 已合入当前分支：已有解释器时失败保留 venv | Windows manager 回归通过；不等于原子升级，也不代表 PE 安装错误消失。 |
| #7 SQLite 每请求新建连接 | 已合入当前分支：每个 Connection 复用连接，失败不自动重试写请求 | Windows 原生服务 100 次 SELECT 使用 1 个客户端端口；端口耗尽与长期负载未验收。 |
| #13 hoisted 被误判未就绪 | 当前分支已有修复：按 nodeLinker 检查，保留 isolated 校验 | Windows 目录判断/保留回归通过；真实 hoisted 冷安装启动仍待测。 |
| #21 UTF-8 配置中文值读错 | 本轮新增修复：PowerShell 配置读取显式 UTF8 | PowerShell 5.1、代码页 936 真实复现及修复后回归；不是所有注释后的键都丢失。 |
| #35 多 JSON 分类解析失败 | 已合入当前分支：提取第一个完整 JSON，保留 schema 校验及回退 | 分类回归通过；不承诺任意模型输出格式都可解析。 |
| 新增：SQLite 认证迁移中断 | 本轮新增修复：结构和版本号放入显式 SQLite 事务 | Linux、Windows、Docker 实际中断/重启回归通过；认证测试 225 passed、27 subtests passed，PostgreSQL 真实升级 smoke 通过。不会自动修复已经半迁移的旧库。 |
| #16/#32 LazyLLM URL / rerank | 当前分支锁定版本已含其他轮次提交的修复 | 报告已有真实 SiliconFlow chat/embed/rerank 结果；不能外推为所有供应商及知识库全流程通过。 |
| Windows doctor 诊断补充 | 将 cst/fix_1026 已有修复移入当前分支 | NTFS module 读取通过，当前 WSL share 失败有明确提示；改善诊断，不是修复 Windows/WSL 文件系统锁实现。 |

### SQL 与检索部署方向：优先 Docker + PostgreSQL + OpenSearch

按用户要求，**面向大知识库和并发使用的后续部署及性能验收，优先采用 Docker：业务关系数据使用 PostgreSQL，分段存储/检索使用 OpenSearch，不把 SQLite/SQLite HTTP proxy 作为这条部署路线的业务数据库**。目标是改善并发吞吐与大数据量处理；本轮没有同负载性能对比，不能承诺具体提升倍数，也不能声称切换后所有卡顿都会消失。

PostgreSQL 和 OpenSearch 分工不同，OpenSearch 不是 SQL 数据库的替代品。仓库 `docker-compose.yml` 已有 PostgreSQL db 服务及 Core/Auth 等连接配置，分段存储配置支持 `LAZYMIND_SEGMENT_STORE_TYPE=opensearch`、`LAZYMIND_SEGMENT_STORE_URI_OR_PATH`；OpenSearch 服务属于 `opensearch` profile，需要实际启用。向量库按工作流所需另行配置，不能将这两项当成全部依赖。

| 范围 | 后续要求与验收 |
|---|---|
| Docker 实际配置 | 检查 compose 展开后的 Core、Auth、算法等有效数据库地址，确认业务 SQL 走 PostgreSQL；启用 OpenSearch profile 并检查分段存储实际地址，排除遗留 local 配置将服务指回 SQLiteStore。报告不存放连接密码。 |
| 本地 SQLite 问题的边界 | #7 的连接、#30 的 SQLite 请求大小及 #33 的 SQLite proxy 空闲事务 TTL 需要在 SQLite 路线单独评价；业务 SQL 改走 PostgreSQL 后不能沿用这些限制解释新链路。但慢 embedding、任务恢复、超大切块仍要独立验收。#11 内部 SQLiteQueue 不保证随业务数据库切换而消失。 |
| 已有数据 | 当前只是部署建议与待办，没有执行数据迁移、删除 SQLite 库或切换用户实例。后续如迁移，需验证业务记录、账号/权限、附件引用与检索索引的一致性，并保留可恢复的原数据。 |
| 性能验证 | 在相同数据、模型、机器资源及并发条件下，记录入库耗时、检索吞吐、p95 延迟、失败率和内存占用。不能通过降低检索质量、减少处理数据或跳过失败样本宣称提速。 |
| 本轮证据边界 | PostgreSQL 16 隔离迁移测试通过；此前 Docker 测试用过 SQLiteStore / Milvus Lite 替代部分依赖，**并未完成 PostgreSQL + OpenSearch 整套入库检索与性能验收**。该项列为优先待办。Docker 也消耗宿主内存，不能把本轮 OOM 当作更换存储即可解决。 |

### 用户截图补充：#39 / #40 为使用阻塞，尚未解决（2026-10-26）

用户截图报告：write/ls/stat/glob 在其环境不可用；kb_search 报 `No source is configured for dynamic embedding source`，语义召回不可用。这两项按**用户报告的使用阻塞、优先定位**保留，不能用现有局部测试通过或“设计行为”将故障关闭。截图不是本机重现记录，也不足以确认所有拟定根因，更不能归咎于用户修改代码。本轮没有修复这两条完整故障链。

| 截图中的问题 | 当前代码核对 | 尚需定位 / 验收 |
|---|---|---|
| #39-1 前端 workspace_permission_mode 只在后台运行时传 | `ChatInput/index.tsx` 的相应发送分支确有 runInBackground 条件；但 `LocalWorkspaceControl.tsx` 另行保存用户权限，`backend/core/chat/conversation_logic.go` 创建会话时读取 UserPermission。因此“该字段没随普通消息发送”不能单独证明 allow_all 丢失。 | 对照普通聊天/后台任务、新会话/已有会话：界面选择、权限保存响应、服务端实际模式和版本、Chat 收到的权限上下文。确认丢失位置后修复，不盲目取消全部权限判断。 |
| #39-2 prepare_host 要求 history_id/run_id | 算法侧 `workspace_authorization.py:prepare_host` 需要活动授权、用户/会话身份，并接受 history_id + run_id **或** task_id + generation；不是所有路线都只接受前一对。`chat_service.py` 已构造前台 execution identity，runner 为任务注入对应身份。 | 沿真实失败请求逐段核对 Core → Chat → 工具，确认缺失/错配字段及发生的请求路径。若正常链路漏传，这是应修的项目问题；身份校验本身不能直接删除。 |
| #39-3 file 工具 ALLOW 路径卡死 | 用户报告尚未在本机复现。当前实现区分 allowed 决策、claim 后 executing 和工具执行；`wait()` 会轮询 pending/preparing，不能仅凭 UI 显示 ALLOW 确定阻塞位置。 | 保存脱敏 operation_id、状态/decision、expires_at、claim 请求及响应、工具开始/结束时间和取消日志。定位在授权等待、claim、文件 IO 还是结果返回；正常授权下 write/ls/stat/glob 均须真实完成，拒绝与取消也须正确结束。 |
| #40 kb_search 动态 embedding 无 source | 当前 LazyLLM dynamic_router 在实际 embed 配置 bucket 取不到 source 时抛出截图错误。它证明**此次调用没有取得有效 source**，不证明全局一定没配置 embedding，也不是 PostgreSQL/SQLite 错误。 | 分别检查：角色确实未配置/未启用；配置存在但用户/工作区/模型角色选择不匹配；Core 到 Chat 的配置注入或并行/子任务上下文传播丢失。以不含密钥的角色标识、source 是否存在及调用阶段判断根因。用实际 kb_search 检索已入库合成文档并返回相关片段/引用；缺配置时应明确指向配置入口，不能卡住或假装召回成功。 |

截图给出的临时绕过也仅按用户描述记录：#39 使用“新建任务 + shell + Python 写文件”；#40 使用 read_document 逐篇阅读 PDF。这些不是本轮完成的修复或保证，不应绕过工作区权限；逐篇读取不等价于知识库语义召回。

**Docker + PostgreSQL + OpenSearch 不会自动解决这两项。** #39 的授权/执行身份链仍需正确；#40 的语义检索仍需有效 embedding 模型配置及请求上下文。推荐部署路线应把这两项加入端到端验收，而不是以数据库切换替代修复。

### 尚未解决 / 尚未完成测试的交接清单

“已阻塞”表示已经执行但没有通过；“待验证”表示证据不足，不等同于确认代码缺陷。下表覆盖本轮对话列出的未完成项；高优先级先验证推荐 Docker 路线，再继续 Windows 原生兼容性验证。

| 优先级 | 问题 / 关联编号 | 当前状态及已有证据 | 剩余动作与通过标准 |
|---|---|---|---|
| P1 | Docker PostgreSQL + OpenSearch 完整链路 | 待验证；已有服务定义及 PostgreSQL 迁移 smoke | 实际启用所需 profiles，确认有效连接与索引后端；完成上传、入库、检索、重启及并发性能对比，保存结果。 |
| P1 | Windows 完整启动、前端与 Chat | 已阻塞；Auth/Core/Proxy/SQLite 和自动登录曾返回 200，前端 3 GiB 堆仍耗尽，RAG pip MemoryError | 准备足够内存余量，分阶段安装与构建；完整启动、重启、页面登录、聊天及文件操作通过。现有健康结果不等于全栈通过。 |
| P1 | 大 XLSX → 切块 → 慢 embedding → 入库 → 检索（#28/#30/#33/#40） | 待集成；大 CSV 预览、请求分批和事务续期已有局部验证 | 用合成大 XLSX 与受控慢响应验证完整链路，检查超大单行、失败重试及最终可检索性；推荐存储路线和 SQLite 路线分别记录。另核对公式缓存、隐藏行和合并单元格。 |
| P1 | 文档 WORKING / DELETING 中断恢复（#34） | 待故障注入；已有局部重试与清理测试 | 在有实际任务的隔离库中中断 worker/Core，重启后能恢复、重试或清理，不长期卡状态、不误删有效数据。认证迁移中断测试不能替代此项。 |
| P1 | 公网登录、Origin 与改密（#17–#20） | 待浏览器集成；403、CORS、认证失败边界已有测试 | 远程入口完成正常登录、退出、过期刷新和人工登录回退；明确不支持的自动登录路径；改密后同步配置并验证刷新/重登，不出现路由循环。 |
| P1 | 真实模型文件写入与权限（#27/#37/#39） | 待集成；权限与准备状态回归通过 | 在隔离工作区用真实模型验证 allow_all、拒绝、审批与 execution identity，检查文件实际结果、权限边界和卡住时的 run/operation 状态。 |
| P2 | Python 冷安装、升级与真实 PE 故障（#4–#6） | 部分通过；venv 已创建、失败保留有回归，RAG 未安装完成 | 冷安装与升级成功，pip/uv check 和 NumPy/Arrow 数据互转通过；实际导入 faiss/Stemmer/fontTools；真实 PE 安装失败后原解释器仍可用，无重复 dist-info。 |
| P2 | Windows isolated/hoisted 与 TEMP（#3/#13/#14） | isolated 安装完成，hoisted 仅目录判断回归；构建 OOM | 独立目录分别安装两种布局，含中文/空格且可写的 TEMP，构建和页面访问成功；区分内存、权限和缺包错误。 |
| P2 | Relay/DSH/嵌入 worker（#8–#10） | 仅参数文件与环境构造测试、普通子进程导入验证 | 实际超长参数调用，检查 worker executable/prefix/依赖与 PYTHONPATH；用用户自改版和干净版对照，排除旧路径泄漏。 |
| P2 | 指定 Windows Caddy 构建（#12） | 待验证；不能以版本号或前端 OOM 推断 Caddy panic | 取得出错产物及 Go build 信息，普通桌面启动并重复访问首页/代理；保存 panic 栈或成功结果。 |
| P2 | Windows Office 原生转换（#23–#25） | Windows 未完成；Docker/macOS 已有部分成功转换 | 配置 LibreOffice/字体，源码及桌面分别测试中文/空格路径 DOCX/XLSX/PPTX、并发、超时和重试；PDF 正确、无残留占锁，不误杀用户 Office。 |
| P2 | GUI、token/ACL 与 PID（#26/#29/#38） | Windows 进程管理回归通过；GUI/真实身份竞态待测 | 普通桌面选择普通/根/中文目录及取消；检查实际进程完整性和读写；隔离环境验证身份不匹配时不杀进程，不能用管理员 shell 代替普通入口。 |
| P2 | Windows Milvus（#31） | 补丁脚本 10 项通过；真实安装/数据未验收 | 核对桌面和源码 wheel、补丁是否实际应用；临时数据反复更新 manifest/索引并重启读取，不出现 WinError 183 或损坏索引。 |
| P2 | SQLite 长时间运行与软锁（#7/#11/#33） | 连接复用和短时事务测试通过；部分 filelock 版本崩溃后可恢复 | 保留本地模式时，验证长时间空闲重连、并发/端口负载、不同 filelock 版本与异常退出、超过空闲 TTL 的业务行为；失败写请求不能被不安全重放。 |
| P2 | 真实供应商与角色（#16/#32/#36/#40/#41） | 已有 SiliconFlow 小请求；不是全部供应商验收 | 核对实际选定账号/模型、动态 URL、自定义前缀、模型权限、embedding 角色到检索结果；补 SenseNova 对应请求及图片协议场景，不读取或发布用户密钥。 |
| P2 | GLM 图片与 PPT 工作流 | 局部工具/排序/导出回归通过；真实模型全流程未完成 | 真实 GLM 出图、素材搜索配置异常恢复、主 Chat 限定范围编辑、增删插入/重排页、关联素材及重新导出 PPTX；检查内容、页序、文件与多会话隔离。 |
| P2 | 已半迁移的认证库 | 新代码可防止后续中断半迁移；旧现场没有自动修复 | 对旧库另做备份和结构/版本核对，制定可验证的恢复方案；不得直接 stamp head 或删除用户库。本轮中断现场仍保留。 |

已跳过或未覆盖的测试也不能记为通过：Windows manager 主包有 4 项 skip（外部发布包 opt-in、8090 占用、PATH 无 sleep、Unix chmod 专用），按对应平台与条件补测；本轮也没有完成完整 CI 平台矩阵。全部待办使用隔离数据和合成样本，并记录主仓库/子模块 SHA、实际安装方式、依赖版本、命令及脱敏日志。

> **2026-10-26 upstream main 复查说明（北京时间）**：本次已执行 `git fetch upstream main`，核对主仓库 `04e5c4bef8e134483611ef48040418e690a2fd4b` 及该提交锁定的 LazyLLM 子模块 `fd4465ec8a6922d9cb1c88e509ec45650ac2e1ed`。复查阶段使用独立源码快照；后续按用户要求创建了 `cst/fix_1026` 分支并实施修复，见文末记录。LazyLLM 子模块没有改动或提交。工作区未找到 `question_list.md`，`test_LIST.md` 为空，因此以本报告已有的 41 项为复查输入，无法核对缺失清单中的额外细节。
>
> **初次复查阅读方式**：下表“初次复查”列是固定上游基线的历史判断；最新汇总见“当前归因与处理状态”列及文首，其余四列及后续 Windows 部署记录保留为 **2026-10-22 历史报告**，其中“本机”“已复现”“健康检查”等不表示本次执行结果。此次运行环境是 Linux，采用源码追踪、离线最小复现和针对性的既有 Go 测试；没有启动 Windows 桌面、真实模型请求或大文件导入。源码位置均相对上述锁定版本；标注“子模块”的路径相对 `algorithm/lazyllm/lazyllm/`。
>
> **初次复查结论（修复前，非当前状态）**：#16 URL 丢路径、#32 动态 rerank 地址错误、#35 多 JSON 解析失败均仍存在；#7 不复用连接已复现，但耗尽未证明；#4 安装失败重建旧 venv 的代码风险仍在。特别纠正历史报告 #23：仓库存在 Office 转换服务源码；#30 的请求限制应区分客户端 15 MiB 与服务端 16 MiB。#35 有规则回退，不能等同于整个聊天崩溃。

核查日期：2026-10-22（北京时间）。源码：`C:\Users\bujiadong.vendor\LazyMind`，主仓库 `7683f8ec`，LazyLLM 子模块 `655cb3ba`。对照用户提供的 41 项清单。

## 核查范围与结论

这份清单不能原样套用到本机。它描述的 `D:\agent\LazyMind`、DSH、F 盘工作区和某些自定义脚本不属于本次源码运行环境。

本机最初运行的是安装目录中的桌面版，使用已打包的 LazyLLM 1.3.0；更新源码没有更新该安装包。按用户后续要求，已关闭桌面应用及其后台进程，并在 `.lazymind-local\audit-20261022` 建立独立的源码验证环境。没有改动桌面数据库，没有下载 Tutorial 子模块。

**已复现的源码问题**包括 URL 拼接丢失 `/v1`、SiliconFlow 动态重排误用 embeddings 路径、任务分类处理多段 JSON 失败、SQLite 代理不复用连接。**本机环境问题**包括源码算法 venv 中存在两份 pyarrow 分发信息，以及 uv 安装可执行入口时发生 Windows PE 资源更新错误。

另一些条目已经改变：Excel 有原始文件读取路线，动态模型地址会在调用时传递，`preparing` 不是可立即审批的状态，权限模式也会在服务端保存。

状态含义：**确认**表示有本机或最小复现证据；**条件风险**表示相应代码仍在，但没有证明本机发生原清单中的完整故障；**未发现/已改变**不代表对所有环境作保证；**待验证**表示证据不足。

## 41 项逐项结果

| 编号 | 清单问题 | 本项目结果 | 证据与适用边界 | 初次复查：upstream main（2026-10-26） | 当前归因与处理状态（汇总） |
|---|---|---|---|---|---|
| 1 | Git / submodule 必须代理 | 未发现当前故障 | 本次已成功获取主仓库和 LazyLLM 更新。GitHub 连通性属于环境条件，不能据此要求所有部署配置代理。Tutorial 保持禁用。 | **环境项，不能认定上游缺陷。** 本次 `git fetch upstream main` 成功；代理需求取决于网络。未做新的子模块网络下载。 | 环境/网络；未确认源码缺陷。 |
| 2 | GOTMPDIR 不存在导致 Go 构建失败 | 未复现所述故障 | 当前 `runtime_env.go` 使用宿主 GOCACHE/GOMODCACHE，不再看到清单描述的默认 `build/cache/go-tmp` 设置。本次源码 manager、Core、代理已编译。验证脚本使用独立 Go 临时目录。 | **未发现清单所述默认配置。** `local/local-runtime-manager/runtime_env.go:37` 设置宿主 Go 缓存，未设置 GOTMPDIR；上游快照的相关 Go 测试可编译运行。用户自行指定不存在目录仍属环境错误。 | 未发现默认配置缺陷；手动变量/自改启动链需对照。 |
| 3 | Windows pnpm isolated 必然缺依赖 | 未发现当前故障 | 本机实际为 isolated，frontend/node_modules 是指向 local/build 的 junction。Vite 启动、已有前端测试和转换均成功，不能把 junction 本身认定为根因。 | **“isolated 必然失败”不成立。** `local/local-runtime-manager/frontend.go:238` 按 pnpm 虚拟目录检查依赖；链接本身不证明缺包。本次未在 Windows 安装 pnpm，原机器的成功记录不作为本次结果。 | 原“必失败”不成立；Windows isolated 安装已做，构建受内存阻塞。 |
| 4 | Python 安装、缓存、并发问题 | **确认本机安装阻塞；部分机制已有保护** | uv 安装 LazyLLM 1.4.0 时 `Failed to update Windows PE resources`；官方失败回退随后清空并重建源码算法 venv，种子包安装再次失败。`acquireAlgorithmPythonLock` 已用锁串行化共享 venv 的算法安装。详见后文。 | **代码风险确认，PE 故障待 Windows 验证。** `local/local-runtime-manager/algorithm_service.go:280` 在任意安装错误后调用 `createVenv(..., true)`，仍可能破坏旧环境；同文件的共享安装锁已存在，不能再断言没有并发保护。 | 主仓库破坏性重试已修；真实 PE 错误仍待验证。 |
| 5 | faiss / pystemmer / fonttools 安装坑 | 未复现所述特定错误 | 本次 pip 重建成功安装 pystemmer 3.1.0。faiss/fonttools 所述故障未复现，Milvus/RAG 可选组件未做完整部署；没有应用 `--no-deps` 或关闭隔离构建。 | **环境/依赖安装项，待验证。** 本次没有在 Windows 安装 faiss、pystemmer、fonttools，无法确认原特定错误，更不能凭旧机器的成功安装判定最新上游全部正常。 | 环境/依赖待验；Windows RAG 安装因 MemoryError 未完成。 |
| 6 | NumPy 与 pyarrow 冲突 | **确认环境异常，未确认清单中的 ABI 因果** | 启动前 uv pip check 报 `pyarrow-19.0.1.dist-info` 与 `pyarrow-26.0.0.dist-info` 同时存在；NumPy 分发为 1.26.4。重复分发信息不等于已证明实际加载 pyarrow 26，也没有复现原清单中的 NumPy ABI 崩溃。 | **环境项，未确认上游 ABI 缺陷。** 重复 dist-info 是原机器状态；本次未取得该 venv，也未复现 NumPy/pyarrow ABI 错误。 | 历史环境异常；未确认上游 ABI 缺陷，Windows 完整依赖待验。 |
| 7 | SQLite HTTP 连接耗尽 | **确认连接未复用；耗尽未压测** | 实际调用 `sqlite_proxy.Connection._call` 12 次，服务端看到 12 个客户端端口，Connection 均为 close。仍使用 urllib.urlopen；没有诱发本机 socket 耗尽或写操作重试。 | **连接不复用已复现；端口耗尽未证实。** `algorithm/lazymind/common/database/sqlite_proxy.py:342` 仍逐次 urlopen；本次 12 次真实 `_call` 对临时 HTTP 服务产生 12 个客户端端口，均为 Connection: close。 | 主仓库连接不复用已修；耗尽及长期压测未完成。 |
| 8 | Relay 命令行超长 | 已有 Windows 处理 | `algorithm_service.go` 在 Windows 设置 `LAZYLLM_PASS_ARGS_BY_FILE=1`；Relay 支持参数文件。此处无需重复加同一开关。 | **已有处理。** `local/local-runtime-manager/algorithm_service.go:761` 为 Windows 设置参数文件开关；锁定子模块 `components/deploy/relay/base.py:16` 支持该开关。没有复现其他来源的命令行超长。 | 已有参数文件机制；真实 Windows 超长 Relay 待验。 |
| 9 | 源码 worker 缺 site-packages | 未复现；源码没有所述兜底 | 缺少组件 catalog 时，`pythonComponentSites` 返回 nil，environment 没有清单建议的 venv site-packages 回退。但 Relay 使用 sys.executable，实测源码 venv 的普通子进程保留同一 prefix 并可找到 requests。不能仅凭 catalog 缺失就断言 worker 一定丢包；完整 Relay/DSH 场景未测。 | **缺兜底代码仍在，但不足以认定 worker 缺包。** `local/local-runtime-manager/python_components.go:132` 无 catalog 返回 nil；Relay 使用 `sys.executable`（子模块 `components/deploy/relay/base.py:86`）。嵌入式/DSH 环境仍需专门验证。 | 未确认正常源码 worker 缺包；用户自改/嵌入链待对照。 |
| 10 | DSH 的 PYTHONPATH 泄漏 | 本机未发现 | 当前会话无该 DSH PYTHONPATH。算法服务会显式构建 PYTHONPATH，网关还会清理继承的 PYTHONHOME/PYTHONPATH/VIRTUAL_ENV。没有 `D:\LazyLLM` 泄漏证据。 | **未证实 DSH 泄漏。** `local/local-runtime-manager/algorithm_service.go:620` 构建算法 PYTHONPATH；`channel_gateway_service.go:183` 清理继承的 Python 环境变量。外部 DSH 启动链不在本次复现范围。 | 未证实泄漏；DSH/用户启动链待对照。 |
| 11 | SQLiteQueue 软锁死锁 | 条件风险，遗留锁行为已复现 | 仍用 SoftFileLock、共享 `.lazyllm_filesystem_queue.db`，没有 60 秒获取超时。临时样本中预存锁文件让 SoftFileLock 超时，FileLock 可获取未持有的同名文件锁；没有复现应用运行中的死锁。多个进程共享锁本身是正常同步。给数据库加 PID 后缀可能改变跨进程队列语义。 | **遗留软锁风险仍在，应用死锁未复现。** 锁定子模块 `common/queue.py:131` 仍使用共享 DB 和 SoftFileLock，未设置有界获取超时。本次 Python 环境没有 filelock，未重跑遗留锁实验；保留跨进程同步语义。 | 依赖/条件风险；新 filelock 局部可恢复，不等于所有版本无问题。 |
| 12 | Caddy 2.10.2 panic | 未复现指定构建问题 | 源码默认仍为 2.10.2；本机已有 caddy 的 Go 构建信息为 go1.25.11，不是清单的 go1.27.2。本次源码前端由 Vite 运行，没有证据支持“版本号相同就会 panic”。 | **仅确认版本仍为 2.10.2。** `desktop/scripts/build-windows-x64.ps1` 固定该版本；版本号不能证明指定 Go 构建的 panic。本次未执行 Windows Caddy。 | 指定构建待验；不是确认的当前 Caddy 故障。 |
| 13 | manager 不兼容 hoisted | 条件风险，当前未触发 | `prepareFrontendNodeModules` 仍建立链接；`frontendNodeModulesReady` 要求 .modules.yaml 和匹配的 virtualStoreDir。当前是可用的 isolated 环境；没有将其改成 hoisted。本次直接启动 Vite，避免为核查而重建现有依赖树。 | **布局兼容风险仍在。** `local/local-runtime-manager/frontend.go:238` 要求 `.modules.yaml` 含匹配的虚拟目录路径；不满足该条件的 hoisted 布局会被判未就绪。未复现完整 hoisted 启动失败；链接相关 Go 测试通过。 | 主仓库 hoisted 就绪判断已修；实际冷安装启动待验。 |
| 14 | esbuild TEMP 写入被拒 | 未复现所述故障 | Vite 和现有前端测试可执行。源码验证使用独立 TEMP/TMP；uv 的 PE 更新错误另列 #4，不能等同于 esbuild 缓存权限错误。 | **环境项，待 Windows 验证。** 本次没有运行 Windows esbuild，无法将 TEMP 权限拒绝认定为上游通用缺陷。 | 历史 TEMP 拒写未复现；本轮构建失败有内存证据。 |
| 15 | Docker .env 污染本地模式 | 未发现当前污染 | local/config.env 使用 SQLiteStore、pipeline；算法环境由 manager 提供 SQLite proxy 和本机向量服务地址。没有把现有库删除或清空。 | **配置项，未确认上游必然污染。** manager 的 SQLite alias 环境相关 Go 测试通过；不代表用户现有 `.env` 无污染，需结合实际启动配置判断。 | 配置/自改加载链待核对；未确认上游必然污染。 |
| 16 | SenseNova base_url 拼接 | **确认 URL 问题** | 实际调用源码 `_get_chat_url`：`https://token.sensenova.cn/v1` 变成 `https://token.sensenova.cn/chat/completions`，加末尾斜杠后保留 /v1。图片协议分支需要具体供应商请求验证，本次未发送模型请求。 | **确认仍存在，离线复现。** 锁定子模块 `module/llms/onlinemodule/base/onlineChatModuleBase.py:78` 的 urljoin 将无尾斜杠的 `/v1` 丢弃；有尾斜杠正常。图片协议分支未验证。 | LazyLLM 缺陷已由当前 pin 修复；非本轮主仓库修改。 |
| 17 | 公网与本地自动登录冲突 | 设计边界，本机未触发 | auto-login 默认只允许 loopback；本机 LAN 自动登录开关为 false，验证通过本机前端和代理入口，没有部署公网穿透。Chat 的监听地址另见部署说明。 | **设计限制。** `local/local-proxy/internal/server/admin_session.go:23` 默认拒绝非 loopback 自动登录；公网使用不能假设继承本机自动登录，不等于鉴权缺陷。 | 自动登录设计边界；公网整体验收待做。 |
| 18 | 公网登录降级/循环 | 条件风险仍在 | localSession.ts 对非 2xx 统一抛错；local 模式 /login 直接重定向聊天，仍保留 localSessionInitialized。清单描述的远程场景有依据，但本次没有公网穿透部署，不能声称已复现完整循环。 | **远程登录降级风险仍在，循环未端到端复现。** `frontend/src/runtime/localSession.ts:81` 非 2xx 统一报错，`frontend/src/router/index.tsx:104` 在本地模式将 /login 重定向聊天。失败后的远程人工登录路径仍需验证。 | 条件风险；完整浏览器登录循环待定位。 |
| 19 | 公网 Origin 被 CORS 拒绝 | 配置行为 | 默认白名单是 localhost / 127.0.0.1 / 配置的 LAN origin，没有用户清单中的公网域名。本次未部署该域名；拒绝未知 Origin 是当前配置预期。 | **预期配置行为。** `local/local-proxy/internal/config/config.go:101` 默认只允许本地 Origin；公网域名须显式配置，拒绝未知 Origin 本身不是 bug。 | 白名单配置行为；实际公网域名待验。 |
| 20 | 改 admin 密码后自动登录失败 | 条件风险 | 代理仍读取 bootstrap 用户名/密码获取或刷新管理员会话。改密后若缓存会话无法续期、环境仍是旧密码，会失败。本次没有修改现有用户密码。 | **条件故障仍在。** `local/local-proxy/internal/auth/admin_session.go:58` 刷新失败后用配置密码重新登录；改密但未同步配置会使重新登录失败，缓存仍有效时不一定立即发生。 | 改密与 bootstrap 配置不同步的条件故障；未自动解决。 |
| 21 | 中文注释 + GBK 吞配置 | 本机不适用 | 本机 .env、local/config.env、local-win.ps1 全部为 ASCII；系统默认代码页 936 下与 UTF-8 读取的配置键一致（21 / 123 项）。清单的 start-local.ps1 不在当前源码中。 | **待 Windows 配置实测。** 原报告的 ASCII 文件和代码页记录不能外推到最新部署；本次未用含中文注释的配置在 GBK PowerShell 下复现吞键。 | 主仓库 UTF-8 中文值解码已修（本轮提交）；非全部吞键。 |
| 22 | 缺 office-convert 导致 DOCX/XLSX 不能聊 | **已改变，广泛结论不成立** | 本地聊天 DOCX/PPTX 可走原生读取；xls/xlsx 绕过 Office 转 PDF，使用 pandas。Office 转换服务仍不属于此源码基线，老格式或真正的 PDF 转换需求另行处理。 | **广泛结论不成立。** `algorithm/lazymind/chat/engine/document_extractor.py:37` 原生读 DOCX/PPTX，`chat_excel_extractor.py:66` 用 pandas 读表格；旧 `.doc` 和真实 PDF 转换需求仍有外部依赖。 | 广泛结论不成立；原生读取已有，旧格式/转换另验。 |
| 23 | office-convert Windows 五处适配 | 当前部署不适用 | 本仓库 compose 引用独立的 office-convert 镜像，当前没有该转换服务的本机 Python 实现可核对。不能把别处修改当成本项目已存在的源码。 | **原报告“无服务源码”需纠正；Windows 原生适配有风险。** 实际存在 `backend/office-convert-service/main.py`；`:53` 默认字体路径面向 Linux，`:538` 硬编码 libreoffice，`:553` 未传隔离环境。已使用 `Path.as_uri()` 和独立临时 profile；不能在缺原始清单时逐一确认所谓“五处”。Docker 路线不等于原生 Windows 路线。 | 服务源码存在；Windows 原生适配仍待验证。 |
| 24 | relocatable Python 启 soffice 必失败 | 待验证 / 当前不适用 | 本机未发现 LibreOffice 安装，也没有启动转换服务。没有证据支持“venv Python 必失败”这一普遍结论。 | **继承环境风险存在，“必失败”未证实。** `backend/office-convert-service/main.py:553` subprocess.run 未传 env，可能继承宿主 Python 变量；是否影响 soffice 取决于实际安装与环境。本次未运行 LibreOffice。 | “必失败”不成立；Windows 环境继承仍待验证。 |
| 25 | soffice 残留占锁 | 本机未发现 | 未启动 soffice，也没有为本次检查杀任何其他 Office 程序。 | **未证实残留占锁；部分预防已存在。** `backend/office-convert-service/main.py:532` 每次转换使用独立 profile，并设子进程超时；未见同处显式清理整个进程树，超时后残留子进程仍需 Windows 验证。 | 成功并发有局部证据；Windows 超时后残留清理待验。 |
| 26 | 文件夹选择弹窗根目录失败 | 条件风险，未复现 GUI 故障 | picker 脚本仍没有显式 RootFolder/SelectedPath；已使用 powershell -STA。未弹出交互选择框。CREATE_NO_WINDOW 本身不等于已证明桌面隔离或无法调用 GUI。 | **代码形态仍在，GUI 故障待验证。** `local/local-proxy/internal/server/workspace_picker_windows.go:12` 未指定 RootFolder/初始 SelectedPath，但已经使用 -STA；仅凭这些不能确认弹窗根目录失败。 | GUI 待验，不能仅凭代码形态认定故障。 |
| 27 | builtin shell 与 workflow 分工 | 设计行为，原描述需细化 | trusted_local_mode 默认 false；当前还有工作区绑定、逐操作授权和 workflow 条件控制。不能简化成“所有 workflow 都禁用 shell”或“只有 trusted 模式才可能有 shell”。 | **设计行为，原描述过宽。** `algorithm/lazymind/chat/engine/subagent/runner.py:416` 将 trusted、有效工作区权限和 workflow_full_trust 分别纳入宿主访问判断；工作区 workflow/host access 相关 Go 测试通过。 | 权限设计行为；真实模型授权/写入集成待验。 |
| 28 | Excel 经 PDF/OCR 导致结构丢失 | **主要路径已修复，并通过合成样本** | Core 的 isSpreadsheetDocument/normalizeSpreadsheetDirectParse 保留原工作簿。实际调用聊天提取器：两张表共 3 行完整读取，205 行文件返回预览并要求从原文件用 Python 分析；没有经过 PDF/OCR。仍需注意公式缓存、隐藏行和合并单元格等语义。 | **主要路径已处理。** `backend/core/doc/task_convert.go:51` 表格绕过 PDF 转换；`algorithm/lazymind/chat/engine/chat_excel_extractor.py:66` 读原始各 sheet，超过内联阈值转预览。本次核对代码，未重做原报告 Excel 合成样本。 | 原始表格主路径已处理；复杂 Excel 语义与大表链路待验。 |
| 29 | PID 复用误杀 | 已有部分防护，仍有边界 | 注册表持久化 StartID，枚举时会比较；installer 的 ForceKillTree 使用身份检查。但通用 forceKillProcessTree 仍按 PID 调 taskkill，pidFileRecords 会读取当前 StartID，不能据此宣称所有 PID 复用竞态都已消除。没有进行杀进程压力测试。 | **部分防护存在，仍有竞态边界。** `local/local-runtime-manager/process_registry.go:21` 保存 StartID；`:167` PID 文件路线取当前身份；`process_native_windows.go:47` 通用强杀仍直接 taskkill /PID。没有通过真实误杀实验确认发生概率。 | 部分防护/Windows 测试通过；真实身份竞态待验。 |
| 30 | 大 Excel 知识库切块失败链 | 条件风险 / 未做大数据压测 | 当前已能原始表格读取，SQLite executemany 也按请求大小分批；15 MiB 请求边界仍在，超大的单行不能靠分批解决。没有导入 81 MB 或真实患者数据，45 分钟 embedding 链路未复现。 | **完整故障链未证实，超大单行限制仍在。** `sqlite_proxy.py:17` 客户端 15 MiB，`local/local-runtime-manager/sqlite_server.go:26` 服务端 16 MiB；executemany 已按编码大小分批，单行过大仍报错。未做 81 MB 导入或慢 embedding 压测。 | 大表完整链路待验；优先 Docker PostgreSQL + OpenSearch。 |
| 31 | Milvus manifest os.rename | **源码部署条件下存在风险；覆盖语义已复现** | 检查前现有源码算法 venv 的 milvus_lite/storage/manifest.py 仍用 os.rename(tmp_path,target_path)；临时文件覆盖测试在本机返回 WinError 183。桌面打包流程已有 patch-windows-milvus.py，不能据此认为普通 pip 源码环境也自动打补丁。本次未启动可选 Milvus。 | **桌面打包已有补丁，普通源码安装待核实。** `desktop/scripts/build-windows-x64.ps1:582` 调用补丁；`patch-windows-milvus.py:49` 把 manifest rename 改为 replace。未安装最新源码环境的 Milvus，不能沿用旧机器结论断言当前 wheel 一定未修复。 | 桌面补丁/脚本已有；普通源码安装与真实 Milvus 待验。 |
| 32 | SiliconFlowRerank 缺 type | **确认** | 该 supplier 未覆写 type，基类返回 EMBED。动态基础地址 /v1 被解析成 /v1/embeddings；显式 /v1/rerank 则不受影响。 | **确认仍存在，继承方法离线复现。** 锁定子模块 `supplier/siliconflow.py:118` 未覆盖 type，rerank 基类也未覆盖；继承 EMBED 导致动态 `/v1` 解析到 `/v1/embeddings`，显式 `/v1/rerank` 正常。 | LazyLLM 缺陷已由当前 pin 修复，已有真实重排记录。 |
| 33 | SQLite 事务 TTL 仅 5 分钟 | 条件风险，清单表述过宽 | 常量仍是 5 分钟，但 withExecutor 每次事务操作会 touch 并重置计时器，是空闲过期机制，不是每个活跃事务固定 5 分钟终止。慢 embedding 造成超过 5 分钟无数据库活动仍可能受影响。过期释放队列的现有用例通过。 | **空闲超时风险仍在，固定总时长说法不成立。** `local/local-runtime-manager/sqlite_server.go:25,418` 使用 5 分钟 TTL，并在每次事务请求重置；长时间无 SQL 可过期。本次过期事务释放等 SQLite Go 测试通过，不证明慢 embedding 全链路安全。 | SQLite 空闲 TTL 是条件风险，非固定总时限；慢链路待验。 |
| 34 | WORKING / DELETING 毒药状态 | 当前桌面数据未发现 | 只读检查桌面 app.db：documents、knowledge_bases、doc_parse_state、doc_service_tasks 均为 0 行。没有卡死任务或同名残留的本机证据，没有改状态或删库。 | **待恢复场景验证。** 原机器空表不能排除上游状态恢复缺陷；本次未制造进程崩溃/删除中断，也未检查用户数据，因此不能判定已修复或确认仍会卡死。 | 真实任务中断恢复待验；认证迁移修复不能替代此项。 |
| 35 | 任务分类 JSON 脆弱 | **确认，触发条件需修正** | 单个 JSON 后跟普通解释文本可以成功；后面再出现第二个花括号对象，会被 find/rfind 一并截入并产生 Extra data。详见源码最小复现。 | **解析缺陷确认，业务影响需收窄。** `algorithm/lazymind/chat/engine/prompts/task_profile.py:674` 多个未被单一代码块隔离的 JSON 对象触发 Extra data；单对象后普通说明正常。`:864` 会捕获异常并回退规则，不能直接写成整个聊天崩溃。 | 主仓库多 JSON 解析已修，保留规则回退。 |
| 36 | dynamic supplier 没绑定 URL | **不能按原根因认定** | 构造函数仍用 self._base_url，但 dynamic_router.forward 会调用 _merge_dynamic_forward_kwargs，把 bucket.url 传给供应商 forward；聊天 forward 使用 runtime_url。本次最小复现确认动态 URL 已传递。无末尾斜杠时仍受 #16 影响。 | **原根因不成立。** 锁定子模块 `module/llms/onlinemodule/dynamic_router.py:114` 将 bucket.url 注入 forward，聊天 forward 使用运行时 URL。无尾斜杠丢路径归 #16，不是没有绑定动态地址。 | “动态 URL 未绑定”原根因不成立；相关路径问题归 #16。 |
| 37 | preparing 不显示审批 | **不能直接视为过滤 bug** | 后端 completePreparingOperation 正常会转成 pending/allowed；projectOperation 还故意将已被决策认领的 pending 投影为 preparing，避免重复审批。后端 Decide 本身拒绝非 pending。若长期 preparing，应查状态完成/索引/认领恢复，而不是直接让前端可审批。 | **过滤 preparing 本身不是 bug。** `backend/core/localworkspace/operations.go:184` 完成准备后转可决策状态；`approvals.go:289` 将已认领 pending 投影为 preparing。准备恢复相关 Go 测试通过，长期卡 preparing 仍需具体现场。 | 过滤 preparing 是设计行为；长期卡住需运行现场。 |
| 38 | Python 低完整性级别 | 本次检查的进程未发现 | 检查当时 3 个源码 Python 进程 token，均为 S-1-16-12288（High），不是 Low。这些进程由本次排查启动，不能据此推断普通桌面启动的权限；没有修改任何目录完整性级别或 ACL。 | **Windows 进程环境项，待验证。** 本次在 Linux，未检查 Windows token/ACL；旧报告的 High 进程记录不能证明普通桌面启动总是正常。 | 普通桌面 token/ACL 待验，不能沿用管理员 shell 结论。 |
| 39 | write 的三个叠加 bug | **部分代码形态相同，完整结论未成立** | 见下文拆分。没有将“看到相同条件判断”直接等同于真实写入死锁。 | **不能确认“三个叠加 bug”。** `LocalWorkspaceControl.tsx:372` 保存权限；`backend/core/chat/conversation_logic.go:337` 读取服务端偏好；`algorithm/lazymind/chat/service/chat_service.py:1223` 构造 execution identity。授权前提不等于正常链路漏传；write 卡住未做真实模型复现。 | 用户报告文件工具阻塞，未解决；三个拟定根因须逐段定位。 |
| 40 | 动态 embedding 未配置、绑定跟会话 | 当前桌面未发现未配置证据 / 绑定是设计行为 | 桌面库中有通过验证的 embed_main、embed_image、reranker 等角色各 1 条；不能认定本机缺 embedding。KB 和工作区绑定按会话及用户隔离符合当前实现。独立源码环境不继承桌面模型配置。 | **配置状态待验证；会话隔离是设计行为。** 本次没有用户模型库，不能沿用旧机器角色记录认定已配置；工作区 owner scope/resolve 相关 Go 测试通过，不能因会话绑定就认定缺陷。 | 用户报告 kb_search 缺 dynamic source，未解决；配置缺失/注入丢失待区分。 |
| 41 | DeepSeek 模型名称经验 | 内置目录已更新，服务端可用性未验证 | 当前 model_catalog.yaml 已有 deepseek-flash、deepseek-v4-pro、deepseek-v4-flash，清单所说内置只剩旧名称不适用于当前代码。具体供应商实时支持、权限及返回质量需其 API 验证，本次没有推断。 | **“内置仅旧模型名”不成立。** `backend/core/config/model_catalog.yaml:140,143,1049` 存在 deepseek-flash、deepseek-v4-pro、deepseek-v4-flash；只确认目录内容，未调用供应商验证名称、账号权限及效果。 | 目录已有对应名称；供应商实时权限/效果待验。 |

## 需要优先处理的实际问题

### A. uv 自举失败会损失已有源码 venv

本次启动经历：已有环境出现两个 pyarrow 分发目录；Chat 要求安装当前源码的 LazyLLM 1.4.0；uv 安装入口程序时报 PE 资源更新失败；`preparePython` 随后调用 `createVenv(clear=true)` 清空现有环境，重建种子包再次遇到同类错误。

这比“pip 卡住”更具体。故障日志保存在 `chat-bootstrap-uv-failure.log`。后续恢复改用 pip 安装当前源码要求的依赖，桌面安装目录未改动。应考虑让安装失败保留既有 venv，使用新目录准备成功后再切换。

### B. URL / reranker / 分类 JSON

- `onlineChatModuleBase.py:78`：基础地址拼接需规范化末尾斜杠，保留调用方路径前缀。
- `siliconflow.py:118`：SiliconFlowRerank 的类型应与 RERANK 路由一致，同时覆盖静态/动态地址测试。
- `task_profile.py:674`：当前提取逻辑无法可靠处理多个 JSON 片段。修复需要明确选择对象及验证 schema，而不应仅依赖首尾花括号。

### C. SQLite 连接与文件锁

- `sqlite_proxy.py:342` 的每次 urlopen 建连已复现，连接池/会话复用应优先考虑；写 SQL 的重试还要处理幂等性，不能直接无条件重试。
- `common/queue.py:131` 的共享 SoftFileLock 有崩溃遗留锁风险；需要有界等待及恢复策略，同时保持跨进程队列语义。
- 普通源码环境安装可选 Milvus 时，要核实 Windows 补丁是否应用；当前打包脚本的修复只覆盖其负责的产物。

## #39 的三个子问题

1. **请求参数只在后台任务发送：代码确实如此，但“allow_all 必然失效”不成立。** LocalWorkspaceControl 会通过 saveUserPermissionPreference / updateWorkspacePermission 保存；conversation_logic.go:335 会读取服务端 UserPermission 并写入新会话。已有会话有冻结的权限/版本语义，需区分新对话、已有对话和权限更新时点。
2. **缺少 execution identity 会被拒绝：已用实际 guard 复现，这是授权前提。** chat_service.py:1254 会构建 `_workspace_execution`；是否走正常 Core 请求链、history_id/run_id 是否被上游传入，才决定是否触发问题。
3. **allow_all 下 write 卡住：未复现。** 现有工作区后端测试通过，但本次未用真实模型和现有用户会话完成完整写入链路，因此不将其标成已修复或确认故障。

## 已完成的验证与证据文件

- `source-probes.json`：真实源码函数的离线最小复现，SQLite 测试仅向临时本机模拟服务发请求。
- `native-probes.json`：当前 Excel 提取器、遗留文件锁以及 Windows rename 的合成样本测试。
- `http-probes.json`：Vite 模块转换及本机自动登录返回 200；受信 Origin 返回 200，未知 Origin 返回 403。
- `frontend-regressions.log`：localSession 与 LocalWorkspaceControl 共 53 项现有测试通过。
- `workspace-go-tests.log`：现有工作区授权/操作相关 Go 测试通过。
- `sqlite-go-tests.log`：SQLite 事务过期测试通过。
- `desktop-data-summary.json`：桌面库只读统计，不包含 API key、密码、用户对话或文档正文。
- `process-integrity.json`：源码 Python 进程实际完整性级别。
- `worker-import-probe.json`：源码 venv 与普通 Python 子进程的 prefix、requests 可用性和依赖版本。
- `dependency-check.log`：重建后的源码基础 Python 环境通过 pip check。
- `health.json`：服务健康检查，最终状态见下节。

## 源码部署与本次变更

隔离数据目录：`C:\Users\bujiadong.vendor\LazyMind\.lazymind-local\audit-20261022`。

前端入口：<http://127.0.0.1:5173/>。代理：127.0.0.1:5024。SQLite：19081。认证：18000。Core：18001。网关：18085。Chat：18046。

本次使用已有前端依赖直接启动 Vite，并编译当前源码 manager / Core / 代理。RAG、Milvus、解析服务、Evo 不属于本次基础服务启动范围。隔离环境不复制桌面的账号、模型密钥和业务数据。

最终检查时间：2026-10-22 11:16（北京时间）。前端、代理、SQLite、认证、Core、网关、Chat 的健康检查全部为 HTTP 200；安装版桌面进程剩余 0 个。本机自动登录与 Vite 模块转换也返回 200。

Chat 沿用源码 manager 的 `--host 0.0.0.0` 默认值；前端及代理入口使用 127.0.0.1。本次未配置公网穿透或外部域名。

源码算法 venv 曾被官方安装失败回退清空，本次已重建为当前源码所需的**基础环境**：LazyLLM 1.4.0、NumPy 2.4.6、pandas 2.3.3，pip check 无依赖冲突。原有可选 RAG/Milvus 依赖没有在这次基础部署中恢复，也没有复制桌面安装包中的依赖来伪装为源码环境。

本次没有修复上述业务源码，只增加排查脚本和证据文件、编译本地二进制，并恢复部署所需 Python 依赖。原有未提交代码改动保留。

停止本次服务：

```powershell
& C:\Users\bujiadong.vendor\LazyMind\outputs\windows-local-audit-20261022\start-audit-services.ps1 stop
```

启动基础服务与 Chat：

```powershell
& C:\Users\bujiadong.vendor\LazyMind\outputs\windows-local-audit-20261022\start-audit-services.ps1 baseline
& C:\Users\bujiadong.vendor\LazyMind\outputs\windows-local-audit-20261022\start-audit-services.ps1 chat
```

## 2026-10-26 本次复查验证记录

版本固定链接：[LazyMind 主仓库](https://github.com/LazyAGI/LazyMind/tree/04e5c4bef8e134483611ef48040418e690a2fd4b)。LazyLLM 使用主仓库 gitlink 指定提交，未使用子模块当前工作目录或其最新分支来替代。

- **离线最小复现**：从固定快照用 Python AST 提取原始 `_get_chat_url`、embedding URL 方法和 `_extract_json` 执行，保留方法体；重排同时检查供应商和 rerank 基类没有覆盖 type。这验证局部函数行为，不代表完整供应商网络请求。
  - `https://token.sensenova.cn/v1` → `https://token.sensenova.cn/chat/completions`；加尾斜杠 → `/v1/chat/completions`。
  - 继承 EMBED 的 rerank URL 处理：`https://api.siliconflow.cn/v1` → `/v1/embeddings`；完整 `/v1/rerank` 保持不变。
  - `{"ok":true} explanation` 成功；`{"ok":true} explanation {"extra":1}` 报 `JSONDecodeError: Extra data`。外层异常处理回退规则的结论来自源码核对。
- **SQLite HTTP 实测**：直接加载快照 `sqlite_proxy.py`，向临时 loopback HTTP/1.1 服务执行 12 次原始 `Connection._call`；12 个不同客户端端口，Connection 请求头均为 close。未连接实际业务数据库，也没有做端口耗尽压测。
- **运行管理器现有测试通过**：在快照 `local/local-runtime-manager` 执行 `go test ./... -run 'Test(SQLiteServer|PythonComponent|PrepareFrontendNodeModules|AlgorithmServiceEnv)' -count=1`，涵盖 SQLite 事务释放、组件安装识别、前端链接和算法环境；其他子包可能显示 no tests to run，不计为功能验证。
- **工作区现有测试通过**：在快照 `backend/core` 执行 `go test ./localworkspace -run 'Test(WorkspacePrepare|WorkspaceResolve|HostAccessSensitive|WorkflowHostAccess)' -count=1`，涵盖准备阶段恢复、工作区解析及宿主权限策略。
- **未执行的验证**：本次 Python 缺 filelock，未重复 SoftFileLock 实验；未安装 Windows Python/RAG/Milvus/LibreOffice，也未复跑历史报告中的 Excel、前端、GUI、token 完整性和健康检查。相关条目保留条件风险或待验证，不能引用历史机器状态证明当前上游无问题。

## 2026-10-26 `cst/fix_1026` 修复与实际部署验证

本节记录复查后的新工作；前文 2026-10-22 Windows 本机记录仍是历史资料。分支以 upstream main `04e5c4bef` 为起点，子模块检出该提交锁定的 `fd4465ec`，子模块工作区保持干净，没有提交子模块。

- **#4 已修复破坏性重试路径**：`local/local-runtime-manager/algorithm_service.go` 在已有算法 Python 可执行文件时，依赖安装失败立即返回错误，不再执行 `uv venv --clear`。新建 venv 首次安装失败后仍保留一次清空重试。新增 Go 回归测试确认原有 Python 文件不会被清空；已运行管理器全部 Go 测试通过（`go test ./... -count=1`，64.6 秒）。这是防止失败后清空的修复；安装器在报错前对旧环境做过的局部包修改仍需后续通过暂存目录安装方案解决，不能称为完全原子升级。
- **#7 已修复每次 SQLite HTTP 调用新建连接**：`algorithm/lazymind/common/database/sqlite_proxy.py` 改为每个代理 Connection 持有并关闭一个 HTTP(S) 连接；错误后关闭底层连接，**不会自动重试写请求**。新增真实本机 HTTP/1.1 服务测试，连续 12 次调用使用同一个客户端端口。Python 回归测试 1187 项通过。真实高并发、长时间空闲连接与网络中断后的业务链路尚未压测。
- **#35 已修复多 JSON 解析**：分类器按 `JSONDecoder.raw_decode` 读取第一个完整 JSON 对象；新增第二个 JSON 出现在说明文字中时仍保留有效分类结果的回归测试。既有异常回退逻辑保持不变。
- **Docker 中的当前源码测试通过**：现有 LazyMind 算法镜像作为一次性运行环境，挂载本分支 `algorithm`、锁定的 `algorithm/lazyllm` 与 `tests`，安装一次性 pytest 后执行上述 Python 测试，结果 1187 passed。镜像内原有 LazyLLM 版本与锁定提交不同，因此必须挂载本分支子模块才能测试当前代码。
- **Docker 服务部署与转换通过**：独立容器挂载当前 `backend/office-convert-service` 源码和临时目录，`GET /health` 返回 200；合成 DOCX 调用 `POST /v1/office/to-pdf` 返回 200，生成 16,974 字节 PDF。测试后已停止并移除独立容器；未修改原有 Docker 项目的业务数据。尝试重新构建该镜像时基础镜像下载较慢，随后改用已有镜像挂载当前源码完成实际服务验证；此项不代表已验证 Dockerfile 的从零构建。
- **Windows 原生启动未完成**：在 WSL 中 `make local-win-doctor` 通过；`make local-win-up` 调用 Windows Go 编译时，对 `\\wsl.localhost\Ubuntu\...\go.mod` 返回 `RLock ... Incorrect function`，没有进入服务启动。Windows C 盘剩余约 4.6 GiB，当前仓库约 16 GiB，无法在本机安全复制整仓到 NTFS 目录重跑。请在有足够空间的 Windows 本地盘检出该分支后执行 `make local-win-up`，再检查 `make local-win-status`、桌面登录、模型请求及文件读写链路。

**仍需用户或上游处理**：#16 URL 拼接和 #32 SiliconFlow 动态 rerank 位于 LazyLLM 子模块，本分支未改动或提交子模块；需在 LazyLLM 上游修复并升级主仓库 pin。#11/#13/#18/#23/#24/#25/#29/#30/#33 的条件风险，及 Windows 安装、Excel 大文件、Milvus、真实模型、GUI/权限链路，仍需相应场景验证。以上 Docker 验证只能证明被测功能，不能代替完整 `docker compose up` 的跨服务测试。

## 2026-10-26 `cst/ppt_template` 合并、主 Chat 工作流增强与 #869 修复

本节为 macOS 当前源码验证，不能代替前文 Windows 实机结果。当前分支为 `cst/ppt_template`。

### 合并记录

- 已合入 `upstream/main` 的 `6423896b5`（PR #868），合并提交 `56dfff210`。
- 已合入 `origin/cst/fix_1026` 的 `d63f7d34d`，合并提交 `6696a519b`。前一节的 Python 环境保护、SQLite 连接复用和分类 JSON 解析修复均已包含。
- `runner.py` 冲突保留上游规则：工作流根据真实产物、包内后置检查和配置的人审判断完成，普通 SubAgent 保留模型完成审核。合并前本地修改已恢复，没有覆盖现有 demo/chat 修改。新增修复仍在工作区，尚未提交或推送。

### 主 Chat 指令范围与 composite 联动

- 增加 `patch_artifact_text`：根据具体产物引用和唯一原文片段局部替换，保留其余正文及元数据；携带 revision 和 draft version 防止覆盖并发修改。完整 Markdown 的“只修改第一节”可局部修改，composite 则定位具体列表项。禁止空匹配、不唯一匹配和 HTML 全文误编辑。
- 主 Chat 和下发步骤的指令明确保留编辑范围，显式页码优先于当前焦点；仅修改大纲时停止，不自动重跑后续步骤。新增/插入/删除/重新生成按已有步骤执行，并传递页范围与关联素材要求；本次未新增通用的跨素材增删页事务，不将提示词约束描述为模型端到端保证。
- PPT 的 composite 标签声明同一个 `page_group: presentation`，共享当前页。拖动/交换页序时将已生成的关联列表一次提交；空的可选列表跳过，非空但页数不一致则拒绝。
- 后端要求完整有效的页序排列，并在一个事务中修改所有关联列表；任一版本冲突整体回滚，成功后通知界面刷新。主 Chat 可直接调用 `reorder_artifact_pages`，不用回退步骤。SDK 读取产物同样遵循保存后的视觉顺序，避免界面与工具按不同页码读取。
- 页序使用稳定 `list_index` 与视觉顺序映射，不改写产物身份。已经导出的 PPTX 二进制不会因面板排序自动变化，仍须重新导出；磁盘中按页码命名的生成源文件与全部增删页场景尚需端到端验证。

### Issue #869

问题来源：[image_generator UNAVAILABLE、10 条 outline 失败、素材分析失败](https://github.com/LazyAGI/LazyMind/issues/869)。

1. **GLM 图片生成调用路径已修复。** 旧适配器依赖 `zhipuai`，且将 `image_size`、`batch_size`、`priority` 等内部参数传入供应商 SDK。增加应用层 GLM 图片 HTTP 适配器，使用本次请求所选模型、base URL 和该角色密钥，只提交 `model/prompt/size`；多图按单图请求逐次执行，不自动重试生成请求。返回图片下载后沿用现有上传、签名及公开结果脱敏逻辑；批次失败清理已下载文件。其他供应商继续原有路径。
2. **不通过降级 PyJWT 修复依赖。** [PyPI zhipuai 元数据](https://pypi.org/pypi/zhipuai/json)中的旧 SDK 要求 `pyjwt>=2.8,<2.9`，与当前 MCP 依赖冲突。此图片路径直接使用已有 `requests`，无需新增旧 SDK 或改变 PyJWT。接口参数参考[智谱 CogView-4 官方文档](https://docs.bigmodel.cn/cn/guide/models/image-generation/cogview-4)。
3. **补齐失败日志。** 外部工具记录可用性、schema、执行及请求异常阶段；GLM 路径记录生成/下载阶段、HTTP 状态和异常类型。不记录密钥、提示词、供应商异常正文或图片 URL，前端继续使用安全的公开错误。
4. **10 条 outline 的完成误判已随 #868 修复并补回归。** 新增 9/10/11 条真实产物的 runner 测试，最终摘要包含“全部通过，无失败项”仍成功，不调用额外模型裁判。没有发现或新增“最多 9 页”的限制。
5. **素材收集分支补回归。** 测试读取当前 PPT 工作流的真实输出声明，全部必需产物存在、可选 `skip_material_collection` 缺失时成功；缺少必需 `requirement_analysis` 且无可提交正文时仍失败。不能用跳过素材收集来规避完成判定。
6. **复现边界。** 当前源码中注入有效 `text2image` 后，`image_generator` 可用性检查可通过，未复现所有配置下都为 false。新增测试使用真实注册表、ToolManager、配置注入及图片结果处理，只在供应商 HTTP/下载边界模拟；没有调用用户付费模型，也没有在 Windows 0.4.0 安装包中实测。若实机仍不可用，新增阶段日志用于进一步定位，不能宣称所有 UNAVAILABLE 原因均已消除。

### 本次验证

Python 使用仓库算法环境 `local/build/deps/python/algorithm/bin/python`，并设置 `PYTHONPATH=algorithm:algorithm/lazyllm`，明确使用仓库锁定的 LazyLLM 源码。只使用 `PYTHONPATH=algorithm` 时本机安装版 LazyLLM 缺 `MCPToolNameError`，导致两个接口测试失败；改用仓库源码后通过，没有以跳过测试规避。

- runner、外部工具、GLM 图片、按需媒体配置、SQLite proxy、任务分类器：**1332 passed**。其后补充静态 GLM 配置和下载失败清理用例。
- GLM 图片（含补充用例）、工作流选择、SDK、Toolkit：**186 passed**。两组有重叠，不合并计算总数。
- `frontend` 的 `vitest.workflow-panel.config.ts`：**22 文件 / 181 测试通过**。
- `backend/core`：`go test ./workflow ./externalcapability ./modelconfig -count=1` 全部通过，包含关联排序事务/冲突以及外部模型配置传递测试。macOS Security API 存在既有弃用编译警告。
- `local/local-runtime-manager`：`go test ./... -run 'TestAlgorithmPreparePython' -count=1` 通过，验证已有 Python 环境在依赖安装失败时保留；其他包的 no tests to run 不算功能覆盖。
- 新增图片适配器、图片测试及外部工具文件 flake8 通过；改动的工作流面板、分组工具、store/request 的 ESLint 通过；`git diff --check` 通过。
- runner 测试文件原有多处 lint 报错仍存在，新增回归部分没有增加这些错误。此前全量前端类型检查有其他文件错误；PPT 广义契约测试有既有 `exposed: false` 预期冲突；命名检查会命中现有未跟踪 demo 文件。未修改这些既有内容来取得全绿结果。
- 尚未验证：Windows 原生运行、真实 GLM 出图、真实模型对主 Chat 中文指令的全流程执行、所有增删插入与重新导出 PPTX 场景，以及完整 CI 平台矩阵。

### 2026-10-26 取消 PPT 导出页数上限

- 移除 `/api/workflow/ppt/export` 对超过 30 页的固定拒绝逻辑；这是 HTML 转可编辑 PPTX 的导出限制，与 #869 的 10 页完成误判不同。
- 新增 31 页、100 页回归，使用真实路由和临时文件，仅模拟导出服务边界，确认所有页面完整传给导出器并返回文件。没有实际渲染 100 页。
- 使用仓库 LazyLLM 和本机 Pandoc 执行 `test_workflow_smoke_tools.py`：11 passed。文件内既有 E306 lint 问题未改动；新增部分无 lint 错误。

## 2026-10-26 智谱模型目录与 LazyLLM supplier 补齐

依据[智谱官方模型概览](https://docs.bigmodel.cn/cn/guide/start/model-overview)及对应 API 文档更新。本节替代前述 #869 中“应用层 GLM 图片 HTTP 适配器”的最终实现描述：图片适配已下沉到 LazyLLM，删除本次此前新增的临时 `glm_image_generation.py`，LazyMind 恢复通过统一 `AutoModel` 调用。按用户要求，**本轮没有重启服务**，运行中的进程尚未加载这些修改。

### LazyLLM

- 更新 GLM 模型清单和类型映射，识别 GLM-5.3/Flash/FlashX/Legal、5.2、5.1、5V-Turbo、4.6V、AutoGLM-Phone、GLM-Image、ASR-2512 等。将 CogVideoX/Vidu 从图片类型修正为视频类型。
- 新增 `GLMTTS` 和 `GLMText2Video` supplier，沿用基类自动注册。TTS 使用 `/audio/speech` 返回编码音频文件；视频使用 `/videos/generations` 创建任务、`/async-result/{id}` 轮询并下载结果，覆盖 CogVideoX 与 Vidu 的文生视频、图生视频、首尾帧、参考图对应模型。
- GLM 图片与 STT 改用已有 requests，不再依赖旧 zhipuai SDK/PyJWT。图片转换工具参数为供应商参数，多图逐次请求，内部调度字段不传入 API；STT 发送 multipart 并关闭文件句柄。
- 动态角色继续使用请求所选模型、base URL、角色密钥；同一模型对象在后续请求换密钥/地址时重新读取配置。生成请求不自动重试，视频轮询有截止时间；失败信息与日志不包含供应商原始正文或密钥，下载生成文件不附带模型鉴权头。
- 修正聊天、Embedding、Rerank 自定义 base URL 的路径保留；标准模型名称按 API 名称发送。新视觉模型可发送图片，远程图片 URL 不再被错误包装成 `data:None;base64,...`。
- 图片生成结果在整批下载成功后才写临时文件，失败批次不会留下前几张图片的临时文件；后续上传签名仍由 LazyMind 负责。

### LazyMind

- GLM 目录新增 `glm-5.3-flashx-legal`、`cogvideox-3`、`cogvideox-flash`、`viduq1-text/image/start-end`、`vidu2-image/start-end/reference`；增加实际 API 名 `cogview-4-250304`，保留旧 `CogView-4` 条目和 supplier 别名，避免目录同步清掉现有 CogView 选择。
- 按官方文档将直连 GLM-5.3-Flash/FlashX/Legal 上下文标为 1M，Embedding-2/3 上下文从原配置的 512/3072 修正为官方标注的 8K。不同供应商的同名托管模型参数不随此变更。
- 动态 runtime_models 增加 `tts` 角色，使默认模型设置中的 TTS 能注入 supplier；不为用户自动选择付费模型或写入密钥。
- 移除默认目录中误标为普通 TTS 的 `GLM-4-Voice` 和 `GLM-TTS-Clone`。它们与 Realtime、GLM-OCR 分别使用音频对话、音色管理、实时会话或 layout_parsing 接口；本次没有实现这些独立协议，也不将它们冒充已支持的普通模型角色。现有目录同步规则会在后续启动时清理已移出的内置默认条目及其默认选择；自定义模型不在此清理范围。

### 验证与交付状态

- 使用 `PYTHONPATH=algorithm:algorithm/lazyllm`：supplier、真实外部图片工具注册/配置注入、动态模型配置、运行角色、模型验证接口及工作流能力声明相关测试 **100 passed**。
- 测试覆盖目录中所有 GLM 模型可实例化到对应 supplier；视频一次创建、多次轮询、失败、空结果、超时；STT 文件释放；TTS 音频返回；生成文件下载不泄漏鉴权；请求密钥/地址隔离；视觉图片消息与自定义 endpoint。只模拟 HTTP 边界，没有真实付费模型调用。
- LazyLLM `tests/basic_tests/Models/test_online_chat_model_runtime.py`：**70 passed**。
- Core `go test ./modelprovider ./modelconfig -count=1`：通过。
- 新增 supplier 与 Python 测试 flake8 通过；主仓库及子模块 `git diff --check` 通过。
- 本轮已修改 LazyLLM 子模块工作区（`supplier/glm.py`、`map_model_type.py`），尚未提交子模块、更新 gitlink 或推送。此前未跟踪的子模块文件保持原样。服务未重启，亦未验证 Windows 或真实 GLM 服务。

接口依据：[GLM-TTS](https://docs.bigmodel.cn/cn/guide/models/sound-and-video/glm-tts)、[GLM-ASR-2512](https://docs.bigmodel.cn/cn/guide/models/sound-and-video/glm-asr-2512)、[视频异步生成](https://docs.bigmodel.cn/api-reference/模型-api/视频生成异步)、[Vidu Q1](https://docs.bigmodel.cn/cn/guide/models/video-generation/viduq1)、[Vidu 2](https://docs.bigmodel.cn/cn/guide/models/video-generation/vidu2)。

## 2026-10-26：Workflow 启动输入绑定协议修复

- 复查本机 Chat 日志，14:52:28 和 14:52:37 两次 `trigger_ppt_workflow` 均在创建 Session 前失败：`input_bindings["generate_background_images"]: unknown field "name"`。当时尚未进入 analyze，并非依赖工具检测提前执行。
- PPT 的 `runtime.post_step_checks` 仍配置在 `analyze_requirements` 完成后调用 `check_ppt_workflow_capabilities`，按分析产物决定实际需要的能力，保持此机制。
- 根因：upstream #868 的输入绑定解析严格限制字段为 `resource_id`、`revision`、`content_hash`，Chat 的文字及附件导入却通过 `asdict(InputResource)` 额外提交 `name`、`mime_type`、`size`，形成接口不兼容。
- 修复 Chat 两条导入路径，仅提交固定资源引用；完整文件描述仍保留在资源存储，不放宽后端校验。
- 回归测试不再直接模拟已裁剪的绑定，而让真实 Chat 导入转换接收完整 `InputResource`，覆盖文字/附件以及对象/JSON 字符串两种工具参数输入。
- 验证：相关文件 flake8 和 git diff --check 通过；`test_workflow_selection.py`、`test_workflow_full_trust.py` 共 74 项通过，其中包括分析后能力检查的真实 PPT 脚本测试。未调用付费模型进行整套 PPT 生成。
- 按本轮授权完成 `make local-down` 后重新 `make local-up`（使用宿主 Go skill-bundler，避免依赖未运行的 Docker）；15:09 本地启动成功，8090 HTTP 200，加载本轮修复及之前的 GLM supplier/模型目录修改。

## 2026-10-26：统一输入预处理与澄清字段启动修复

- 增加 SDK 统一输入预处理，Workflow preparation 和外部 Skill task 入口共用，Chat 的文字/附件转换也复用同一逻辑。完整资源描述转换为引用，支持多文件列表并保留顺序、重复引用与版本哈希，不修改调用者数据；仅移除已知展示元数据，未知字段和非法引用继续交给 Core 严格校验。
- 15:17 日志确认另一处启动错误：`input must name an external material: target_customer`。发现模式把澄清字段提示为外部材料，模型提交后未按实际 package 分类。
- 现在启动前读取真实包契约：外部材料导入为引用；已声明但不属于外部材料的澄清答案追加到请求上下文，交给 analyze 使用；未知字段在导入前拒绝。修正发现模式的工具描述，避免将澄清字段称作外部材料。
- 新增 SDK HTTP 边界预处理测试，以及使用真实 PPT 澄清声明的发现/显式选择两种启动回归；验证原始请求和受众、风格答案均保留。
- 验证：新增/修改 Python 文件 flake8、git diff --check 通过；SDK、预处理、Chat 选择与分析后检查共 170 项测试通过。15:18 重启 Chat 成功，运行状态 ready，8090 返回 HTTP 200；未做真实模型完整 PPT 生成验收。

## 2026-10-26：多对话 Workflow 隔离检查

- 用户澄清是在询问另开图片工作流是否会与运行中的 PPT 对话冲突，尚未报告图片工作流实际失败。检查日志确认 PPT 已通过启动与 analyze，正在生成页面。
- 检查 Chat 每次请求绑定独立 LazyLLM SID（ContextVar）、每次 Workflow 工具注入创建独立 session_holder，后端步骤与产物按 session_id 查询。
- 新增两个线程同时启动同一图片工作流的回归，使用 barrier 强制 preparation 重叠，验证各对话分别绑定自己的 session，重复 trigger 不会串到另一对话，agentic_config 也保持隔离。后端请求用现有客户端边界模拟，未进行真实双模型端到端并发验收。
- 未修改运行时代码，未重启或打断正在生成的 PPT。

## 2026-10-26：图片工作流素材排版

- 截图中的材料摘要被挤在约 200px 的窄列内，右侧大片空白：网格 auto-fill 在空材料隐藏后仍保留空列。
- 通用 Workflow 网格改为 auto-fit，空列自动折叠；文字/JSON 材料占满整行，图片材料继续网格显示。最小列宽受容器宽度约束，避免窄屏溢出；显式配置的网格列规则仍保留。
- 仅修改前端布局，不改变工作流会话或生成逻辑。
- 验证：WorkflowPanel ESLint、git diff --check 通过；按本地模式环境变量完成 Vite 构建，8090 HTTP 200。前端静态资源已更新，刷新生效，无需重启 Chat；尚未对用户当前会话刷新后做视觉验收。

## 2026-10-26：真实图片搜索缺少配置的明确阻断

- 图片工作流的 image_search_tool / image_search_and_validate 在实际需要搜索且没有配置任何支持的搜索引擎时，抛出既有结构化能力缺失标记，包含网络搜索设置链接、配置 Tavily/Bocha 后重试的提示。
- PPT 的 ppt_search_web_images 使用相同检查（按它实际支持的 Tavily/Bocha 检查）。已有候选图片 URL 直接进入验证，不要求配置搜索；配置存在但没有图片结果仍返回 empty，和缺少配置区分。
- 更新素材收集提示：缺配置必须停止并提示，不能静默跳过、用生成图片替代或将 Wikipedia 文本当成真实图片收集；配置后重试收集步骤并保留已有产物。
- 新增测试覆盖图片旧/批量/PPT 三入口无配置阻断、配置存在但无结果、已有 URL 无需搜索。测试不访问真实网络或使用真实密钥。
- 尚未重启/重新加载运行时，避免打断用户现有生成任务；新工作流包提示需在服务重新加载后使用新会话验证，旧会话固定版本不会自动替换。
- 用户确认任务完成并授权重启后，16:04 已完成 local-down / local-up，加载本轮搜索检查与工作流包更新；服务启动成功。

## 2026-10-26：智谱多模态与 GLM-Image 编辑能力复核（未重启）

- 官方文档 https://docs.bigmodel.cn/cn/guide/models/vlm/glm-5.3-flash 明确 Flash/FlashX/Legal 接受图像、视频、文本、文件，输出文本；当前 catalog 已为三者保留 llm + vision=true，GLM-V 系列已有 vlm 类型。
- 官方开源仓库 https://github.com/zai-org/GLM-Image 明确支持图像编辑、多图条件输入，并给出自部署 SGLang /v1/images/edits 示例。
- 智谱托管模型页 https://docs.bigmodel.cn/cn/guide/models/image-generation/glm-image 仍注明文本输入；图像生成 API https://docs.bigmodel.cn/api-reference/模型-api/图像生成 只公开 images/generations，未找到托管编辑参数/接口。不能将开源模型能力直接作为当前供应商 API 能力。
- 更新 catalog 中英供应商说明与模型注释，说明托管/开源区别；修正 LazyLLM 过于笼统的“不支持编辑”报错，明确是当前托管 API adapter 只接入文生图。未虚增不可调用的 image_editing 条目，未改正在运行的配置，未重启。

## 2026-10-26：LazyLLM CI 与子模块合并修复

- 为 GLMTTS、GLMText2Video 补齐中英文 API 文档注册，修复 doc_check 无法生成构造函数文档。
- GLM supplier HTTP/下载层移除 catch-all、重复日志与 RuntimeError 包装，使用 raise_for_status 并直接向上抛出原始请求异常；不在 supplier 内重试或恢复。新增异常类型/对象保持一致、只发送一次请求的回归测试。
- 视频生成拆为输入图片转换、模型输入约束、尺寸映射和轮询方法，保留协议行为，满足现有 C901 复杂度阈值 12，不增加忽略规则。
- LazyLLM 合入上游 98ad0da5（微信滑动图片组功能），主仓库将引用包含双方修改的后代提交，解决与 upstream/main 的子模块指针冲突。
- 合并后的全量 doc_check 暴露额外 4 处文档注册缺口（ImageGroup.validate_images、MilvusStore.rebuild、LocalFileSpanExporter、LocalConsumeBackend），已补中英文文档，不改业务实现或检查规则。
- 最终验证：flake8 使用仓库 .flake8（max-complexity=12）通过；英文/中文完整 tests/doc_check 各 1476 passed、19 xpassed；GLM supplier 与图片调用回归 46 passed。
- LazyMind 已合入 upstream/main 87a859b5d，子模块引用包含 98ad0da5 及 GLM 修复/文档的后代提交。原有本地 demo 改动已恢复并保留备份 stash，本轮未重启服务。

## 2026-10-26 PR #826 CI 修复与子模块指针更新

- 核对 GitHub Actions run `40293241240`：Linux/macOS 算法各 10 项失败，Windows 25 项失败；Core 缺少排序错误目录注册。LazyMind lint/doc_check 已通过。
- 附件测试对齐已实现的原生 PPTX 按需入口 `pptx_analyzer`，验证原生文字、页数、分页结束、禁止 OCR 与原文件不变；上传不预处理及通用入口拒绝 PPTX 的约束保留。附件工具禁用测试覆盖每个工具单独启用及全部禁用。
- 工作流测试对齐内部素材 `user_visible` / `exposed` 及素材页仅显示摘要的约定，不删测试、不跳过失败用例。
- Windows 额外 15 项失败来自测试使用系统默认编码读取中文 YAML/JSON/源码，相关读取显式指定 UTF-8。
- Core 注册 `order must be a complete permutation`，错误码 `2003173`、HTTP 400，并补中英文翻译及生成的前端错误文案。
- LazyLLM gitlink 从 `d9887e1d` 更新到已推送的 `d14216a8`，本轮没有修改 LazyLLM；未改任何 doc_check 测试。
- 本机验证：算法全量 **4685 passed、23 skipped、21 subtests passed**；LazyMind doc_check **2 passed**；跟踪源码 Python lint、Go 格式/状态后端边界、跟踪文件 workflow naming、migration immutability、test locations、错误翻译同步及 diff 检查通过。
- Core 全包测试中，错误目录及 workflow 等包通过；本地文档目录测试需使用与 CI 一致的 UTC，Git 下载测试需系统 Git 并清除继承的 GIT_EXEC_PATH（本机自定义 Git 的动态库缺失）。对应失败包已复验通过。未在本机运行 Windows/Linux 新矩阵，交由 push 后 CI 复验。
- 原有未提交的本地 PPT demo 改动保留且不入提交。完整工作区 lint 会扫描该未跟踪 demo 并报既有问题，因此另对跟踪文件验证。未重启服务。

## 2026-10-26 非 Windows 项实际补测及 Windows 交接清单

本节是新增实测结果，不把前文的“未复现”改写成“已修复”。按用户最新要求，暂停更新飞书，只在本地 report 记录。暂停前仅在飞书第 16 项追加了 URL 条件复测，保留原有“desktop nova 模型使用正常”备注；其余项目未写入飞书。

环境：macOS arm64、本机算法 Python 3.11.15、Go 1.25.1。开始回归时主仓库 HEAD 为 `d88a02aec`，测试期间另一提交将 HEAD 更新为 `582e7bce3`（17:27，subagent 策略移动）；随后额外重跑受影响的 runner/PPT 编辑测试。LazyLLM 为 `d14216a8`。本次测试使用当前工作区，包含已有未提交 demo 改动，并非干净发布产物；没有重启服务，没有改供应商或业务实现，没有修改 doc_check 测试。新增的临时诊断测试执行后移出源码目录，作为证据保存。

### 测试结果与证据

证据目录：[docs/reviews/report-retest-2026-10-26](../reviews/report-retest-2026-10-26/)。测试日志、诊断脚本、JSON 输出都保存在该目录；临时数据库、合成附件、测试 HTTP 服务已清理，未修改现有业务库或密码。

| 实际执行 | 结果 | 证据 |
|---|---|---|
| SQLite proxy/config/queue、Excel、附件读取、任务分类、动态模型、subagent runner 共 8 个算法测试文件 | 1394 passed，9 warnings | `python.log` |
| HEAD 更新后：subagent runner 与 PPT 编辑回归 | 143 passed，1 warning | `runner-current-head.log` |
| 原生 Office reader 与 Windows Milvus 补丁脚本测试（补丁逻辑在 macOS 上运行） | 18 passed，2 subtests passed | `office-patch.log` |
| local-runtime-manager：`go test ./... -count=1` | 所有含测试包通过；主包 65.723 秒 | `manager.log` |
| local-proxy：`go test ./... -count=1` | auth/config/server 包通过 | `proxy.log` |
| Core：`go test ./localworkspace ./doc ./documentdirectory -count=1` | 三包通过 | `core.log` |
| 前端 localSession / LocalWorkspaceControl | 53 passed | `frontend.log` |
| 补充诊断：403 自动登录、无效 bootstrap 凭据、hoisted 元数据、活跃事务 TTL | 4 个诊断用例达到所述预期；部分预期是“错误仍会出现”，不表示已修复 | `remote-login.log`、`password.log`、`extra-go.log` |
| 当前真实源码 URL/type 调用、进程崩溃软锁、依赖互转、动态参数注入、85.7 MB CSV、100 次 HTTP 请求及断连 | 详见下表，不做一概通过结论 | `probes.json`、`queue-crash.json`、`deps-catalog.json`、`extra.json`、`http.json` |
| 实际 Office 转换函数：中文带空格路径、两份并发 DOCX→PDF | 显式配置字体与临时 libreoffice 命令别名后成功，每份 9930 字节，1.45 秒 | `office.json`、`office.log` |

Go 执行使用 `TZ=UTC` 并清除宿主 `GIT_EXEC_PATH`，避开既有本机时区和自定义 Git helper 环境问题；没有据此修改生产逻辑或测试断言。Python 命令设置 `PYTHONPATH=algorithm:algorithm/lazyllm`；附件测试明确使用仓库现有 Pandoc。诊断测试只模拟外部 HTTP/认证响应，保留被测解析、连接、权限和状态实现。

### 41 项对应结论

| 编号 | 本次实际结果 / 未覆盖边界 |
|---|---|
| 1 | 本机 `git ls-remote upstream refs/heads/main` 成功，网络访问可用；不说明其他机器无需代理，也未新建完整 submodule checkout。 |
| 2 | 当前 manager、proxy、Core 相关包实际编译并测试通过。没有复现默认 GOTMPDIR 配置导致构建失败；不测试用户手动设置无效目录的环境错误。 |
| 3 | 当前 macOS 前端依赖可运行 53 项测试；Windows pnpm isolated/junction 安装链路交 Windows 测试，不能外推。 |
| 4 | manager 全套包含 `TestAlgorithmPreparePythonKeepsExistingVenvAfterInstallFailure`，再次通过：已有 venv 安装失败不会被清空。不是原子升级；Windows PE 资源更新失败仍交 Windows 实机。 |
| 5 | 当前 venv 实际 import faiss 1.15.0、Stemmer、fontTools 4.67.0 成功。仅验证本机已安装依赖，不证明 Windows 冷安装通过。 |
| 6 | 本机 NumPy 1.26.4 / pyarrow 25.0.1 各一份分发记录；实际 NumPy→Arrow→NumPy 数据互转、结果校验成功。原机器重复 dist-info 状态未取得。 |
| 7 | 真实 HTTP/1.1 回环服务，8 个线程在同一 Connection 提交 100 次请求，服务端只见 1 个客户端端口；服务端断开响应时抛错，失败请求只发送 1 次；下一次显式调用成功重连。另有 12 次顺序复用回归通过。未做长时间空闲、千连接负载或 Windows 端口耗尽压测。 |
| 8 | manager 中 Windows 参数文件开关的跨平台环境构造测试通过。真实 Windows 超长 Relay 命令交 Windows 实机。 |
| 9 | 从当前算法 venv 启动真实 Python 子进程，sys.prefix 与父进程一致，requests 可导入。完整 Relay/DSH/嵌入式 worker 未运行，不能证明所有 worker 环境。 |
| 10 | manager 全套环境构造测试通过；本次未运行外部 DSH 启动链，不把源码环境检查记成 DSH 泄漏已解决。 |
| 11 | **当前依赖下未复现遗留软锁阻塞**：filelock 3.32.6；测试进程真实获取 SQLiteQueue 锁后 `os._exit(0)` 留下锁文件，新进程无需手动清锁即可入队、出队。另测空遗留锁也自动恢复。此结论依赖当前 filelock 版本，不外推旧版本或远程文件系统；代码仍未提供显式有界等待。 |
| 12 | 本机 8090 实际 HTTP 返回 200；不涉及报告指定的 Windows Caddy/Go 构建 panic，交 Windows 指定产物验证。 |
| 13 | **条件问题仍可复现**：临时完整 vite 文件布局配 `nodeLinker: hoisted`、不含 runtime virtualStoreDir 的元数据，实际 `frontendNodeModulesReady` 返回未就绪及路径不匹配原因。只验证就绪判断，不等于所有 pnpm hoisted 元数据都失败；未更改现有依赖树。 |
| 14 | 本机 Vite/Vitest 转换及测试成功；Windows TEMP 拒写属于 Windows 环境项，留交接步骤。 |
| 15 | manager SQLite alias / 算法环境回归通过；未将用户现有 Docker .env 注入运行服务，也未认定任何用户配置必然无污染。 |
| 16 | **仍有条件缺陷**：直接调用当前 `_get_chat_url`，`https://token.sensenova.cn/v1` 变成 `/chat/completions`，丢失 `/v1`；`/v1/` 与完整 `/v1/chat/completions` 保留路径。未向真实模型发送请求。与飞书“desktop nova 正常”不矛盾，输入条件不同。 |
| 17 | 实际 proxy 测试验证 loopback 放行、LAN 默认拒绝、显式允许 LAN、伪造 forwarded-for 拒绝。没有部署公网穿透。 |
| 18 | 补测 ensureLocalSession 收到 HTTP 403：抛错且只有 1 次 fetch，没有函数内部无限重试；已有会话竞争/刷新测试通过。**完整浏览器路由登录循环仍未验证**，需要远程入口与路由联调，不能标已解决。 |
| 19 | proxy CORS 测试验证已允许 Origin / 预检放行、未知 Origin / 预检拒绝及无 Origin 情况，通过。公网具体域名仍需在部署配置中验证。 |
| 20 | 模拟认证服务拒绝 bootstrap 凭据，真实 AdminSessionManager 返回失败，仅 1 次认证请求；原有刷新失败回退登录测试通过。**旧密码条件仍会失败**，没有自动修正配置。本次未修改任何真实密码。 |
| 21 | GBK PowerShell / 中文配置是 Windows 项，本机不测试；交接含具体文件内容与键值比较方法。 |
| 22 | 当前 DOCX/表格原生读取、PPTX 专用 analyzer 和 RAG 关闭场景测试通过，不能再写成“没有 office-convert 就都不能聊”。旧 DOC/PPT 或需转换成 PDF 的场景另算。 |
| 23 | **默认配置问题可见**：当前 macOS 直接导入转换服务报 `no CJK translation font is installed`，默认候选路径面向 Linux；设置现有 Arial Unicode 字体并提供临时 libreoffice 命令包装后，真实转换成功。不是默认部署已通过；Windows 原生适配另测。 |
| 24 | 当前可重定位 Python venv 调用真实转换函数成功（配置同 #23），所以“venv Python 必失败”不成立。未注入损坏的 PYTHONHOME，也未验证 Windows LibreOffice 环境继承。 |
| 25 | 两份真实并发转换使用独立 profile、中文带空格来源路径，都产出有效非空 PDF。只覆盖成功并发；超时后进程树清理、残留 soffice 与 Windows 文件锁尚未验证。 |
| 26 | Windows 文件夹选择 GUI 不在 macOS 测试；交接普通目录、盘符根目录、取消操作。 |
| 27 | subagent runner、Core localworkspace 权限/宿主访问回归通过；trusted、工作区授权、workflow full trust 是不同控制条件，不能概括为 workflow 一律不能用 shell。 |
| 28 | 实际合成小 XLSX 多 sheet 完整读取、201 行预览、RAG 关闭测试通过；原生读取无需 PDF/OCR。没有证明公式缓存、隐藏行、合并单元格语义已完整保留。 |
| 29 | manager 当前平台全套通过，但 Windows PID 复用/taskkill 竞态不在本机执行；不得写成误杀风险已消除。 |
| 30 | **部分链路实测通过**：85,656,011 字节合成 CSV 通过实际提取器，0.446 秒返回 6693 字符预览，inline=false、total_rows=-1（未读取全表维度）；SQLite executemany 分批及单行超限拒绝回归通过。仍不等于 81 MB XLSX→切块→45 分钟 embedding→入库全链路通过，后者缺隔离集成环境/模型调用条件。 |
| 31 | Milvus Windows 补丁脚本测试在 macOS 通过，只能证明补丁转换逻辑；Windows 已安装 wheel、manifest 覆盖、普通源码安装是否打补丁仍需 Windows。 |
| 32 | **缺陷仍存在**：真实 SiliconFlowRerank 实例的 type 为 EMBED；动态 `/v1` 与 `/v1/` 都生成 `/v1/embeddings`，显式 `/v1/rerank` 正常。本次不改 LazyLLM，仅记录可复现错误；未调用远程服务。 |
| 33 | 补测实际 SQLite HTTP 事务：测试 TTL=500ms，每 100ms 查询一次，800ms 后提交成功，证明操作续期，不是固定总时限。既有遗弃事务到期释放测试通过。生产 5 分钟无 SQL 的长 embedding 风险仍在，没有将测试 TTL 改进生产设置。 |
| 34 | Core doc/documentdirectory 测试通过，覆盖失败解析可重试、无活动任务的残留安装状态可删除、实际删除及引用清理。**未完成真正杀 worker / Core 后 WORKING、DELETING 状态恢复的全链路故障注入**，不能关闭该项，也不能拿空数据库当证据。 |
| 35 | 当前任务分类回归通过，包括多 JSON 文本提取；已有 fix 合入记录仍成立。没有据此声称模型输出任何格式都可解析。 |
| 36 | 调用真实 `_merge_dynamic_forward_kwargs` 并提供测试 bucket，返回结果包含动态 url 和 model；算法动态配置测试也通过。这里只替换配置来源，未替换被测注入方法；未进行真实供应商请求，#16 路径缺陷仍单列。 |
| 37 | Core localworkspace 实际准备/决策认领失败恢复测试通过。preparing 本身不是可决策状态；长期 preparing 的具体现场仍需 run/operation 状态证据。 |
| 38 | Windows token/ACL 完整性级别不在 macOS 测试，交 Windows 按普通桌面入口检查，不沿用管理员 shell 的结果。 |
| 39 | 前端权限保存、Core 工作区授权/操作、算法 subagent 回归通过；未通过真实模型完成 allow_all 下整个文件写入流程，因此“实际 write 卡住”仍待集成验证。 |
| 40 | 动态模型配置及 Core owner/workspace 隔离回归通过；未读取用户密钥或据此断言实际 embedding 角色已配置。需在隔离会话验证选定角色→embedding 请求→检索。 |
| 41 | 实际解析当前 model_catalog.yaml，确认 deepseek-flash、deepseek-v4-pro、deepseek-v4-flash 均存在；未验证供应商实时型号可用性、权限和回答效果。 |

### 交给 Windows 的测试步骤

请在 **NTFS 本地盘**检出目标主仓库及 gitlink 指定子模块，记录两个 SHA、Windows 版本、安装方式（源码/桌面）与 Python/filelock 版本。源码与桌面分开测试；不要用 WSL 路径结果替代原生 NTFS。使用临时工作区与合成文件，不清空现有用户数据库。

| 项目 | Windows 端操作 | 预期与需回传证据 |
|---|---|---|
| #3、#13、#14 前端依赖 | 在独立目录分别用 isolated、hoisted 安装；运行启动与页面访问。分别使用正常可写 TEMP、中文/空格 TEMP 路径。 | 依赖检测应与实际可启动状态一致；保存 .modules.yaml 的 nodeLinker/virtualStoreDir、缺失文件路径、esbuild 错误和 HTTP 状态。权限错误须与缺包分开。 |
| #4、#5、#6 Python 安装 | 在一次性环境做冷安装和已有 venv 升级；复现 PE 安装失败时，比较失败前后解释器是否仍存在、是否可启动。检查重复 dist-info，并实际导入 NumPy/Arrow/faiss/Stemmer/fontTools。 | 失败不得清空原 venv；返回原始安装错误。回传 pip/uv check、分发版本、解释器路径和 PE 错误上下文。不得在用户主环境刻意制造破坏。 |
| #8、#9、#10 worker | 从普通桌面及源码各启动一次 Relay/DSH；传超长参数（超过 Windows 命令行上限），检查参数文件与 worker 的 sys.executable/prefix、requests 导入、PYTHONPATH。 | 参数文件生效、worker 可加载所属环境依赖、没有旧 DSH 路径泄漏；回传脱敏路径/日志。 |
| #12 Caddy | 使用报告实际报错的 Caddy 产物，记录 `caddy version` 与 Go build 信息；从普通桌面启动后多次请求首页及代理路由。 | 不 panic；若失败回传完整 panic 栈与可执行文件版本，不能只报“2.10.2”。 |
| #21 编码 | 临时配置中插入中文注释，并在其后加可识别测试键；使用 Windows PowerShell 5.1（GBK 默认代码页）和项目实际启动入口读取。 | 注释后的键和值不丢失；回传文件编码、代码页、实际解析键列表（脱敏）。 |
| #23、#24、#25 Office | 配好 Windows LibreOffice 与字体；普通源码 venv 和桌面环境分别转换中文/空格路径 DOCX/XLSX/PPTX；并发两次；隔离环境再做超时失败后重试。 | PDF 可打开、内容正确、独立 profile 无冲突；超时后无本次转换遗留进程占锁，不误杀用户 Office。记录命令、版本、返回码、PID。 |
| #26 GUI | 普通桌面入口打开文件夹选择器，依次选普通目录、盘符根目录、中文目录并取消一次。 | 窗口可见且非阻塞；返回路径准确，取消不产生错误绑定。 |
| #29 PID | 在一次性测试环境验证 registry 的 PID/StartID 不匹配时拒绝结束进程；再验证正常退出清理。 | 不杀身份不匹配进程；日志含身份比较与拒绝原因。不要通过随机杀实际用户进程制造 PID 复用。 |
| #31 Milvus | 分别核对桌面包、普通源码 venv 中 manifest 写法；对临时 Milvus 数据做重复 manifest 更新并重启读取。 | 没有 WinError 183，已有目标可安全替换；记录 wheel 版本、补丁是否应用与调用栈。 |
| #38 权限 | 普通桌面启动 Python 后检查进程完整性级别及临时工作区读写；与管理员 shell 单独比较。 | 不用管理员进程的 High token 代替普通启动结论；失败时回传 token/ACL 与实际拒绝路径，不直接修改 ACL 掩盖问题。 |

另外，#18 公网路由、#30 慢 embedding 大表入库、#34 进程中断后的状态恢复、#39 真实模型写文件、#40/#41 真实模型角色与权限，属于**跨平台集成待测**，不应全部转交成 Windows 专属问题。当前只完成上表明确列出的局部验证，未具备隔离远程入口/真实模型故障注入条件，仍保留待测。


## 2026-10-26 补测后修复与硅基流动真实接口验证（最新结论）

本节更新前一节修复前的观测结果。飞书仍暂停更新；Windows 专属项目与尚未覆盖的集成场景维持原交接清单，不标为已修复。

- **#16 已修复**：LazyLLM 聊天 URL 按 URL path 添加 `chat/completions`，无尾斜杠时不再丢失 `/v1` 或自定义代理路径，保留 query，完整 endpoint 不重复拼接。覆盖静态构造地址和运行时动态地址。
- **#32 已修复**：在 rerank 基类统一返回 `RERANK`，SiliconFlow 等继承者不会再错误继承 EMBED；动态基础地址走 `/rerank`，完整重排地址保持不变，普通 embedding 仍走 `/embeddings`。供应商保持薄封装，没有额外吞错或请求重试。
- **#13 已修复**：manager 解析 pnpm 元数据的 nodeLinker；hoisted 不再要求 isolated 专属 virtualStoreDir，但继续校验 Vite 可执行入口和模块文件。isolated 的路径校验保留；新增测试确认现有有效 hoisted runtime 目录不会被清空。
- LazyLLM 修复提交：`abf630b8`（已推送 origin/codex/glm-supplier-1026）；本次 LazyMind 提交将 gitlink 同步到此提交。

验证：新增请求级测试在修复前 17 项失败，修复后与既有在线运行时测试共 **92 passed**；manager 全套通过（主包 65.651 秒）；LazyMind 算法 **4688 passed、23 skipped、21 subtests passed**；LazyLLM 中英文 doc_check 分别 **1476 passed、19 xpassed**；按 LazyLLM 仓库配置执行 changed-file flake8 通过，LazyMind Go 格式及测试位置检查通过。没有修改 doc_check 测试或用放宽断言消除失败。

用户提供 key 后新增真实 SiliconFlow 请求（key 未写入文件、日志、报告或 Git）：

| 请求 | 模型与输入 | 实际结果 |
|---|---|---|
| 模型目录 | `/v1/models` | HTTP 200，确认本次模型在账号可见目录中 |
| 重排：基础地址 `/v1` | BAAI/bge-reranker-v2-m3；查询“中国的首都是哪里？”；两条合成候选 | 实际 `/v1/rerank`，北京候选排名第一（约 0.9875），成功 |
| 重排：基础地址 `/v1/` | 同上 | 实际 `/v1/rerank`，排序相同，成功 |
| 重排：完整 `/v1/rerank` | 同上 | endpoint 保持不变，排序相同，成功 |
| 聊天：无尾斜杠 `/v1` | Qwen/Qwen2.5-7B-Instruct；要求仅回答 OK | 实际 `/v1/chat/completions`，返回 `OK` |
| Embedding：无尾斜杠 `/v1` | BAAI/bge-m3；合成中文句子 | 实际 `/v1/embeddings`，返回 1024 维向量 |

证据位于 `docs/reviews/report-retest-2026-10-26/fix-*.log` 和 `siliconflow-live-*.json`。真实接口验证针对本次账号、模型及测试输入，不代表整个知识库入库/检索、Windows 桌面或所有供应商已端到端通过。


## 2026-10-26 Windows 原生补测与配置编码修复（WSL 主机）

已直接切换并拉取 `origin/cst/ppt_template`，主仓库基线 `7154e8451`；子模块只检出分支已有 gitlink `abf630b8`，没有修改或提交其指针。此前 macOS 记录保持原有环境边界。本节使用宿主 Windows PowerShell 5.1.26100.9711（默认代码页 936）及真实 Windows EXE，不以 Linux 测试替代 Windows 结果。

| 项目 | 本次复查判断 | 证据与剩余边界 |
|---|---|---|
| #21 配置编码 | **确认中文配置值被错误解码，已修复。** | NTFS 临时 UTF-8 无 BOM 配置经过实际 Import-EnvFile：修复前中文值不相等、注释后的 ASCII 键正常；不能描述成全部键被吞。两个配置读取入口显式 UTF8 后通过，新增 Windows Go 回归调用真实 PowerShell 解析函数。旧 GBK 编码配置应转换为 UTF-8。 |
| #4 原 venv 保护 | **Windows 平台回归通过。** | manager 原生测试验证安装失败保留旧解释器；模拟安装器，不等于真实 uv PE 错误已消除。 |
| #13 hoisted | **Windows 平台回归通过。** | isolated/hoisted 就绪判断和保留已有 hoisted 目录测试通过；实际冷安装仍单列部署验证。 |
| #8、#10 参数与环境 | **Windows 环境构造回归通过。** | 参数文件开关及 gateway Python 环境隔离通过；没有完整超长 Relay/外部 DSH 运行证据。 |
| #29 进程管理 | **现有 Windows 测试通过，真实 PID 复用仍未证明。** | 包含子进程创建标志、Job 分配失败回退、进程树过滤；不得等同于所有身份竞态消除。 |
| Windows 启动 doctor | **补入当前分支缺失的已有修复。** | 移入 cst/fix_1026 的 module 读取诊断：同一 go.mod 在 NTFS 临时目录通过；WSL share 锁失败给出本地盘/WSL 启动提示。 |

manager 主包：WSL 交叉编译 Windows EXE 后由 Windows 执行，197 个顶层测试通过、4 个跳过、0 失败。跳过：外部网络发布包 opt-in、8090 已占用、PATH 无 sleep、Unix chmod 专用测试。不是完整 `go test ./...` 所有子包结果，也不是原生 Windows 冷构建结果。[验证说明](../reviews/report-windows-retest-2026-10-26/README.md)。

空间核查纠正：C 盘仅约 3 GiB 可用，但 D 盘 NTFS 有约 235 GiB 可用，因此不能再用 C 盘不足作为本机无法原生部署的结论。已在 `D:\LazyMind-report-1026` 展开主仓库与锁定子模块的干净源码快照，再覆盖本次启动脚本修复；独立 runtime 数据目录 `D:\LazyMind-report-1026-data`，服务端口整体加 20000（前端优先 28090、实际避让至 28091，SQLite 39081）。已执行 Windows 原生启动，阶段结果及最终阻塞见下文，不记为整套通过。

按用户授权清理 Docker：仅移除未被容器引用的镜像及超过 7 天未使用的构建缓存，分别报告回收 969.3 MB、83.2 GB。没有删除容器或数据卷；此数字为 Docker 报告，不等于宿主 VHD 文件已自动缩小。

补充已完成：Windows 系统 Python 加载本次快照的实际 SQLite proxy，访问上述 Windows SQLite 服务，连续 100 次 SELECT 1 均正确且只使用 1 个客户端端口；不会据此声称 Windows 端口耗尽压测通过。Windows Python 在 NTFS 快照执行 `python -m unittest discover -s tests -p test_desktop_windows_milvus.py -v`，10 项通过（含中断后索引不发布、失败保留已有索引），验证补丁逻辑，不代表普通源码安装已自动应用补丁或真实 Milvus 数据导入完成。

### 新定位：SQLite 认证库迁移中断后无法重启

这是本轮停止首次启动时在**独立测试库**触发的中断恢复问题，不应误记成正常全新安装必然失败或 Windows 专属问题。现场版本号停在 c9d0d3d31d10，d8b8d6142f1a 的前 5 个新列已经存在，重新启动再次加列失败。原现场完整保留在 `D:\LazyMind-report-1026-data\data\stores\sqlite\auth-interrupted`；没有清理用户现有数据库。为继续新环境验证，测试实例另建认证库。

根因是 Python sqlite3 的 legacy 事务模式不会为 DDL 自动发 BEGIN。修复仅调整 `backend/auth-service/alembic/env.py` 的 SQLite 在线执行：显式 BEGIN IMMEDIATE，把结构变更和迁移版本更新放进同一事务；不改历史迁移文件、版本号或业务表定义，PostgreSQL 路线保持原事务方式。**此改动预防后续半迁移，不会自动修复已经损坏的旧库**，旧现场应按具体已执行步骤处理，不能盲目推进版本号。

新增回归实际执行迁移，并在目标 ALTER TABLE 前通过 `os._exit(71)` 终止进程；修复前留下 5 列而失败，修复后结构/版本均回到原状态，重启和重复 upgrade head 均成功。Linux、原生 Windows、隔离 Docker 三处通过；认证服务完整现有测试 225 passed、27 subtests passed，相关 flake8 通过。新测试位于认证服务现有 CI 收集目录。此前观察到的认证健康失败已恢复，Windows Auth/Core/Proxy/SQLite 与自动登录接口均返回 HTTP 200，尚不将前端/Chat 标为通过。

### 原生部署与交接边界

本轮确实执行了 NTFS 上的 `local/scripts/local-win.ps1 up`（即 make local-win-up 调用的入口），完成 Go 原生构建、Pandoc/Skill catalog、Python/venv 和 pnpm isolated 依赖准备。前端两次默认构建分别退出 0xc0000005、0xc0000409，后者明确报 `memory allocation ... failed`；单独限制 Node 堆至 2 GiB、Rust 并发至 2 后，报 JavaScript heap out of memory。RAG pip 同期在文件校验读取时报 MemoryError。Node 为 24.14.0，当时 Windows 可用虚拟内存约 2.6 GiB；无法据此认定 Windows TEMP 权限、pnpm isolated 缺包或 Caddy panic。

已暂停此前 `lazymind-fix1026` Docker 测试栈并关闭 WSL 本地测试服务以释放资源，保留数据；原有其他 Docker 项目未停止。后续原生重跑需有足够内存余量并避免并行执行多个平台的整套部署。GUI、普通桌面 token/ACL、Office Windows 原生转换、真正 PID 复用、完整 RAG/慢 embedding/真实模型写文件仍维持交接，不能以本轮局部测试关闭。

临时 PostgreSQL 16 容器还验证了真实 upgrade head、重复 upgrade head 与 head 读取，成功后已移除。最终 Windows 测试服务也执行 down 并确认 stopped，独立数据库、缓存及源码快照均保留。若重跑，先在 PowerShell 设置 `LOCALAPPDATA=D:\LazyMind-report-1026-cache\localappdata`、`GOCACHE=D:\LazyMind-report-1026-cache\go-build`、`GOMODCACHE=D:\LazyMind-report-1026-cache\go-mod`、`UV_CACHE_DIR=D:\LazyMind-report-1026-cache\uv`，再在 `D:\LazyMind-report-1026` 执行 `local/scripts/local-win.ps1 up`；不要将临时配置覆盖到用户生产实例。

最后一次在测试服务全部停止后，独立设置 `NODE_OPTIONS=--max-old-space-size=3072`、`RAYON_NUM_THREADS=2` 构建：完成 9807 个模块转换后仍因 V8 堆内存耗尽退出 134。因此未继续启动全栈，不能标记前端或 Chat 已验收。一次与 down 重叠的构建受到停止流程干扰，未计为有效验证；最终结论采用停止完成后的独立重跑。尝试释放 WSL 文件缓存需要 sudo 密码，未实际执行，也没有调整系统 pagefile/内存设置。证据 `frontend-3gb-memory-failure.txt`。

## 2026-10-27 合并 upstream main 并解决冲突

先同步远端 `cst/ppt_template` 的 Windows 修复 `7ebf5a464`，再合入 upstream main `71a0e5a82`（包括 #872 工作流/聊天/子任务状态同步）。唯一文本冲突位于 `tests/algorithm/test_workflow_sdk.py`：保留本分支的联动排序、视觉页序测试，并保留上游的 trigger_history_id 传递测试，没有删除或放宽断言。LazyLLM 指针仍为 `abf630b8`。

合并后验证：算法 4712 passed、23 skipped、21 subtests passed；前端 workflow-panel 22 文件、199 项通过；Core workflow/chat/localworkspace 三包通过；2359 项错误码翻译同步检查通过；无未解决的 Git 冲突，diff whitespace 检查通过。本地未提交 demo 改动单独备份，不纳入合并提交；服务未重启。

## 2026-10-27 更新 LazyLLM 上游指针与 CI 异步测试修复

LazyLLM 从 `abf630b8` 更新至上游 main `7a07b4e6`，包含已合入的 GLM #1447 和 prepared chat requests / per-call execution controls #1363。原 GLM、接口路由修复随上游提交保留。

[CI 失败任务](https://github.com/LazyAGI/LazyMind/actions/runs/40438029391/job/121972127008?pr=826) 虽名为 Test Workflow Panel boundaries，实际执行前端完整测试。失败是 `FeishuReuse.test.tsx` 在账号详情异步加载完成前同步查找连接中提示。改为等待提示出现，继续验证连接中禁止重连、完成后显示断开按钮以及不调用 resume；未修改通知业务逻辑或删减断言。

升级子模块时，算法动态模型路由测试的模拟 HTTP 响应缺少新版传输层使用的 `raw`，补齐 urllib3 HTTPResponse；保留实际共享模型调用与供应商、模型、URL、鉴权断言。不改 doc_check 测试或 LazyLLM 源码。

本地 macOS 验证：算法全套 4712 passed、23 skipped、21 subtests passed；LazyLLM 接口路由、请求控制、请求审查与传输测试 150 passed；飞书相关测试 9 passed；前端类型检查及修改文件 ESLint 通过。LazyLLM 测试所需 trustme / PySocks 安装在临时目录，不改服务依赖。额外对算法测试文件执行 flake8，结果均为 HEAD 已有问题（未使用导入与空白格式），本次无新增；该目录不在主仓库 lint-python 范围内。

按 CI 参数执行前端全套 `vitest run --maxWorkers=2 --minWorkers=1`：439 个文件、4071 项全部通过，耗时 402.13 秒（当前工作区含本地 demo 测试）。`git diff --check` 通过。以上为本地验证，推送后的远端 CI 结果另行确认。未重启服务，未修改飞书表格；本地 demo 改动不纳入此次提交。

## 2026-10-27 再次同步 main：模型自动回退合并

合入 upstream main `2ddbde274`（#841，聊天和子任务模型自动回退）。LazyLLM 最新 main 仍为 `7a07b4e6`，它包含上游 LazyMind 使用的 `ff44994c`，合并后保留较新的 `7a07b4e6` 指针。

`subagent/runner.py` 的唯一文本冲突同时保留模型候选、当前候选与初始切换信息，以及本分支 PPT 编辑扩展的工具调用/失败限制。动态路由测试的 HTTP raw 模拟采用上游实现，去掉重复初始化。

回归发现并修复两处语义兼容问题：上游子任务回退测试的工具边界 mock 补充接受本分支 `agent_type` 关键字参数；工作流等仅获取配置的调用不能确认聊天回退协议的模型切换，因此固定模型不可用时继续返回明确错误，避免静默换模型。保留原回归断言，没有改成容许错误行为。

验证：算法全套 4876 passed、23 skipped、27 subtests passed；Go chat/modelconfig/subagent/workflow 四包通过；合并涉及的算法代码 flake8、前端类型检查、2359 项错误码翻译检查通过。前端按 CI 参数执行全套，439 个文件、4147 项通过，耗时 398.69 秒。无未解决的冲突，diff whitespace 检查通过。以上为本地验证；未重启服务、未修改飞书表格；本地 demo 工作单独备份，不纳入合并提交。
