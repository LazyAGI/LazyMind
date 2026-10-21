# 按需加载工具

用户设置 `enable_tool_retrieval` 默认关闭，通过 `/user/chat-settings` GET/PATCH 管理。
设置页位于任务设置的对话页；修改从下一次请求生效。普通 SubAgent 与 Workflow
步骤继承发起请求的快照，第三方执行器保持原有行为。关闭后继续使用原 Toolkit 展开机制。

开启后，`search_tools` 通过已安装的 bm25s 检索当前允许、尚未完全加载的工具或组。
每个组是一个索引条目：组名、原组描述及当前可用且尚未加载成员的名称、说明和参数文本共同
参与评分，不再先给成员评分后取最高分。独立工具沿用原有检索文本。

文件基础能力由当前已注册的 `FileSystemToolkit` 和会话资源工具
`read_file_resource` / `search_file_resource` 预加载，执行仍受工作区或资源权限限制。

按组检索与加载的能力包括：

- 云服务：FeishuFS、FeishuWikiFS、NotionFS、GoogleDriveFS。
- 业务：MailToolkit、KBToolkit、ExternalDatabaseToolkit、ScheduleToolkit。
- 写作：WriterCreateToolkit、WriterRevisionToolkit，分别加载。
- 记忆与技能管理：MemoryTools、SkillManagementToolkit。
- 搜索：WebSearchToolkit、AcademicSearchToolkit、WikipediaToolkit。

CloudFileToolkit 本身不整组加载，按实际云服务拆分；当前动态 Wiki 配置产生
FeishuWikiFS，因此不能只配置 FeishuFS。网页和学术搜索仍只选择当前可用的首个服务商。
MCP 按稳定 Server ID 构造 `mcp:<id>` 动态检索组，名称用于描述与检索；仅收录当前
角色实际注册且通过 `allowed_tools` 过滤的成员。没有 ID 的旧配置保持单工具检索。
MCPClient 支持接收稳定 Server ID；LazyMind 使用主线的 Host 授权身份构造逻辑，
通过通用运行元数据声明稳定 Server ID 与安全工具 identity。该 identity 还区分来源命名空间、
服务地址、传输方式、启动参数与 wire name，显示名称或凭据轮换不改变身份。
LazyMind 按稳定工具 identity 去重，再交给 LazyLLM ToolManager 注册。带稳定 ID 的 MCP
工具始终使用原始 wire name 和工具 identity 派生的固定别名，不依赖当前是否存在同名工具、
Server 显示名称或注册顺序。ToolManager 将 identity 当作不透明字符串，仅用于 hash；
可读前缀取自 adapter 的 `__mcp_tool_name__`，缺少时固定使用 `mcp`。相同 Server 内 `foo.bar` 和 `foo-bar` 也保持独立身份。
别名使用请求内 wrapper，保留原始远端调用及 schema 元数据，不修改共享缓存中的 callable。
最终名称仍碰撞时明确报错，不使用随注册集合变化的数字后缀。无 ID 的旧配置维持原名行为。
该名称消歧同时适用于开启和关闭检索的模式。其他独立函数保持单工具检索，
基础工具及 Host 场景必需工具保持原预加载规则。

LazyMind 显式设置 `max_search_results=5`、`matched_member_limit=3`；LazyLLM 提供相同默认值，
其他 Host 可通过这两个正整数参数覆盖候选上限和成员摘要上限。搜索不加载 schema。
`search_tools.limit` 默认取 `min(5, max_search_results)`，范围为 1 到配置上限；
检索使用英文停用词配置，建议使用英文能力关键词。组结果包含 `name/type/description` 及
`matched_members`：默认最多 3 个尚未加载的相关成员，仅展示名称和简短说明，不包含参数
schema。成员通过组内 BM25 选取，不改变组排名；仅组用途命中时可为空。`detail` 只控制
组介绍或独立工具说明的摘要/全文，成员始终保持简短。组内还有可能未展示的成员。

`load_tools` 按组名一次展开全部当前可用成员，下一轮直接调用，不需要 `get_*_methods`。
LazyMind 的 `RETRIEVAL_POLICY` 指导默认优先整组加载；明确仅需某一成员时才单独加载。
LazyLLM 的工具描述只说明组／成员加载及下一轮生效的机制，不指定整组优先策略。
仍允许按准确成员名加载和卸载；部分加载的组可继续检索并补齐，全部加载后隐藏。
索引随当前目录、尚未加载成员和描述变化，在下一次搜索时重建。已加载成员不再
贡献组分数；组描述自身命中时仍可返回该组，全部加载后不再构造候选。

