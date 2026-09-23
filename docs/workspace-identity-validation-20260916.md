# 工作区持久身份修复验收（2026-09-16）

## 结论

新工作区创建阻塞已修复，macOS 持久身份、错误展示及权限主流程已验证。不能宣布跨平台完整验收通过：Windows NTFS/ReFS 和系统重启尚未实测；网页 Skill `run_script` 调用存在独立超时问题。

不迁移旧授权、不修复历史项目绑定，不调整权限策略、CI 或模型配置。现有 8090 开发环境已更新。

## 环境与证据

- 新目录：`data/workspace-identity-20260916`，授权名称“验收-v2-20260916”。
- 工作区：`lws_f6d06c4aa43260b5e647799d450f916c`。
- 实际身份：`fsid:darwin:v2:fe5cde2d22644c7f975be74fe8b3c73e:157475676`。
- 网页主会话：`6aba7bd8-743a-4105-bd0f-81409fb7b77f`。
- 连续创建及 MCP 会话：`a9d659ef-72f5-4614-a145-48ff275668d4`。
- 原始接口 SSE、构建和测试日志保存在新目录的 `evidence/`，均为临时验收产物，不提交数据库或运行数据。

## 自动化与原生检查

| 检查 | 结果 |
| --- | --- |
| 容器 Core `go test ./localworkspace ./chat ./conversationgroup` | 通过 |
| 项目实际目录替换后重新授权，创建冲突且回滚会话和绑定 | 专项测试通过 |
| 容器前端三个相关测试文件 | 99 项通过；最后的重试选择补充断言所在 27 项再次通过 |
| macOS 原生身份专项测试 | 通过：卷 UUID/inode 相同而设备号变化；路径/句柄一致；关闭句柄、目录替换、符号链接替换拒绝 |
| Windows amd64 交叉编译测试包 | 通过；未运行，不代表 NTFS/ReFS 验收 |
| 前端 local 模式 Vite 构建 | 通过 |
| OpenAPI 新鲜度、错误码翻译检查 | 通过 |
| `git diff --check` | 通过 |
| 全量 TypeScript 类型检查 | 未通过；输出包含仓库其他文件的大量错误，本次修改的组件/工具文件未出现在错误输出中；不将全量类型检查计为通过 |

## 真实服务结果

| 场景 | 方式与结果 |
| --- | --- |
| 旧授权创建失败 | 网页显示“目录当前不可用”，保留用户消息和目录选择，停留在新会话 URL，无错误重连；改选有效目录后成功 |
| 新目录连续创建 | 网页纯文本回复“创建成功”；第二次通过真实聊天接口回复“第二次创建成功”，两者复用同一项目 |
| 重复授权 | 连续注册同目录返回同一工作区 ID、version=1；未合并或隐藏旧记录 |
| 计算器 | 网页实际 calculator `17+25=42`，无需审批 |
| 按需确认：区内写入 | 网页 write/read 完成；磁盘 `inside.txt` 严格为 `result=42` |
| 按需确认：区外写入 | 网页产生审批，刷新后恢复，允许一次后执行；`data/v2-outside-once.txt` 为 `outside-once` |
| 始终询问：拒绝写入 | 网页拒绝，工具返回 `workspace authorization denied`，`denied.txt` 不存在 |
| 始终询问：停止写入 | 待审批时通过真实停止接口结束执行；`stopped-pending.txt` 不存在，网页显示“执行已结束，请求已失效”，批准按钮消失 |
| 失效请求重复批准 | 接口拒绝且文件仍不存在；当前返回 HTTP 500，错误分类仍有改进空间，本次未改后端审批状态 |
| Shell 每次审批 | 同一会话两次真实 Shell `printf shell-v2-a/b`，各自产生一次审批；通过真实审批接口允许一次，均返回预期 stdout、exit 0 |
| 完全信任取消 | 网页确认弹窗取消后仍是“始终询问” |
| 完全信任实际执行 | 测试接口设置权限，真实模型依次调用 write、Shell、MCP，无审批；`trusted.txt=trusted-v2`、stdout=`trusted-shell-v2`、DeepWiki 返回目录 |
| MCP 拒绝/允许一次/允许后续 | 真实 DeepWiki `read_wiki_structure(modelcontextprotocol/python-sdk)`；拒绝被拦截，允许一次和后续允许均返回目录，下一轮新调用无需审批 |
| 刷新与服务重启 | Core/Chat 重启后工作区仍 active，绑定和权限版本保留；待审批刷新恢复已通过。MCP 后续授权后再次重启 Core 并刷新网页，新发起 DeepWiki 调用成功且无需审批 |
| 真正网络中断恢复与超时文案 | 自动化回归覆盖；未人为断开整机网络或等待真实审批超时 |

