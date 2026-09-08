# Local Workspace 前后端限定执行方案

> 交给下一位 coding agent 的主入口。按任务顺序执行，可使用 executing-plans 工作流；逐项更新复选框与实际证据。本文件是编码计划，不表示实现或端到端验收已经完成。

**Goal:** 在算法完全不变的前提下，由 Core 管理任务工作区、授权状态和请求上下文，复用现有算法工具与询问交互，完成 Local/Desktop Work 工作区功能。

**Architecture:** 前端展示授权和权限设置；目录选择与本机身份传递一次性复用旧版 Local/Desktop 工作区适配，随后冻结；Core 从数据库构造本任务的数据范围及当前规则，通过现有 ChatRequest 和普通子任务参数传递。算法沿用官方原有执行能力。采用用户确认的业务上下文约束，不新建算法拦截器、进程沙箱或默认 MCP 文件服务。

**Tech Stack:** Go 1.25 / GORM / PostgreSQL 与 SQLite / React 18 / TypeScript / Vitest / 现有 OpenAPI 生成链。

## 0. 权威约束与交接方式

1. 用户最新要求：**算法代码不能有任何改动；只修改前后端代码；优先通过后端传入的数据和任务规则约束算法，不强行增加算法代码约束。**
2. 最终功能 PR 默认白名单为 `backend/`、`frontend/` 和本方案目录的四份交接文档；用户最新批准对旧版**工作区相关** `local/`、`desktop/` 补丁做一次性提取，提取和聚焦验收完成后立即冻结，不再基于它们继续修改。始终禁止修改 `algorithm/`、`tests/algorithm/`、`evo/`、`workflows/`、`skills/`、无关 `scripts/`、`.github/`、根目录配置和锁文件；禁止修改 LazyLLM gitlink、依赖版本或无关打包配置。
3. 不通过 monkey patch、动态注入 Python、替换算法工具、修改模块搜索路径、符号链接或修改运行模式间接绕过上述边界。
4. 本目录四份文档是用户明确要求的交接材料和功能分支白名单例外；本轮先单独上传文档，后续 coding agent 必须随每个实现批次同步更新并提交它们，方便其他 agent 随时接手。不带入父目录旧方案、图片或其他无关文档。
5. 本文件取代父目录 `HANDOFF.md`、`IMPLEMENTATION_DESIGN.md`、`IMPLEMENTATION_PLAN.md` 中“必要时最小修改 algorithm/local/desktop”的开放式许可。算法始终冻结；Local/Desktop 只有 0.1 节列出的旧工作区补丁可一次性提取，之后不存在“必要时再改”的权限。旧勾选结果和旧测试数字仍不是完成证据。
6. 不重做全局 Office、知识库、Writer、工作流调度、文件监控或资源授权；不把界面改成手动文件管理器；不自动禁用所有 Skill/子 Agent/Workflow 来伪装满足范围约束。
7. 不默认新增通用 MCP、通用文件服务、命令 Broker 或跨请求租约。任务 0 已确认官方算法在非 trusted 模式不能创建/追加宿主文件；任务 3A 仍按 3.5.8 暂停。一次性恢复旧 Local/Desktop 接入不会改变官方算法的该能力边界，也不授权启用 trusted、修改算法或在冻结点后继续调整本机运行代码。
8. 保留未知用户改动；不得 reset/clean、覆盖其他工作树、处理 `docs/plan/.DS_Store`。不自动关闭旧 PR、强推或发布新 PR。
9. 编码期间持续维护本目录的既有方案与交接文档；任何与本功能有关的小改动也必须同步更新对应的范围、任务、验收或决策记录，不能只改代码。若变化影响产品行为、兼容性、安全边界或验收标准，先取得用户确认再修改方案和实现。这四份文档与对应代码在同一功能分支更新，除此之外不得扩大文档修改范围。
10. 严格控制代码量和抽象数量。每个任务先复用既有 service、handler、类型、错误、路由和前端组件；不为单一调用增加接口层、manager/facade、通用框架或重复 DTO。一个局部小功能原则上最多新增 1 个生产文件、生产代码净增不超过约 200 行；平台分文件、生成代码或安全上必须隔离的实现可例外，但超过前述规模必须先用 `git diff --stat` 说明不可复用的原因并重新 Review。测试代码不计入该行数门槛，但同样禁止复制大段 fixture。每个任务结束记录生产代码净增、复用点和删除/替代的重复实现。

### 0.1 Local/Desktop 一次性提取与冻结边界

以 `feature/newWorkZone` 相对 `TARGET_BASE` 的差异为行为参考，不原样复制。一次性提取阶段必须先优化旧实现：优先复用官方已有 `selectFolder()`/desktop bridge、Local Proxy 的现有 CORS、AdminSession、route proxy、`writeJSON` 和配置结构，以及 Core 已有认证、通用错误和工作区 service；只保留目录选择、候选 token、目录身份校验、Core 授权转发、必要 IPC/本机环境传递及其聚焦测试。删除重复 DTO、重复错误映射、重复 HTTP envelope、重复 path/identity 校验、旧算法回调和未消费的环境变量，不新增 manager/facade。优化与接入在同一个一次性批次完成，不把明显问题留到“冻结后再修”。完成聚焦测试后记录文件 hash 和冻结提交点；后续若发现问题，只报告并等待用户重新授权，不直接修补这些文件。

明确排除旧分支中的无关差异：Caddy 版本变化、`desktop-build.test.mjs` 测试删减、assistant bridge 服务改写及测试删减、删除 `local/scripts/assistant-bridge-win.ps1`，以及任何仅为旧算法 host token/InternalResolve 通道服务且官方算法不再消费的环境注入。最终逐 hunk Review，不能整体 checkout `local/` 或 `desktop/`。

预计允许触及的候选范围如下；正式提取前以实际调用闭环再次缩减：

- Desktop：`desktop/electron/src/main.js`、`preload.js` 及工作区/bridge 聚焦测试。
- Local Proxy：`local/local-proxy/internal/server/` 下工作区选择、身份与必要路由/CORS 文件及聚焦测试。
- Local Runtime Manager：仅 Core/Local Proxy 真正需要的工作区模式或本机 caller 凭据传递及其测试；不向官方算法注入未消费的旧 workspace token。
- `local/lazymind-cli/internal/assistantbridge/`、无关构建脚本和版本配置不在候选范围。

本批规模以“相对旧实现实质减少生产代码”为验收项：Desktop 不再新增第二套目录选择入口；Local Proxy 不复制 Core 的授权业务规则，只负责本机选择和已认证转发；Core 是 workspace DTO、reason 和授权状态的唯一权威。若优化后仍需接近旧版约 650 行 Local/Desktop 工作区生产增量，必须在提取前列出无法复用的具体平台差异并再次 Review。

### 0.2 审计基线

- 当前仓库位置：`/Users/zouyu/Downloads/LazyMind-main`；接手 agent 可在自己的路径 clone，命令不得依赖该绝对路径。
- 已审查功能分支：`feature/newWorkZone`，`e7ed8a4189bb627e96814fc2f34818693cbc2050`，以下称 `FEATURE_SNAPSHOT`。
- 原上游 PR：<https://github.com/LazyAGI/LazyMind/pull/693>。
- 已审查原上游基线：`2823ebe17b3cbbb944dca411176ebee6845b64a2`，以下称 `AUDITED_BASE`。
- 开始编码时必须重新读取 **LazyAGI/LazyMind 的 main**，记录实际 `TARGET_BASE`；它可能比审计基线更新。个人 fork 的 `origin/main` 已混入旧功能，不能作为干净起点。
- 原 PR 132 个文件中，算法/算法测试 23 个、local 16 个、desktop 6 个，都不进入新 PR。LazyLLM gitlink 在原 PR 的功能差异中本来相同；应继续与新目标基线相同，不固定为旧文档写的某个更早版本。

## 1. 可复用清单：区分上游已有与旧 PR 新增

下列路径相对仓库根目录。符号名比行号稳定；查阅上游能力时用 `git show AUDITED_BASE:路径`，不能误把当前已修改的 Python 当作上游能力。

| 位置/符号 | 来源 | 复用方式与限制 |
|---|---|---|
| `backend/core/chat/conversation_logic.go::buildChatRequestBody`、`chat.go::buildLazyChatRequest` | 上游 | 现有 query/user_query、history、files、retrieval 和 agent 字段序列化；保留用户原始消息与显示内容 |
| `backend/core/chat/localfs_paths.go::applyLocalFSPathsForChat` | 上游；当前 PR 扩展过 | 普通任务保持原扫描源逻辑；绑定工作区任务改为单独构造本任务 sources，不能在原 sources 后 append |
| `algorithm/lazymind/chat/engine/tools/local_fs.py::LocalFileToolkit` | 上游，只读 | 消费 source_id/paths/file_extensions；已有 ls/glob/grep/read/string_replace/info。不是新增 create/append 工具 |
| `algorithm/lazymind/chat/service/chat_service.py` 的 `backend.resources` 上下文 | 上游，只读 | query 与 user_query 不同时，扩展 query 会进入模型上下文；用 ContextPrompt 实证，不假设自定义字段会自动渲染 |
| `backend/core/chat/context_usage.go` | 上游 | 上下文预览与导出有独立组装路径，必须接入与真实请求相同的工作区构造函数 |
| `frontend/src/modules/chat/components/AskCard/`、`AssistantMessage/index.tsx` | 上游 | 现有 ask_pending 显示与 ask_answers_structured 提交；无需新增文件权限审批卡或 ToolLimitPending 协议 |
| `conversation_logic.go::buildHistoryMessages/replaceAskUserToolResult` | 上游 | 询问结果写回下一轮模型历史；忽略、部分填写、完整提交三种状态必须区分 |
| `frontend/src/runtime/desktopBridge.ts::selectFolder` | 上游 | Desktop 已有系统目录选择，返回路径或 null；不新增 Electron IPC |
| `backend/core/systemdeps/config.go::IsLocalRuntime`、`frontend/src/runtime/mode.ts` | 上游 | Core 使用既有 `LAZYMIND_RUNTIME_MODE=local`；前端区分 local/desktop。不能依赖旧 PR 新加的 workspace 环境变量 |
| `backend/core/localworkspace/`、`common/orm/local_workspace_models.go`、`chat/local_workspace.go` | 旧 PR | 提取授权、列表、版本、目录身份、绑定；删除仅服务于算法 InternalResolve 的契约和新错误码依赖 |
| `frontend/.../ChatInput/LocalWorkspaceControl.tsx` 与工作区 SCSS | 旧 PR | 保留菜单、搜索、授权/撤销/风险弹窗及视口修复，替换 native bridge/Local Proxy 专用请求 |
| `chat/conversation.go::StopChatGeneration` | 上游 | 提取已有主任务、外部任务、Workflow、普通子任务停止逻辑为可复用服务，撤销后调用 |
| `subagent/store.go::InterruptConversation`、`runner.go::CancelRuns` | 上游 | 中断普通子任务，不新建取消协议 |
| `chat/conversation_logic.go::handleTaskCreated`、`chat/subagent_workspace.go` | 前者上游，后者旧 PR | 普通子任务 create/resume 都在 Core 重建父上下文；不可复用模型传入的工作区权限快照 |
| 上游子任务 runner 的 `parent_agentic_config`、`runtime_instruction` | 上游，只读 | 普通子任务可接收既有 sources 和文本规则；不能假设它拥有 ask_user |
| `backend/core/common/text_file.go` | 上游 | 复用文本扩展名表；增加返回排序副本的访问函数，避免复制第二张表 |
| `common.AppError.WithDetail`、`ReplyAppErr` | 上游 | 已有通用错误码 + detail.reason；前端 workspace 文案由普通 zh-CN/en-US 字典维护 |
| Core OpenAPI 构建器、前端 core-client 生成器 | 上游 | 只生成 Core 合同；不更新 chatbot-client 或根目录 api/ 产物 |

### 1.1 不应照搬的旧实现

- `local_fs_sources` 中的 `workspace_id`、`workspace_version`、`workspace_permission_mode`、`relative_paths` 等新算法约定。新 Core 内部可保留工作区元数据，但不能指望原样算法读取它们。
- `file_extensions: ["*"]`：上游是精确扩展名匹配，星号不会让所有文件可见。
- `environment_context.workspace`：上游环境提示主要读取 locale/time；加字典键不等于加模型上下文。
- 工作区 `shell_tool` 覆盖、ToolCallGuard 审批重放、tool_limit_pending 扩展、Win32/进程隔离、算法子任务事件变更。
- `InternalRegister/InternalPrepareReauthorization/InternalResolve` 及其专用 host token 注入。改用 Core 已认证的公开业务接口；不向算法增加回调要求。
- TaskCenter/ToolLimitCard 的旧算法审批展示改动，以及无关 Workflow、Skill 测试基线修复、Caddy 升级、chatbot-client 差异。
- “清空 history 就撤销了文件权限”“取消信号发送成功就代表所有文件操作已原子停止”等不成立的结论。

