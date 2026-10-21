# cst/product_prs 修复 Review 与测试交接

> 历史审查记录：反映当时提交的问题和测试。后续修复、当前状态及统一测试步骤见 [问题、修复记录与测试总表](product-prs-review-fixes-and-tests.md)。

## Review 范围与结论

基于 `LazyAGI/LazyMind upstream/main@9cbc57c5deb3b5fdded71c6b051041c1bff3aa30` 建立 `cst/product_prs`，导入 `CarlosShaoting/LazyRAG workflow_dev@5b586402a21a638b29f37bec21ed5b0f0491466d` 的增量并修复。保留 main 当前的 LazyLLM 子模块指针 `ab67c1893872fdc2895fc412d226e61a0524c985`。这份记录审查实际行为、重复功能和共享层影响，不按严重程度分级，不以 CI 状态代替验收。

原审查的 R1–R7 均已落实代码修复及针对性回归验证。新增产品交付、图片 V2 和 PPT 功能仍需要下文真实服务端测；本地测试没有证明真实模型输出质量或整个 UI 到供应商链路已经可用。

### AI Writer 必须保持 main 原有行为

原分支对共享 Markdown 编辑器、Slot 文档渲染、面板展开/跟随行为的修改没有作为产品功能一起迁入。以下内容与上述 main 基线保持一致：

- `MarkdownArtifactEditor.tsx/.scss`、`SlotComponents.tsx`、`WorkflowPanel.scss`。
- `frontend/src/modules/chat/store/workflowPanel.ts` 及原有面板测试、Writer Slot 测试和中英文共享翻译。
- 现有 Writer 工作流及 `algorithm/lazymind/document_tools` 文档处理实现。

`WorkflowPanel/index.tsx` 仅在 `workflow_id === 'product_solution_delivery'` 时增加产品项目区、产品展示和下载入口；其他工作流继续走 main 的面板路径。移除了分支新引入的共享展开/跟随 helper 和依赖这些行为的 PPT 配置。

共享 `ArtifactRewriteDialog` 保留了 PPT 多元素选择扩展，但只在 `selection.type === 'ppt_html'` 时允许点击 PPT 选区后保持弹窗打开。Markdown 和 Writer IR 的关闭规则、修订请求及保存机制仍走 main 原有分支。

共享执行器只有产品工作流指定步骤启用产品的轮数、超时和工具限制；普通工作流直接使用原来的事件流。产品阶段读取/导航工具不再注入现有 Writer、图片或 PPT 会话。新增 `launch_user_input/current_user_input` 是附加上下文字段，原 `user_input` 传递逻辑保留。

### R1：上传图片编辑缺少必需源图产出

已用 `publish_edit_contract` 从当前 attempt 的 `remote_inputs.source_image` 读取实际绑定，校验图片和四项编辑约束后发布 `authoritative_image` 与 `edit_contract`。上传绑定优先于模型传入的 URL；多图、缺图、不完整约束均拒绝执行，不能通过模型猜路径或读取另一次运行的最新产物补齐。

两个必需输出共同约束步骤完成。验证使用真实 SubAgent context 和实际图片文件，检查发布图片字节与上传图一致；不是只检查提示词包含字段名。

### R2：产品正文转换损坏代码、URL 与证据事实

已去掉对整个 Markdown 的状态翻译、`null` 替换和外部证据否认。正文原样交给已有 Writer 发布机制，代码块、业务枚举、链接和来源编号不改写。产品提示词也明确要求保留这些内容，定向修订不能顺带重构未选中正文。

产品仍复用现有 Writer 能力；没有新增第二套共享 Markdown 编辑器，也没有改动 Writer 文档保存契约。

### R3：阶段导航替用户批准决定

已删除自动接受逻辑。结束当前阶段和切换到当前阶段修订不会接受任何决定；带未处理硬阻断项进入其他阶段会被拦截。显式暂缓可作为草稿携带，但决定仍为 proposed，不得作为 accepted 基线。