检索展示描述由 Host 的 `GROUP_DESCRIPTIONS` 提供；LazyLLM 的可选 `group_descriptions`
对内置组只覆盖展示文本，缺省使用原组描述；动态 MCP 组使用 Server 名称构造描述。原 Toolkit 描述、gateway 和关闭检索时的激活逻辑
不变。原子目录新增 `group_descriptions` 祖先映射，确保外层组使用自己的描述。

加载在同一笔事务中先卸载再加载。未知名称、权限限制、必需工具保护或持久化错误都会
阻止提交。状态仍保存原子工具名，恢复时不会自动扩组。状态格式升级为 version 2：
version 1 只有旧 public name，无法可靠识别原来的 Server，因此忽略其中的全部可选加载记录，
重新计算 Host 必需工具及已记录 Skill 的当前依赖；可选工具需重新加载。只读预览不落盘，
初始化通过硬上限校验后才原子写入 version 2；失败保留旧文件。

工具软预算为现有有效输入预算的 10%。卸载后未超过阈值即可完整加入本批工具，
超过后只能卸载或幂等加载。Host 必需工具和已读取 Skill 的允许依赖绕过软预算，
仍受硬限制约束。普通加载、Host 初始化与 Skill 依赖新增均在状态事务提交前，由
Host 校验候选工具 schema 加最终系统提示、Skill 固定提示及当前输入与固定运行上下文。
固定内容超过有效输入预算时抛出 ToolExecutionError，加载、卸载及 Skill 依赖状态均不提交。
这次校验不计入可压缩历史、不执行压缩；压缩后、模型请求前仍检查完整上下文。
新加载定义只在下一轮模型请求生效。

状态位于 `agentic_workspace/tool-retrieval-state/`，按用户、会话和 Agent 标识隔离。
文件锁与原子替换保证加载成功前状态已落盘；上下文预览只读。恢复时重新验证当前目录
和 Skill 依赖，历史工具调用不重新激活工具。必需性消失后解除保护但不自动卸载。
跨 Host 同步范围与现有运行时目录一致。

## 验证

基于 LazyMind `83322b4` 和 LazyLLM `8cf9e610` 实施。最新 LazyLLM 移除了旧
`list_dir`/`write_file` 导出，因此主仓库文件包装器同时适配了新接口，保留原覆盖审批
和递归目录契约。

容器内验证包括：

- LazyLLM 检索、分组、预算原子性、下一轮曝光、线程状态传播及旧模式执行回归。
- AgentExecutor 搜索/加载/执行完整链、Skill 文件依赖、状态恢复/隔离/写失败、只读预览和硬预算。
- 用户设置保存、隔离、请求快照、SubAgent/Workflow 继承与隔离；文件及上下文压缩回归。
- PostgreSQL 迁移路径一致性、PostgreSQL/SQLite 新字段默认值与 up/down/up。
- 前端开关加载、保存、失败恢复和原任务入口设置回归。

前端全量 TypeScript 检查受已有 `src/modules/chat/utils/message.test.ts:49` 语法错误阻断。
未修改该无关文件。

## 本地对照与后续线上验收

2026-09-17，在无外部工具凭据、默认注册目录和内置文件工具下，以脚本模型驱动
真实 AgentExecutor 和 calculator，分别执行 `1+2`、`6*7`。两种模式均得到正确结果。
这验证执行链和工具定义占用，**不代表真实模型成功率或成本评估**。

| 指标（每个任务） | 关闭 | 开启 |
| --- | ---: | ---: |
| 模型请求轮次 | 2 | 2 |
| 峰值工具定义 token 估算 | 7826 | 2401 |
| 暴露但未使用的工具定义数 | 34 | 13 |
| 搜索/加载调用数 | 0 | 0 |

独立集成测试覆盖需要搜索和加载的路径。实际模型总 token、费用、缓存命中以及
飞书/Notion/Google Drive、知识库、邮件恢复的外部服务端到端效果尚未实测。
上线试用应对同批业务任务记录成功率及这些指标，再判断收益。

复用现有 `agent_lab_event_path`/`context_compression_event_path` telemetry：
`tools_ready` 记录每轮工具名称和 schema token 估算，结合既有工具调用事件统计未使用
schema、搜索/加载次数；模型 usage 中可用的成本和缓存数据沿用现有统计。未新增看板。

## 2026-09-18 分组检索与成员摘要验证

现有容器内：LazyLLM 工具检索测试 9 passed；LazyMind 工具检索、注册、文档装配及
Workflow SDK 测试合计 51 passed；LazyLLM 修改文件 flake8 和两仓库 diff 检查通过。