## 2. 固定产品与数据语义

### 2.1 范围和权限

- 只给 Local/Desktop 的 Work 任务展示工作区入口；一个任务绑定一个 grant，创建后不换绑。Chat 不通过本功能取得工作区数据。
- 绑定任务的 `local_fs_sources` 只有当前有效工作区；普通任务继续走现有数据源授权。明确上传附件、显式知识库等已有授权保持各自语义，不借此次改造全局删除它们。
- `always_ask`：通过本轮任务规则要求对文件修改、命令、联网和应用副作用先沿用 ask_user 询问。
- `ask_as_needed`：常规授权目录内读写沿用用户任务授权；不确定、高风险或扩大操作范围时沿用已有询问规则。
- `allow_all`：前端风险确认后保存；对用户任务范围内可批准操作减少重复询问。它不代表扩展目录，也不意味着原样算法的新权限枚举已生效。
- 权限模式是 Core 的业务设置，向算法表达为现有上下文文本；不是新增 Python `workspace_permission_mode` 参数处理器。
- 修改权限对**下一次实际算法请求/新启动或重试的普通子任务**生效。进行中的算法调用继续使用已收到的快照；前端提示“已保存，下次执行生效”。不为了热更新启动另一轮模型或自动重复用户操作。
- 已经出现的询问不会因切到 allow_all 自动提交；沿用该询问自己的用户回答。部分填写、关闭或忽略卡片不等于同意。
- 撤销先落库，再请求现有停止流程，后续执行请求在 Core 被拒绝。已执行的动作不回滚，历史内容不删除；不宣称进程级强制隔离或撤销已经交付的内容。

### 2.2 文件能力兼容性检查（必须在开始时执行）

上游 `LocalFileToolkit` 读取已有路径与精确替换已有文本已经存在；主任务 `write_file` 创建/追加宿主文件使用另一路径解析。**上游 Desktop 构建默认 trustedLocalMode=false，宿主路径创建不能仅靠 local_fs_sources 开启。**

本计划不修改运行模式。任务 0 必须对目标实际部署执行读/建/改检查并记录真实结果：

- 目标已有能力能完成时，使用现有工具，继续整个执行计划。
- 如果创建文件失败，先报告工具名、错误和部署状态；不得改算法、设置 trusted 模式、制造符号链接、静默改为手动编辑或把全部完成打勾。任务 0 已在 macOS 官方算法基线确认该失败。
- 用户随后允许先提出后端受控写入方案。只有 3.5 和任务 3A 经用户 Review 后，才允许实现该窄 MCP；在它通过真实验收之前，“创建/追加”仍保持未通过。

这是工具能力的核查，不是要求额外安全隔离。用户接受业务上下文约束，不等于原有工具一定具备所有新文件操作。

### 2.3 本地目录选择

- Desktop：复用 `selectFolder()`，null 立即结束；选中后向 Core 申请候选；用户点击“允许访问”才将候选转换为 grant。
- Local 浏览器：Core 发起系统目录选择，前端通过短轮询读取结果。复用旧 Local Proxy 的候选存储/系统选择器思路，在 `backend/core/localworkspace/` 内实现，不修改 Local Proxy。
- Core 用既有 `LAZYMIND_RUNTIME_MODE=local` 和现有认证/RBAC 做业务门禁；前端 runtime/source 字段只是交互元数据，不能作为权限依据。
- Desktop 返回的路径不是“可证明来自原生选择器”的签名凭据。本方案依赖已登录本地会话与显式授权；Core 仍规范化并检查目录，候选绑定用户、有效期、目录身份，授权时再次核对。不得在文档中夸大这条链的证明能力。

## 3. 统一接口与内部类型

### 3.1 Core 业务接口

以下均以 `/api/core` 为前缀，沿用 `handleAPI` 与 `{code,message,data}`。读取用 `qa.read`，变更用 `qa.write`。保留现有列表/绑定/撤销接口路径以减少前端改动。

| 方法与路径 | 请求 | 成功 data |
|---|---|---|
| GET `/local-workspaces:capabilities` | 无 | `{enabled, native_picker_available}`；由 Core 判定 |
| POST `/local-workspaces:prepare` | Desktop `{path}`；重授权 `{workspace_id}`，二者互斥 | `{canceled:false,selection_token,display_name,path,expires_in_seconds:300}` |
| POST `/local-workspaces:select` | `{}`，Local 原生选择 | `{selection_id,status:"pending"}`；立即返回，HTTP 200 |
| GET `/local-workspace-selections/{selection_id}` | 无 | pending；或 `{status:"selected",candidate:{...}}`；或 canceled/failed；failed 携带稳定 reason |
| DELETE `/local-workspace-selections/{selection_id}` | 无 | `{status:"canceled"}`；撤销未完成选择/候选 |
| POST `/local-workspaces:authorize` | `{selection_token}` | `PublicWorkspace`；候选单次消费 |
| GET `/local-workspaces` | 既有 query/include_inactive/page_size | `{items:[PublicWorkspace]}` |
| POST `/local-workspaces/{workspace_id}:revoke` | `{version}` | `{workspace_id,status:"revoked",version,affected_task_count,stop_requested,stop_failed_count}` |
| GET `/conversations/{conversation_id}:workspace` | 无 | 既有 `BindingView` |
| PUT `/conversations/{conversation_id}:workspace-permission` | `{permission_mode,version}` | `{workspace_id,permission_mode,permission_version,effective_at:"next_request"}` |

候选与 Local 选择状态都保存在一个进程内存储中，用户隔离，5 分钟过期；每用户至多一个未完成系统选择。重启后用户重新选择，不新增持久化表。后台进程使用自己的 5 分钟 context，不能绑在已经返回的 HTTP 请求 context 上；取消和过期调用 cancel。前端 500ms 轮询，卸载/切任务/取消时终止，并丢弃迟到结果。

Local 选择器按构建标签放在 `picker_darwin.go`、`picker_windows.go`、`picker_other.go`：分别复用现有 osascript、PowerShell STA、zenity 调用形式。参数固定，不拼接模型文本；区别用户取消与程序错误。依赖缺失返回 picker_unavailable，不能要求改 installer 或自动安装依赖。

### 3.2 内部快照与共享方法（新建）

文件：`backend/core/localworkspace/context.go`。这是 Core 内部类型，不加入算法 SDK。

```go
type ContextSnapshot struct {
    WorkspaceID       string
    Root              string
    WorkspaceVersion  int64
    PermissionMode    string
    PermissionVersion int64
    Sources           []map[string]any
}

// nil,nil 表示没有任务工作区；存在失效绑定时返回业务错误，不能退回全局数据源。
func ResolveForConversation(ctx context.Context, db *gorm.DB,
    userID, conversationID string) (*ContextSnapshot, error)

// 只用于新 Work 草稿预览；校验已有 grant，不创建任务、不写绑定。
func ResolveForDraft(ctx context.Context, db *gorm.DB,
    userID, workspaceID, permissionMode string) (*ContextSnapshot, error)

// 输入旧 query，输出一次性增强后的 query；不改变 user_query/history/files。
func BuildRequestQuery(original string, snapshot *ContextSnapshot) string

// actor 只取 main/subagent；普通子任务复用相同目录和权限规则。
func ModelNotice(snapshot ContextSnapshot, actor string) string
```

`common/text_file.go` 增加 `func TextFileExtensions() []string`，返回现有 map 键的排序副本。Sources 的唯一元素如下；没有新增算法契约键。

```json
{
  "source_id": "local-workspace:lws_example",
  "paths": ["/Users/example/project"],
  "file_extensions": ["go", "json", "md", "py", "ts", "txt"]
}
```

例中扩展名仅展示形状，实际使用完整 `TextFileExtensions()`。无扩展名文件、`.env` 和二进制/Office 不能谎称被该扩展名过滤器覆盖；按现有其他工具能力处理并实测，不添加 `"*"` 或改 Python。

### 3.3 模型可见工作区说明

后端固定规则与目录数据分开生成，目录名称/路径使用 `encoding/json` 编码，保留原用户文本。ModelNotice 的含义固定如下，可用中文或英文稳定模板：

```text
本任务的用户已在界面选择并授权以下本地工作区。
工作区数据：{"root":"/Users/example/project","permission_mode":"always_ask","permission_version":3}
用户请求中的相对本地文件路径以该目录为基准。调用需要绝对路径的现有工具时，按该根目录解析。
优先使用现有 local_fs 工具列出、搜索、读取和精确修改匹配类型的文件。
创建、覆盖或追加文件只使用当前运行环境实际提供的能力；工具拒绝时说明原因，不把内部产物目录当成已写入用户目录。
工作区之外的目录不在本任务授权范围。保留已有任务产物、明确附件和其他资源各自的用途。
权限规则：对文件修改、命令、联网及应用副作用先使用已有 ask_user 询问并等待用户回答。
一次回答只对应所询问的操作，取消、未提交和权限模式切换都不构成该操作的同意。
```

另外两档替换“权限规则”一行，按 2.1 的语义生成。普通子任务追加：“不能直接询问用户；需要新增确认时返回主任务说明，不执行尚待确认的操作。” 不在 source 对象里伪造 approved=true。

BuildRequestQuery 使用 `ModelNotice + "\n\n" + original`。每个最终请求组装点只调用一次；不使用正则删除用户原文中同名标记来实现去重。`user_query` 和 UI 的 `displayQuery` 保持原值；已渲染上下文不写回用户消息或持久化为下一轮 query。

### 3.4 错误码与翻译：保持目录白名单

不提取旧 PR 对 `common/error_catalog_additional.go` 的 workspace 专用数字码，也不修改根目录 `i18n/errors/core.json` 或手改生成的 error-codes.ts。

在 `backend/core/localworkspace/errors.go` 包装已有 catalog error：

```go
func Error(reason string, status int, catalogMessage string) *common.AppError {
    return common.ResolveAppError(catalogMessage, status).
        WithDetail(map[string]any{"reason": reason})
}
```

| reason | HTTP | 复用消息 key |
|---|---:|---|
| mode_forbidden | 403 | forbidden |
| invalid_selection / path_invalid | 400 | invalid request |
| selection_expired / binding_conflict / revoked / path_unavailable | 409 | conflict |
| workspace_not_found | 404 | resource not found |
| binding_locked | 409 | conflict |
| picker_unavailable / stop_failed | 503 | service unavailable |

编码前用既有 `ResolveAppError` 确认 conflict/service unavailable 的映射；若目标基线只提供通用 HTTP fallback，则直接复用该 fallback 对应的已有通用码，不注册新数字码。前端优先读 `error.response.data.data.detail.reason`，在 `chat.workspace.errors.*` 显示 zh-CN/en-US 本地化；未知 reason 走现有通用错误，不向用户回显系统命令 stderr/主机堆栈。

### 3.5 后端受控文本写入 MCP（历史草案，当前禁止实施）

#### 3.5.1 选择此通道的原因

官方算法已经消费 Core 传入的 `mcp_config`，并用现有 MCP client 将远端工具加入主 Agent。普通 HTTP 写入接口不会自动成为算法工具，因此无法闭环；修改 Python 工具、开启 trusted 模式或改 Local/Desktop 又违反冻结边界。采用 Core 内部、系统管理的窄 MCP 可以复用现有算法能力和仓库已依赖的 `github.com/modelcontextprotocol/go-sdk`，不新增生产依赖。

该 MCP 不是用户在“工具管理”中创建的服务，不写入 `mcp_servers` 表、不受用户 MCP 总开关影响、不出现在通用 MCP 设置页。Core 只在当前请求存在有效工作区绑定时，把它追加到最终 `mcp_config`；普通 Chat、未绑定 Work、Cloud、已撤销或目录失效任务都不获得该工具。Workflow-bound turn 不注入；第一版仅供主 Agent 使用，普通子任务和 Workflow 继续使用 3.2/8 节的读取与规则上下文，不承诺创建/追加宿主文件。

#### 3.5.2 路由、工具与请求合同

新增 Core 内部路由 `POST /mcp/local-workspace/v1`，使用现有 Streamable HTTP MCP SDK 的 stateless/JSON response 模式。该路由不进入公共业务 OpenAPI；最大请求体 1 MiB，只接受 Core 为单次主请求生成的 Bearer capability token。

MCP 暴露两个工具：

```text
local_workspace.write_text
  input:  {relative_path, content, mode:"create"|"append", create_parents?:boolean}
  output: {status:"written", relative_path, mode, bytes}
       或 {status:"confirmation_required", confirmation_id, relative_path, mode, bytes}

local_workspace.commit_write
  input:  {confirmation_id}
  output: {status:"written", relative_path, mode, bytes}
```

固定限制：