DeepWiki 实际返回前三个顶层章节为 Overview、FastMCP / MCPServer Framework、Client Framework。一次模型仅口头声称调用、没有实际工具记录的回答已排除，不作为通过证据。一次生成停滞在进入工具前，停止并重启服务后完成后续验证，不计作该轮通过。

## 未通过或待验收

1. **Skill 网页链路未通过**：临时启用内置 `paper-search`，网页指定技能后模型实际调用 `get_skill` 和 `run_script(scripts/get_bibtex.py, [1706.03762])`；三次调用（包括一次带版本号）均在 30 秒超时，另一个会话复核也超时。授权没有拦截，未弹审批。直接使用相同 native Python 和 `DummySandbox.execute_script` 运行同一脚本，均可联网返回 `Attention Is All You Need` 的完整 BibTeX。因此只能定位到服务调用上下文与独立调用之间的差异，尚不能断言网络故障或脚本逻辑错误；没有放宽超时或修改无关配置绕过。
2. **Windows 原生未验收**：需要 Windows 本地 NTFS、ReFS 分别运行身份测试和服务流程。128 位高位差异测试已编写，交叉编译不能替代运行。
3. **系统重启未验收**：由用户安排后复核 macOS 卷 UUID/inode、新绑定和权限。设备号重编号目前由原生单测模拟，不声称已实际重启验证。
4. **平台范围**：FAT/exFAT、UNC 持久稳定性不承诺；Linux 保持旧身份格式，不宣称跨重挂载稳定。

临时 MCP（`msp_4fc74964eaa24ccb9b22b17dcf43115f`）与技能（`2dcd2bff-3327-4106-bc99-89db04c0b545`）测试后停用，两条测试会话恢复为“始终询问”；保留带验收标记的会话、目录与日志供核对，不删除或迁移历史授权。

## 失效审批 HTTP 错误修复验收（19:10 补充）

本次只修改审批错误分类和审批场景文案，未修改 Skill、工作区身份、权限规则或模型配置。

- 运行状态缺失、同一执行已结束或终结：HTTP 409，`execution_inactive`；运行标识不匹配仍为 `binding_conflict`。真实存储及解码错误保留 HTTP 500。
- 已过期审批在归属校验后优先返回 HTTP 409，`selection_expired`；决策 claim 后再次读取也检查过期。
- 前端审批错误区分“执行已结束，请求已失效”和“请求已过期”，不改工作区选择过期文案。
- 容器 `go test ./localworkspace ./chat` 通过；新增 claim 期间过期测试后重新执行 `go test ./localworkspace` 通过。覆盖真实 HTTP handler、停止/结束/状态清理、存储故障与损坏数据、超时优先、后续授权未写入和不可执行。
- 容器 LocalWorkspaceControl 40 个测试通过。生产构建（`VITE_LAZYMIND_MODE=local`）通过；有现存资源和包体积警告。首次误用 Vite 保留模式名 `--mode local` 被拒绝，随后使用正确环境变量成功构建。
- 已更新现有 8090 前端和 18001 Core；首次网页调用未产出审批，停止该轮并重启现有 Chat 后重试成功。未将首次调用计为通过。
- 网页会话 `6aba7bd8-743a-4105-bd0f-81409fb7b77f` 实际 write 请求进入“等待批准”，目标 `approval-inactive-fix.txt`。点击停止生成后，重放 allow_once、allow_future、reject，均返回 HTTP 409、`execution_inactive`。页面显示已取消生成，待审批入口消失，目标文件不存在。
- 本次操作 ID：`79792bdf6e2ee7ff31d395aff1841ed54baa01fd71a79214cf5e41fdfde18ef2`。原始响应证据：`data/workspace-identity-20260916/evidence/inactive-approval-fix.json`。
- 旧测试请求 `4fd02ed418d5d13147f43e629b432762cb7cfa59a8e81845b36ce50e74eb4414` 已真实超时，重放 allow_once 返回 HTTP 409、`selection_expired`。

Skill 本次未修复。后续诊断已确认：主脚本获取 BibTeX 并退出 0 后，日志初始化产生的 multiprocessing resource_tracker 仍持有 stderr 管道，导致 capture_output 等待 EOF 并报超时；相同服务环境下临时子进程使用 split 日志模式成功返回，merge 模式复现超时。此结论取代前文未确定根因的描述，服务日志配置未因此修改。
