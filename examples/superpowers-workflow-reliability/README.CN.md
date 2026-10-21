# 亲眼看到：Brief 缺失时，Workflow 如何阻止继续执行

[English](README.md) · [演示步骤](DEMO.md) · [Advanced CLI](ADVANCED.CN.md) · [Review 响应](REVIEW_RESPONSE.md)

这是基于 LazyMind 的可复用 Superpowers 派发可靠性 Workflow 集成。

Superpowers [#2282](https://github.com/obra/superpowers/issues/2282) 关注“Brief 尚未就绪就派发”。
Skill 描述执行约定；本示例为主动进入 Workflow 的任务增加可观察的准入边界。
历史原生 Skill 正常样本成功，演示中的 Brief 缺失明确属于 **Fault Injection（故障注入）**，不冒充上游 Bug 的自然复现。

## 为什么需要 LazyMind？

- **Reliable：** Core 只在前置步骤成功后授权实现步骤；固定版本的原生业务校验拒绝缺失或空 Brief。
- **Observable：** Panel 展示 Brief、步骤状态、代码与实际测试输出 Artifact，保留失败 Attempt。
- **Recoverable：** Core 保留 Attempt 与用户恢复入口；Advanced 路径已验证 UI Retry。本次原生示例的编辑后同 Session 恢复在测试服务中仍为 **BLOCKED**。

[原生小示例](../../workflows/superpowers-brief-showcase/workflow.yaml) 复用现有 Codex 插件、状态图、
脚本工具、步骤后置校验和人工审核。没有外部 Python 程序推进这张图。
Codex 准备 Brief，LazyMind 原生 worker 校验其绑定版本，通过后 Codex 在新建临时目录执行开发任务；因此 LazyMind 也需要可用的对话模型。

## 在 Codex 中试用

支持 macOS/Linux，需要运行本 PR 分支的 LazyMind（原生 worker 和模型可用）、桌面 Codex、Node.js >= 22.19。
插件复用桌面自带 Codex，不要求另装 Codex CLI。

1. 检出分支，按[正常安装方式](../../docs/quick_start.md)运行或重启 LazyMind；启动时注册新 Workflow 包。
   在 Workflow 设置中将 **Superpowers Brief Gate Showcase** 设为手动启用。

   ```bash
   git clone --branch feat/superpowers-workflow-eval --single-branch \
     https://github.com/NicoYang1984/LazyMind.git LazyMind-showcase
   cd LazyMind-showcase
   ```

   已部署的服务也可由管理员通过[现有 Authoring API](../../skills/workflow-agent-kit/references/existing-workflow-authoring.md)
   发布[包中的四个文件](../../workflows/superpowers-brief-showcase/)。自定义脚本遵循现有信任审批。
   执行插件不提供发布工具，不能自动安装服务端缺失的包。

2. LazyMind「设置 → 外部 Agent 集成 → Codex」点击连接，使用[官方接入方式](../../integrations/codex-workflow/README.md)。
   首次安装后打开一个新的 Codex 任务加载工具；已经加载插件的会话可以继续使用。不改变其他 Codex 任务的行为。

3. 对 Codex 说：

   > 使用 LazyMind Workflow 插件运行 Superpowers Brief Gate Showcase，打开 Panel。
   > 只在新建临时目录实现任务，完成后等我审核。

   Codex 发现并启动 Workflow、绑定会话，Codex 发布 Brief 后启动原生校验，等待时结束当前轮次；
   官方队列设计为唤醒同一任务，Codex 必须重新读取 Core 状态再执行。本次 CLI 宿主的插件测试需要显式续接，队列 accepted 后桌面自动执行尚未验收。
   查看 `labels.py`、三个断言及真实测试输出，再在 Panel 审核。

4. 新开一次 Workflow 运行，发送以下明确的故障注入请求：

   ```text
   使用 LazyMind Workflow 插件运行 Superpowers Brief Gate Showcase。
   FAULT_BRIEF_MISSING
   ```

   预期原生校验事件包含 `BRIEF_MISSING`，校验步骤失败，实现步骤没有获授权的 Attempt。
   此时已经运行过原生校验 SubAgent，不能声称“零模型调用”或“零操作系统进程”。

   保留失败状态供查看；编辑 Brief 后同 Session Retry 属于后续产品验收项，不是必做的试用步骤。

原生校验结束后 Codex 若没有继续，可在同一绑定任务中发送：
“重新读取 LazyMind Workflow 状态，只执行下一个获授权的步骤。”需要时手动打开返回的 Panel URL。
队列 `accepted` 不代表已经执行；不要编造 thread ID 或绕过 Core 准入。

## 真实证据

[复现演示](DEMO.md)包含真实插件 MCP 调用、原生 Gate、正常运行经人工审核完成，以及缺失 Brief 的阻断。
[证据与图片哈希](evidence/showcase/receipt.json)保留独立 Core **409** 拒绝和零实现 Attempt 的记录。
正常插件运行使用同线程手动续接，不冒充桌面无人干预 E2E。
已有两张真实 Panel 截图；视频为 **NOT_RECORDED**。可选的 60 秒录屏指南不是运行或 Review 前置条件。

- 主案例：[2282：Brief 准入与恢复](cases/issue-2282.md)。
- 补充案例：[2245：Git Base SHA 与有限 Coverage](cases/issue-2245.md)。
- [对照与执行边界](comparison.md)、[公开历史证据](evidence/public/README.md)、[验证记录](PR_VALIDATION.md)。

[Advanced 指南](ADVANCED.CN.md) 保留 CSV Quick Start、首次安装与重复使用、自定义单任务、动态 Coverage、
六步骤执行器和 UI Retry。Python 仍然必要，因为该执行器拥有本地 Codex 子进程启动边界并在启动前复核；
需要实际 Git/覆盖校验时使用这一条路径。

安装 `requirements.txt` 后，在示例目录执行：

```bash
python3 -m unittest discover -s . -p 'test_*.py' -v
python3 verify_rc_evidence.py
```

它们验证代码和历史证据，不等于新跑了插件 E2E。

## 支持边界

桌面自动续接、编辑 Brief 后原生同 Session Retry 尚未完成，作为后续产品验收项，不阻塞本 PR 的有界 Showcase 验收。
实测 Panel 保存了人工 Brief 新版本，但禁用了 Retry；详见[原始观察与后续项](REVIEW_RESPONSE.md#remaining-product-boundaries)。

原生演示校验的是 **Brief Artifact**，不是 Codex 主机上的 Superpowers Brief 文件；
硬约束是 Core 的实现步骤授权，插件没有普通工具的 pre-tool 拦截能力。
外部步骤提交的代码/测试 Artifact 仍由 Agent 报告，Core 不认证它与主机文件一致。
请查看实际临时文件，或使用 Advanced 执行器的确定性 Git/Coverage 校验。

原生校验使用 LazyMind 配置模型，实现使用 Codex 模型，均不认证后端模型真实身份。
不覆盖完整 SDD 规划/Reviewer 循环、任意进程隔离、共享 Index 安全、Codex Todo UI 同步；
没有成功率、速度、Token 效率提升的公平对照结论。不修改生产 Core/Adapter。
新增代码沿用 [Apache-2.0](../../LICENSE)，上游材料保留 [MIT](UPSTREAM-LICENSE)。