- 只接收相对路径；拒绝绝对路径、卷名、空路径、NUL、`.`/`..` 逃逸、超过 4096 字节或超过 64 层的路径。
- 第一版只允许 `common.TextFileExtensions()` 中的 UTF-8 文本；显式拒绝无扩展名、隐藏文件（包括 `.env`）、二进制/Office 和设备/管道/socket。
- `content` 必须是合法 UTF-8，单次最多 1 MiB；响应和日志不回显正文，不记录 capability token、宿主根路径或系统错误原文。
- `create` 只能创建不存在的普通文件，可按参数在根目录内创建父目录；目标已存在返回冲突，绝不覆盖。
- `append` 只能追加已存在的普通文本文件，不隐式创建，不接受符号链接；单文件追加前后的总大小不得超过 8 MiB。
- 第一版不提供 overwrite/delete/move/copy/chmod、glob 写入、命令或任意 URL。已有文件的局部修改继续复用 `local_fs.string_replace`。

#### 3.5.3 每次请求的最小授权令牌

Core 在最终真实 Chat 请求组装时生成 32 字节加密安全随机 opaque token，仅将 `Authorization: Bearer <token>` 放入该系统 MCP 的 headers；token 不进入 query、history、historyExt、数据库、日志或前端。Core 内存只保存 token 的 SHA-256 索引及：userID、conversationID、runID、workspaceID/grant version、actor=`main`、到期时间和是否 preview。

- token 只在本机 Local/Desktop 且有效绑定时生成；TTL 不超过当前上游请求超时，并在请求完成/取消时立即失效。
- MCP 每次调用都重新读取 conversation owner、binding、grant status/version 和目录身份；撤销、换版本、目录替换或 token/run/conversation 不匹配时安全失败。
- ContextPrompt 预览使用 `preview=true` token，只允许工具发现；任何写调用都拒绝。普通子任务、Workflow 和公共 AttemptContext 不获得 token。
- 重启会清空 token 和待确认操作，结果是安全失败并要求重新发起，不新增持久化 secret 表。
- 路由使用独立 verifier，不复用或放宽现有 capability MCP 的用户 Bearer 校验；不把入站用户 Authorization/Cookie 转发给算法。

#### 3.5.4 路径与写入安全

写入前后均调用 `ResolveForConversation` 和目录身份检查。服务在工作区根目录下解析路径，逐段 `Lstat`，拒绝任意现存符号链接；父目录创建也逐级检查。进程内按 workspaceID+relative_path 串行化同一目标，避免本服务自身并发覆盖。

- create 使用 `O_CREATE|O_EXCL`，发生写入/flush/close 错误时删除本次未完成文件；文件权限受系统 umask 约束，默认请求 0644，父目录请求 0755。
- append 在确认阶段记录目标内容 SHA-256 与大小；执行时再次核对，变化则返回 `binding_conflict`/`file_changed`，不盲目追加。使用同目录临时文件写入“旧内容+追加内容”、flush、close 后替换目标，避免部分追加；临时文件名随机且保证清理。
- 这是用户接受的业务层约束，不宣称进程沙箱。外部进程仍可能制造极窄的 TOCTOU 竞争；测试需覆盖可控目录替换/符号链接/并发竞态，并在最终残余风险中披露。

#### 3.5.5 与三档权限及现有 AskCard 的衔接

- `ask_as_needed`：create/append 已被窄化为授权目录内、受限文本、无覆盖/删除的常规操作，可直接执行；所有超出该安全集合的请求直接拒绝，模型可用现有 ask_user 解释或请求用户改用其他方式，但询问不会扩展工具能力。
- `allow_all`：与 ask_as_needed 使用同一硬边界，只减少模型层重复询问；绝不扩大目录、文件类型、大小或操作集合。
- `always_ask`：`write_text` 首次调用只把完整操作暂存在 Core 内存并返回 `confirmation_required`，不触碰磁盘。模型随后按返回的 marker 调用现有 `ask_user`；Core 在转发 `ask_pending` 前识别 marker，并用服务端保存的 relative_path、mode、字节数和内容摘要生成唯一的 boolean 卡片，不能信任模型自行描述操作。

用户完整提交 AskCard 后，Core 只在 ask_id、confirmation_id、owner、conversation、workspace 和服务端生成的问题全部匹配且回答为“是”时，将该操作标记为 approved。部分填写、忽略、关闭、回答“否”或其他 ask_id 均不批准。下一轮模型只能调用 `commit_write(confirmation_id)`；实际路径、内容和 mode 从 Core 暂存项读取，不能在 commit 时替换。批准单次消费，成功/失败结果短期缓存以支持响应丢失后的幂等查询；TTL 到期或撤销会使其失效。

已出现的确认不因权限切换自动批准；用户对该卡片明确回答“是”仍只批准卡片对应的单个操作。该流程复用现有 AskCard/ask_answers_structured，不新增前端审批卡和算法协议字段，但需要 Core 在 ask_pending 持久化/回写处增加严格的 marker 识别与服务端规范化。

#### 3.5.6 新增/修改文件边界

新增：

- `backend/core/localworkspace/write_service.go`：输入校验、目录复核、create/append 与幂等结果。
- `backend/core/localworkspace/write_capability.go`：请求 token 与待确认操作的有界内存存储、清理和撤销失效。
- `backend/core/localworkspace/write_mcp.go`：独立 MCP handler、verifier 和两个工具 schema。
- 对应 `_test.go` 文件；真实文件系统测试只使用 `t.TempDir()`。

修改：

- `backend/core/chat/conversation.go`、`conversation_logic.go`：真实请求注入系统 MCP、请求结束吊销 token、always_ask marker 规范化及结构化回答校验。
- `backend/core/chat/context_usage.go`：只注入 preview token，并验证预览不能写。
- `backend/core/main.go`：构造并挂载内部 MCP，向 chat/localworkspace 注入依赖；不修改启动参数或运行模式。
- `backend/core/localworkspace/handlers.go`：撤销成功后同步失效该 workspace 的 token/待确认操作。

任务 3A 即使未来重新获批，也不得修改 `algorithm/`、`tests/algorithm/` 或已冻结的 `local/`、`desktop/`，不得修改通用 MCP 数据模型/设置 UI、根目录依赖与锁文件，也不得新增数据库迁移或生产依赖。

#### 3.5.7 方案 Review 结论（当前阻断实施）

2026-09-08 对官方目标基线进行逆向 Review 后，3.5 当前版本不能直接进入测试/实现，必须先解决以下问题：

1. **P0：每请求 token 会造成算法 MCP 缓存无界增长。** 官方 `_mcp_server_cache_key` 对包含 headers 的完整 server 配置求 hash，而 `_mcp_tool_cache` 只按 300 秒判断命中、没有删除过期 key。若每个请求注入不同 Bearer token，每轮都会永久增加一个持有 MCP tool/client 的缓存项，同时旧 token 也会留在算法内存中。冻结算法的前提下不能通过修改缓存修复。
2. **P0：run 生命周期绑定时序不成立。** 当前 Core 在 `handleStreamChat` 内才生成 `run_id`，晚于最终 Chat request 和 `mcp_config` 组装；按原方案无法在生成 token 时绑定真实 run。客户端断开后 Core 还可能继续 drain 上游流，因此 token 也不能简单绑定 HTTP request context 或在浏览器连接断开时吊销。
3. **P0：always_ask 的可用性依赖模型准确转发 marker。** 安全上可以失败关闭，但模型若未按格式调用 ask_user，用户永远看不到确认卡。还缺少“一次只允许一个 pending write”、重复 marker、确认时原操作已变化以及服务端规范化卡片在 SSE/持久化/重连三条路径完全一致的合同。
4. **P1：append 的临时文件替换不等价于追加。** 同目录 temp+rename 会更换 inode，并可能丢失原文件 mode、ACL、扩展属性、硬链接语义和外部 watcher 状态；不能在未定义这些影响时称为 append。直接 `O_APPEND` 又需要明确部分写、崩溃恢复和外部进程并发的残余风险。
5. **P1：逐段 Lstat 仍有 TOCTOU。** 检查后到 open/rename 之间，外部进程可替换父目录或符号链接。若安全边界要求抵御本机对抗进程，需要 Unix `openat/O_NOFOLLOW` 与 Windows reparse-point handle 的独立实现和测试；仅靠 portable filepath/Lstat 只能声明为非对抗、尽力约束。
6. **P1：请求体与正文上限冲突。** MCP 最大请求体和 `content` 都写成 1 MiB，JSON envelope、UTF-8 转义和协议字段会使合法 1 MiB content 必然超过路由上限。必须降低 content 上限或提高有明确余量的 request 上限。
7. **P1：预览授权与真实授权没有可证明隔离。** 若 preview 使用独立 header，会加剧缓存泄漏；若复用可写 token，预览与并行真实 run 可能无法区分。ContextPrompt 应只验证工具 schema/说明，不能持有可以执行写入的 invocation capability。
8. **P1：MCP 错误并不使用 Core `{code,message,data}` envelope。** 现有 `localworkspace.Error` 不能直接作为 MCP 稳定合同；需要单独定义 MCP structured result/error reason，并测试不会把 `os.PathError`、根路径或正文带回模型。
9. **P1：真实模型可能继续选择官方 `write_file`。** 两个写工具同时存在，单有工具注册不保证模型会为宿主工作区选择新 MCP。必须更新 ModelNotice，明确“宿主工作区 create/append 使用 local_workspace 工具，内部 artifact 才使用 write_file”，并以真实 ContextPrompt 和模型工具调用验证，不能只测 wire 中出现 MCP。
10. **P2：第一版只支持主 Agent 是明确产品缺口。** 普通子任务/Workflow 仍不能创建或追加宿主文件；UI 和交付说明必须准确，不能把“任务工作区支持创建”无条件描述为所有 actor 都支持。

**推荐修订方向：**

- 将 MCP server 的认证 header 改为“Core 进程级稳定内部 caller token”，使同一 Core 生命周期内 `mcp_config` 完全稳定、算法只产生一个 cache key；另生成短期 `invocation_handle` 绑定 user/conversation/run/workspace，作为工具必填参数进入本轮增强 query。handle 单独不能调用 MCP，外部泄漏者还缺少内部 caller token；不写 history/user_query/数据库，run 终止即失效。Core 重启会产生一个新 cache key，此残余增长需记录并用重启/内存测试评估。
- 把 `run_id` 生成提前到最终请求构造之前，并让 token activation/deactivation 跟随 Core 的真实 run terminal/drain 生命周期，而不是浏览器 request context；同一 conversation 仍使用现有 run decision/请求互斥规则。
- preview 只传稳定 MCP server config，不生成/激活 invocation_handle；工具列表可发现，但 call 必须因缺 handle 失败。确认 ContextPrompt 本身不会执行工具。
- always_ask 每个 conversation/run 最多一个 pending write。MCP 返回 confirmation_id 后，只有带已知 marker 的 ask_pending 才被服务端规范化；SSE、缓存事件和 historyExt 必须持久化同一规范化对象。该机制仍有“模型不发 ask 即无法继续”的可用性限制，必须通过真实模型实测决定是否接受。
- create 保持 `O_EXCL`。append 在进入实现前单独确定文件语义：推荐优先保持原 inode/ACL/xattr，使用平台安全句柄和 append 模式，明确极端崩溃可能留下部分追加；若产品要求全有或全无，则需接受 replace 对元数据/watcher 的影响或扩大设计保存平台元数据。
- 将 `content` 上限降为 512 KiB、append 后总文件上限保留 8 MiB，MCP request body 保留 1 MiB；使用字节数而非 rune 数计量。
- 为 MCP 定义独立 structured reason：`invalid_path`、`unsupported_file`、`content_too_large`、`workspace_revoked`、`file_changed`、`confirmation_required`、`confirmation_rejected`、`capability_expired`、`write_failed`；面向模型的 message 固定且脱敏。

在上述修订得到用户确认前，任务 3A 仍保持“方案 Review 中”，不得编写其生产实现。

#### 3.5.8 第二轮方案 Review（第一轮方向已获批准，仍有新阻断项）

用户已批准 3.5.7 的总体修订方向，但要求继续 Review。2026-09-08 对官方算法的工具执行、日志、渲染和 MCP SDK 合同继续核查后，确认还存在以下问题；这些问题处理前仍不能编写任务 3A 的生产实现：

