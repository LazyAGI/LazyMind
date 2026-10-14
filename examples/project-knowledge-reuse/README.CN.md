# 复用项目知识库，连续完成文档任务

[English](README.md) · [完整提示词](prompts.CN.md) · [验证记录与判据](verification.md)

**当前状态：已有 Codex CLI / DSH headless 真实输出和工具记录，尚未整体验收。** 来源链接、取证记录准确性和独立复现仍待验收。查看[本轮实跑与问题记录](runs/2026-10-14/README.md)，不把文件生成当作验收通过。

这个案例复用仓库已有的“知策”产品方案、PRD、评审和交接材料。用户在熟悉的 Codex 或 DeepSeek Harness（DSH）中，通过 LazyMind 插件使用项目知识库；LazyMind 负责检索与原文读取，外部 Agent 负责写作和保存文件。不运行产品方案 Workflow，不新增研究系统。

本例重点是**跨 Agent、跨会话复用同一批项目资料**：在 LazyMind 准备一次知识库后，Codex 和 DSH 都通过插件检索、读取同一套原文，在追问、局部修改和新的空白会话中保留依据与未决状态。展示成果由各宿主独立写作，不靠复制另一段聊天的摘要。本地文件同样可以复用；保留[对照记录](verification.md#comparison--对照)，不宣称插件必然更快或操作更少。

## 输入与来源

| 固定输入 | 原始仓库案例 | 文档自身的状态 |
| --- | --- | --- |
| [design.txt](inputs/design.txt) | [产品方案 HTML](../../skills/featured/product_solution_delivery/assets/design.html) | v1.0，方案待确认 |
| [prd.txt](inputs/prd.txt) | [PRD HTML](../../skills/featured/product_solution_delivery/assets/prd.html) | v1.0，示例草稿，规则待确认 |
| [review.txt](inputs/review.txt) | [评审 HTML](../../skills/featured/product_solution_delivery/assets/review.html) | v2.0，退回修改，R-01–R-05 未解决 |
| [handoff.txt](inputs/handoff.txt) | [交接 HTML](../../skills/featured/product_solution_delivery/assets/handoff.html) | v2.0，draft，engineering_readiness: blocked |

来源版本和每份 HTML 的 SHA-256 见 [sources.json](sources.json)。原始案例提示词见 [featured.yaml](../../skills/featured/product_solution_delivery/featured.yaml)，原 Workflow 的适配来源见 [SOURCE.md](../../workflows/product_solution_delivery/SOURCE.md)。本例的连续任务提示词是为复现新增的测试指令，不冒充原宣传演示的逐字记录。

知策是虚构产品。这些展示稿不是已批准需求、真实实现或已完成测试。文中提到的其他材料（例如原型、版本清单）不在本次四文件输入包内，不能宣称读取过。保留文档间的状态差异和未决问题，不把它们拼成“已经通过”的项目基线。

输入为原文提取：保留正文、章节锚点、链接、表格单元格与图中文字，省略视觉布局、导航和样式；完整视觉内容以原始 HTML 为准。以下命令只需 Python 3 标准库，在仓库根目录执行：

```sh
python3 examples/project-knowledge-reuse/prepare_inputs.py --check
# 需要重建相同的输入文件时：
python3 examples/project-knowledge-reuse/prepare_inputs.py
```

源文件校验和变化时脚本会停止，不能静默更新本例版本。升级输入应先审阅变化，再显式更新清单、快照和验收依据。

## 首次准备

1. 启动并登录自己的 LazyMind，记录实际部署版本及模型。仓库 HEAD 不能代替运行版本；本地开发验证仅使用隔离的 8091。
2. 按 [Codex 接入说明](../../integrations/codex-workflow/README.md)或 [DSH 接入说明](../../integrations/dsh-workflow/README.md)，在 LazyMind「设置 → 外部 Agent 集成」连接所选宿主。记录安装、配对、授权和必要重启成本。若已有连接指向其他实例，先使用独立配置或核实切换影响。
3. 在 LazyMind 创建专用知识库 `showcase-zhice-v1`，**只上传 `inputs/` 中的四个 TXT 文件**，等待实际解析/索引完成。不上传提示词、验收判据或后续生成的输出。
4. 在外部 Agent 新建会话，用插件列出知识库和文档，确认四个文件均可读取。记下实际知识库 ID 和文档 ID。同名知识库不够精确；[提示词](prompts.CN.md)中的占位符必须替换为工具实际返回的 ID。
5. 让 Agent 通过 `knowledge.search` 检索，再通过 `knowledge.document.get` 请求正文或解析切片，读取相关原文。检查截断标记及分页，需要时继续读取；摘要不能替代原文。保留真实工具调用证据；只有连接成功或文件上传成功不算检索验证。

## 连续任务

按 [完整提示词](prompts.CN.md)顺序操作，每步检查并保存成果后再继续。

| 步骤 | 操作与交付 | 需要观察什么 |
| --- | --- | --- |
| A | 生成方案摘要 `01-summary.md` | 有来源、区分案例设定和已验证事实 |
| B | 追问 R-05，生成 `02-scope-evidence.md` | 找到方案与 PRD 原文，准确解释启动前/后的区别 |
| C | 生成局部修改建议 `03-design-revision.md` | 只改指定段落，记录依据，不替用户批准规则或关闭评审项 |
| D | **新建空会话**，生成 `04-handoff.md` | 只给知识库标识，重新检索四份原始材料，找回 R-01–R-05 和关闭条件 |

步骤 D 不上传 A–C 的成果、不粘贴上轮对话、不自动沿用宿主记忆。记录宿主是否带入项目说明或持久记忆。步骤 C 的建议没有回写知识库，所以 D 应说明当前源文件仍未更新；这是资料复用验证，不是自动同步编辑验证。

输出使用 Agent 正常的本地文件写作能力保存。逐个打开文件、核对引用，并确认用户能获取成果。引用至少包含输入文件名、章节或 R/FR/AC 编号、实际读取的文档 ID；工具提供可打开的来源链接时一并保留，不编造 URL。核查依据时必须能在原文找到对应内容。

## 验收和限制

Codex、DSH 必须分开实跑并记录环境；一种宿主通过不代表另一种通过。随后由未参与实现的人只按本目录说明复现。完整要求与尚未完成的记录见 [verification.md](verification.md)。通过后再保存脱敏实跑输出、补录演示和宣传结论；不预填成功结果或效率数字。

本例只有四份中文静态材料，不证明大规模检索能力、索引实时更新、写回版本管理或生产就绪。首次建库、索引、插件安装和人工核查均有成本；实跑表明直接读取同一目录也能复用这些资料，因此本例不宣传比本地文件更省操作。