产品界面展示决定原文、依据和完整结构，提供逐项确认/暂缓。请求绑定用户实际看到的 session state version、decision ID 和 proposal hash；版本变化返回冲突后需重新查看，网络重试复用原命令，不能刷新 hash 后偷偷确认新内容。后端保留 owner 检查和幂等处理。

旧 `product-workflow-default` 自动确认不会被重放成用户确认；读取历史 workspace 或继承 seed 时重新开放待确认项，保留审计历史。真实 SQLite repository 回归验证了 finish 持久化、重复命令、旧 hash/错误 owner 拒绝、刷新恢复及提案变化后重新确认。

### R4：普通图片路线忽略搜索和知识库

普通准备步骤已接通已有图片搜索/校验及 KB 查询。实际检索结果进入参考摘要和生成提示词，通过校验的图片发布为 `material_images`。各来源独立执行，KB 失败或无命中不影响 Web 检索，也不丢弃其他来源的成功结果。工具异常、空结果和成功分别记录在 `retrieval_results` 及材料摘要中，不直接升级为准备步骤或整个流程失败。

例如 KB 服务不可用、Web 找到有效图片时，继续使用 Web 图片，摘要如实标明 KB 未取得资料。即使所有来源都失败，仍保留各工具的失败记录，明确没有取得经过验证的外部参考，不把失败改写成“无需外部资料”，也不伪造知识库事实。产物发布/存储本身的错误仍正常上报，不被检索容错吞掉。用户明确取消参考要求时才跳过检索尝试。

V1 的图片校验、搜索和字幕实现提取到安装包公共模块 `image_workflow_support.py`，V1 通过原函数名转出，V2 复用该模块。这样不需要安装包或不可变工作流包里存在另一个工作流源码目录。V1 通用供应商路线保留；V2 是明确的 Seedream 路线，不替代 V1 的既有供应商能力。

### R5：启动需求与本轮指令混用

启动输入与本轮输入分别传递。图片 V2 的纯“继续/确认/重试”不再替代需求；明确修改可覆盖冲突的启动条件。普通图片比例及表情包数量/字幕读取有效需求，准备阶段保存的比例修订在后续确认时继续有效。编辑约束同时保存当时的用户需求，供下一步精确绑定使用。

修改后仅验证图片 V2 使用新增字段；Writer/PPT 等既有 `user_input` 协议不改写。

### R6：Mermaid 分支条件被丢弃

支持的普通流程图保留边标签与回边，独立图使用独立 SVG marker。子图、特殊箭头及 sequence/state 等超出简化渲染器支持的语法，展示明确说明和完整转义源码，不能画出缺失分支的“成功”图。移除了原有几类会丢失结构的替代渲染器。

当前不承诺这些复杂图形都有图形化预览；保证不静默遗漏原文。若必须完整图形化，应另行接入完整 Mermaid 实现并验证。

### R7：独立钳制宽高导致源图变形

改为按同一缩放系数计算宽高，并在供应商尺寸约束和取整后检查比例误差。标准横图、竖图、方图和可表示宽图保持比例；极端比例无法同时满足像素和边长约束时，报错要求用户选择支持的画幅，不静默拉伸或裁剪。

### 与已有功能的重叠处理

| 增量 | main 已有能力 | 本分支处理 |
|---|---|---|
| 产品写作与修订 | Writer 生成、修订、Markdown 编辑、保存与冲突恢复 | 复用原实现；只保留产品编排和项目界面 |
| 图片 V2 搜索、校验、字幕 | 图片 V1 已有这些能力 | 提取公共实现复用；保留 V1 路由和公开函数 |
| 产品任务完成和产物发布 | main 的 SubAgent 完成契约、远程文件发布 | 保留 main 契约；产品发布仅在产品条件下启用 |
| PPT 多元素修改/风格流程 | PPT 原有局部修订、预览与导出 | 在 PPT 内扩展；不带入共享编辑器/跟随模式改写 |
| 产品决定和项目版本 | 本分支新增产品流程 | 显式确认、幂等及版本校验，不以导航替代批准 |

## 交接到同事已有仓库