固定目录取自 136 个真实工具 schema。仅为离线构造目录绕过可用性过滤，网页搜索固定
GoogleSearch、学术搜索固定 SciverseSearch；不据此宣称这些连接器已授权或业务可用。
使用原来的 43 条人工英文能力查询和 3 条双意图查询，未按结果重新调词：

| 方式 | Top-1 | Top-5 |
| --- | ---: | ---: |
| 原子工具评分后按组取最高分 | 39/43 | 41/43 |
| 当前实现：组描述与成员信息组成单个索引条目 | 41/43 | 42/43 |

3 条双意图查询均在 Top-5 找齐两个目标。仅组介绍的搜索结果平均估算 185.5 token，
增加成员摘要后为 433.6 token；这里是相同结果集合的文本估算，不是模型实际计费。
“write a report”仍未召回 Writer，词汇覆盖和词形匹配的边界保持可见，未加入查询扩展。
邮件草稿、知识库上下文扩展、数据库 schema、Writer 补丁查询均展示相应成员摘要；
摘要是匹配提示，不保证最合适的执行方法总排第一。

使用现有 Qwen/Qwen3.8-Flash-Next 服务，以真实模型驱动受控工具调用循环，比较相同目录
和任务下的“仅组介绍”与“组介绍＋成员摘要”。覆盖草稿修改、数据库检查、论文搜索与
文档修订、宽泛邮箱管理、不支持的订票付款任务，每个条件每任务运行 3 次，共 30 次：

| 指标 | 仅组介绍 | 组介绍＋成员摘要 |
| --- | ---: | ---: |
| 目标业务组调用/不支持能力时未执行业务调用符合预期 | 15/15 | 15/15 |
| 调用未暴露的工具 | 0 | 0 |
| 搜索调用总数 | 38 | 33 |

检索与加载使用真实控制器；业务执行统一返回无副作用的模拟成功结果，未执行真实邮件、
数据库、Writer 或外部连接器操作，未验证业务参数执行正确性。此对照不是完整生产
AgentExecutor 的真实模型端到端验收；AgentExecutor 的搜索、组加载和下一轮调用链由
脚本模型集成测试覆盖。实际模型费用、缓存及总用量未统计。

两种展示方式的本批选择结果相同；成员摘要减少了部分额外搜索，但数据库任务搜索次数
反而从 5 次变为 6 次。不支持能力仍可能反复搜索。小样本不证明泛化收益或总体成本下降。

## 2026-09-18 #735 定向补齐验证

基于 LazyMind `ab9c8017`、LazyLLM `c48945c5` 的后续修改，在现有 chat 容器验证：

- LazyLLM 检索、工具调度与运行时测试：28 passed。
- LazyMind 检索、MCP 名称与缓存、AgentExecutor、工具注册、提示装配、上下文压缩与估算：128 passed。
- 覆盖 MCP 稳定 ID 分组、同名 Server 隔离、角色过滤、单成员与整组加载及旧模式。
- 覆盖加载／Skill／Host 初始化硬上限失败不落盘、混合卸载加载回滚；分别验证系统提示、
  当前输入、Skill 固定提示计入上限，可压缩历史不参与提前拒绝。
- 脚本模型验证加载失败作为工具错误返回后仍能进入下一轮；模型侧上下文和生成的
  `load_tools` schema 均包含整组优先指导。不是外部服务的真实模型端到端验证。

复用原 136 个工具 schema 的目录构造及原 43 条能力查询、3 条双意图查询，将旧控制器
和新控制器在同一目录上对照：两者 Top-1 均为 41/43、Top-5 均为 42/43，双意图均为 3/3。
额外加载匹配成员后重跑相同查询，31 条查询结果发生变化；成员摘要均排除已加载成员。
这些变化符合新评分语义，不以部分加载前后的排名相同作为验收条件。

### 跨 MCP Server 同名工具与 CI 适配

在 AgentExecutor 去重前对跨 Server／普通工具同名的 MCP 成员按稳定 Server ID 消歧。
仅包装冲突工具，保留 schema 签名、运行元数据和远端调用闭包；别名分配与输入顺序及
Server 显示名称无关，不修改共享缓存。不冲突成员及同一成员重复注册维持原行为。

测试使用真实 MCP adapter 和受控 client，覆盖检索开关两种模式、有／无同名普通工具、
两组分别加载并调用正确 Server、缓存复用、顺序交换和 Server 改名。相关回归 132 passed。
同时适配旧环境变量测试中已移除的 allow_unsafe 参数，以及新增迁移后的目录计数。