1. **P0：官方算法会记录所有工具参数，Core 无法在事后补救。** `tool_call_guard.py` 的常规 info 日志会记录最多 240 字符的 `args`；配置 Agent Lab/上下文压缩事件文件后，`telemetry.py` 还会记录最多 400 字符的 `args_preview`。仅 `set_session_env.value` 有专用脱敏，冻结算法下不能把工作区工具加入该名单。因此原协议把 `content` 和 `invocation_handle` 作为 MCP 参数时，会同时违反“正文/凭据不进日志”的合同。Core 即使在 SSE、缓存和 ChatHistory 入库前清洗 `<tool_call>`，也清不到算法进程已写出的日志。
2. **P0：`<tool_call>` 帧会携带完整参数。** 官方 `_tool_call_frame_text` 把完整 arguments 序列化进流式文本；Core 的 `stripToolTags` 只负责展示，不等于入库前删除敏感参数。若继续采用 MCP 写入，Core 必须在所有缓存、持久化、重连和外部聊天投影之前，对工作区工具帧做结构化脱敏，至少删除正文和授权句柄；但这仍不能解决第 1 项算法日志。
3. **P0：单步直写无法提供可证明的 append 幂等。** MCP Go SDK 的工具 handler 不暴露可直接用作业务幂等键的 JSON-RPC request id；模型重试、网络响应丢失或跨轮重试都可能再次调用同一 append。`ask_as_needed`/`allow_all` 也必须采用 prepare/commit，而不是只有 `always_ask` 两阶段：prepare 保存不可变操作并返回 `operation_id`，commit 只接收该 id；相同 id 返回缓存结果，不能重复追加。
4. **P1：进程内幂等不能覆盖 Core 崩溃窗口。** 不新增持久化操作账本时，Core 在磁盘已写但结果尚未登记的瞬间崩溃，重启后无法证明该 operation 是否完成。create 可通过目标存在与内容 hash 辅助判断，append 不能仅凭末尾相同判断（原文件可能本来就有相同后缀）。第一版必须明确“同一 Core 生命周期内幂等；进程崩溃后返回状态未知并禁止自动重放”，或扩大范围增加持久化 ledger/migration；不能宣称跨崩溃 exactly-once。
5. **P1：待操作内存需要资源上限。** 512 KiB 正文若按用户/会话不断 prepare，会造成 Core 内存 DoS。必须限制每个 conversation 最多一个未终结 operation、每个 user 和全局的 pending 数/总字节、短 TTL，并在拒绝、完成、撤销、run terminal 和过期时释放。Go string/byte slice 不承诺可靠物理擦除，只能保证引用释放且不记录日志。
6. **P1：已存在的内部 service token 不能直接替代随机 caller token。** Local 默认内部 token 是固定开发值，可能被知道；若 Core 对外绑定到非 loopback，只有该 token 的 MCP 路由不足以构成安全边界。继续采用 Core 启动时生成的 256-bit 随机 MCP caller token，header 在一个 Core 生命周期内稳定，从而把算法 cache 增长降为“每次 Core 重启最多一个新 key”。每 run 的 invocation 标识只做范围 fencing，单独不能通过 caller 认证。
7. **P1：always_ask 的跨轮状态还需精确定义。** operation 必须绑定服务端 ask_id/confirmation_id；肯定回答后只授权“下一次符合条件的真实 run”提交原不可变内容，run 未提交即终止则批准失效。用户在回答“是”的同时提出修改内容时，不能静默把新文字套到旧批准上：只能提交旧 operation，或废弃它并重新 prepare/询问。
8. **P1：append 的风险分级和文本语义不完整。** `ask_as_needed` 不能把所有 append 都视为低风险。第一版应对可执行文件位、脚本/配置类扩展名、超过阈值的追加强制 AskCard；`allow_all` 仍只在固定安全集合内免问。正文按 UTF-8 字节原样追加、不自动补换行，拒绝 NUL；带 UTF-8 BOM 的既有文件保留 BOM，既有文件须整体通过 UTF-8 校验。父目录创建失败只回滚本次创建且仍为空的目录。
9. **P1：系统 MCP 的工具注解不能当授权控制。** MCP `ToolAnnotations` 只是 client hint。服务端必须再次检查 caller、operation、run、owner、binding/grant version、目录身份和权限状态；commit 的参数不得再包含 path/content/mode。MCP 失败使用 `isError=true` 加脱敏 structured reason，不能依赖模型理解文字错误。
10. **P2：主 Agent 内部产物中转不能消除日志问题。** 官方 `write_file`/`save_chat_artifact` 同样经过统一工具日志，正文仍会出现在参数预览；且 text artifact 会把正文持久化到 artifact。它不能作为规避第 1 项的替代方案。让模型在普通回复中输出特殊标记再由 Core 截获也缺少可靠协议和流式原子性，不纳入方案。

**第二轮后的建议决策：**

- 在“算法必须与官方完全一致”和“文件正文/授权材料不得出现在算法日志”两条约束同时成立时，当前官方工具执行链无法安全实现模型直接创建/追加宿主文件。这不是 Core 单独脱敏可以修复的问题。
- 保持严格边界的推荐默认值是：任务 1–7 继续实现读取、精确替换已有文本、授权/撤销/询问和前端；任务 3A 的 create/append 暂停并明确未完成。
- 若产品必须交付 create/append，只能由用户另外明确选择其一：A. 允许对官方算法做极窄的参数脱敏修复并将其作为需要持续对齐上游的补丁；B. 接受本地算法日志可能包含正文前 240 字符、诊断遥测可能包含前 400 字符的风险，同时 Core 仍执行完整持久化/SSE 脱敏。选项 B 不符合本方案当前安全底线，不作为推荐值。
- 无论选择 A 或 B，后续正式合同都应采用稳定的 Core 进程随机 caller token、per-run fencing、所有权限模式 prepare/commit、受限内存配额，以及“同进程幂等、崩溃后不自动重放”的 append 语义；这些修订替代 3.5.2–3.5.5 中的请求级 token 和单步直接写描述。

在用户对上述冲突作出新决策前，3.5.2–3.5.6 保留为被 Review 的历史草案，不得作为实现合同；3.5.7 与本节共同构成当前有效 Review 结论。

## 4. 执行任务 0：建立干净基线与兼容性证据

**产物：** 新功能工作树、TARGET_BASE、复用验证记录；此阶段不要提取整套旧代码。

- [ ] 读取所在目录及父目录的 AGENTS.md，重点遵守 `backend/core/migrations/AGENTS.md`。记录 git status 与当前未知修改。
- [ ] 读取真正上游 main 并建立隔离分支；使用分支名前缀 `codex/`。以下命令使用已知上游 URL，不假定 origin 是上游：

```bash
git fetch https://github.com/LazyAGI/LazyMind.git main
TARGET_BASE=$(git rev-parse FETCH_HEAD)
git worktree add -b codex/workspace-backend-frontend ../lazymind-workspace-backend-frontend "$TARGET_BASE"
git diff --name-only 2823ebe17b3cbbb944dca411176ebee6845b64a2 "$TARGET_BASE" -- algorithm backend/core/chat frontend/src/runtime
```

- [ ] 将 TARGET_BASE 的值记录到交接记录。若分支/工作树已经存在，检查其状态后复用，不删除或覆盖。后续命令在新工作树执行。
- [ ] 对目标运行环境执行原样算法的读/建/改验证，文件放独立临时目录：`existing.txt` 初始为 `alpha\n`；读取、替换 alpha→beta、创建 `created.txt`、创建 `nested/result.txt`、追加第二行。记录每步工具名、参数、输出、磁盘实际内容和目标已有运行模式。不能用 FEATURE_SNAPSHOT 的 Python 完成此检查。
- [ ] 使用既有 ContextPrompt 导出核对：扩展 query 中的工作区目录和规则进入上下文、原始 user_query 不变、local_fs 工具存在。测试可通过临时 Go HTTP 测试/人工请求驱动，不修改算法测试文件；如需 Python 临时验证，脚本放临时目录且设置 `PYTHONDONTWRITEBYTECODE=1`，缓存/日志不得写入 algorithm/。
- [ ] 记录支持矩阵：Local、Desktop 各自读/替换/创建/追加/询问。尚未运行不写通过；创建失败按 2.2 处理。
- [ ] 跑一次原有请求/询问/停止的聚焦基线，后续按任务扩展测试，不重复跑无关全仓测试消耗时间。

## 5. 执行任务 1：提取 Core 工作区数据与授权管理

**提取文件（来自 FEATURE_SNAPSHOT，先看 diff 再按文件提取）：**

- `backend/core/common/orm/local_workspace_models.go`
- `backend/core/localworkspace/service.go`
- `backend/core/localworkspace/directory_identity.go`
- `backend/core/localworkspace/directory_identity_unix.go`
- `backend/core/localworkspace/directory_identity_windows.go`
- `backend/core/chat/local_workspace.go`
- `backend/core/common/orm/all_models.go` 中仅工作区模型注册
- `backend/core/chat/conversation.go`、`conversation_logic.go` 中仅创建/复用任务绑定的调用点
- 三组 dev migration 的原始 up/down：`20260901061506_create_local_workspaces`、`20260902120000_allow_local_workspace_writes`、`20260903023152_add_workspace_permission_mode`
- 已有 `version_mode/v0_3/20260805000000_workflow_runtime_release.up.sql/.down.sql` 中仅工作区最终表结构的差异

**新建：** `backend/core/localworkspace/errors.go`、`context.go`。

**调整：** `localworkspace/handlers.go`、`service.go`、`backend/core/routes.go`；旧 PR 中 handler 只提取 List/Revoke/ConversationBinding/UpdateConversationPermission 和目录检查，不提取 internal 执行能力接口。

**测试提取并调整：** `chat/local_workspace_mode_contract_test.go`、`chat/local_workspace_schema_test.go`、`chat/local_workspace_permission_mode_contract_test.go`；`localworkspace/directory_identity_test.go`、`handlers_contract_test.go`；Core 根包 workspace HTTP/list/schema 测试。不要带入调用 InternalResolve 的旧断言。

- [ ] 先写/调整测试：Work 可绑定，Chat/Cloud 禁止；一任务仅一 binding；跨用户拒绝；撤销后同路径重授权生成新 ID；旧任务不能恢复；乐观版本冲突。
- [ ] 将 `Enabled()` 改为复用 `systemdeps.IsLocalRuntime()`，不再依赖 `LAZYMIND_LOCAL_WORKSPACE_RUNTIME`。Register 的 source 只作 local/desktop 展示元数据，不再要求 source 等于运行时字符串；授权门禁由后端统一判断。
- [ ] ResolveForConversation 检查用户、Work 类型、binding、有效 grant 和目录身份；无绑定返回 nil，失效绑定返回错误；ResolveForDraft 只读现有 grant。
- [ ] 用已有通用错误 + reason 替换该功能的旧 workspace 新数字码。测试断言 HTTP、已有通用码和 reason，不能放松为“任意错误”。
- [ ] 三组已共享迁移保持原文件名/SQL/时间戳。不要因为重做 PR 把三步合成新 dev 文件；需要修正时新增 UTC 秒级版本，且更新已有 v0_3 aggregate 的最终形态。禁止新建第二份 aggregate。
- [ ] 跑 `go test ./localworkspace ./chat ./migrate`；数据库迁移还需按现有 runner 验证 SQLite/PostgreSQL 的新建、已有模式升级、数据保留、回退。不要沿用旧文档的 PostgreSQL 已通过结论。

**任务完成条件：** 两表数据模型及业务 API 不依赖任何被冻结模块；新分支尚无算法变化。

## 6. 执行任务 2：一次性精简接入 Local/Desktop 目录选择并冻结

本任务是唯一允许修改 Local/Desktop 的批次。以旧实现行为为参考、以官方当前文件为落点，逐 hunk 重写最小补丁；不得整体 checkout 旧目录。Core 始终是 grant、binding、permission 和 reason 的唯一业务权威，本机层只证明“路径来自当前用户实际选择”并转发授权。

**Desktop 候选文件：** `desktop/electron/src/main.js`、`preload.js` 及工作区/bridge 聚焦测试。复用现有 `activeWindow()`、`dialog.showOpenDialog`、`readStatus()`、AdminSession 获取和现有 Core 地址；保留旧版一次性 256-bit selection token、webContents 绑定、5 分钟 TTL 和授权前目录身份复核。不能让 renderer 直接提交任意 path 代替选择证明；因此现有通用 `selectFolder()` 保持兼容，工作区可以有窄的 select/authorize IPC，但不得复制通用窗口、状态或 HTTP helper。

**Local 候选文件：** `local/local-proxy/internal/server/workspace*.go`、`server.go`、必要的 `cors.go` 及聚焦测试；Runtime Manager 只保留 Core/Proxy 确实需要的模式和 caller 凭据传递。复用既有 `AdminSessionManager`、CORS allowlist、`writeJSON`、route 配置和 HTTP transport；workspace handler 只保存短期候选并调用 Core 注册/重授权，不复制 Core 的 grant/permission 规则。官方算法不接收 workspace host token。