当前修复分支先保存在本地，未自动推送。维护者先把本分支推到 `CarlosShaoting/LazyRAG` 后，同事在已有本地仓库执行（无需 clone）。确保同事的 `origin` 是自己的 fork，工作区已有改动先自行保存：

```bash
git fetch https://github.com/CarlosShaoting/LazyRAG.git cst/product_prs
git switch -c cst/product_prs FETCH_HEAD
git push -u origin cst/product_prs
```

如果本地同名分支已存在，使用另一个新分支名，避免覆盖已有工作。然后在自己的 fork 上创建到 `LazyAGI/LazyMind:main` 的 PR；也可在登录 GitHub CLI 后执行（替换账号名，若本地改了分支名也同步替换）：

```bash
gh pr create --repo LazyAGI/LazyMind --base main \
  --head 同事的GitHub账号:cst/product_prs \
  --title 'Fix product, image and PPT workflows while preserving Writer behavior' \
  --body-file docs/product-prs-review-and-tests-20260930.md
```

后续修复提交并推送同一分支即可更新该 PR。若 main 又有新提交，需要重新检查共享文件差异和下列行为，不能直接用整个 dev 文件覆盖。

## 测试：已完成的本地行为验证

以下结果来自本地运行，不代表真实模型端测完成：

- R4 来源独立容错修订后，复现回归、图片 V2 工具和工作流契约合计 60 项通过，覆盖 KB 异常/空结果/Web 成功、KB 成功/Web 异常、双方不可用，以及产物存储异常仍正常上报。
- Writer 编辑/源码保真/表格/IR 编辑：4 个前端测试文件、103 项通过；Writer Python 工作流、流式草稿、恢复和源码契约分别 4/41/2/28 项通过。
- 共享 Workflow 面板与 store：147 项通过。共享修订弹窗 24 项通过；Writer Slot 文档和 PPT Slot 的已有测试通过。产品决定界面 3 项通过，覆盖显式确认、暂缓及网络重试复用命令。
- 图片 V2 与本次复现回归最终一组 61 项通过；工作流选择最终 40 项通过（包含 Writer 不暴露产品工具）；旧 V1 搜索、字幕和表情模式测试通过。
- 产品 Python 交付逻辑 39 项通过，交付契约 5 项通过；PPT 脚本 87 项及 10 个子测试、运行时 38 项及 2 个子测试通过。
- Go `workflow/...`、`doc`、`skillv2/...` 通过，包括实际数据库层产品决定/导航测试。远程执行器 25 项、SubAgent runner 95 项、SDK 89 项、toolkit 16 项、full-trust 13 项通过。
- 三个工作流包编译检查、前端生产构建及补丁空白检查通过。它们只证明装配兼容性，不能代替下面端测。

有 Python 测试依赖的已有开发环境中，可分组运行以下命令。各组建议用独立进程，避免历史测试的模块替身污染后续测试；`python` 需指向安装了本仓库依赖的解释器。

```bash
PYTHONPATH=algorithm:algorithm/lazyllm python -m pytest -q tests/algorithm/chat/test_workflow_review_regressions.py tests/algorithm/chat/test_image_workflow_v2_tools.py tests/algorithm/chat/test_image_workflow_v2_baoyu.py
PYTHONPATH=algorithm:algorithm/lazyllm python -m pytest -q tests/algorithm/chat/test_workflow_selection.py
PYTHONPATH=algorithm:algorithm/lazyllm python -m pytest -q tests/algorithm/chat/test_writer_workflow_runtime.py tests/algorithm/chat/test_writer_plugin_draft_stream.py tests/algorithm/chat/test_writer_stream_recovery.py tests/algorithm/chat/test_writer_source_contract.py
PYTHONPATH=algorithm:algorithm/lazyllm python -m pytest -q tests/algorithm/chat/workflows/test_product_solution_delivery.py tests/algorithm/chat/test_product_workflow_delivery_contract.py
```

在 `backend/core` 运行 `go test ./workflow/... ./doc ./skillv2/...`。在 `frontend` 运行：

