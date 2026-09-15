# Workspace 文件工具与授权

## 职责

- LazyLLM `FileSystemToolkit` 提供通用 host filesystem 操作。`HostFileResolution` 只保存最终参数和文件意图，实际 IO 由原工具负责。
- Algorithm `WorkspaceAuthorizationPolicy` 在 `prepare_tool_calls` 中决定 ALLOW / ASK / DENY。
- Core 保存 workspace 配置和会话 shell grant，承接 ASK 的审批、claim、complete；不重新计算 host_access 的路径权限，不执行其文件 IO。
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

- NONE → ALLOW；UNDECLARED → DENY。
- DECLARED 只读 → ALLOW。
- 写入和删除：allow_all 放行；always_ask 询问；ask_as_needed 仅在全部修改位于绑定 workspace 内时放行。
- `.env/.git/.ssh` 等名称不形成额外权限规则。
- 可信已安装 Skill 的 run_script 放行；其他未知 OPAQUE 拒绝。
- shell 首次询问。仅本次允许只批准原调用；本会话后续允许持久化 shell grant，并立即作用于当前 run；grant 不跨会话。
- shell 与 run_script 均在单个工具 batch 内 exclusive 调度。

ALLOW 和 DENY 不创建 Core operation。ALLOW 保留本地执行时路径身份检查；ASK 经 prepare-batch、等待、claim、execute、complete。整个 batch 的 ASK 都进入终态后才执行工具，拒绝项返回 SKIPPED，保持原始顺序。

Shell operation 使用 capability=shell、operation=shell、空 path 和绑定参数摘要，UI 展示有长度上限的命令摘要；省略内容以省略号标明。Core 仍验证用户、会话、run/lease、workspace 绑定版本、审批状态与有效期。

## 上下文

`root` 是用户授权边界，未绑定时为空；`cwd` 是 filesystem 相对路径基准，绑定时为用户 workspace，未绑定时为会话内部目录。SubAgent 继承 Main Agent 的 root/cwd，但其 scratch、artifact 和 spill 目录独立。Spill 通知提供绝对路径，避免将任务内部目录误当成 cwd。

ToolResolutionContext 只携带 managed_roots、managed_files 和当前请求的 citation_state。前两者归一化，citation_state 保持同一对象，使同轮生成图片后能够解析新短引用。

## 升级与边界

先更新 LazyLLM，再更新主仓库 gitlink 与 Algorithm/Core/前端。新增 conversation_tool_grants 增量迁移支持 PostgreSQL 和 SQLite，同时纳入 v0_3 既有聚合；不改写已共享增量迁移。

旧 Core local execution 模式保留给原消费链路。Windows 盘符、UNC 路径按本地路径识别，输入物化按平台处理；原生 Windows IO 验证需要 Windows 环境。权限快照按请求固定，shell 的本次新增 grant 由 run 局部集合补充。
