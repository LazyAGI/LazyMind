# 8091 实际部署与验收

日期：2026-10-20。P7 必需的变更范围验收已完成；以下分时记录保留发现和修复经过，最终结论见末尾。

## 部署证据

- 使用专用 Makefile + `runtime-8091.make.env` 停止 8091；停机前活动 SubAgent、Work 步骤、解析任务、未过期 generating 均为 0。
- 私有备份：`/Users/dengyuang/.local/share/lazymind-backups/8091/knowledge-mcp-20261020T044658Z`，332,165,595 bytes。十个 SQLite 使用 Backup API，全部 integrity_check=ok；保存对应原件、索引、独立配置和源代码补丁。恢复尚未演练，不称为已验证恢复。
- 完整分支启动退出码 0，耗时约 84 秒（不含技能/CLI 等前置构建）。Core cwd、Chat PYTHONPATH、Caddy frontend root 均指向此 worktree。
- 本轮仅验收进程启用 management、Artifact V2 及候选预算 128 resources / 32 MiB / 15 s / 4 MiB/s；默认开关保持关闭。
- 8090 Caddy PID 7676 未改变，HTTP 200；未操作其配置或数据。
- 浏览器真实进入知识库页面；历史论文集 6 份 PDF 显示已存储。另由原件验收脚本确认既有 parsed 文档的公开 get、search、内部 active filter 均可读。

## 首轮发现

- MCP create 缺省处理算法导致真实 DocServer `invalid algo_id: None`；已复用既有默认算法、增加回归测试并部署，后续真实导入解析成功。
- 工具预览 API 丢弃 Core 发送的 system_mcp_config；修复为请求局部传递，无全局状态写入，相关 49 项测试通过，部署后 MainChat 动态发现与建库成功。
- 现有默认模型 deepseek-v4.1-flash 返回 MissingSessionID；官方 DeepSeek 配置返回认证失败。未更改用户全局模型。
- 新专用会话显式选择现有 Kimi k3，真实返回“P7 模型连接成功。”，model_call_finished=finish、run_finished=completed。后续 Agent 验收使用该会话级配置。

## 后续证据

资源、原件、Chat/Work/CLI 由互不重叠的 KM-E2E fixtures 验证，原始 API 响应与凭据保存在私有目录；公开报告仅记录结果和必要标识。缺陷修复与重启后继续记录真实结果。

### 13:15 阶段记录

- 公共 MCP 11 项知识库工具、跨库整树迁移/空目录/预算拒绝/回执重放和部分成功，均完成真实 API 验收；预算三次串行 121 资源、32 MiB 实测约 0.79–0.82 秒，不等同并发负载或 p95。见 resource-live-acceptance.md / resource-budget-calibration.md。
- MainChat 实际动态加载并创建两库；CLI roots 握手及文件传输成功，越界和无 roots 拒绝。内部跨库批准返回新 ID；删除批准后资源消失；拒绝、快照变化和取消后旧确认均正确拦截。见 mcp-client-e2e-report.md。
- 页面普通编辑实测暴露无变化 learning capabilities 的多余 PUT；修复为只提交实际变化，12 项 UI 测试通过。重建后浏览器保存描述成功、对话框关闭，API 回读 scene/处理级别保持。截图保存于本任务 visualizations/knowledge-page-verified.png。
- 实际远程 PDF、固定 Work revision 和非默认租户导入均已持久保存、解析并可读取；历史文档检索保持。处理状态只读观察新增精确任务绑定和批量快照，501 文档查询 6 次；缺失证据返回 unknown。Core 已重载，等待新接口复验。
- Work 首次模型请求 400 被定位到 save_artifacts 项 schema 的未展开引用；真实相同模型请求仅修复此 schema 后返回 200。通用类型提示保留 Annotated，相关 91 项测试通过，待 Chat 重载后的真实 Work 验证。
- 内部 uploaded_file 初次返回 INTERNAL，公开入口对相同原件成功；正在用真实新会话复现并记录服务端根因。此项尚未通过。

### 13:22 追加结果