```bash
node node_modules/vitest/vitest.mjs run --config vitest.workflow-panel.config.ts
node node_modules/vitest/vitest.mjs run src/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor.test.tsx src/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor.real.test.tsx src/modules/chat/components/WorkflowPanel/MarkdownArtifactEditor.table.test.tsx src/modules/chat/components/WorkflowPanel/WriterIRDocumentEditor.test.tsx
node node_modules/vitest/vitest.mjs run src/modules/chat/components/WorkflowPanel/ArtifactRewriteDialog.test.tsx src/modules/chat/components/WorkflowPanel/SlotWriterDocument.test.tsx src/modules/chat/components/WorkflowPanel/ProductProject.test.tsx src/modules/chat/components/WorkflowPanel/ppt/SlotHtmlSlide.test.ts
```

## 测试：合入前必须执行的真实服务端测

准备配置好 LLM、Seedream `ARK_API_KEY`、图片搜索及含品牌资料的知识库的开发实例；构建本分支并同步这三个工作流的新 revision。保留一份升级前 Writer 文档/会话用于恢复验证。记录每项 session、revision、操作步骤、前后文件和预期/实际结果。

| 场景 | 操作 | 通过条件 |
|---|---|---|
| Writer 旧文档 | 打开已有文档，切换可视/源码模式，编辑表格、代码块、链接、引用，再保存刷新 | 正文和格式保真；原先的历史版本、焦点、展开与恢复行为一致 |
| Writer 定向修订 | 选中文字修订；模拟网络失败/版本冲突后重试；在弹窗外点击 | 只改所选范围；原关闭规则和冲突提示保留；未保存修改不丢失 |
| Writer 流式生成 | 从原入口新建文档，生成中打开编辑区，结束后保存并重开 | 原来的流式展示、草稿恢复及完成状态一致；没有产品导航工具干扰 |
| 产品正文 | 生成/定向修订含 `accepted`、`null`、URL `/accepted`、WEB 来源和 Mermaid 的正文 | 业务值、URL、证据事实不被展示层改写；定向修改不破坏未选中内容 |
| 产品决定 | 留一个硬阻断提案未处理，尝试进入下一阶段；确认/暂缓后刷新；结束项目 | 未处理时拦截跨阶段；暂缓仍是草稿；finish 不自动批准；刷新保留明确操作 |
| 产品并发 | 两个页面打开同一提案，另一页修改提案后在旧页确认；重复提交同一命令 | 旧版本明确冲突；不自动批准新提案；重复命令不重复接受/创建阶段 |
| 图片上传编辑 | 上传一张带唯一标记的横图，只要求改帽子颜色 | 编辑使用该图；身份/背景/画幅保留；下载源图不被覆盖；缺图/多图明确失败 |
| 搜索与 KB | 分别制造 KB 失败/Web 成功、KB 成功/Web 失败、双方无结果或异常 | 保留并使用成功来源；失败/空结果分别记录，不直接使准备步骤失败；双方失败时明确无有效参考，不虚构来源事实 |
| 图片需求延续 | 初始 16:9，先“继续”；准备时改 9:16 后再“继续”；修改表情包数量及逐字字幕 | 不回到默认方图；已保存修订仍生效；最终数量/字幕与修改一致 |
| 编辑极端比例 | 普通横/竖/方图和 8:1 源图分别局部编辑 | 普通比例不变形；不支持比例明确拒绝；用户指定支持的比例后可运行 |
| Mermaid | 有是/否分支和回边的流程图；另测 subgraph、sequence loop、虚线箭头 | 支持图保留条件；不支持语法完整源码可见，无缺边假图 |
| PPT | 选风格生成，取消/重新选择多元素后局部修订，预览并导出 | 不误关 PPT 选择弹窗；修改范围正确；未选页/元素保留；导出可打开 |
| 旧图片工作流 | 明确选择原 `image-workflow`，用原配置生成并添加字幕 | 原供应商路线和字幕逻辑仍可用，未被 V2 自动替代 |

真实供应商调用、完整浏览器端到端操作和真实导出文件的人工验收尚未执行。上述项目全部完成并记录结果后，再判断是否可合入主仓库。
