# 工具配置与认证闭环 P1

2026-09-22，在 `dya/enterprise-search-poc` 实施。LazyLLM 子模块和主仓库均有本次未提交修改；保留原有工作区修改。未提交、推送或部署。

## 分阶段差异

| 阶段 | 实现入口 | 行为 |
| --- | --- | --- |
| P1-A | LazyLLM `FunctionCall` / `ReactAgent`；`agent_runtime/executor.py` | 公开同步 `before_model_request`，先准备再获取工具快照、压缩和校验预算。首轮及工具结果轮均可附加内部通知。预览不调用准备或通知；强制总结不调用准备。 |
| P1-B | LazyLLM `toolsManager.py` / `tool_retrieval.py`；`tool_registry.py`；Core `mcp/capabilities.go` | 从现有工具和 MCP 配置生成目录。未就绪工具保留真实 schema，未知 MCP 仅有服务入口。eager、gateway、retrieval 共用准备检查。blocker 不进入普通失败重试。搜索保持有效绑定，明确选择的 provider 不自动替换。 |
| P1-C | `agent_runtime/tool_configuration.py`；Core `chat/tool_configuration.go`；前端 `ToolConfigurationCard` | 持久化用户、会话、历史轮次、运行、服务及邮箱目标；按任务去重。后端复核状态与配置摘要版本。请求前批量读取动作，校验预算后更新绑定，通过实际模型轮次结束事件确认通知投递。结构化事件和历史查询驱动配置卡片。 |

配置动作表只保存引用、状态和配置摘要哈希，不保存 token、密码或 OAuth state。内部运行配置只通过受内部凭据保护的接口传递；公开查询不返回凭据。发送邮件等业务权限仍由原执行边界处理。

MCP 现有 `enabled` 同时承担了“未经验证不可执行”和“关闭服务”两种语义。新增 `discovery_enabled` 属于现有 MCP 配置，用来单独记录可发现性。新建服务可发现但不可执行；显式禁用后隐藏。迁移仅为原本启用的服务回填可发现状态，历史已禁用服务保持隐藏；批量启用可以恢复未验证服务的可发现性，但不能使其直接执行。

迁移为 `20260922085706_tool_configuration_actions`，已同步 v0_3 aggregate 的 up/down。未新增版本目录或手工工具目录注册表。

## 验证记录

- LazyMind 定向 Python 回归：210 项通过，涵盖 Host 闭环、执行器、retrieval、MCP OAuth/加载/命名、工具筛选、事件转换、Workflow 范围、邮箱工具。
- LazyLLM 与压缩时序回归：45 项通过，涵盖模型事件、请求前回调、工具加载与预算。
- 前端卡片及聊天事件回归：34 项通过。
- Go `./mcp`、`./migrate`、Core 根包全包测试通过。迁移在 SQLite 和单独启动的临时 PostgreSQL 16 上完成 up/down 往返；未使用业务数据库。
- Core 专项验证覆盖动作去重、用户/任务归属、配置变化版本、过期确认拒绝、公开字段脱敏、邮箱目标匹配、MCP 未授权→待选工具→就绪→撤权。
- OpenAPI 导出、生成客户端及 stale 检查通过。

受控模型测试实际驱动 ReactAgent 循环：首个工具调用遇到 blocker，下一次正常请求获取新的目录和状态通知，后续业务调用成功，最终答复后没有额外轮次。覆盖 eager、gateway、retrieval；没有重放被阻塞的调用，也没有根据配置成功事件自动发起聊天。

失败恢复测试验证：目录/预算校验失败恢复旧目录或原凭据及展开状态；不确认未成功加载的通知；再次正常请求可恢复。未知 MCP 搜索结果不伪造成员。搜索 B 已绑定时，A 后来就绪不抢占 B。

## 尚未通过或尚未验证

- 当前工作区 Chat 全包有 `TestProjectCreationInheritanceTrashAndRestore` 失败，发生在 `localworkspace.Register`，返回 `Invalid request`；不是配置动作专项测试失败。单独复跑仍失败；明确排除这一项后，Chat 其余全包测试通过。本次没有扩展修改项目目录权限逻辑。
- 前端全量 `tsc --noEmit` 报 648 个错误；新增配置卡片、聊天事件接线和生成客户端没有出现在错误列表中。全量类型检查不能记为通过。
- 没有启动或部署完整 8091，因此尚未验证浏览器登录后的真实聊天路径、真实账号 OAuth 和真实邮箱收发。受控授权服务与受控模型测试不能替代这些验收。8090 未修改或重启。

本次没有增加自动续跑、业务调用重放、跨角色迁移或全量凭据/缓存架构改造。配置卡片完成后，若任务已经结束，仍由用户发送“继续”。

## 2026-09-24：运行时职责与 MCP 修复

实现基线：LazyMind `3f7f3a3a`、LazyLLM `c7bde8c3`；分支 `dya/tool-configuration-runtime-fixes`。
本节记录本轮增量，前文是此前 P1 的历史记录。