- 内部 uploaded_file 新会话和同会话第二轮均真实成功，分别创建 doc_79beaaccf1b886ef1341673e03241ab3、doc_ec67c510de8f7bf3631181de20972588；复用同一个受控原件引用，每次绑定独立。首次失败同期日志出现 proxy 事务嵌套错误，但缺少逐调用错误记录，不能断言它就是首次失败的唯一原因。
- 确认当前 conversationCheckpoint 仍裸 BEGIN IMMEDIATE：独立真实 HTTP SQLite proxy 红测复现并发写误入其他事务和 rollback 影响；复用已有代理事务 helper 后 commit/rollback/cancel/outer-tx、并发 Document+Receipt 和 race 通过。待安全点 Core 重载后再查实际失败会话。
- READY4 的原件状态验证全部通过：remote/Work/nondefault parse_status SUCCESS，coverage 分别2/2和1/1；专用文档删除后 native active filter 从1到0、旧 public get 拒绝；既有 rerank 文档 get/search/native filter 保持可读。
- READY5 的宿主本地字节上传成功（45 bytes，源/下载SHA256相同），普通上传无确认；后续专用删除批准、历史 ask_answered 和终态 reason=normal 均通过。
- Work 新任务实际知识库 create/list 成功；Writer/Image 调用链正在等待终态。实际 SubAgent 的少量 tool-retrieval state 被发现落在 checkout，已交由宿主模块定位配置传播；当前还不判定运行目录隔离验收通过。

### 13:29 追加结果

- READY6 原始失败会话复验同一原件、目标和名称成功，返回 doc_6f9bc58d7269759181e5bfbb505ef21e；实际公共 list 读回四个本次导入 ID 均存在且 parse_status SUCCESS。Core 重载后嵌套事务错误、management error 计数均为 0。
- 当前 Python/LazyLLM 38 文件 704 项全部通过；CLI coreapi/mcpbridge、frontend scoped typecheck 通过；Core common 全套与 focused vet 通过。Broadened chat vet 的既有 cancel warning单列，不计为全绿。
- 发现 public list 的 processing_status 仍另读持久 pending；已修为共享 DocumentMetadata携带完整observed state，list/get不再重复查旧表。待下一次Core reload验证ready。
- 8091 专用测试账号禁用后：旧JWT MCP HTTP401、内部native HTTP403，未修改共享角色。readonly写拒绝为定向测试覆盖，未为此创建/修改全局角色。
- 新版固定 PPT revision 实际完成需求分析、材料管理、原生 read_document和save_artifacts；Work Writer/Image任务均succeeded。验收模型选择仅临时改8091用户llm偏好并finally恢复原ID，其他role/source/share读回一致。旧revision兼容专用fixture仍在最终收尾。

### 最终发布结果

- 真实 list/get 的 indexed fixture 返回 `processing_status=ready`；失效 Work 固定版本返回 `NOT_FOUND`。资源验收 A 库 running task 列表为空，实际浏览器刷新后旧 99+ 状态消失，更新了同路径截图。
- Writer/Image 均成功；当前 PPT revision 5 的材料步骤成功，旧 KB-only Workflow revision 3 在发布 revision 4 后仍按固定旧版本执行成功。所有临时模型偏好完整恢复。详见 workflow-compatibility-live-verification.md。
- Checkout tool-state 来源已确认是诊断脚本缺少 workspace 环境配置，非线上服务写入；诊断材料私有保存，实际 Work scopes 位于隔离 runtime。
- 管理能力默认启用，三个 Core/宿主入口共用同一开关判断；显式 false 可回退。跨库移动仍须校准预算。8091 私有 make 配置持久化本次测量预算及已有 Artifact V2=true，以保持固定交付版本来源可用；不改 Artifact V2 的代码默认值。
- 未操作 8090；备份恢复未演练。未推送或创建 PR；LazyLLM 本地依赖提交需随主库发布。
- 保留真实性限制：Research 创建/查询成功后末轮模型挂起并正规取消；PPT 实测是本次变更的材料步骤；串行预算不是并发容量；Core 全套原有失败和 broad vet 警告另列。

- Final release checks: targeted Core release tests and Chat/Work default/rollback tests passed; focused vet passed. The local runtime manager full suite passed with bundled Node (65.875 s); its initial Homebrew Node run aborted because of the already diagnosed missing dylib. Actual released service returns 11 public knowledge tools, 9 management methods and 5 native KB methods; 8091/8090 HTTP 200, unchanged 8090 Caddy PID 7676.