现有 chat 容器完整 algorithm 测试：2927 passed、16 skipped、18 subtests passed。
现有 Go 容器迁移目录与 Tool Retrieval 迁移定向测试通过；未修改 CI 工作流或业务授权逻辑。

### 机制与 Host 策略边界清理

跨 Server 的名称消歧移到 LazyLLM ToolManager 的合并注册入口，保留现有按需别名、
远端调用和缓存不可变行为。MCPClient 接收 Host 的稳定 ID；目录暴露 source/origin/identity，
LazyMind 按实际目录建立 Server 组。显示名称仍由 Host 保留，旧无 ID 配置行为不变。

LazyLLM 支持配置候选与成员摘要上限；LazyMind 显式保持 5/3。整组优先指导保留于
Host 最终系统提示，库层 load_tools 描述只说明加载机制。英文关键词提示保持不变。

现有 chat 容器完整 algorithm 回归：2927 passed、16 skipped、18 subtests passed。
最终 LazyLLM 检索、调度、运行时及 MCP 注册回归 42 passed，主仓库相关复验 41 passed。
API 文档定向检查 7 passed，全量 Python lint（algorithm/backend/evo）通过。
复用原 136 个 schema、43 条能力查询和 3 条双意图查询：修改前后 Top-1 均为 41/43、
Top-5 均为 42/43、双意图均为 3/3，部分加载后的查询结果也一致。
这些验证使用离线目录与受控 MCP client，不代表外部 MCP 服务端到端验收。

### MCP 稳定身份与加载状态修复

带稳定 Server ID 的 MCP 工具始终使用 identity 派生的固定模型名称，替代上文历史实现中
“发生冲突时才生成别名”的行为。加入或移除其他 Server、同名本地工具不会改变已有工具名称；
同 Server 的不同 wire name 即使规范化结果相同，也保留独立身份和调用路由。
LazyMind 通过公共 `get_tool_runtime_metadata` 读取元数据并在注册前按 identity 去重。

本次明确使 version 1 的可选加载记录失效，防止旧 public name 被当前其他工具复用。
Host 必需项与 Skill 依赖按当前目录重建，成功校验后写入 version 2；只读预览及失败事务
不改旧文件。此后状态继续保存稳定原子名称，不引入 identity 状态映射层。

现有 chat 容器验证：完整 algorithm 2930 passed、16 skipped、18 subtests passed；
LazyLLM 检索、注册、运行时、调度及文件授权模式定向回归 58 passed；API 文档定向检查
7 passed，两仓库相关 lint 与 diff 检查通过。测试覆盖真实文件状态跨请求恢复、增删
同名工具后仍调用原 Server、同 Server 标点名称碰撞、最终别名冲突拒绝和旧状态升级。
MCP 服务使用受控 client，不代表外部服务端到端验收。

后续 opaque identity 修正：移除 ToolManager 对 identity 的 JSON 解析。官方 adapter 的
模型名称保持不变，无需再次使状态失效。容器定向回归 LazyLLM 61 passed、LazyMind 44 passed，
修改文件 lint 与 diff 检查通过；未重复运行完整 algorithm 测试。

### 合并主线工作区授权与 CI 验证

合并 LazyMind main `66d2fe0d5` 及其固定的 LazyLLM `66cacd0d`。保留工作区授权、
SubAgent 权限快照和主线拆分后的文件资源实现，不恢复已删除的本地文件工具包装器。
MCP adapter 未传 server_id 时只声明来源，允许 Host 注入主线安全 identity；分组使用
稳定 Server ID，执行继续经过工作区授权。由于 Host identity 纳入主线授权作用域，旧
MCP 可见名称可能变化，对应可选工具需要重新加载；旧名称不会恢复为其他工具。

现有容器完整 algorithm：3074 passed、17 skipped、18 subtests passed；最终 LazyLLM
检索、MCP 注册、运行时、调度及授权模式定向回归 62 passed。额外验证 Windows 路径修复、
Go 迁移与对话设置、OpenAPI，四个客户端缓存均 fresh。完整 make lint 通过。

随后为解除子 PR 与最新 LazyLLM main `71579a52` 的冲突，合并其 Writer 更新，保留
云文件读取和 Windows 本地路径修正。最终重点回归 220 passed、5 skipped；主仓库完整
algorithm 再次 3074 passed、17 skipped、18 subtests passed。扩展 Writer 检查为
571 passed、10 skipped、7 failed；相同 7 项在未修改的 `71579a52` 上复现，其中 6 项
为容器缺少 Pandoc，1 项为 Feishu 测试夹具缺少 metadata 返回值，未纳入本 PR 修复。
