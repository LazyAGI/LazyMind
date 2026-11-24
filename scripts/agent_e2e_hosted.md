# GitHub 托管的 macOS / Windows Agent CI

目标工作流：`.github/workflows/agent-e2e-hosted.yml`（Actions 中的 **LazyMind Agent E2E (GitHub-hosted)**）。
这条工作流使用 GitHub 提供的临时 macOS Intel 和 Windows x64 虚拟机。每个 Job 都从当前提交构建并启动 LazyMind，安装三个真实 Agent CLI，创建临时测试账号，通过正常的本地认证接口连接 MCP。无需在触发者电脑上安装 LazyMind 或 Agent，也无需保持个人电脑在线。

## 首次配置

每个仓库的使用者须配置自己的模型服务 Key：

1. 在仓库 **Settings → Secrets and variables → Actions → Secrets** 添加 `LAZYMIND_CI_LLM_API_KEY`、`LAZYMIND_CI_IMAGE_API_KEY`。
2. 在同一页 **Variables** 配置可选的公开路由字段：`LAZYMIND_CI_LLM_BASE_URL`、`LAZYMIND_CI_LLM_MODEL`、`LAZYMIND_CI_LLM_API`、`LAZYMIND_CI_IMAGE_BASE_URL`、`LAZYMIND_CI_IMAGE_MODEL`、`LAZYMIND_CI_IMAGE_API`。
3. 未设置公开字段时使用 `scripts/agent_e2e.providers.example.json` 的默认值：OpenAI Responses / `gpt-6.1-sol`，SiliconFlow / `Kwai-Kolors/Kolors`。Codex 的托管配置需要 Responses 协议；模型须支持工具调用。模型服务需有对应模型的有效权限及余额。

真实 Key 只放在 Secrets 中。不要提交 Key、复制个人账号数据库，或把 Key 填入工作流 YAML。普通非探测的图片场景需要生图 Key，PPT、论文及只读连接探测只需要 LLM Key。固定测试文本在代码中公开，Agent 与外部模型服务会处理这些测试文本。

## 运行

在 Actions 选择上述工作流并点击 **Run workflow**：

- `agent=all`、`case=all`、`platform=all`：共 18 个业务检查。全量运行按平台和 Agent 拆成 Job，各 Job 顺序执行 3 个场景，最多同时运行 2 个 Job，报告按平台和 Agent 归档。
- `probe_only=true`：只验证真实 Agent 的 MCP `workflow.list/get`，不生成业务产物。
- `approve_test_reviews=true`：明确允许测试控制器自动确认本次固定材料的论文大纲和图片方案；不会审批其他用户任务。不允许自动确认时，审核节点会停止，测试报告会如实记录。
- 单个业务场景失败后会记录失败并继续其他场景；两个平台的矩阵使用 `fail-fast: false`。环境未能启动等公共前置条件失败时，该平台无法开始业务测试，另一平台继续。

每个业务用例最多 45 分钟，每个托管 Job 最多 6 小时。实际运行会消耗 GitHub Actions 配额以及外部模型的用量；不使用付费的大型 Runner。

手动运行和启用的定时运行会先执行脚本单元测试。涉及本套 CI 的 PR 在同一工作流下只运行这项检查，无需账号、模型 Key 或本机服务。

## 安装与隔离