### 公共刷新边界

LazyLLM 提供 `ToolManager.get_tool_group_state(name)` 与
`refresh_tool_group(name, *, definition=None, tool_config=None, load=False, available=True)`。
状态摘要不返回内部对象或凭据；刷新统一处理凭据缓存、provider、目录、曝光与加载状态。
顶层和嵌套组均可刷新，保留父级名称前缀并更新祖先 gateway。
`replace_tool_group()` 复用同一实现并保留异常传播的兼容行为。

刷新结果为 `ready`、`prerequisites_unmet`、`budget_blocked` 或 `unavailable`。
仅更新显式配置项；空值清除指定项，清除凭据不会因前置条件失败而恢复旧授权。
普通更新先校验再原子落盘，失败恢复原目录、凭据及加载状态；取消也先回滚再传播。
权限撤销或白名单缩减时，产品先调用 `available=False` 阻止旧工具，后续刷新失败仍保持禁用。
无论调用来自 eager、gateway 或 retrieval，执行前的权限检查继续保留。

LazyMind 只保存稳定组名，通过公共接口刷新，不再访问 SDK 凭据缓存或 provider/激活内部状态。
配置状态与运行可用性分开；Core 返回 ready 不会抹掉本轮目录失败，也不会为连接故障创建配置卡片。
新的 runtime 恢复工具时不恢复旧加载意图。只有 `pending_delivery=true` 且恢复成功才发送成功通知，
并沿既有模型请求生命周期 ack；目录或预算失败不确认成功通知。
卡片文案为“配置已完成”，任务结束后仍由用户手动继续。

### MCP 加载、检查与预览

初始化共用批量加载入口，单批最多 4 个并发服务，按输入顺序返回带服务标识的结果，完成后统一登记。
服务超时沿现有 loader/transport 执行，单服务异常隔离；OAuth callable 不进入共享缓存。
取消时停止并清理排队协程；已经进入阻塞网络调用的 Python 工作线程无法被强制终止，仍由原超时收尾。

Core `check` 仅解析目标服务；初始化 capability 与 runtime 从同一请求内快照派生。
批量 action 查询和列表刷新按服务去重探测，不跨请求缓存授权结果。
OAuth 状态失败或单服务 header 损坏只令该服务 unavailable；归属校验及数据库错误仍使请求失败。
内置 Notion 的发现与按需创建保留。

内部 capability 增加可选 `tools`、`tools_discovered_at` 和 `tools_complete`，复用现有 MCPServerTool 表，
按当前权限、授权与白名单裁剪。预览用不可执行适配器复用 MCP schema、身份、名称归一化和曝光流程，
不创建 OAuth 连接、不取 token、不调用远端工具、不刷新或确认 action。
未知目录仍保留入口；报告和 prompt 导出增加 `mcp_catalog` 的 `source`、`complete`、`missing_services`。
该信息表示“已发现目录快照”，不承诺远端实时一致，不改变 `preview_accuracy` 的模型路由含义。
该数据走内部动态 payload，前端本地报告类型已同步；不改变公开 OpenAPI schema。

前端可见且运行时每 5 秒刷新；任务结束而 action 未就绪或暂不可用时每 30 秒刷新。
后台暂停定时请求，重新可见或收到配置更新事件立即刷新；任务结束且所有 action ready/forbidden 时停止。
同一卡片只保留一个在途请求，继续比较版本并清理卸载后的更新。

飞书保留统一搜索新契约。已核对 supplier、双语 schema 文档、渲染调用方及 personal-document-search
的 `source_type`、范围、1–20 分页和 `results/has_more/page_token`，未发现需要旧契约兼容的调用点。

### 本轮验证与限制

- LazyLLM + LazyMind 定向回归：275 项通过，另有 3 个 subtest 通过；包括嵌套组、撤权、预算失败、
  取消清理、真实 ToolStateStore 原子落盘失败、MCP 并发和预览身份一致性。
- Core 全量：`TMPDIR=/private/tmp GOTOOLCHAIN=go1.25.11 go test ./...` 通过。
  默认 macOS `/var` 临时目录会触发 5 个既有项目路径校验失败，均在未修改基线复现；无需改业务代码。
- 前端相关 4 个 suite：42 项通过；相关 TypeScript 检查通过。OpenAPI stale 检查四个服务均 fresh。
- Chat 全量：2882 通过、30 失败、1 跳过。30 个失败与未修改基线完全相同，没有新增失败；不标记全量通过。
- 前端全量：2838 通过、23 失败。22 个失败在未修改基线复现；剩余性能计时用例单独重跑通过。
  全量仍不能标记通过。
- 独立复查发现的嵌套前缀、祖先 gateway、加载路径、旧通知、取消回滚及服务隔离问题均已补回归修复。

没有部署、没有重启 8090/8091，原工作区模型配置修改保留。
真实 OAuth 与浏览器端到端尚未执行；受控目录和模型测试不替代这些结果。
