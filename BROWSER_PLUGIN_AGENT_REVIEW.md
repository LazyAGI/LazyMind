# 浏览器插件 PR：Agent 主流程 Review

审查日期：2026-09-11。审查对象：`cst/browswer_plugin`，修复前提交 `1ed8591c`。
PR 差异基线为已合入的 upstream `e92dc9b7`（#707）；另核对了最新 upstream `552ebc60`（#709），其未改变本文涉及的 Python Agent 实现。本次没有再次合并 upstream。

## 结论

浏览器插件通过系统 MCP 接入现有 Agent 执行器，架构可以保留。但原 PR 有两个已复现的兼容问题，以及一个侵入普通 Chat 请求的 Skill 自动安装逻辑。仅通过已有单元测试不足以证明与 upstream 兼容。

| 优先级 | 发现 | 影响 | 最终处理 |
| --- | --- | --- | --- |
| P1 | 压缩器在重新设定触发阈值后绕过输入预算判断 | 所有 Agent 都可能发送超预算上下文 | `pruner.py` 整体恢复为 upstream 实现 |
| P1 | 浏览器识别依赖转换前的 MCP 方法名 | 200 轮、浏览器执行提示词、VLM 查看工具不生效 | 兼容带点/下划线名称，基于系统 MCP 服务身份识别 |
| P2 | 飞书关键词触发同步自动安装 Skill | 包缺失或安装失败会导致普通 Chat 返回 500；请求会修改用户 Skill 安装状态 | 按需求彻底删除自动安装链路 |

## 发现与证据

### 1. 压缩器改变了所有 Agent 的预算安全判断

原 `pruner.py` 在 `next_pressure_tokens > 0` 时允许忽略 `effective_input_budget`。这一判断没有浏览器限定。

对相同历史和压缩状态进行离线对照：输入预算 10000 tokens，历史估算 10535，下次触发阈值 11379。原 PR 摘要调用次数为 0，输出仍为 10535；upstream 调用一次摘要后降到约 3300。这里使用本地假摘要，不依赖外部模型。

修复完整撤销 PR 对 `pruner.py` 的改动，保留 upstream 原有缓存收益、摘要选择和预算判断。模型窗口长度继续使用 #707 的配置；浏览器大结果的可搜索落盘格式保留在 `compactors.py`，不修改窗口长度和压缩触发规则。

### 2. 浏览器能力识别与真实 LazyLLM 适配器不兼容

当前锁定的 LazyLLM `2cc07741` 已将 `browser.open` 转换为 `browser_open`，而 PR 将 callable 的 `__name__` 当作原始协议名保存，随后仍匹配 `browser.`。使用真实 `generate_lazyllm_tool` 可复现：默认 retries=20 不会提升，截图工具也不会衍生出视觉查看工具。旧测试直接构造带点名称，因此未覆盖这一行为。

修复新增统一的浏览器能力名称识别，保留 MCP 调用闭包，由 Core 的系统 MCP 工具集合触发浏览器策略。用户 MCP 工具不会影响浏览器轮次和提示词。浏览器预算至少为 200 轮；如果用户原本配置了更大预算则保留。配置 VLM 时开放只读视觉查看；未配置时继续使用 DOM 工具。

### 3. 删除关键词触发的 Skill 自动安装

删除 `auto_builtin_skill.go`、其自动安装测试、Chat 请求中的调用及专用错误码/翻译。Chat 不再因为出现“飞书”等文字而自动创建或启用 Skill。

保留手动安装入口、飞书内置 Skill 包和已有安装。未对用户数据库执行卸载或清理。新增 HTTP 请求回归测试验证没有内置目录时飞书问答仍能到达 Agent 服务，且不创建 Skill 记录。

## 验证与范围

- 新增上下文超过预算但未到延迟阈值的回归测试。
- 新增真实 LazyLLM MCP 适配器测试，覆盖浏览器名称、200 轮、更高既有预算、VLM 有/无和无关 MCP 服务。
- 检查 Sidechat 只读工具策略、MCP 缓存、工作区文件边界及浏览器工具结果处理。
- Go 回归覆盖 Chat、Browser 和错误目录；同步前端错误码生成物。
- Python 定向回归：96 passed，1 条现有 jieba/pkg_resources 弃用警告。首次扩展到运行时事件测试时缺少 pytest-asyncio；安装仓库测试依赖后重跑全部通过。
- Go：`go test ./chat ./browser ./common` 全部通过；飞书问答 HTTP 测试确认返回成功且 Skill 记录数为 0。
- `make lint-python`、新增/修改 Python 测试文件的 flake8、Workflow 命名检查、测试目录检查、错误码生成一致性检查及 `git diff --check` 通过。
- `pruner.py` 与最新 upstream 完全一致；Chat 入口及附加错误目录恢复到已合入 #707 的 upstream 状态。
- 以上为代码与离线回归验证，未进行飞书页面写入或线上模型调用。

本次不修改 LazyLLM 子模块，不处理四个未跟踪 SQL 文件。后端改动通过 `LAZYMIND_DYNAMIC_PROMPT_MODULES=false make local-up` 重新构建并加载。