- [ ] 提取前先列出准确文件和预计生产净增；Desktop 或 Local 任一侧超过约 200 行时，说明 selection proof、平台 picker、身份复核中哪些代码无法安全复用，再开始该侧实现。
- [ ] 测试使用 fake picker/clock，不弹真实窗口；覆盖取消、跨用户/webContents、TTL、重放、目录删除/替换、未知 token、LAN/非 loopback 拒绝和 Core 失败脱敏。
- [ ] authorize 只消费 selection token，不接受 renderer/browser 提交 canonical path、directory identity、source 或 owner。Core 注册前再次校验当前目录身份；取消不创建 grant。
- [ ] Local picker 不读取目录内容、不创建 watcher/source/知识库；若同步系统 picker 会占用请求，必须沿用旧实现已验证的超时/取消行为，不另造后台任务框架。
- [ ] 排除 Caddy 版本、assistant bridge、Windows 脚本删除、构建测试删减和算法 token 注入；用 `git diff --name-status` 逐项证明。
- [ ] 运行 Desktop bridge/工作区测试、Local Proxy server/workspace 测试和 Runtime Manager 相关环境测试。测试通过后记录允许文件的 SHA-256、当前 commit 和实际生产净增，标记 Local/Desktop 冻结；此后的任务不得修改这些文件。

**完成条件：** Local/Desktop 用户可取得与旧版相同的目录选择、取消、短期候选、授权和重授权体验；实现明显复用官方基础设施且不带回无关差异。冻结后发现缺陷时停止并请求用户重新授权，不能直接修复。

## 7. 执行任务 3：统一主任务、预览、重试的请求上下文

**修改：**

- `backend/core/localworkspace/context.go`、`backend/core/common/text_file.go`
- `backend/core/chat/localfs_paths.go`
- `backend/core/chat/conversation.go`、`context_usage.go`
- `backend/core/chat/conversation_logic.go` 中已有历史附加/序列化适配处

**新增测试：**

- `backend/core/localworkspace/context_test.go`
- `backend/core/chat/local_workspace_request_context_test.go`
- `backend/core/chat/local_workspace_context_preview_test.go`

**保留并回归：** `chat/localfs_paths_test.go`、`tools_test.go`、`conversation_logic_test.go`、`context_usage_test.go`。

- [ ] 扩展名复用函数先测试“排序、返回副本、不包含星号”，不递归扫描用户文件夹来生成枚举。
- [ ] 在 `applyLocalFSPathsForChat` **调用 scan 前**判断已绑定 Work：有效绑定设置 snapshot.Sources 后返回；不存在绑定才调用旧 `loadLocalFSSourcesForChat`。撤销/失效绑定返回错误，不降级加载全局 sources。
- [ ] 增加共享快照加载/查询增强接入：真实 Chat 在 runtime/feature/workflow/附件转换均完成后、交给 handleNonStreamChat/handleStreamChat 之前读取并应用最终 snapshot；preview 在交给 buildLazyChatRequest 前使用相同函数。二次读取使请求组装期间发生的撤销也能被识别；不创建跨请求锁/租约。
- [ ] 新 Work 草稿预览使用 ResolveForDraft(raw.workspace_id, raw.workspace_permission_mode)，检查 `run_in_background`；不落库，不创建任务。已有任务预览使用绑定和已保存的模式，忽略前端试图重写路径/权限状态的值。
- [ ] 始终保留原 query 中已有 mention/context，再前置工作区说明；保留 user_query、displayQuery、history、files。约束只适用于本任务本地工作区；不更改 kb_id、附件授权和原工作流资源绑定。
- [ ] 不向 environment_context 塞没有消费者的规则；不在 buildLazyChatRequest 增加 Python 未识别的字段。最终 wire payload 只出现既有字段。
- [ ] 真实 Chat 与 preview 的现有 mentioned conversation 附加代码把 history 断言为 `[]map[string]string`，而 buildChatRequestBody 生成的是 `[]map[string]any`。只在这两个被触及的组装点统一为 `[]map[string]any` 并新增“引用会话不丢历史”回归，不能通过重建空 history 来添加规则。
- [ ] 在现有 historyExt 中记录 `workspace_context`（workspace_id、grant version、permission mode/version；不必存 root）。每轮只记录执行快照；不读取旧 ext 的权限作为下一轮权威来源。
- [ ] `ResumeChat` 仅续接 SSE，不启动新的算法调用；不要因用户重连重复注入/执行。真正的 regenerate/retry/new turn 重新查询快照。

下列 Go 用例可以直接放入新测试文件，约束纯函数和“不污染原始请求”的接口。其余 HTTP 测试复用 `newToolsTestDB`、`startChatToolsTestServer`、`seedRuntimeModelConfig` 捕获实际 wire JSON。

```go
package localworkspace

import (
    "strings"
    "testing"
)

func TestBuildRequestQueryKeepsOriginalAndAddsWorkspace(t *testing.T) {
    original := "请把 alpha 改为 beta。\n保留原格式。"
    snapshot := &ContextSnapshot{
        WorkspaceID: "lws_one", Root: "/tmp/project one",
        WorkspaceVersion: 2, PermissionMode: "always_ask",
        PermissionVersion: 3,
    }
    got := BuildRequestQuery(original, snapshot)
    for _, want := range []string{original, "/tmp/project one", "always_ask", "ask_user"} {
        if !strings.Contains(got, want) {
            t.Fatalf("query missing %q: %s", want, got)
        }
    }
    if got := BuildRequestQuery(original, nil); got != original {
        t.Fatalf("unbound request changed: %q", got)
    }
}
```

**HTTP 测试的固定断言：**

1. fixture 同时存在两个用户、两个工作区、一个 scan source；绑定任务 wire 的 `retrieval.local_fs_sources` 恰好 1 项，根目录与绑定一致，没有 scan source 路径。
2. `message.user_query` 等于原请求；`message.query` 有且仅有一份本轮 ModelNotice；history 原消息数量/内容未丢失。
3. sources 只有 source_id/paths/file_extensions；不发送 workspace_id、relative_paths、permission_version 等旧新增算法契约字段。
4. 预览不产生 conversation/binding 行；同一已绑定任务的预览和真实发送拥有相同工作区信息。
5. 切换模式后新请求为新版本；旧 historyExt 仍保存原快照；撤销后 fake algorithm 接收请求次数不增加。
6. 普通 Chat/未绑定 Work 的 wire 与原有行为相同，不吞掉 scan-control-plane 错误。

**验证：** `go test ./chat ./localworkspace ./common -run 'Test.*(Workspace|LocalFS|BuildChatRequestBody|BuildLazyChatRequest|TextFile)' -count=1`；再用真实 ContextPrompt 导出核对内容可见，不只检查 Go 中间 map。

## 7A. 暂停任务 3A：Core 受控创建/追加闭环

**禁止接手 agent 实施。** 3.5.2–3.5.6 是被 Review 否决的历史草案，旧 token、write/commit 和测试清单均不是当前合同。3.5.8 已确认官方算法的工具参数日志与“正文不进日志”安全底线冲突；用户尚未授权修改算法或接受日志风险。

接手 agent 只执行任务 1–7 中不依赖 create/append 的部分。不得创建 `write_service.go`、`write_capability.go`、`write_mcp.go`，不得添加系统 MCP，也不得为了补齐验收开启 trusted。真实场景中的 create/append 只记录官方能力缺口，不计为本阶段通过。只有用户后续明确作出新决策并重新批准完整合同，才可重开本任务。

## 8. 执行任务 4：普通子任务与 Workflow 私有执行上下文

**新增/调整：**

- `backend/core/localworkspace/subagent_context.go`
- `backend/core/chat/subagent_workspace.go`、`conversation_logic.go::handleTaskCreated`
- `backend/core/subagent/handlers.go::InternalGetExecutionSpec`
- `backend/core/chat/subagent_workspace_test.go`
- `backend/core/subagent/local_workspace_execution_spec_test.go`

共享接口放在 localworkspace，避免 chat↔subagent/Workflow 的 import cycle：

```go
func RebuildSubagentParams(ctx context.Context, db *gorm.DB,
    userID, conversationID string, original map[string]any) (map[string]any, error)
```

返回新的 map，保留原有附件、Skill、workflow runtime 等参数，只重建属于工作区的权威部分。对于无工作区的任务保持旧行为；不把无关 parent 配置整包删除。对于已绑定任务：

- parent_agentic_config 的 user_id/conversation_id 取数据库确定的所有者和会话；local_fs_sources 使用当前 snapshot 的唯一 source。
- runtime_instruction 保留原任务已有内容，追加 ModelNotice(snapshot,"subagent")；参数来自模型时不能把其声称的根目录或 approved 标记视为工作区授权。
- 不把 Core 内部权限结构加进算法 schema，不自动扩展 ev.Tools/LegacyTools；existing tool 配置未提供某项能力时返回正常任务能力不足结果。

- [ ] create 路径先调用 RebuildSubagentParams，再序列化到 `SubAgentTask.Params`，随后启动。
- [ ] **resume 路径必须更新数据库 Params 后再启动**。上游普通子任务 runner 从数据库 task.params 读取，单改 HTTP RunRequest.Params 不足以生效。校验 existing task 属于当前 conversation/user；不能凭模型给出的 task_id 恢复另一用户或另一会话任务。
- [ ] 为避免 ModelNotice 在 resume 上累加，将 Core 保存的原始 runtime_instruction 和快照版本放在 `parent_agentic_config._core_workspace_context` 中，从该原文重新构造本轮说明。上游 runner 已将 parent_agentic_config 排除在 Task Parameters 的直接提示词展示之外；不要另加会被展示的顶层 Params 元数据。create 时忽略模型提交的同名元数据，由 Core 初始化；resume 只信任从所属任务数据库行加载的 Core 元数据，不信任本轮事件覆盖值。对没有 Core 元数据的旧任务，将其原有 runtime_instruction 视为初始原文。该键仅由 Go 维护，不要求 Python 识别；不要用正则删除任意用户标记文本。
- [ ] Workflow 已有私有 `/internal/subagent/tasks/{task_id}/execution-spec`。在其原有 executor/lease 认证成功后，从 task.CreateUserID/task.ConversationID 重建 private params，返回新的 task DTO 与同样的顶层 params。不要修改永久任务记录的 compiled workflow 合同。
- [ ] 私有返回里的 `params` 与 `task.params` 保持同一个快照；上游 remote_executor 使用 spec.params 更新 task.params 后启动，普通 runner 又从 task.params 读取。用响应 JSON 断言两处一致。
- [ ] 保持 `workspace_path` 为系统内部子任务产物目录。禁止把宿主 root 加到公共 `executor.AttemptContext`、公开 outbox 合同、workflow.yaml、公开远程文件路径或 artifact 输出合同中。
- [ ] 保留 Workflow 的既有输入、工具、产物所有权；工作区说明不要求固定 Workflow 把产物自动写入用户目录。只有已有工具/输入合同能完成的本地操作才执行；主任务需要落盘时仍遵循任务 0 的能力结果。
- [ ] ordinary subagent 和 Workflow 都不能直接 ask_user；规则要求其遇到新的确认需求先返回主任务。用户确认后的后续主请求可再发起对应任务，不新增跨 Agent 询问通道。

**测试必须覆盖：** create 与 resume 的 DB Params；跨用户/跨任务恢复；已撤销 grant；permission version 更新；未绑定任务附件/Skill 参数保留；private execution spec 的两份 params 一致；公共 AttemptContext 不含宿主 root；内部产物 WorkspacePath 未改。

**验证：** `go test ./chat ./subagent ./localworkspace ./workflow/executor -run 'Test.*(Workspace|ExecutionSpec|Subagent|SubAgent|InterruptConversation|Context)' -count=1`，加一次真实子任务读取 fixture，确认消费的是正确路径而非只看 Go 响应。

## 9. 执行任务 5：复用询问、权限更新与撤销停止

**修改：** `backend/core/chat/conversation.go`、`conversation_logic.go`、`backend/core/localworkspace/handlers.go`、`backend/core/main.go`。

**新增：** `backend/core/chat/conversation_stop.go`、`local_workspace_lifecycle_test.go`、`local_workspace_ask_test.go`。

### 9.1 询问

- [ ] 不提取旧 ToolLimitPending/approval_kind 等算法审批改动。保留原 ask_pending → AskCard → ask_answers_structured → buildHistoryMessages 的链路。
- [ ] bound workspace 的完整卡片回答校验 ask_id 对应当前会话最后一个未回答的询问，沿用其题目/选项；不能把另一卡片 ID 当同意。测试普通非工作区问答不受影响。
- [ ] 工作区规则声明：未提交、部分填写和切换模式都不是批准；已经回答的具体操作可以执行，不要在 always_ask 下一轮重复询问同一已明确同意的动作。
- [ ] 若现有通用历史回写的“Do NOT re-ask”与工作区权限待确认冲突，只在 bound workspace 且存在未提交 pending 时补充本轮规则：“未获得该操作同意，跳过该副作用；可继续用户另外授权的操作”。不全局更改其他 AskCard 的忽略语义。
- [ ] historyExt 的工作区快照与 ask_pending 一起保留；模式切换不调用 submit/decision，不伪造用户回答。