- Go 1.25.14、Node 22.22.2、pnpm 10.0.0、uv 0.12.10；Python 服务由 LazyMind 源码运行时管理器按其配置安装。
- Python 的 CI 兼容约束见 `scripts/ci-agent-tools/python-constraints.txt`。UMAP / Numba / llvmlite 使用同时提供 Python 3.11 双平台预编译包的版本，避免无人值守环境临时编译 LLVM；不修改本机私有兼容配置。
- 前端构建沿用项目 macOS 安装包工作流的 4 GiB Node 堆内存设置，避免 Vite 构建超出默认堆限制。
- Windows 在启动服务前预编译 Core，避免两核虚拟机的首次 Go 编译超过 Chat 等待 Core 的五分钟窗口。连接器宿主标识通过正常 Go CLI 初始化；账号仍通过正常认证取得。
- Codex 0.151.0、DeepSeek Harness 0.2.1-rc.2，使用 `scripts/ci-agent-tools/` 下各自的锁文件安装。每个 Job 只安装所选 Agent，单个 Job 运行 `all` 时安装三个。WorkBuddy 5.11.4.44121853 来自官方更新服务，安装前核对固定 SHA-256。使用的是 WorkBuddy 自带的 CLI。
- PPT 导出依赖使用工作流自带锁文件安装，并安装、实际启动 Chromium。
- PPT 用例固定选择原生输出：`workflow.start` 显式传入 `clarification_values.presentation_output_format=native_pptx`，分析阶段保持该值。控制器将这个固定用例已确认的输出选择绑定到实际 MCP 启动请求；Agent 遗漏该字段或误选 HTML 时也以 `native_pptx` 启动，保留其他启动答案、固定 revision 和幂等键，并在调用摘要记录实际绑定的输出格式。该绑定只作用于本套 CI 的 PPT 用例，不改变产品中用户选择 HTML 的行为。宿主导出使用的 HTML 仅为内部中间文件，不发布为工作流 HTML 预览；最终向 `native_pptx` 发布真实 PPTX。
- 托管模型的 PPT 用例通过测试控制器提供的原生导出服务运行可信导出程序，避免 Codex 的 macOS 沙箱限制 Chromium 的系统通信。服务只监听本机、要求本轮短期令牌，只接受本测试的固定三页 deck；离线渲染、禁用字体嵌入、不接受任意命令或外部文件。Agent 仍须生成内容、发布真实 PPTX 并完成 LazyMind 工作流。
- 中文字幕使用来自 Google Fonts 官方仓库固定提交的 Noto Sans SC，核对 SHA-256 并验证汉字字形，附带字体许可；不依赖 Windows 是否预装中文字体。
- 模型 Key 由测试控制器持有；Agent 使用本次启动的本机代理和短期随机令牌。三个 Agent 都使用本次注入的 LazyMind 连接、工作流 ID 和 revision，并在业务前做只读探测；旧连接最多恢复一次。
- Codex 的 CI 临时配置保留 workspace-write 写入限制和审批机制，并显式启用工作流所需的网络访问，使可信生图辅助程序可以连接本次代理及图片服务。
- Windows 通过 Codex 官方 `windowsSandbox/setupStart` 接口初始化 elevated 沙箱，在本 Job 的专用 Codex home 中复用初始化结果；每次业务仍注入独立的 MCP 连接配置和新会话。避免没有沙箱实现时，连读取测试材料也被迫走额外审批。
- 账号、数据库、凭据和模型配置放在 VM 的临时目录。上传产物仅包括选定报告、公开构建来源记录和实际 PNG / PPTX / Markdown 交付物；不会上传整个运行目录。
- 报告区分测试通过、业务状态、文件检查及未独立验证的视觉或学术质量，并记录 LazyMind SHA、工作流 SHA、Agent 版本及 LazyMind 工作流 revision。
- JUnit 同时记录各轮 CLI 退出码、超时及耗时；业务产物已完成但 CLI 超时或异常退出时，测试仍判失败并保留实际产物。
- 控制器并行写入提示词、读取 CLI 输出，避免长提示词和启动日志同时填满管道造成互相等待；CLI 不读取输入时，超时仍会生效。
- 失败时附带有长度限制、脱敏后的 CLI 最后答复、退出状态及 MCP 调用摘要，便于定位跨平台问题；不包含推理内容、完整模型事件、工具输入或私有配置。
- 模型接口明确报告余额耗尽时，场景标记为 `provider_quota_exhausted`，保留有限的 HTTP 状态和错误代码，不再尝试重连来修复额度问题。CLI 即使返回退出码 0，只要报告宿主错误，也会停止该场景的后续轮次；其他场景仍分别记录结果。
- 生图用例只要求白色小猫、512×512 单张静态图片，不要求动作、不添加字幕或其他文字。验收时，生图服务自动附带的 AI 生成类水印不计为文字。必须发布 generated_image_output 完整图片，不能只发布底图或路径。解码、尺寸与哈希检查不证明视觉质量；generated-base.png 同时保留供人工验收。命令行参数 meme 为兼容旧调用保留名称。

## 定时 main 快照测试

同一托管工作流在每周二、周四北京时间 22:00（UTC `0 14 * * 2,4`）触发。GitHub 排队可能延迟，且工作流必须存在于默认分支。配置模型 Secrets 后，将仓库 Actions Variable `LAZYMIND_AGENT_E2E_SCHEDULE_ENABLED` 设置为 `true` 启用定时运行。各仓库独立设置该变量，普通手动运行可直接选用例。

定时任务读取 main 最新提交，创建 `ci-日期-时间-run_id-attempt` 轻量 tag，再使用同一个 SHA 运行脚本检查和 hosted E2E。每个平台按 Agent 分组执行 PPT、论文、普通生图场景，共 18 项。固定测试的审核由测试控制器处理，失败不会自动重试生图。整个过程从源码构建、启动服务，不生成安装包、不创建 Release。

只有定时任务的打 tag 步骤使用可写的仓库 GITHUB_TOKEN，测试任务只有 `contents: read` 权限。E2E 直接依赖快照和脚本检查，`ci-*` tag 与现有 `v*` 发布规则分开，GITHUB_TOKEN 的 tag push 不触发其他工作流。
