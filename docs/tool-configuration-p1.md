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