### 9.2 停止与撤销

从 StopChatGeneration 提取现有 service，handler 仍负责解析 HTTP 和回复：

```go
func StopConversationExecution(ctx context.Context, db *gorm.DB,
    stateStore state.Store, userID, conversationID, historyID, reason string) error
```

新函数沿用当前实现的 ownership、run decision/cancel signal、external requestStop、StopActiveWorkflowSession、InterruptConversation/CancelRuns、NotifyChatCancel。先做现有停止回归，不顺手重写这些协议。

localworkspace 通过启动时注入的回调调用它，避免包循环：

```go
type StopConversationFunc func(context.Context, string, string) error
```

在 main 组装该回调；参数为 userID/conversationID，内部传空 historyID 与工作区撤销原因。测试注入可观测替身，不伪造 HTTP Request 调用另一个 handler。

- [ ] Revoke 的数据库事务先校验用户和版本、设置 revoked、增加 grant version、取得其绑定会话 ID；提交之后请求停止这些会话，不在数据库事务中等待网络。
- [ ] 停止调用使用独立有界 context，不因用户关页面丢失；不超过现有超时边界。`stop_requested` 表示请求已发起，不能解释成所有操作已完成停止。
- [ ] 停止有失败时授权仍保持 revoked，返回 stop_failed_count 并向前端显示“授权已撤销，部分任务停止请求失败”；不回滚授权，不回报“全部停止”。已有停止接口允许用户重试，不新增后台无限重试系统。
- [ ] 下一轮主请求、普通子任务 create/resume、Workflow private execution spec 都重新读 DB 并拒绝 revoked，覆盖已排队但尚未取到上下文的执行。
- [ ] UpdateConversationPermission 继续使用乐观版本，返回 effective_at=next_request。当前执行不中途替换上下文、不自动重放；对 revoked/path_unavailable 的绑定拒绝变更并刷新前端状态。

**测试用例：** grant 事务回滚时不调用 stop；撤销成功只停止其绑定会话；stop 某一项报错不恢复授权；已结束任务重复 stop 不复活；切模式不提交询问/不启动新请求；取消信号产生竞争时原 run decision 规则保持原样。

**验证：** `go test ./chat ./localworkspace ./subagent -run 'Test.*(Workspace|Ask|Stop|Cancel|InterruptConversation)' -count=1`。真实任务验证“撤销后不再派发新轮次/子任务”，不把模型收到输入后的一切操作描述成可立即硬撤销。

## 10. 执行任务 6：提取前端控件，复用现有桥和询问 UI

**提取旧 PR 的工作区相关差异：**

- `frontend/src/modules/chat/components/ChatInput/LocalWorkspaceControl.tsx`
- `frontend/src/modules/chat/components/ChatInput/index.tsx`、`index.scss`、`types.ts`
- `frontend/src/modules/chat/components/newChatContainer/hooks/useChatConversation.ts`
- `frontend/src/modules/chat/components/newChatContainer/index.tsx`、`types.ts`
- `frontend/src/modules/chat/pages/chatLayout/index.tsx`
- `frontend/src/i18n/locales/zh-CN.ts`、`en-US.ts` 中仅 workspace 文案

**新增：**

- `frontend/src/modules/chat/utils/localWorkspace.ts`：Core 请求、候选选择、业务 reason 映射。
- `frontend/src/modules/chat/utils/localWorkspace.test.ts`

**测试：** 调整 `ChatInput/LocalWorkspace.contract.test.tsx`；补充 `chatLayout/index.test.tsx` 的上下文预览与实际发送合同；复用 `AskCard/index.test.tsx` 与 `runtime/desktopBridge.test.ts`。

服务接口固定为：

```ts
export type WorkspacePermissionMode = "always_ask" | "ask_as_needed" | "allow_all";
export interface WorkspaceSelectionCandidate {
  canceled: boolean;
  selection_token?: string;
  display_name?: string;
  path?: string;
  expires_in_seconds?: number;
}

export function selectWorkspaceCandidate(
  runtime: "local" | "desktop", signal?: AbortSignal,
): Promise<WorkspaceSelectionCandidate>;

export function prepareWorkspaceReauthorization(
  workspaceId: string,
): Promise<WorkspaceSelectionCandidate>;

export function authorizeWorkspace(selectionToken: string): Promise<LocalWorkspaceView>;
```

`WorkspacePermissionMode` 沿用已有 ChatInput/types.ts 的单一定义；`LocalWorkspaceView` 可从控件提取到工具模块再 re-export，不能创建两套不一致的接口。生成客户端就绪后可用其类型约束 API 层；日期字段按现有 Core JSON 字符串处理。

- [ ] Desktop 选择直接 import `selectFolder`；取消返回 `{canceled:true}`，不请求 prepare、不授权、不改变选择。有路径才 POST prepare，Core 成功后再显示授权弹窗。
- [ ] Local 先 POST select，再 500ms GET；pending 不显示失败，selected 取 candidate，canceled 结束，failed 映射 reason。AbortSignal、组件卸载、切任务、选择“不使用工作区”都会取消本次流程；选择器即使迟到也不能覆盖新状态。
- [ ] `reauthorize` 对接 POST prepare `{workspace_id}`；没有用户点击“允许访问”不调用 authorize。返回新 grant 后更新最近列表；不恢复旧绑定。
- [ ] 删除控件内 `DesktopWorkspaceBridge`、selectLocalWorkspace/authorizeLocalWorkspace/reauthorizeLocalWorkspace 和所有 `/_local/workspaces:*` 请求。不要改全局 desktopBridge.ts 的 API 实现。
- [ ] 使用 `axiosInstance` 继承 Local session 初始化和认证；操作内使用 `silentError: true`，通过函数级的 `detail.reason` 文案避免重复 toast。不修改全局 request.ts 来兼容本功能。
- [ ] UI 同时满足前端 Local/Desktop Work 条件和 Core capabilities.enabled；未绑定已存在任务仍按现有设计隐藏无意义占位，新任务保留选择入口。
- [ ] 保留菜单互斥、搜索、视口内定位、Esc/遮罩/关闭取消、不使用工作区、撤销受影响任务数量、allow_all 四类风险说明。
- [ ] 权限修改成功展示“已保存，下次执行生效”；mode 和 permission_version 使用后端返回值，不做失败后仍保留的乐观更新。stop_failed_count>0 显示独立状态，不把撤销成功显示为失败或全部停止。
- [ ] 将 workspace_id、草稿 permission mode 加入真实提交和 ContextUsageButton 的 buildRequest/staleKey 输入；已有任务不发送根目录。工作区变化使预览变 stale，不能显示旧预览为最新。
- [ ] 继续复用 AskCard 与 AssistantMessage 的既有 onSubmit 链，不新增 approve API，不调用旧 toolLimitDecision 来批准文件操作。

直接可用的服务层取消测试：

```ts
import { beforeEach, expect, it, vi } from "vitest";
import { axiosInstance } from "@/components/request";
import { selectFolder } from "@/runtime/desktopBridge";
import { selectWorkspaceCandidate } from "./localWorkspace";

vi.mock("@/components/request", () => ({
  axiosInstance: { post: vi.fn(), get: vi.fn(), delete: vi.fn() },
}));
vi.mock("@/runtime/desktopBridge", () => ({ selectFolder: vi.fn() }));

beforeEach(() => vi.clearAllMocks());

it("Desktop 原生选择取消时不向 Core 申请候选", async () => {
  vi.mocked(selectFolder).mockResolvedValue(null);
  await expect(selectWorkspaceCandidate("desktop")).resolves.toEqual({ canceled: true });
  expect(axiosInstance.post).not.toHaveBeenCalled();
});
```

**其余必要交互断言：**

1. selectFolder 返回路径后只调用 prepare；确认前 authorize 调用数为 0。
2. 任意取消路径不改原 selected workspace/permission mode；过期候选显示重新选择提示。
3. Local 选择轮询取消后没有继续 setState；迟到 response 不覆盖新任务或新候选。
4. 当前任务撤销与列表其他 grant 撤销各自更新正确条目。
5. allow_all 关闭/取消保持原模式；确认一次才 PUT；冲突重新拉绑定状态。
6. 后台执行期间可以保存权限，但提示下一次执行生效；不自动提交 AskCard、不自动发送新聊天请求。
7. AskCard 完整同意、拒绝、部分填写和忽略，发送的结构化数据维持上游格式。
8. 中英文关键文案齐全；键盘焦点返回触发入口；菜单不超视口。

**验证命令，在 frontend 执行：**

```bash
node_modules/.bin/vitest run src/modules/chat/utils/localWorkspace.test.ts src/modules/chat/components/ChatInput/LocalWorkspace.contract.test.tsx src/modules/chat/components/AskCard/index.test.tsx src/runtime/desktopBridge.test.ts src/modules/chat/pages/chatLayout/index.test.tsx
node_modules/.bin/eslint src/modules/chat/utils/localWorkspace.ts src/modules/chat/components/ChatInput/LocalWorkspaceControl.tsx src/modules/chat/components/ChatInput/index.tsx src/modules/chat/pages/chatLayout/index.tsx src/modules/chat/components/newChatContainer/hooks/useChatConversation.ts
```

如果 node_modules 不存在，按仓库已有包管理器/锁文件安装，不升级依赖或重写锁文件。旧文档的共享 node_modules 情况只供参考，不能当作当前事实。

## 11. 执行任务 7：Core OpenAPI、生成客户端与文件范围检查

**修改：** `backend/core/openapi_manual.go`、必要的 `openapi_registry.go` 路由类型、`main.go`；`frontend/scripts/openapi/specs/core.yaml`、`frontend/src/api/generated/core-client/api.ts`、必要的 `frontend/scripts/openapi/.openapi-cache.json`。

**测试：** `backend/core/local_workspace_openapi_test.go`、`local_workspace_selection_http_test.go`、新增 `backend/core/openapi_export_target_test.go`。

- [ ] 列表、选择流程、授权、绑定、权限 effective_at、撤销停止结果和错误 detail 的 OpenAPI 与真实 handler 完全一致。不要把 candidate prepare 的 path 复制到任务绑定请求；绑定只允许 workspace_id/permission mode。
- [ ] 删除未采用的 internal resolve 工作区路由和 schema。保留上游原本存在的其他 private 路由。
- [ ] 当前 `--export-openapi` 会写入根目录 api/ 和绝对路径 `/openapi-export/`，不能直接使用后再回滚非白名单文件。增加显式单文件导出入口 `--export-openapi-to <path>`，仅写指定目标，原有导出行为不变。

新增 helper 的完整形状：

```go
func exportRegisteredOpenAPITo(outputPath string) error {
    r := mux.NewRouter()
    r.UseEncodedPath()
    registerCoreRoutes(r)
    raw, err := buildOpenAPISpecFromRouter(r)
    if err != nil { return err }
    var spec map[string]any
    if err := json.Unmarshal(raw, &spec); err != nil { return err }
    body, err := yaml.Marshal(spec)
    if err != nil { return err }
    return os.WriteFile(outputPath, body, 0o644)
}
```

在 main 的启动配置/服务启动前识别这个 flag，要求恰好一个非空输出参数；参数错误返回非零。测试写到 t.TempDir()，解析 YAML 并核对新业务路径；不启动数据库或后台服务。相应 imports 已在 main 现有文件中使用，编码时按 gofmt 整理。

- [ ] 只导出到 frontend Core spec，然后只生成 core-client：

```bash
# 在 backend/core
go run . --export-openapi-to ../../frontend/scripts/openapi/specs/core.yaml

# 在 frontend
node scripts/openapi/generate-api.mjs core
node scripts/openapi/check-stale.mjs
node scripts/i18n/sync-error-codes.mjs --check
```

- [ ] 不生成 chatbot-client，不运行全服务批量生成，不手改 error-codes.ts。若生成器触及其他服务，先定位原因并限制调用范围，不能把无关变化一并提交。
- [ ] 运行迁移不可变检查和既有升级脚本：

```bash
# 在仓库根目录，TARGET_BASE 为任务 0 记录的真实提交 SHA
python3 scripts/check_migration_immutability.py --base "$TARGET_BASE"
bash scripts/test_migration_upgrade.sh
```

按该脚本现有参数/环境配置临时 PostgreSQL，禁止指向用户生产数据库。环境依赖未满足时记录未运行，不替换测试、不改脚本、不降低检查。

## 12. 验证顺序与必须交付的证据

### 12.1 自动化矩阵

