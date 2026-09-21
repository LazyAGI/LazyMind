# Workspace 文件工具与授权

## 职责

- LazyLLM `FileSystemToolkit` 提供通用 host filesystem 操作。`HostFileResolution` 只保存最终参数和文件意图，实际 IO 由原工具负责。
- LazyLLM 不提供内置 policy 或安全配置；`authorization_policy=None` 时 ready 调用默认 ALLOW，调用方传入的策略决定 ALLOW / ASK / DENY。prepare 失败始终保留原错误，不执行工具。
- Core 通过可信执行上下文传递部署标识。非本地 Algorithm 不传 policy；本地 / Desktop 使用 `WorkspaceAuthorizationPolicy`，上下文异常不能降级放行。
- Core 保存 workspace 配置和会话 shell/tool grant，承接 ASK 的审批、claim、complete；不重新计算 host_access 的路径权限，不执行其文件 IO。
- `conversation_workspace` 管理会话内部目录；`chat_artifact` 发布可下载产物；`file_resources` 处理附件、PDF/Office、解析缓存和窗口读取。

## 文件工具

| 工具 | 行为 |
| --- | --- |
| read | offset 从 1 开始，默认 500 行 / 64 KiB；上限 2000 行 / 256 KiB。长物理行每 1024 字符分为逻辑行，使用 next_offset 连续读取，不跳过内容。 |
| ls | 单层目录；默认 200 项，上限 1000 项。 |
| glob | rg --files；最多 100 项，输出最多约 64 KiB，30 秒超时。 |
| grep | rg 内容检索；最多 100 项，输出最多约 64 KiB，单条 snippet 最多 1000 字符，30 秒超时。超长结果记录返回 truncated。 |
| write / edit | 先构造与编码最终内容，再通过同目录临时文件原子替换；失败保留原文件。 |
| mkdir / move / remove / stat | 复用原工具语义；递归删除仍需显式指定。 |

`glob/grep` 要求安装 ripgrep，不提供第二套递归搜索实现。

Main Agent 同时拥有 filesystem、`read_file_resource/search_file_resource` 与 artifact 发布能力。资源读取支持附件、fr_xxx 和会话内部文档，绑定 workspace 不会移除这些工具。SideChat 使用附件与资源限定的只读版本。

## 权限

### 工作区持久身份

授权和项目绑定共用平台身份实现：

- macOS：`fsid:darwin:v2:<卷 UUID 的 32 位十六进制>:<inode 十进制>`。通过 `fgetattrlist` 从打开的句柄读取卷 UUID，同一句柄读取 inode，不持久化重挂载可能变化的设备号。
- Windows：`fsid:windows:v2:<卷序列号的 16 位十六进制>:<File ID 的 32 位十六进制>`。使用 `GetFileInformationByHandleEx(FileIdInfo)` 保留完整 64 位卷序列号和 128 位文件 ID；路径观察也即时保存完整身份，不依赖 `os.SameFile` 的旧文件索引。
- Linux：继续使用原有设备号/inode 格式，不承诺跨重挂载稳定。

接口失败、无效身份、目录或符号链接替换均拒绝，不回退到路径授权。Windows 验收范围为本地 NTFS、ReFS；不承诺 FAT/exFAT、UNC 的持久稳定性。Algorithm 单次调用内的设备号/inode 检查不变。

本次不迁移旧授权或修复历史项目绑定；验收必须新建目录重新授权。同路径但真实身份不同仍触发项目冲突，不能通过放宽校验复用。

创建阶段已知的工作区业务错误直接展示原因并保留消息和目录选择，不重连未创建的会话；网络错误继续沿用恢复流程。审批 `expired + execution_inactive` 展示“执行已结束，请求已失效”，真正超时仍展示“已过期”。

### 执行策略

- NONE、DECLARED 只读 → ALLOW；UNDECLARED 表示未提供声明，不代表危险等级。
- allow_all 是完全信任：所有 ready 调用通过 host_file 授权，包括 shell、未声明和 OPAQUE 工具。
- always_ask：写入/删除、shell、UNDECLARED 每次询问，忽略历史会话授权，不提供后续允许。
- ask_as_needed：工作区内写入/删除放行，其他路径询问；shell 和 UNDECLARED 有对应会话授权时放行，否则询问。
- 未绑定 workspace 时所有路径都视为工作区外，现有产品入口使用 always_ask。
- 可信已安装 Skill 的 run_script 放行；其他未受信任 OPAQUE 在非完全信任模式拒绝。
- `.env/.git/.ssh` 等名称不形成额外权限规则；shell 与 run_script 保持 exclusive 调度。
- 完全信任不绕过业务权限、参数准备、文件身份检查、工具限制和取消机制。

ALLOW 和 DENY 不创建 Core operation。ALLOW 保留本地执行时路径身份检查；ASK 经 prepare-batch、等待、claim、execute、complete。整个 batch 的 ASK 都进入终态后才执行工具，拒绝项返回 SKIPPED，保持原始顺序。

`selected_indices` 仅选择调用；ready ASK 必须显式出现在 `approved_indices` 中。ready DENY 或未批准 ASK 导致执行前整批报错；selected 中的 preparation failure 只返回原失败，不阻止合法调用。

通用工具审批使用 capability=tool、operation=tool、空 path 和 tool_identity。单次批准绑定 call_id、tool_identity、arguments_digest 和 run/task 身份。界面显示工具名、来源和未知文件访问范围，不存储原始参数。

MCP 身份为 `mcp:v1:<sha256(canonical_descriptor)>`，描述包含来源命名空间、持久 server ID、连接目标、transport、启动参数与协议工具原名，不含认证凭据。模型 alias 不作为授权身份；同名工具注册行为不在本次修改范围。无持久 server ID 或稳定注册身份时仅允许一次。

会话授权键为 `tool:<tool_identity>`，只做一次摘要，与 `shell` grant 独立。Core 根据 operation 创建时冻结的 permission mode/version 决定是否允许 allow_future，仅 ask_as_needed 且身份稳定时支持工具后续授权；审批期间修改会话设置不改变旧 operation 规则。

Shell operation 使用 capability=shell、operation=shell、空 path 和绑定参数摘要，UI 展示有长度上限的命令摘要；省略内容以省略号标明。Core 仍验证用户、会话、run/lease、workspace 绑定版本、审批状态与有效期。

## 上下文

`root` 是用户授权边界，未绑定时为空；`cwd` 是 filesystem 相对路径基准，绑定时为用户 workspace，未绑定时为会话内部目录。SubAgent 继承 Main Agent 的 root/cwd，但其 scratch、artifact 和 spill 目录独立。Spill 通知提供绝对路径，避免将任务内部目录误当成 cwd。

ToolResolutionContext 只携带 managed_roots、managed_files 和当前请求的 citation_state。前两者归一化，citation_state 保持同一对象，使同轮生成图片后能够解析新短引用。

## 升级与边界

先更新 LazyLLM，再更新主仓库 gitlink 与 Algorithm/Core/前端。扩容 conversation_tool_grants 的 capability 字段并修改 CHECK 约束，增量迁移支持 PostgreSQL 和 SQLite，同时纳入 v0_3 既有聚合；不改写已共享增量迁移。

Core 仅接受 host_access，保留 prepare-batch、状态查询、claim、complete；文件 IO 由 Algorithm 执行。Windows 盘符、UNC 路径按本地路径识别，输入物化按平台处理；原生 Windows IO 验证需要 Windows 环境。权限快照按请求固定，shell/tool 的本次新增 grant 由 run 局部集合补充。
