# Superpowers 派发可靠性案例

[English](README.md) · [设计](../../DESIGN.md) · [对照结果](comparison.md) · [发布审计](../../RELEASE_AUDIT.md)

本例包含标准 Workflow、局部外部执行器、自动化测试和真实执行证据，覆盖 #2282，
并部分覆盖 #2245。[Workflow 源码](../../workflows/superpowers-reliability/) 使用
六个步骤和必需 JSON Artifact；执行器在实际启动 Codex 子进程前检查 Brief、
可观察 Todo 和真实 HEAD。

**硬检查仅约束通过此执行器发出的派发。仅加载 YAML 不会拦截所有 Codex 工具。**
本次未修改 Runtime 或 LazyLLM 子模块。

## 快速开始

需要 Python 3.9+、Git、macOS/Linux、已登录且支持指定模型的 Codex CLI，以及
具有 workflow.control.v1 正式接口的 LazyMind。先按[启动指南](../../docs/quick_start.md)
启动服务。本轮独立审计使用 macOS、Python 3.9.6、Git 2.50.1 和 Codex CLI
0.155.0-alpha.9；CLI 必须支持 --ignore-user-config、--ephemeral 及指定模型。
更换模型或 CLI 版本属于新的实验条件。完整命令见英文 README；
从本目录可先运行：

```bash
python3 -m unittest discover -s . -p 'test_*.py' -v
python3 fixtures.py /tmp/workflow-fixture
```

将已有 LazyMind 会话令牌存入权限为 0600 的私有 JSON 文件，格式为
`{"token":"..."}`，不要提交到 Git。显式指定当前实例的 API 地址。
首次安装该用户下的同名 Workflow 时，用 run.py 的 --publish-only 创建、校验和发布，
然后在 Workflow 设置中启用。重复运行应复用已有的精确 workflow_ref，并通过公开
目录/详情接口核对版本、场景文件和状态图（路径见英文 README）。该发布入口是创建
空白草稿，不是更新器；在已安装同名 Workflow 的本轮实例上重复发布实际触发唯一键
冲突，未启动子进程。原生对照入口为 native.py；加 --persist-session 可保留本次会话，再用
export_tool_trace.py 按该运行的 thread ID 和准确工作目录提取工具记录（完整命令见
英文 README）。不导出推理、普通消息或其他会话；加密工具消息只保留哈希。
所有实验使用新临时仓库，不 reset、不强制清理。

--fault 可选择 missing-brief、empty-brief、wrong-base、dispatch-failure、
missing-coverage、missing-todo、stale-todo；预期失败返回非零退出码。派发失败使用明确标注的故障回调；
Coverage 缺项在真实子 Agent 完成后删除一行。
probe_runtime.py 验证真实服务拒绝越过前置步骤和缺少必需产物的成功提交。

恢复缺失/空 Brief：用上游 task-brief 重新生成同一文件，打开
`<前端地址>/workflow-runs/<session_id>` 点击“重试”，以相同参数加 --resume、
去掉 --fault 继续。执行器认领页面创建的同会话执行，不伪装人工批准。
当前恢复仅支持 Prepare 成功、派发校验失败的情况，不自动重发结果不明的子任务。

并行复现入口为 parallel.py（完整命令见英文 README）。它创建全新临时仓库和
两个 linked worktree，检查路径/Index 无冲突后并行运行两个真实 Workflow；
必须观察到实际时间重叠、两会话完成且父仓库不变，才报告成功。

## 实际完成度

两次原生实验及最终 revision-2 的正常/并行 Workflow 均通过 3 个原始测试和
9 个独立验收用例。早期 revision-1 正常及恢复运行只记录了 3 个原始测试，
没有为历史证据补写 9 项验收结果。
本次原生样本没有复现 Brief 缺失或 Base 错误。Workflow 对缺失/空 Brief、错误 Base
均在启动前阻断，启动计数为 0；Coverage 缺项和派发失败均无最终成功 Artifact。
同一失败会话经正式页面重试后完成，保留两次 Attempt。Todo 缺失或提前标记完成
也在启动前阻断。双 Agent 并行实验实际重叠 57.548 秒，两会话分别完成六步骤，
独立 worktree、分支与 Index，父仓库未改变。本轮独立审计的 28 项自动化测试全部通过。

初次原生 CLI 摘要流未输出 spawn；补跑从本实验会话提取了 3 次真实派发调用与回执，
确认 Brief 在派发前完成。加密消息体未解密，子 Agent 内部完整工具轨迹及实际
模型身份仍未核验。桌面 queue 唤醒和任意进程隔离未验证。有界并行实验与小样本不用于
估算故障率或提升百分比，不能把模型自己的报告当作身份认证。

默认对话模型已确认是 deepseek-flash，新对话实际返回 FLASH_READY。BAAI/bge-m3
已选中，提供商验证及模型就绪接口均通过，先前密钥故障仅作历史保留。
编码对照两边均保持请求 gpt-6-astra，宿主为
Codex CLI 0.155.0-alpha.9。原生 Skill 有两轮 Reviewer；精简 Workflow 使用一个
Implementer 和确定性验收，所以总耗时/Token 不用于效率优劣结论。

本例是阅读固定版本 Skill 后手工编写的包，经 Agent Kit 正式接口发布；没有声称
服务端 Skill 快照绑定或 AI 自动转换。详见 [#2282](cases/issue-2282.md)、
[#2245](cases/issue-2245.md) 和 [证据索引](evidence/manifest.json)。