| 编号 | 验证 | 必须观察到的结果 |
|---|---|---|
| A1 | 任务 1 的数据/迁移测试 | 用户隔离、一任务一绑定、撤销重授权不复活、版本冲突 |
| A2 | 目录选择服务与 HTTP 测试 | 取消/迟到/重放/TTL/身份变化正确，未授权不创建 grant |
| A3 | 捕获真实 Chat wire payload | 单工作区 source、真实扩展名、用户原消息保留、无未识别算法字段 |
| A4 | ContextUsage/ContextPrompt 请求测试 | 草稿不落库，真实提交与预览一致，原历史不丢失 |
| A5 | AskCard + 后端历史回写 | 完整回答续跑、拒绝/忽略不被解释为批准、切模式不自动回答 |
| A6 | 子任务 create/resume | DB Params 权威、owner 匹配、恢复不使用旧权限 |
| A7 | Workflow execution-spec | 顶层与 task.params 一致、宿主路径仅私有、compiled/public 合同不变 |
| A8 | 撤销/停止 | 停止使用已有链路，失败有报告，撤销后不派发新执行 |
| A9 | 前端交互 | menus/取消/风险确认/状态刷新/视口/中英文通过 |
| A10 | OpenAPI 与错误字典 | Core 合同一致，未修改根目录 i18n 和 chatbot-client |
| A11 | create/append 能力复核 | 明确记录官方非 trusted 算法不支持；任务 3A 暂停且本阶段没有 MCP/trusted/算法改动 |

任务聚焦检查通过后，执行一次与变更范围匹配的总体检查：

```bash
# backend/core
go test ./...

# frontend
node_modules/.bin/vitest run
node_modules/.bin/tsc --noEmit
node_modules/.bin/vite build
node scripts/openapi/check-stale.mjs
node scripts/i18n/sync-error-codes.mjs --check
```

完整 typecheck 如有失败，先对 TARGET_BASE 复现再分类，不能直接引用旧文档“都是历史问题”。本 PR 引入的错误必须修复；无关基线失败记录命令与文件，不扩大范围修复或关闭检查。

### 12.2 原样算法上的真实场景

人工/真实集成用临时目录，不对用户工作文件运行测试。Local 与打包 Desktop 分开记录：

1. 新 Work 选择目录→取消→无 grant；再次选择→允许→绑定；确认现有 Desktop bridge 足够，Local picker 实际能打开。
2. `existing.txt` 写入 alpha；让主任务读取并精确替换为 beta；检查磁盘实际内容。
3. 尝试新建 `created.txt`、`nested/result.txt` 和追加第二行，仅复核并记录官方算法当前能力缺口；本阶段不得为使其通过修改算法、启用 trusted 或实现任务 3A。
4. always_ask 请求修改：出现既有 AskCard；确认前检查文件没改；同意后完成一次；拒绝后未执行该动作。不要只用“回复里说询问过”作为证据。
5. allow_all 开启前显示风险框；取消不变，确认后下次执行采用新规则；已有 pending 不被自动提交。
6. 新任务 B 绑定另一目录；导出两者上下文，验证 source 和规则独立；未绑定 Chat 无新增工作区上下文。
7. 普通子任务读同目录已有文件；恢复时切权限，确认新快照在数据库及实际执行生效。
8. 触发一个本来可用的 Workflow，确认它经私有 spec 获得规则，内部产物路径、预览和现有资料引用正常；没有把它改造成通用文件执行器。
9. 运行中撤销：grant 先 revoked，调用已有停止路径，随后请求/新子任务拒绝；记录取消响应与最终任务状态。授权目录的历史内容不会被擦除。
10. 同路径重新授权：新 grant 可用于新任务，旧任务仍不恢复。

把“自动测试通过”“真实模型行为通过”“平台未测”“现有工具能力缺口”分别记录，不能把上一类推导成后一类。Windows 如缺少真机，列为未验收；旧文档的历史豁免不自动代表本次用户已豁免。

### 12.3 最终边界与冻结检查

在准备提交/交付前同时检查 committed、staged、unstaged、untracked。算法及其他禁区相对官方基线必须零差异；Local/Desktop 只允许最终 Review 通过的一次性工作区清单，且在冻结点后不能再出现提交；文档只允许本目录四份交接文件。

```bash
git diff --exit-code "$TARGET_BASE" HEAD -- algorithm tests/algorithm evo workflows skills .github .gitmodules LAZYLLM_VERSION
git diff --check
git diff --cached --check
git status --short
```

再运行严格的双目录检查（支持路径空格/非 ASCII；不只检查最后一次 commit）：

```python
# 从功能工作树根目录运行；调用方传入任务 0 的 TARGET_BASE。
import subprocess
import sys

base = sys.argv[1]
def paths(*args):
    raw = subprocess.check_output(["git", *args])
    return [p.decode("utf-8", "surrogateescape") for p in raw.split(b"\0") if p]

changed = set(paths("diff", "--name-only", "-z", base, "HEAD"))
changed.update(paths("diff", "--name-only", "-z"))
changed.update(paths("diff", "--cached", "--name-only", "-z"))
changed.update(paths("ls-files", "--others", "--exclude-standard", "-z"))
allowed_exact = {
    # 一次性提取完成后，把经 Review 的 Local/Desktop 文件逐项填在这里；禁止目录级放行。
}
allowed_docs = {
    "docs/plan/local-task-workspace-file-access/backend-frontend-only/IMPLEMENTATION_PLAN.md",
    "docs/plan/local-task-workspace-file-access/backend-frontend-only/findings.md",
    "docs/plan/local-task-workspace-file-access/backend-frontend-only/progress.md",
    "docs/plan/local-task-workspace-file-access/backend-frontend-only/task_plan.md",
}
outside = sorted(
    p for p in changed
    if not p.startswith(("backend/", "frontend/"))
    and p not in allowed_docs
    and p not in allowed_exact
)
if outside:
    raise SystemExit("Forbidden changed paths:\n" + "\n".join(outside))
print("PASS: all changed paths are in backend/frontend or the reviewed frozen Local/Desktop manifest")
```

该片段可存于临时文件，不新增根目录 scripts 工具。确认 submodule 工作树也干净；嵌套模块内容改动不能只靠 gitlink 相同判断。任何禁区文件有变动，先定位来源并在自己的工作范围内处理；不得清理用户未知改动。

## 13. 下一个 agent 的执行记录模板

每个任务提交独立、可审查的局部变化；是否创建本地 commit 按当次用户授权，未经要求不 push/更新 PR。发现新目标基线改变接口时更新计划中的对应合同和证据，不能继续照抄过期代码。

最终交接报告必须包含：

- TARGET_BASE 与最终 HEAD；实际修改文件清单；算法零差异结果；Local/Desktop 一次性清单、文件 hash 与冻结点。
- 已复用的组件、实际新增的前后端接入点，以及没有采用的旧跨层改动。
- 每条验证命令、退出码、通过/未通过/未运行；基线失败与新增失败分开。
- Local/Desktop 原样算法读/建/改结果，真实目录证据与当前能力限制。
- 询问、权限下一次执行生效、撤销停止、普通子任务、Workflow 私有上下文的结果。
- 任何没有完成的需求保留为未完成，不使用“整体已可用”掩盖。

可直接发送给下一个 coding agent 的任务文本：

> 请按 `docs/plan/local-task-workspace-file-access/backend-frontend-only/IMPLEMENTATION_PLAN.md` 执行 Local Workspace 重做。以文档记录的官方 TARGET_BASE 建立功能分支，算法和 LazyLLM 必须保持官方零差异。Local/Desktop 只允许在一个批次内逐 hunk 提取并精简旧工作区接入：复用官方 Desktop selectFolder/bridge、Local Proxy CORS/AdminSession/proxy 和 Core 授权能力，排除 Caddy、assistant bridge、脚本删除及旧算法 token；测试通过后记录文件 hash/冻结点，此后不得再改。严格控制代码量，小功能超过约 200 行或新增 1 个以上生产文件先 Review。任务 3A create/append 已暂停，禁止新增 MCP、修改算法或开启 trusted。随代码同步更新本目录文档，不继承旧 PR 的完成结论。

## 14. 本文档的验证状态

- [x] 复查上游现有请求字段和主要消费者。
- [x] 复查当前 PR 的可提取前后端实现和禁区依赖。
- [x] 定位 Desktop selectFolder、AskCard、ContextPrompt、StopChatGeneration、普通子任务 DB Params 和 Workflow 私有 execution-spec。
- [x] 当前 FEATURE_SNAPSHOT 的请求/询问/LocalFS/目录身份/停止存储/错误翻译聚焦测试通过，四个 Go 包均为成功退出。
- [x] 当前 FEATURE_SNAPSHOT 的 Desktop bridge 与 AskCard 既有测试通过：2 个文件、9 个测试。
- [x] 文档结构、嵌入 JSON/Python/shell 语法及本轮仅文档变更检查通过；示例 Go/TypeScript 属于待实现代码，未声称编译通过。
- [ ] 任务 0 在目标部署的原样算法完整读/建/改检查，由下一位 agent 开始时执行。
- [ ] 本计划所述新前后端代码、测试和最终真实平台验收，尚未执行。

### 14.1 编码启动后的任务 0 实测补充

- `TARGET_BASE=245bc26dca1f2e8b56b0766cf72fdfcdb49138d9`；隔离分支 `codex/workspace-backend-frontend` 直接建立于最新 `upstream/main`。
- 已用官方 LazyLLM gitlink `2e3d00ac3ae4ae983cb7d0ca4bd231c1a56ebfc0` 和未修改的算法源码，在 macOS 临时目录执行工具级检查；`trusted_local_mode=false`。
- `LocalFileToolkit.read` 成功读取 `existing.txt` 的 `alpha\n`；`LocalFileToolkit.string_replace` 成功替换为 `beta\n`，磁盘复核一致。
- 通过主任务 `write_file` 对同一宿主临时目录执行新建 `created.txt`、新建 `nested/result.txt` 和向 `existing.txt` 追加均失败，统一返回 `ToolExecutionError: path must stay inside the current main-Agent workspace`；两个新文件均未出现在磁盘，原文件保持 `beta\n`。
- 因此 macOS 当前原样算法能力矩阵暂定为“读：通过；精确替换已有文本：通过；创建/嵌套创建/追加宿主文件：不支持”。该缺口按 2.2 保持未通过，不授权修改算法或运行模式；Local/Desktop 仅能执行 0.1 节的一次性精简接入，不能用于开启 trusted 或规避该能力结论。完整 Local 服务与打包 Desktop 场景、ask_user 和 ContextPrompt 仍待后续环境验收。

### 14.2 阶段一首批测试状态与待决策项

- 已增加任务 1 的数据模型、授权/绑定、用户隔离、撤销重授权、通用错误 reason、权限快照、表结构及迁移合同测试；尚未实现生产功能。
- 在官方 `TARGET_BASE` 运行聚焦测试时，预期因缺少工作区模型、服务、表和迁移而失败；失败与待实现范围一致。
- 原样算法实测已把“创建/追加宿主文件”从假设变成确认的能力缺口。继续按当前方案可交付数据组织、目录授权、读取、精确修改、询问、撤销和 UI，但创建/追加验收必须保持未通过。
- 若用户要求在冻结 `algorithm/`、`local/`、`desktop/` 的同时补齐创建/追加，需先批准新增后端业务写入入口；该方案必须新增所有权、路径规范化与目录身份复核、扩展名/大小限制、原子写入、覆盖与追加语义、ask_user 承接方式、撤销竞态、审计与错误脱敏测试，不能仅增加一个无约束写文件接口。

编写日期：2026-09-08。进一步检索证据见同目录 `findings.md`，文档制作记录见 `progress.md`。

## 2026-09-08 当前接手执行记录（优先于历史状态）

- 文档远端已 fetch 并核对：`aa53e9699ba9f6b06383d3fe2eb94adf538a1a83`。
- 当前工作树 `/Users/theone/Downloads/lazymind-workspace-core`，新分支 `codex/local-workspace-core`，直接建立于用户指定 `245bc26dca1f2e8b56b0766cf72fdfcdb49138d9`。原工作树仍在 `feature/newWorkZone`，开始时 status 干净，没有覆盖改动。
- 官方 main 只读查询为 `bac0dc102775488df19908dbcf4f5fe4e47a084c`；本轮遵循用户指定基线，不自动升级。
- 已完整阅读四份文档、`.cursor/rules/coding-standards.mdc`、迁移 AGENTS.md；父目录没有 AGENTS.md。遵循用户明确要求及本文历史交接的测试人工 Review 门禁，先交付任务 1 首批测试，不写生产功能。
- 当前只导入四份文档，生产净增 0。复用现有 ORM 测试数据库、迁移 runner 和聚焦测试，不引入框架。
- 已运行官方基线 `go test ./chat ./subagent ./common -run 'Test(BuildChatRequestBody|BuildLazyChatRequest|ReplaceAskUserToolResult|ApplyLocalFSPathsForChat|InterruptConversationStopsOnlyActiveTasks|ErrorCatalogCodesHaveTranslations)' -count=1`：三个包通过，退出码 0。
- 文档冲突解释：0.1 与任务 2 及用户最新要求优先；2.3、3.1、任务 6 中直接提交 renderer path / Core picker / 删除窄 workspace IPC 的旧描述不再是实现合同。任务 2 前应形成精确的本机选择证明接口测试并 Review；不得按旧条款绕开选择证明。
- 任务 3A 继续暂停。上述历史机器实测不当成本机验收；本机工具能力、前端及 PostgreSQL 尚待验证。
- 下一步：完成任务 0 可运行的本机检查、编写任务 1 测试并报告预期失败/异常失败，然后等待人工 Review。

### 当前批次 T1-RED-1：测试已提交 Review，生产实现未开始

- 实际文件：`backend/core/localworkspace/service_contract_test.go`（195 行）、`backend/core/chat/local_workspace_schema_test.go`（138 行）、`backend/core/chat/local_workspace_mode_contract_test.go`（67 行）、`backend/core/migrate/local_workspace_migration_contract_test.go`（66 行）。四个测试文件 +466/-0；生产文件新增 0、生产净增 0。
- schema 测试逐文件参考 FEATURE_SNAPSHOT 并补齐 permission/version/updated_at；其他测试新写，复用 `newToolsTestDB`、`startChatToolsTestServer`、`MigrateAllModelsForTest`、`openRawSQLite`、`execMigrationFileForDriver`、通用错误 catalog。未提取生产实现，无重复生产层。
- `go test ./localworkspace ./chat ./migrate -run 'Test.*(Workspace|LocalWorkspace)' -count=1`：退出 1，缺失 API/模型的编译阻断、缺少表和迁移、Chat/Cloud 缺失拒绝门禁，均与未实现范围一致。服务用例因编译阻断尚未执行，不能宣称行为测试已验证。
- 当前批次覆盖 runtime、owner、撤销后解析与同路径重授权、草稿不落库、reason、撤销版本冲突、Chat/Cloud HTTP 门禁、表/唯一绑定/cascade、三组迁移存在性与 SQLite 迁移升级回退。完整 Work 创建绑定/换绑、权限更新 CAS、目录身份变更、aggregate/dev 等价与 PostgreSQL 新增合同仍待后续测试批次；本批不代表任务 1 测试阶段全部完成。
- 本机原样算法工具级复核：读 alpha、精确替换 beta 与磁盘断言成功；create/nested-create/append 均返回 `ToolExecutionError: path must stay inside the current main-Agent workspace`。未启用 trusted、未添加 MCP。脚本退出 0 但有官方退出清理 logger 异常，详见 findings。
- 算法、算法测试、LazyLLM gitlink/子模块工作树、Local/Desktop 与指定官方基线零差异。任务 2 未开始，没有冻结清单/冻结提交；不得将当前零改动当成该任务验收完成。
- 门禁：依用户执行要求 3 及本目录 progress 的两阶段约定，停在本批测试人工 Review。下一步先 Review 本批测试和未覆盖项；批准后继续任务 1，任何生产小批次仍受约 200 行/1 新文件门槛，任务 2–7 尚未开始，3A 暂停。


### 2026-09-08 T1-SCHEMA-1：ORM 与迁移生产批次

- 用户明确批准直接进入生产，并批准超出原 297 行 SQL 的最小修复批次；共享的六份旧工作区 migration 从文档快照逐文件提取并通过 `scripts/check_migration_immutability.py --base 245bc26d...`，未改写历史 SQL。
- 生产修改：新增 `common/orm/local_workspace_models.go`，在 `all_models.go` 注册两表；新增三组历史 dev migration 和一组 `20260908065108_fix_workspace_binding_timestamp` 修正 migration；更新既有 v0_3 aggregate；`migrate/run.go` 将声明 `PRAGMA foreign_keys=OFF` 的 SQLite migration 固定到单连接，在事务前暂停外键、事务内执行 `foreign_key_check`、所有出口恢复原设置。
- 清理：新事务 helper 替代 apply up/down 与 migration 测试 helper 的重复 Begin/Rollback/Commit，共删除 52 行旧事务代码；没有删除仍被 dialect 容错测试使用的 `execMigrationSQL`/column-change helper。
- 规模：本批生产约 +554/-42，净增约 512 行。其中不可压缩部分为八份获批 SQL（历史三对 214 行、修正一对 46 行）及 aggregate 83 行；runner +84/-42；ORM +44。未新增 manager、facade、DTO 或依赖。
- 双数据库验证：`MIGRATION_TEST_POSTGRES_DSN=... go test ./migrate -count=1` 通过；SQLite/PostgreSQL 独立 ORM schema 测试通过；`go test ./common/orm -count=1` 通过。测试覆盖 FK 原始 ON/OFF、SQL/历史/integrity 失败回滚、up/down、绑定数据保留、修正 migration 时间戳保留、约束与索引、aggregate/dev 语义等价。
- 禁区：algorithm、tests/algorithm、LazyLLM、Local/Desktop 与官方基线零差异。任务 2 尚未开始。
- 下一步：继续任务 1 service/errors/context 小批次，使首批服务测试从 compile-red 进入行为验证，再单独接 handler/chat 绑定。


### 2026-09-08 T1-CORE-2：Core 授权、绑定与 API 生产批次

- 用户在迁移批次后再次指示继续生产。本批完成任务 1 的 Core service/errors/context、Unix/Windows 目录身份、四个公开 handler/route，以及 Chat 请求前门禁和会话+binding 原子事务；没有实现任务 2 的本机 picker/token，也没有内部算法执行接口。
- 实际生产文件：新增 `backend/core/localworkspace/{service.go,errors.go,context.go,directory_identity.go,directory_identity_unix.go,directory_identity_windows.go,handlers.go}`、`backend/core/chat/local_workspace.go`；调整 `chat/conversation.go` 与 `routes.go`。生产 +706/-1，净增 705 行。测试增加 `chat/local_workspace_binding_contract_test.go`、`localworkspace/handlers_contract_test.go` 并扩展 service contract，共 +281。
- 估算偏差：服务层先估 380–450 行，最终增加到 705 行。不可复用部分为四个公开 API（249）、会话原子绑定/门禁（130）、三平台身份实现（84）、grant/snapshot/error（238）和路由（5）。现有 `store.DB`、`common.AppError/ReplyAppErr`、GORM transaction、`systemdeps.IsLocalRuntime`、Conversation ORM 和 mux route 均已复用；没有 manager/facade、通用框架、重复业务 DTO 或新依赖。
- 业务权威：Core 计算并保存目录身份；Register 的 source 仅为 local/desktop 展示元数据，不与 runtime 字符串比较。Resolve 每次检查 owner、Work、binding、grant status/version 和目录身份；目录被替换时旧 grant 单向变为 path_unavailable。撤销和权限变更使用乐观版本，权限响应明确 `effective_at=next_request`。
- API 行为：Chat/Cloud 带 workspace 参数时在任何下游调用和会话落库前拒绝；Local Work 首次创建时 Conversation 与 binding 同事务；已有任务不能新增/切换 binding。List、Binding、Revoke、Permission 仅访问 owner 数据；Binding API仍可显示 revoked 状态，执行 Resolve 则拒绝。
- 清理：List 从逐 workspace Count 改为单次 group 查询；新事务/错误 helper 仅在确有复用时使用。未删除仍被旧业务调用的代码。两次工具命令因在 `backend/core` 工作目录仍带仓库前缀而在写入前失败，已改用正确路径；一次 Windows `go test` 尝试执行 PE 文件导致 exec format error，改为 `go test -c` 验证编译。这些均未造成产品文件部分覆盖。
- 验证：`go test ./localworkspace ./chat ./migrate . -count=1` 全通过；PostgreSQL `go test ./localworkspace` 与 `go test ./migrate` 全通过；`GOOS=windows GOARCH=amd64 go test -c ./localworkspace` 生成有效 PE32+；迁移不可变、diff check、禁区零差异和 LazyLLM 子模块干净均通过。
- 未验证：真实 Local/Desktop 选择与授权转发、前端、ContextPrompt/请求增强、询问/停止、子任务/Workflow、OpenAPI。任务 3A 仍暂停。下一步严格进入任务 2，一次性完成 Local/Desktop 优化、接入、测试、hash 和冻结。


### 2026-09-08 T2-NATIVE-FREEZE：Local/Desktop 一次性接入完成并冻结

- 冻结提交：`ec4676e0d0fb290d81b3160a56e798849ea2d4e4`。此提交之后禁止修改下列 Local/Desktop 文件；发现问题先报告用户并等待重新授权。Backend/Frontend 后续任务不受此冻结影响。
- 实际生产规模：任务 2 全批生产 +756/-11，净增 745；测试 +498。Local/Desktop 生产部分约净增 686：Desktop main/preload、Local Proxy workspace/picker/server/CORS、Runtime Manager Core/Proxy caller token；其余为 Core内部入口和 Frontend bridge 类型。旧 Local Proxy workspace.go 为 425 行，最终 398 行左右；删除两份重复 directory identity 平台文件并由 Core统一计算 identity，合并 Core HTTP helper，复用 transport/AdminSession/writeJSON/requestFromLoopback/CORS/route 配置。
- 选择证明：Desktop candidate 绑定 webContents、AdminSession user、五分钟 TTL 和本机文件对象 proof；Local candidate 绑定 AdminSession user、五分钟 TTL 和 `os.SameFile` proof。authorize 只接受 selection token，消费后不可重放，注册前重新 realpath/stat。重新授权必须再次弹系统 picker并选择 Core记录的同一 canonical path；取消不创建 candidate/grant。
- Core继续是 grant、binding、permission、reason 和目录 identity 唯一业务权威。本机层只证明选择并转发 path/source；Core内部入口使用 runtime caller token、重新计算目录 identity，不向算法注入 token。
- 明确排除并由 diff 审计确认：Caddy版本、assistant bridge、Windows脚本删除、build测试删减、config.env、algorithm_service/token注入、旧 InternalResolve执行通道、Local Proxy重复 identity 文件均未带入。
- 冻结前验证：Local Proxy `go test ./...` 与 Windows test binary编译通过；Runtime Manager `go test ./...`（约65秒）与 Windows编译通过；Desktop node测试49项通过；Frontend Desktop bridge Vitest 11项和 TypeScript检查通过；Core localworkspace及根包通过；algorithm/tests/algorithm/LazyLLM零差异。
- SHA-256 清单：

```text
ada4ee40a6ec1edde0fc41f7a86d36157b70fcd4a359f19193a920b20119a47d  desktop/electron/src/main.js
bff62d69cffe94ac07302d48c7d5725030971b1b25269fed9565dddd05e986e1  desktop/electron/src/preload.js
ca42379a87872f70f2e6ea5d4a31caa35c0b570bf619741c90ae303b10a72ac1  desktop/scripts/local-workspace-contract.test.mjs
4febe148728ae644bb7db91cf59bba82b0ed201172f7d002de1789b07753c57e  desktop/scripts/preload-bridge.test.mjs
a30dac12f9f0e27f71cf68cb08e3d96f5435c77f8ff872bb5cb13f70262f6abe  local/local-proxy/internal/server/cors.go
886c459122cf348603981518fc640578efcb0464a059cc836d49f12b242302ef  local/local-proxy/internal/server/server.go
f0c048689f1b76b62bf34e6f5e30bef074cf3775bbbb4363fd39250339187774  local/local-proxy/internal/server/workspace.go
12f304f9073e2539fee6a79efd7be119f2726871735c80c814c0357a1585f11a  local/local-proxy/internal/server/workspace_picker_darwin.go
9d47b5c650e966441990273afb52cfd184820154c8ba7045c5d890069173be42  local/local-proxy/internal/server/workspace_picker_other.go
7f6858e5db2d2ceebf6aec4c279481bcbceeaa5516faec72c50cc6c540d88e75  local/local-proxy/internal/server/workspace_picker_windows.go
8e86a5697abb906395c4ccba9e79d74156d57513e80ade826e9d94c46b3c89e4  local/local-proxy/internal/server/workspace_selection_contract_test.go
292e3dfa12ab35448792092205df8b5e7925cbf85c05b2f2a50184734ffbc379  local/local-runtime-manager/core_service.go
94299dfda753f9b77eb4ca27964438ae0f2714b8707101576cf953a378850a12  local/local-runtime-manager/local_proxy.go
f9f7d345ee718b7783a0eda75b6727bd053ad04d505df109d8c3dd4e3f8d1822  local/local-runtime-manager/local_workspace_env.go
11f7f4a1918fcd24c43134a3afa9c2ed85fb31e359a48ac19273eebc001dca02  local/local-runtime-manager/local_workspace_env_test.go
```

- 实施异常：三次从子目录执行却带仓库前缀的写命令在首个重定向/读取前失败，没有部分写入；之后固定从工作树根执行。首次 hash脚本因 zsh未按换行拆分路径失败，改用 NUL分隔后生成以上清单。
- 下一步：任务 3 只修改 backend/frontend；Local/Desktop 文件冻结，不再触碰。任务 3A仍暂停。
