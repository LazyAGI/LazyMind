# 论文算法输入输出接口规范

**临时开发交接资料：整个 temp/ 目录须在提 PR 前删除，不作为正式产品文档。**

日期：2026-10-15。用途：给算法负责人独立设计、开发、自测，以及给骨架负责人实现统一适配和校验。

**状态：下文是目标 v2 规范，不是当前代码能力说明。** 当前公共入口和仓库内 fixtures 仍使用 v1。2026-10-19 已确认：各模块先基于代码和文档开发，联调时统一差异，不把全局 v2 迁移作为开工前置条件。当前字段以 [contracts.py](../../../algorithm/lazymind/document_tools/paper/contracts.py) 为准；返回结构变更必须同步调用方，不能直接把对象返回给仍要求字符串的骨架。

**F6 当前实现（2026-10-19）：** 已接真实模型调用，一次生成 `ReviewResult`，返回 `review: str`（Markdown）、`review_data: dict`（结构化意见）、`is_demo: bool` 共三个字段。提示词在 `algorithm/lazymind/document_tools/paper/prompts/peer_review_prompt.py`，入口为 `paper/peer_review.py`，共享类型在 `paper/review/schema.py`。F6 调用测试使用真实模型，不用 monkeypatch；格式与排版测试直接调用函数。下文的完整 v2 Review / ContentRef 尚未接入。当前完整样例见 [F6 运行样例](../../../tests/fixtures/paper-writing/peer_review.json)，model_response/output 只是格式示例；不要直接传入 temp/examples 下的目标 v2 对象。

## 当前实验接口

2026-10-26 补充：模型生成字段为 `code: str`、`config: dict`、`dependencies: list[str]`，以及原有方案、讲解、执行需求和预览字段；固定四个文件由 `experiment/generation.py` 组装，README 复用 explanation，标准库实验显式返回空依赖列表。公共返回的 `code_files`、执行器和 F4 交接格式不变。下方 examples 的 `model_response` 是打包后的内部 ExperimentDraft 样例，不再是原始模型响应；公共 input/output 样例仍有效。每次 F3 至多一次局部模型修复：设计字段修复或模拟预览修复二选一，修复后再次校验失败直接报错，不整步重生成。

流程补充：`文献综述 → 调整选题 → 实验研究` 是合法前向路径。实验节点接收 refine_topic_pool/document，复用现有 sequence 选择最新材料池，保留原论文与综述；初次研究选题仍需经过综述。调整选题后如需查文献，仍走已有原生 rewind。

2026-10-22：F3 一次生成方案、代码、执行需求和模拟预览；程序根据本次许可、依赖和 30 秒预算决定是否复用现有受限执行器运行。复杂/未允许/执行失败时交付标记来源的预览，实际成功时使用计算结果。入口 `api.run_algorithm('experiment', inputs, allow_execution=False)`，Runtime 根据 ask_user 回答传入关键字参数；业务 inputs 中不能自行增加授权字段。完整样例见 [examples/experiment.json](examples/experiment.json)，与 tests/fixtures/paper-writing/experiment.json 相同。

目录：F3 内部在 `paper/experiment/`，公共入口为 `run(inputs, *, on_event=None, allow_execution=False)`；executor/worker 接现有 paper/execution，handoff 转换新 F4 输入并保留来源。英文 Prompt 仍在统一的 paper/prompts。生成、预览修正、执行分别有阶段状态，执行不增加模型调用。

| 输入字段 | 类型与要求 |
| --- | --- |
| topic | 必填非空 string |
| request | 可选 string，默认空串 |
| question | 必填非空 string，来自 F1 或用户明确的实验问题 |
| evidence | 可选 string/null，累积的 F2 综述与依据，缺失必须说明 |
| data | 可选 {columns:string[], rows:JSON标量[][]}/null；列名唯一、行列数一致、拒绝 NaN/Infinity |

只接受上述五个字段，不接受任意额外参数。输入不被修改；超出 16000 token 的输入直接报错，不悄悄截断。算法不读取 pool、附件路径或授权记录。

| 输出字段 | 类型与要求 |
| --- | --- |
| experiment | executed:bool、plan:string、results:{columns,rows}/null、report:string；executed 由实际执行回执设置；report 为 Markdown |
| code_files | 恰好四项 {path:string, content:string}：experiment.py、config.json、requirements.txt、README.md；不允许其他路径/重复文件；代码必须声明字面量 RESULT_COLUMNS 并定义 run_experiment(config, input_data)；配置必须 JSON 对象；总计最多 200 KB |
| result_meta | schema_version:1、execution_status、data_origin、method、reason:string/null、assumptions:string[]、metrics:{column,description,unit:string/null}[]、seed:int/null、column_types:{列名:类型}；execution_log:string、elapsed_seconds:number 为实际执行记录 |
| is_demo | synthetic/none 为 true；实际仿真仍是 synthetic，须结合 execution_status 区分实际计算和模型示例 |

execution_status 为 not_run/succeeded/failed/timeout；只有 succeeded 对应 executed=true。未运行/失败须说明原因；method 为 empirical/simulation。模型预览最多 200 行/30 列，实际结果最多 10 万行/100 万单元格；流式最终结果只预览前 20 行，下载与 F4 使用全部数据。只要真实结果时不生成兜底预览，实际执行失败后 F4 提示缺少结果。

生成内部增加 execution_requirements={device:cpu/gpu,estimated_seconds:int,requires_network:bool,requires_external_data:bool,reason:string}，及独立 metrics 定义。方案与代码仍一次生成，需求只用于程序判断，实际超时和访问边界由执行器落实。

首次模型输出使用 results.dimensions（column/values）及 results.metrics（column/description/unit/数值 values）；程序从同一组列组装对外 columns/rows 和 result_meta.metrics。内部格式见样例 model_response，不是传给 F4 的格式。

模拟预览的列名、行数、类型或结果元数据不合格时：先用 ExperimentDesign 独立验证方案、说明和代码；这些字段也不合格则立即失败。只有预览/元数据错误才额外调用一次模型，基于代码 AST 读取的 RESULT_COLUMNS 动态建立逐行对象 schema，每行必须含全部且仅含声明列，类型严格、指标名限声明中的数值列。局部返回 rows/metrics/assumptions/seed/method；已有合法 seed（包括 null）及 method 由 schema 固定，不可变更。转换回既有 Preview/F4 格式后再次全量校验，方案和代码不变，模拟假设同步更新。不补空列、不截断数组、不按位置强改列名；修正再次失败即停止。局部输入含 schema 最多 8000 token、结果最多 20 行；不自动全文重试。公开输入输出、文件名和下游契约不变。

代码与结果约束（2026-10-21 收尾）：

- experiment.py 顶层声明一次 `RESULT_COLUMNS = {'trial': 'integer', 'method': 'string', 'absolute_error': 'number'}`。列名非空、无首尾空格且唯一，最多 30 列；类型仅接受 string/integer/number/boolean。代码只定义同步 `run_experiment(config, input_data)`，恰好两个位置参数，无默认值、装饰器、可变参数。平台通过 AST 读取声明和签名，不导入生成代码。
- 函数返回非空的 `list[dict]`，每条记录恰好包含声明的字段。`config` 是配置对象，`input_data` 是 null 或 `{columns, rows}`，CSV 单元格为字符串，由算法按需求显式解析。记录不接受 null、数字字符串、NaN/Infinity；boolean 不作为 integer/number。
- 模拟预览的列名及每个单元格类型必须匹配声明；内部按列保存的预览会按 RESULT_COLUMNS 重排列及对应值，不因列顺序不同而失败，也不会只换列名。指标必须为 integer/number，描述和单位不能是空白（无单位用 null）。无预览时保留计划中的列声明，metrics=[]、data_origin=none。
- 模型生成对象的多余字段在 ExperimentDraft 入口清理（含代码文件、预览维度和指标对象）；有预览时忽略不适用的 no_results_reason，无结果时仍要求非空原因。必填字段、类型、文件白名单及代码签名仍严格检查；对外接口和用户输入不会因此放宽。解析与清理均为本地程序，不调用模型全文重试。
- 打包层追加公共 `run_experiment.py` 导出器及带具体列名/顺序/类型的 `result_schema.json`。用户检查后运行 `python run_experiment.py`；导出器按声明排列记录，全部通过校验后生成一致的 results.json/results.csv。字段错误会失败，不会把错误记录自动改名或转成数字。
- F4 入口重新核对结果表、column_types、指标及来源；两份不同的 data/experiment 必须先明确选择数据源，不能静默混用。column_types 是对象，读取时不依赖其 JSON 键顺序；真实列顺序由结果的 columns 数组确定。旧材料无 column_types 时兼容既有表格/数值指标检查，新 F3 不允许省略。
- 此约束保证结构和类型；任意研究代码的科学正确性仍需具体样例核验，格式合法不等于方法或结论已验证。

运行层：

- 复用 runtime.clarification_fields → ask_user；仅明确允许本次服务容器内运行后绑定 allow_local，未给或无效按 simulate_only。Runtime 读取绑定后传 allow_execution，模型输出不能设置 executed。执行位置为当前 LazyMind Linux 服务容器内的受限子进程。
- 独立 F3 入口使用 experiment_question 文本槽，不要求用户编写 JSON；缺少目标先补问。经过 F1/F2 时使用当前 question/evidence，不覆盖已确认材料。
- 发布实验研究.md、实验代码.zip、实验结果.json 及内部材料池。成功执行的 ZIP 包含 results.json/csv；未执行或失败降级使用 preview_results.json/csv。所有文件使用同一最终结果，执行日志有内容时打包 execution.log。
- F3 使用私有流式回调：阶段状态及模型公开的 reasoning 输出（若提供）进入右侧子任务；方案、代码、模拟结果经既有 artifact_stream 协议进入左侧对应卡片，每个文本 delta 恰好一个 Unicode 字符，由既有 Writer 前端逐帧显示，不再以 512 字块显示。若进入局部修正，更新阶段状态，仅转发 reasoning，不将另一种 schema 的 JSON 追加到首次草稿；校验后替换结果预览。生成中的预览不当作已校验产物；全部校验和保存成功后才发布最终文件，失败会作废预览。原始 JSON 不直接打印到 Chat。
- 任务 REST 刷新不能清空该任务已接收的瞬时预览；前端按 stream_id/chunk_index 保留最新内容。浏览器整页重载时仍依赖现有流重放/最终文件，不将生成中的不完整 JSON 当正式数据。算法函数签名仍为 run(inputs)，可选 on_event 是运行适配层的回调，不要求实习生处理 SSE 或材料池。
- ZIP 包含四个算法文件、公共 run_experiment.py、结果 Schema、来源说明、固定未执行警告；有数据时附 input.csv，有模拟结果时附 preview_results.json/csv。预览 CSV 防公式注入；原始 input.csv 保留输入值。不会把业务环境变量、凭证、整个材料池打包。
- F3→F4：handoff.to_analysis 将结果转为现有 F4.data={columns,column_types,units,rows}，类型映射按列排序，单位按指标匹配；方案/假设/来源写入 data_context。result_meta 留在材料池，不作为 F4 未声明字段透传。F4 现有真实算法执行统计与绘图；产出 limitations/caption 确定性保留来源，再沿用结构化 analysis/charts 交给 F5/F6。
- 模拟来源也记入原有材料版本链，重新导入带 synthetic 标记的业务输入仍保留演示属性。不另建授权系统，不修改通用 Chat。

### F4 内部统计计划恢复（2026-10-26）

- 公共 `run(inputs)`、`analysis`、`charts` 与 F3/F5 交接格式不变。仅模型规划响应不再包含 `jobs[].id`，图表使用 `finding_indices: int[]`（从0开始的任务序号）；`analysis/job_identity.py` 分配规范 ID、合并完全相同的任务，转换为内部既有 `finding_ids`。后续代码、结果校验和发布都使用规范 ID。
- 旧模型响应中的临时 ID 仍兼容；同名且计算相同直接合并，同名但计算不同不能猜测图表归属。此时最多一次仅修正图表任务序号的模型调用，不重生成整份分析计划。单位及证据正文仍由来源绑定，不因图表局部修复而改名。
- 均值/独立或配对差值的区间、t 检验遇到样本不足或零方差时，保留同一观测范围的描述性点估计，`interval/test` 为 null；限制写入 `analysis.limitations` 与受影响图表说明。不能编造噪声、误差条、p值，也不能说“无显著差异”。仍核验数据来源、缺失值、配对和数值；不把坏数据或不可估计的其他模型一概包装成成功。
- 代码生成输入显式保留 `interval_level=null` 和 `correlation_test=false`，禁止模型默认补95%区间。运行结果中多算的未请求区间/检验由程序置空，保留原始结果及规范化结果用于追溯；仍严格复核点估计、已请求推断、任务集合和字段结构。不会拿独立复核器的答案替代实际计算。

### 当前审稿到修订的交接

| 字段 | 类型 | 用途 |
| --- | --- | --- |
| review | string | 前端写作控件显示的 Markdown，仍可编辑 |
| review_data.summary | string | 总体评价 |
| review_data.issues | object[] | 按严重程度排序，和 Markdown 中 R1、R2 的编号对应；允许无问题 |
| review_data.limitations | string[] | 本次审查限制，包含缺少证据或分析材料的说明 |
| review_data.manuscript_sha256 | string | 被审稿正文的 SHA-256，64 位小写十六进制 |
| review_data.review_sha256 | string | 对应 Markdown 的 SHA-256，用于识别编辑后的不一致 |

每条 issue 固定字段：`id`（R1 等，唯一）、`severity`（major/minor/suggestion）、`category`（method/evidence/analysis/structure/language/citation）、`location`（非空章节说明）、`quote`（原文或 null）、`problem`、`suggestion`（非空文本）、`action_type`（text_revision/needs_material/needs_experiment）。拒绝未知字段和错误类型；quote 非空时必须出现在原稿。

- 摘录仅允许连续空白/换行差异；匹配成功后保存稿件中的实际片段，不放宽字词、数字或拼接引用。找不到时与格式错误合并为一次局部修复，由模型选择原文起止行号、代码复制原文；不重新生成整份报告，修复失败不发布产物。模型摘录错误不应误报为缺少论文文件。

- F6 保存 `模拟审稿.md` 和 `审稿意见.json`；结构化内容也随材料池保存。前端正文仍只展示 Markdown，不新增步骤或 JSON 页签。
- F5 修订函数直接收到 `inputs['review_data']`，不用读文件、材料池或解析 Markdown。需要材料/实验的问题不能靠生成文字冒充完成。
- 修订前同时检查稿件、报告摘要；任意一份已编辑则报“结构化意见已过期，请重新审稿后再修订”。本期不做 Markdown 反解析、自动同步或单条勾选。
- 历史 Markdown-only 输入仍兼容，`review_data` 默认 null；新 F6 输出必须包含结构化意见。旧固定执行图仍将结构化内容保存在原有材料池里，不强行增加未声明的文件槽。
- 目前 F5 仍为演示算法：本次只接通结构化交接，不代表已实现真实修改。

## 一 开发者先看这张表

### 当前文件入口（2026-10-19）

- 新执行图没有 prepare 节点。用户上传论文后，Chat 将准确附件名绑定到 manuscript_file，直接选择 peer_review；Runtime 在调用算法前执行 initialize_pool。不要让用户提供业务 JSON 或 pool.json。
- 可选入口：manuscript_text（直接正文）、paper_topic（主题）、review_request（审查要求）。paper_input 只兼容现有程序化输入，不是材料池文件；文件与正文同时提供时拒绝冲突版本。
- entry.read_manuscript 复用 LazyLLM PDFReader/DocxReader，支持文本型 PDF、DOCX、UTF-8 Markdown/TXT；20 MB 文件、20 万字符限制，绝不静默截断。PDF 有页面提取不到文字时要求补可解析稿件，不承诺扫描件 OCR、图像/公式理解。
- 后续节点优先读取已经确认的材料池与编辑产物，不重新加载原附件覆盖新稿。算法仍只接收普通业务字段，不接触文件。
- 新图里“调整选题”配置 hide_when_step_skipped: literature_review；综述分支跳过时隐藏该未执行页签。下游论文修订仍展示；已执行结果及回退后的恢复不受影响。
- Core 在首次选择入口时补齐入口分支记录；直接选 peer_review 后，前六个步骤和阅读心得隐藏，只保留模拟审稿与下游论文修订。没有入口记录的旧失败会话在重试时也会补齐。
- 旧固定版本会话的 prepare 调用仍兼容，但新配置没有该步骤、页签或清单产物。配置/前端修改需部署后验证。

下文仍是 v2 目标设计，不要据此改变当前算法接口。

算法函数统一为 `run(inputs: dict) -> dict`，只有 **1 个位置参数、1 个返回对象**，不增加 `*args`、`**kwargs`、路径或 pool 参数。表中的字段数是归一化后的对象键数，不是 Python 函数参数个数。

所有输入都有 `topic / request / constraints` 三个公共字段；所有输出都有 `is_demo`。下表的数量已包含这些公共字段。

| 步骤 | 所属算法文件 | 输入字段数 | 输出字段数 | 输出业务字段 |
| --- | --- | ---: | ---: | --- |
| 研究选题 | research_topic.py | 7 | 2 | question |
| 调整选题 | research_topic.py | 7 | 2 | question |
| 文献综述 | literature_review.py | 8 | 3 | evidence、papers |
| 实验研究 P1 | experiment/__init__.py | 8 | 2 | experiment |
| 数据分析 | data_analysis.py | 5 | 3 | analysis、charts |
| 论文写作 | write_document.py | 13 | 3 | outline、manuscript |
| 论文修订 | write_document.py | 13 | 3 | outline、manuscript |
| 模拟审稿 | peer_review.py | 7 | 2 | review |
| 阅读心得 | reading_reflection.py | 5 | 2 | reflection |

路径均位于 `LazyMind/algorithm/lazymind/document_tools/paper/`。选题和写作各有两种 mode，不新增两个算法包。插件 `scripts/tools.py` 仅调用共享 Runtime。

### 附件及阅读顺序

1. 本文：字段含义、跨字段规则、错误行为、边界职责。
2. [九步完整样例目录](examples/)：每一步都给出所有字段，不用省略号代替真实 JSON。根对象 `{step,input,output}` 只是测试样例包装，算法实际只收 `input`、只返回 `output`。
3. [可复用能力清单](reused.md)：优先复用的现有函数及需要适配的边界。

本文的字段定义与跨字段规则必须一起实现：结构校验检查字段集合、类型和数量；跨字段一致性、内容摘要、引用存在性、安全与语义前置条件由 Python 校验函数完成。不能以“Schema 验证成功”代替全部校验。

## 二 尽量复用现有格式

| 内容 | 已有来源 | 本规范的处理 |
| --- | --- | --- |
| 文稿与大纲 | document_tools/writing.py 支持 Markdown，内部复用 WriterDocument / WriterBlock | 算法边界继续用 Markdown，不再另造章节树；内部调用 Writer 时沿用已有 IR 转换 |
| 审稿定位 | lazyllm.tools.writer.data_models.writer_ir.ContentRef | 直接复用字段名和定位语义；本接口采用明确的 Markdown 定位子集 |
| 文件和知识库资源 | lazyllm.tools.writer.data_models.task.InputResource | 接入层复用，不把任意路径直接传入算法；解析后转业务对象 |
| 论文检索结果 | search/base.py 的 title/url/snippet/source/extra 和 get_content 的 content | 统一映射为 Paper；保留作者、年份、DOI 和内容覆盖范围，不把 snippet 假装成全文 |
| 写作要求 | 旧论文 pipeline_tools.normalize_academic_parameters | 复用 paper_language、paper_type、citation_style 等名字与枚举，不照搬六项全部必填 |
| 表格数据 | 当前 paper/contracts.py 的 columns/rows | 保留二维表结构，补充同序 column_types、units；不在算法边界引入 DataFrame |
| 产物保存 | document_tools.artifacts.persist_artifact_json、现有 save_artifacts | 仅骨架使用；标准 JSON envelope 不混进算法输入输出 |

旧论文流程中的 chapters/children 是另一种大纲适配格式，不作为新接口第三种并列入参。需要复用旧校验规则时，在 F5 内转换到该格式后调用；不能让调用方猜传 Markdown、Writer IR 还是旧大纲 JSON。

**新增部分**：Question、Evidence、结构化 Analysis、Review、Reflection，以及表格列描述。这些不是声称仓库已有标准，而是本轮为明确算法交接补充的业务结构。

## 三 所有算法共同遵守的规则

### 参数和默认值

| 字段 | 类型 | 规则 |
| --- | --- | --- |
| topic | string | 去除首尾空白后非空；入口缺失时补充，不用文件名擅自猜 |
| request | string | 本轮要求，缺省归一化为 `""`；不是启动时旧要求 |
| constraints | Constraints | 固定 5 个字段，见下表 |
| is_demo | boolean | 所有成功输出必填；只取 true/false，不接收 0、1、字符串 |

Constraints：

| 字段 | 类型 | 缺省及限制 |
| --- | --- | --- |
| paper_language | `"zh-CN" | "en"` | 初始默认 zh-CN；入口已有明确选择时沿用 |
| word_target | positive integer 或 null | 默认 null，表示无硬性篇幅；整数不包含 bool |
| paper_type | enum 或 null | 默认 null；枚举 research / empirical / literature_review / theoretical / case_study / conference |
| citation_style | enum | 默认 GB/T 7714；可选 APA 7、Chicago Author-Date、MLA 9、IEEE、Vancouver、GB/T 7714 |
| focus | string[] | 默认 []；每项非空，传递用户明确的重点 |

以上语言和引用风格默认值是本次建议的开发默认，不是 PRD 已确认要求。产品入口可显式覆盖，不能覆盖用户已确认的选择。其他优先级档位不凭空新增；暂将具体要求保留在 request/focus，产品定档后统一扩展契约。

通用规则：

- 归一化后所有声明字段都存在；缺失可选对象变 null，缺失列表变 []。必填业务材料不能靠默认空对象蒙混通过。
- 不接受未知字段；输出严格等于声明集合。不要额外返回 pool、path、next、status、文件名或工具调用记录。
- 字符串不自动转数字；`"false"` 不转 bool。只有接入层经明确解析规则可以做转换，并保留转换记录。
- 不修改 inputs；输出是新对象。失败抛异常，不返回“抱歉”充当成功正文，不把部分结果写回共享材料。
- JSON 禁止 NaN/Infinity；日期采用严格 ISO 日期，时间采用带时区的 RFC3339 字符串，不能用自由自然语言日期。
- 模型与服务授权沿用平台。算法可调用现有受控检索、模型和执行能力，但不得自行读取任意路径、绕过权限或创建公共产物。
- 演示标识由骨架向下游传播；包含演示材料时不能仅凭算法返回 false 就洗成真实结果。
- 本文的初始容量保护建议：最多 100 篇论文、单篇正文 100 万字符、总论文正文 2000 万字符；表格最多 10 万行、200 列且不超过 100 万单元格；最多 20 张图、单图 SVG UTF-8 不超过 2 MiB；单份 Markdown 不超过 200 万字符。超限返回 INPUT_TOO_LARGE，不静默截断。这些是待实现工程限制，不是已验证性能承诺。

### 原始资源如何转换

接入层先用已有 InputResource 表达 file/url/kb/text 等资源，再完成授权读取、解析、去重和格式转换。算法只接收本文的业务对象，不接收原始 PDF、Excel 路径、未解析的实验日志或 Writer JSON envelope。

| 原始内容 | 接入层交付给算法的格式 | 不能确定时 |
| --- | --- | --- |
| PDF／知识库论文 | Paper，明确全文、节选或摘要及是否截断 | 无正文时报告解析失败；不能捏造正文 |
| 检索结果 | title/url/source/extra 映射元信息；get_content 的 content 映射 text | 只有搜索预览时不得进入证据集合；继续获取或返回证据不足 |
| CSV／Excel | DataTable，确定表头、列类型、单位 | 多工作表未选择、重复表头、混合类型等要求明确选择或修复 |
| 实验服务结果 | Experiment，results 是 DataTable | 原始日志不是结果表，服务适配器必须明确转换，否则拒绝 |
| 已有大纲／稿件 | Markdown string | 原始文件先解析，不能把文件路径当正文 |
| 外部自由文本审稿意见 | Review，必须锚定当前稿件，生成稳定问题 ID | 无法定位时允许文档级意见；不能伪造章节或引文位置 |

原始数据适配是单独的骨架／接入任务，不由七位算法开发者各自重复实现。结构化已有材料也要校验，不能因为来自知识库就跳过格式检查。

## 四 公共业务对象

本节所有对象字段均需存在，只有显式标注 null 的字段可为空。数组最小长度见描述；未规定最小长度的数组允许 []。字段集合以本节及第五节为准，完整实例见 examples/。

### Paper 论文对象

固定 10 个字段：

| 字段 | 类型 | 约束 |
| --- | --- | --- |
| id | string | 非空；优先保留来源系统稳定 ID，同一调用内唯一；没有时接入层分配，不由模型随意改号 |
| title | string | 非空标题 |
| text | string | 非空已解析文本，不是路径 |
| url | string 或 null | 有值时是 HTTP(S) 来源地址，不允许 file/javascript 等协议 |
| authors | string[] | 每项非空；未知用 []，不编造 |
| year | positive integer 或 null | 未知用 null；不填 0 |
| doi | string 或 null | 去掉 doi.org URL 前缀后保存 DOI；未知用 null |
| content_type | fulltext / excerpt / abstract | 区分证据覆盖范围；search_preview 不允许作为 Paper |
| source | string | 真实来源标识，例如 upload、kb、arxiv、semantic_scholar |
| truncated | boolean | 内容窗口被截断时必须 true；不能仅因为 content_type 是 excerpt 就隐去截断事实 |

检索 extra.authors / year / DOI 对应上述字段；Arxiv extra.published 只在合法日期可确定时提取年份。检索 snippet 只是候选预览；get_content 返回 abstract 就按 abstract，不能改成 fulltext。原始 InputResource、授权、文件定位关系留在接入层，Paper.id 与来源映射保持稳定。

相同 id 但正文或身份冲突时拒绝，不能后者覆盖前者。DOI 相同可由接入层明确合并并维护 ID 映射；不同标题不能仅凭字符串相似就自动合并。

### DataTable 表格对象

固定 4 个字段：

| 字段 | 类型 | 约束 |
| --- | --- | --- |
| columns | string[] | 至少 1 列，列名非空且唯一 |
| column_types | enum[] | 与 columns 等长且同序；string / integer / number / boolean / date / datetime |
| units | `(string 或 null)[]` | 与 columns 等长且同序；未知或无单位为 null，不猜测 |
| rows | 二维数组 | 至少 1 行；每行严格等于列数 |

单元格规则：null 表示缺失；非 null 必须符合所在列类型。integer 不允许 bool；number 只允许有限 int/float，不允许 bool；date 必须为合法 YYYY-MM-DD；datetime 必须含时区。全空列不能自动推断类型，必须由入口声明。数字列里的 `"75"` 不在核心算法调用前偷偷转换，接入层只有明确列类型/解析规则时才能转换。

旧 v1 `{columns,rows}` 可以由兼容适配器补列类型和 units；只有全列类型一致、可无歧义判断时才推断，否则返回 INVALID_INPUT。空值不填 0，重复行不自行去重，缺失行不自行删除，单位不自动换算。

### Question 研究问题

固定 3 字段：`summary: 非空字符串`、`candidates: Candidate[1..10]`、`selected_id: string|null`。

Candidate 固定 6 字段：`id / question / innovation / rationale / validation_plan` 均为非空字符串；`source_refs` 是不重复的 Paper.id 数组，可为空。候选 id 唯一；selected_id 非空时必须命中一个候选。create 默认不替用户选题，selected_id=null；refine 可保留用户已选且仍存在的 ID。

创新点必须描述为候选假设；无文献支撑时不能标成已证明的新颖性。形式校验只能查字段和引用存在性，不能证明创新真实。

### Evidence 综述证据

固定 4 字段：`summary: 非空字符串`、`items: EvidenceItem[1..]`、`gaps: string[]`、`limitations: string[]`。

EvidenceItem 固定 6 字段：`id / paper_id / claim / quote` 为非空字符串；`locator: string|null`；`relation: support|contrast|background`。

每个 paper_id 必须存在于同行交付的 papers；证据 id 唯一；quote 必须是该 Paper.text 的原文片段（仅允许统一换行和连续空白后比对，不能用改写句冒充原文）。页码只有来源可核实时才填，否则 locator=null。摘要或截断材料的局限必须保留。检验 quote 存在不代表验证 claim 一定被支持，后者是算法质量验收。

### Experiment 实验结果

固定 6 字段：`executed: bool`、`plan: 非空字符串`、`results: DataTable|null`、`report: 非空 Markdown`、`code: 非空字符串|null`、`logs: 非空字符串|null`。

- plan 模式：executed=false，results=null，logs=null；可以返回建议代码，但不能声称执行。
- execute 模式：只有实际受控执行成功才返回 executed=true；results 和 logs 必须非空。失败抛异常，不伪装 plan 成功。代码与日志需脱敏，不含密钥、令牌。
- 本期不是通用二进制实验产物协议：返回代码/日志文本，骨架保存；大型模型文件等仍由执行服务的现有资源能力管理，不能塞 Base64。
- Mock 的成功执行样例只用于格式测试、is_demo=true；不得把虚构 executed=true 用于真实运行展示。模拟算法不能执行时应返回 plan 结果，后续数据分析需要另供数据。

### Analysis 分析结论

固定 6 字段：`data_sha256: 64 位小写十六进制摘要`、`summary: 非空字符串`、`row_count: positive integer`、`methods: string[1..]`、`findings: Finding[1..]`、`limitations: string[]`。

Finding 固定 6 字段：`statement: 非空字符串`、`columns: 不重复列名数组`、`calculation: 非空字符串`、`value: finite number|null`、`unit: string|null`、`sample_size: integer >= 0`。

columns 至少一个且全部存在于输入表；sample_size 不得超过输入行数；row_count 等于输入行数。calculation 说明实际计算或观察依据；数值结论须给 value 和单位，无单位用 null。非数值观察可 value=null，但不能由此跳过依据。任何排除缺失值、过滤、分组、单位换算都写入 methods/limitations，不能静默修改原表。

data_sha256 由统一 helper 计算标准表的 `json.dumps(data, ensure_ascii=False, sort_keys=True, separators=(',', ':'), allow_nan=False)` 的 UTF-8 SHA256。算法输出必须与实际输入一致，辅助校验同一数据版本；不能用摘要证明计算正确。

### Chart 图表

固定 4 字段：`id: 非空字符串`、`title: 非空字符串`、`columns: 不重复列名数组`、`svg: 非空 SVG 字符串`。

charts 是 0..20 项数组；不强制每次分析都绘图。ID 唯一，columns 至少一个且引用有效列。不能返回路径/URL/Base64 代替 SVG。骨架在发布前检查 SVG 大小、XML 根节点并拒绝脚本、事件处理器、外部资源、foreignObject、DOCTYPE/实体引用；不执行算法生成的 SVG。安全校验不通过整次结果不发布。

### Review 审稿意见

固定 5 字段：`manuscript_sha256`、`summary`、`decision`、`issues`、`limitations`。

- manuscript_sha256：对输入稿件原字符串按 UTF-8 计算 SHA256，不先换行或格式化；避免拿旧稿件的意见修新稿。
- summary 非空；decision 只允许 revise / ready / insufficient_evidence，表示本轮检查结论，不是期刊录用决定。
- issues 是 Issue[]；revise 必须至少 1 条，ready 必须为空；insufficient_evidence 可以为空，但 limitations 必须说明缺哪些材料。
- limitations 为 string[]；没有原始数据/全文时明确未验证范围，不宣称已检查真实性。

Issue 固定 8 字段：`id`、`severity`、`category`、`target`、`quote`、`problem`、`suggestion`、`source_refs`。

| 字段 | 类型及规则 |
| --- | --- |
| id | 非空且同一 review 内唯一 |
| severity | major / minor / suggestion |
| category | method / evidence / analysis / structure / language / citation |
| target | 复用 ContentRef，见下 |
| quote | 原稿精确片段或 null；原文问题应提供，缺失章节/全文级建议可为 null |
| problem、suggestion | 两个非空字符串，不只给“建议优化” |
| source_refs | 不重复的 Paper.id[]；外部文献依据必须在输入 papers 中 |

ContentRef 归一化为固定 5 字段：node_id=null、placeholder_id=null、heading_path:string[]、document_root:bool、occurrence:integer>=1。当前业务稿件是 Markdown，只使用 heading_path 或 document_root 二选一：全文级为 true+[]；局部为 false+非空完整标题路径。重复标题用 occurrence，必须实际存在。node_id/placeholder_id 留 null，不要求算法生成虚构 Writer 节点 ID。

如果复用 Writer IR 审稿定位，F6 内部适配后仍需映射回上述 Markdown 子集；不要增加另一套 target 格式。quote 有值时必须出现在目标段落范围内，不只检查全稿是否出现。

### Reflection 阅读心得

固定 6 字段：`summary: 非空字符串`、`paper_ids: 不重复且非空 Paper.id[]`、`key_points: Insight[1..]`、`comparisons: Insight[]`、`inspirations: Insight[]`、`limitations: string[]`。

Insight 固定 2 字段：`statement: 非空字符串`、`source_refs: 不重复且非空 Paper.id[]`，全部来自输入论文。

单篇 comparisons=[]；多篇至少 1 条比较，每条比较引用至少两篇不同论文。启发是推论，不等同论文原结论。全文不可用时明确范围；不能把摘要心得展示为全文精读。

## 五 九种调用的完整契约

以下是输入三项公共字段之外的全部字段；返回字段数包含 is_demo。所有可空字段必须填 null，不允许省略。示例附件展示完整输入和输出。

### F1 研究选题与调整选题

| 输入字段 | 类型 | 条件 |
| --- | --- | --- |
| mode | create / refine | 由步骤决定，不由算法自行切换 |
| papers | Paper[] | 可为空 |
| question | Question 或 null | 原问题，可为空 |
| evidence | Evidence 或 null | create 可空，refine 必须非空 |

返回：`{is_demo: bool, question: Question}`。

create 只给主题即可提出候选问题，但无证据时不保证新颖性；refine 必须携带 evidence 对应 papers，不能只给原综述文件名。保留未变的候选 ID，新增候选使用新 ID，删除的候选不得继续被 selected_id 引用。

样例：[研究选题](examples/research_topic.json)、[调整选题](examples/refine_topic.json)。

### F2 文献检索与综述

| 输入字段 | 类型 | 条件 |
| --- | --- | --- |
| papers | Paper[] | selected / with_references 模式至少 1 篇；search 可空 |
| question | Question 或 null | 可空 |
| evidence | Evidence 或 null | 可复用的旧证据；存在时校验 paper_id |
| scope | selected / search / with_references | 默认：有选定论文用 selected，否则 search |
| max_papers | integer 1..100 | 默认 20；包含已选论文，不是额外新增数量 |

返回：`{is_demo: bool, evidence: Evidence, papers: Paper[1..100]}`。

selected 不联网加论文，输出 papers 与选定集合一致；若选定数量超 max_papers 先报错，不静默丢论文。search 可按主题检索补充，输出保留全部输入 papers，并追加本次实际采用且被 evidence 引用的新论文，不能因本轮未引用而删掉后续 question 等材料仍使用的来源；with_references 只额外纳入所选论文的直接参考文献，不递归扩展，不混同“自动拓展论文”功能。已选论文保留，附加论文数量受剩余预算限制。

零可用证据返回 INSUFFICIENT_EVIDENCE；无全文可使用明确标识的摘要，但限制主张范围。引用论文正文必须在 papers 中；检索只能得到题录时不能凭题录写实质综述。focus/request 传递读侧已经确认的主题和重点，不重复询问。

样例：[文献综述](examples/literature_review.json)。

### F3 实验研究 P1

| 输入字段 | 类型 | 条件 |
| --- | --- | --- |
| papers | Paper[] | 用于 question/evidence 引用校验，可空 |
| question | Question 或 null | 有多个候选但未选择时，先明确具体问题 |
| evidence | Evidence 或 null | 可空 |
| data | DataTable 或 null | 可空；不强制所有实验都使用表格 |
| execution_mode | plan / execute | 默认 plan；execute 需实际服务支持和授权 |

返回：`{is_demo: bool, experiment: Experiment}`。

plan 可以只给主题生成建议，不产生伪实验结果。execute 不等于在宿主机随意跑生成代码；执行服务、超时和授权由现有受控能力提供。服务不可用报 EXECUTION_FAILED；输入方案不明确报 MISSING_MATERIAL。execute 可用的研究问题：selected_id 指向候选，或只有一个候选；不能擅自从多个候选中挑一个。

样例：[实验方案](examples/experiment.json)。

### F4 数据分析与绘图

| 输入字段 | 类型 | 条件 |
| --- | --- | --- |
| data | DataTable 或 null | 用户直接提供的数据 |
| experiment | Experiment 或 null | 来自实验步骤或接入适配的结果 |

返回：`{is_demo: bool, analysis: Analysis, charts: Chart[0..20]}`。

数据来源规则固定：

1. data 非空且 experiment=null：使用 data。
2. data=null，experiment.executed=true 且 results 非空：使用 results。
3. 两者都非空：AMBIGUOUS_INPUT，不猜优先级，不自动拼表。
4. 都没有，或只有未执行的实验方案：MISSING_MATERIAL，不凭方案造数据。

结构合法之后仍需业务前置检查：数值计算必须使用数值列；样本量或列不足不能生成该统计结论。可完成描述分析时正常返回并列 limitations；请求明确要求无法完成的分析时返回 MISSING_MATERIAL 或 INVALID_INPUT，不悄悄替换成另一种分析冒充完成。

样例：[数据分析](examples/data_analysis.json)。本例只有两个单次得分，允许算差值，不允许声称显著提升。

### F5 论文写作与论文修订

当前骨架接收 F4 的结构化 analysis 和 charts 列表，charts 默认 []；旧版文字 analysis 和单图 chart 字段保留。F5 写作算法目前仍是演示实现，材料传入不代表已实现真实论文生成。

| 输入字段 | 类型 | 条件 |
| --- | --- | --- |
| mode | draft / revise | 由步骤决定 |
| papers | Paper[] | 可为空；但引用的来源必须存在 |
| question | Question 或 null | 可空 |
| evidence | Evidence 或 null | 可空 |
| analysis | Analysis 或 null | 可空；不能据此声称核验了原始数据 |
| charts | Chart[] | 默认 []，已有图表可带入 |
| outline | Markdown string 或 null | 可空；非空大纲为已给定结构，未经要求不推倒重写 |
| manuscript | Markdown string 或 null | draft 可空；revise 必填 |
| review | Review 或 null | draft 必须 null；revise 必填 |
| selected_issue_ids | string[] | draft 必须 []；revise 非空且全部在 review.issues 中 |

返回：`{is_demo: bool, outline: Markdown string, manuscript: Markdown string}`。

draft 至少有 papers/question/evidence/analysis/outline/manuscript 中一种有效材料，不能仅靠 topic 生成完整研究结果。已有稿件的一般修改可用 draft+request；按审稿意见修订用 revise。若选题仍有多个候选且没有选定，必须明确目标，不自动任选。

revise 前校验 review.manuscript_sha256 等于当前稿件摘要；不一致返回 STALE_MATERIAL，必须重新审稿或明确重新匹配意见，不能套用旧定位。选择“全部修改”由接入层展开为所有 issue ID，不用空数组暗示全选。

大纲是 Markdown：正好一个 H1 标题、至少一个 H2 章节、标题层级不跳级。正文也为一份完整 Markdown，包含同一标题和大纲声明的章节顺序；不返回 diff、文件路径或代码围栏包住的整篇正文。修订返回完整新稿和同步大纲，不只返回修改段落。

复用 Writer：内部可使用 WriterDocument / WriterBlock 规划章节、保留编号和定位；最终转换为上述 Markdown。引用标识沿用实际 Paper.id，由骨架/引用渲染转换成展示编号；不把引用编号当永久身份。正文不能引用不存在的论文 ID。

图表采用算法在本次输出内分配的唯一 ID，骨架保存后保持稳定；修改同一图表时保留 ID，新图使用新 ID。Markdown 中图表使用 `[图表:ID]` 占位标记，ID 必须出现在 charts 中；骨架落盘后替换为已有 Writer 可用的资源引用。此标记是待实现的论文边界约定，不是声称现有 Writer 已支持；发布前不得残留无法解析的标记。论文算法不自己生成带临时绝对路径的 Markdown 链接。

F4 有原始表，负责校验图表列来源；F5/F6 没有原始表，不能再次证明图表或数值准确。内部传递时骨架保留上游已校验的来源关系；外部直接提供 analysis/charts 时接入层须校验对应数据或明确标为未核验材料，稿件/审稿不得宣称已经复算。本文不将缺原始数据的字段结构校验包装成数据真实性保证。

word_target 非空时复用旧流程 count_academic_units 的中文字符/英文单词口径，目标容差初始为 ±10%；不足时明确检查失败，不自动填充无证据内容。摘要、参考文献等是否计入由同一个计数函数统一，不由实习生各自实现。引用/数字准确性仍需真实材料核验。

样例：[论文写作](examples/write_document.json)、[论文修订](examples/revise_document.json)。本附件是短格式样例，不是满足完整论文质量要求的成稿。

### F6 模拟审稿

当前已实现：`run(inputs)` 接收 topic、request、manuscript、evidence、analysis；evidence 为可空文本，analysis 接受 F4 的结构化 Analysis、旧版非空文本或 null。骨架保留结构化内容及来源，F6 阅读这些材料，不重新计算原始数据。
内部 `ReviewResult` 固定包含非空 summary、issues 列表、limitations 字符串列表。
每条 issue 固定包含 severity（major/minor/suggestion）、category（method/evidence/analysis/structure/language/citation）、location、quote（非空字符串或 null）、problem、suggestion、action_type（text_revision/needs_material/needs_experiment）。拒绝未知字段；按等级排序后由代码生成 R1、R2 等编号，渲染成单份 Markdown。quote 非空时检查它确实出现在本次稿件；章节归属和重复位置消歧尚待任务 3。
模型使用现有 AutoModel 与 WriterToolBase 的结构化提示/解析，不操作 pool 或文件，不修改旧 workflow；缺辅助材料会在报告中明确未核验范围。
F6 不使用 Writer 默认的全文重试：一次生成后汇总字段与摘录错误，只将错误字段/意见交给模型修复一次，有效意见原样保留。
修复结果必须覆盖且仅覆盖指定路径，合并后重新执行完整格式与原文摘录校验；最多两次模型调用，不叠加重试。
局部修复的 quote 使用起止行号，由代码复制原稿连续行；最终对外的 quote 仍为原文字符串，F5 接口不变。
无法解析的 JSON、缺少意见列表或修复后仍不合法会明确失败，不静默补默认值、丢弃意见或自动重新审稿。长稿处理与端到端耗时仍需持续验收。

以下为联调时对齐的目标 v2 契约，不是当前返回类型：

| 输入字段 | 类型 | 条件 |
| --- | --- | --- |
| papers | Paper[] | 可空；引用核验依赖对应论文 |
| manuscript | Markdown string | 必填非空 |
| evidence | Evidence 或 null | 可空 |
| analysis | Analysis 或 null | 可空 |

返回：`{is_demo: bool, review: Review}`。

不以“无外部材料”阻止语言或结构审查，但必须标出未核验的实验、数值或引用。问题必须可定位或明确是全文/缺失内容建议；目标和 quote 校验失败不发布 review。输入稿件的规范化必须发生在计算摘要之前，审稿/修订使用完全相同的文本。

样例：[模拟审稿](examples/peer_review.json)。

### F7 阅读心得

| 输入字段 | 类型 | 条件 |
| --- | --- | --- |
| papers | Paper[1..100] | 至少 1 篇可读材料 |
| evidence | Evidence 或 null | 可复用，引用必须仍在 papers 中 |

返回：`{is_demo: bool, reflection: Reflection}`。

输出 paper_ids 与输入选定集合一致；单篇给关键点和启发，多篇增加比较，不能变成仅串联摘要。后续 PPT 消费渲染后的阅读心得和论文来源；本算法不生成 PPT、不触发外部流程。

样例：[阅读心得](examples/reading_reflection.json)。

## 六 结构化结果如何展示及编辑

Question/Evidence/Analysis/Review/Reflection 以结构化对象为传递依据，骨架用确定模板渲染 Markdown，继续使用原写作控件。F5 的大纲和稿件本身就是 Markdown；F3 复用 report 展示。不要求算法同时维护两份互相矛盾的结构化结果和正文。

用户编辑后必须同步，否则下游会读到旧结构：

- 编辑 F5 文稿：以用户保存的 Markdown 为准；旧 Review 摘要失配，不继续套用。
- 编辑结构化报告的 Markdown：保存原始编辑版，标记结构待同步；由共享适配器解析回对应类型并重新校验。不能直接把字符串塞回 Question/Evidence/Review 对象，也不能用旧结构覆盖用户编辑。
- 解析失败或修改造成引用/定位失效：保留编辑稿并明确提示，暂停依赖该材料的后续执行。允许用户修正或重新生成；不可默默继续用旧值。
- 这是 v2 必做的骨架改造，当前 v1 的字符串编辑导入不能直接适用于新对象。

## 七 中间入口与参数传递

用户/上游提供文件或业务材料，由入口内部函数创建本次状态；不设置用户可见的材料准备步骤，不要求用户或算法负责人提交 pool.json。

| 入口 | 最少业务材料 | 缺少时 |
| --- | --- | --- |
| F1 | topic | 补主题 |
| F2 | search 模式 topic；selected 模式 topic+papers | 补主题/论文，不制造文献 |
| F3 | plan 模式 topic；execute 需明确问题及服务所需材料 | 保持方案或明确失败，不伪造结果 |
| F4 | topic+一份标准数据，或有实际结果的 Experiment | 补数据或解决多来源冲突 |
| F5 | topic+有效研究材料/大纲/稿件之一 | 补研究材料，不靠标题编结果 |
| F6 | topic+稿件 | 补稿件 |
| F7 | topic+至少一篇论文 | 补论文 |

调整选题和论文修订是各自模块的模式，不新增前端初始入口；其独立 API 可按对应完整输入测试。任意入口指七个业务功能可跳过前序，不指可以跳过必要材料。

实际传递责任：

1. 平台绑定上一步产物文件；共享 Runtime 读取并还原业务对象。
2. Runtime 只选取当前模块声明的字段，补默认值、检查引用及版本，随后调用 `run(inputs)`。
3. 算法返回固定结构；Runtime 先做结构与跨字段校验，再保存 JSON、渲染文稿、保存图表并统一发布。
4. 旧材料保留；失败不覆盖已确认产物。下游读取新版本，不靠对话历史猜输入。
5. 每条执行边再次校验下一步必要条件：例如 F3 只出了方案就不能直接跑 F4；F4 出了分析可以供 F5 使用。进入某一步成功不代表任意后续分支都可无条件执行。

pool 仍是 Runtime 的内部材料快照，可以保存这些对象及版本关系；不是算法输入，也不是已完成的跨 Chat/知识库公共资料库。v2 需要显式契约版本，不得让旧裸文本按同名字段冒充新对象；版本由 Runtime 包装管理，算法无需重复携带。

## 八 错误协议与验证分工

失败通过统一异常上抛，成功返回对象不混入 error/status。Runtime 将异常整理为固定错误对象 `{code,field,message,retryable}`；这是拟增加的异常适配，当前 MissingMaterials/ValueError 尚未具备全部字段。

| code | 触发例子 | retryable |
| --- | --- | --- |
| INVALID_INPUT | 数据行宽不一致、类型错误、未知字段 | false |
| MISSING_MATERIAL | 审稿缺稿件、分析缺数据 | false |
| AMBIGUOUS_INPUT | 同时传直接数据和实验结果、实验目标有多个未选问题 | false |
| UNSUPPORTED_FORMAT | 当前不支持的文件/结果格式 | false |
| INPUT_TOO_LARGE | 行列/文本/图表超过边界 | false |
| INSUFFICIENT_EVIDENCE | 检索只有题录，没有可支持综述的内容 | false |
| INVALID_OUTPUT | 算法漏字段、编造来源 ID、非法 SVG | false |
| STALE_MATERIAL | 审稿意见属于旧稿件 | false |
| EXECUTION_FAILED | 模型/执行服务失败 | 只有确认是临时故障时 true |

field 为 JSON 路径，如 `input.data.rows[2][1]`、`output.review.issues[0].target`；全局错误用空字符串。message 要指出预期和实际，如“score 列要求 number，收到字符串 '高'”。retryable=false 指不能原样自动重试，用户补正后可以再执行。

三层检查不可混为一谈：

- 结构层：键集合、类型、枚举、数量、null、字符串空白。由 Schema/Pydantic 实现，模型对象配置 forbid extra；复用 Writer 类型时额外施加本文限定子集。
- 关联层：行列长度、列类型、ID 存在、证据原文匹配、数据/稿件摘要、选定问题、图表安全、重复 ID、结果数量。由共享 Python 校验实现。
- 质量层：统计方法适用性、计算正确、创新性、综述覆盖、引用是否真正支持结论、审稿是否合理。由算法测试和真实场景验收；字段合法不能替代质量通过。

## 九 各模块交付时的最低测试集

| 范围 | 必须覆盖 |
| --- | --- |
| 所有模块 | 完整正常输入；最小合法输入；未知字段；错类型；缺必填；输入不被修改；非法输出不发布；不含 pool/path；演示标记保留 |
| F1 | 无论文候选；有文献调整；重复候选 ID；selected_id 不存在；无证据不夸大创新 |
| F2 | selected/search/with_references 三模式；零结果；摘要而非全文；未知 paper_id；伪造 quote；预算不足；来源去重 |
| F3 | 仅方案；执行成功；无服务/未授权/超时；只有日志无结果；多候选未选；失败不返回成功实验 |
| F4 | 直接表格、实验结果；两种来源冲突；列宽/列类型/日期错误；NaN；缺失值；单样本；摘要不匹配；0/多图；恶意 SVG |
| F5 | 已有大纲、已有稿件、研究材料写作；修订指定问题；旧审稿摘要；缺引用；图表占位解析；保留未要求修改内容；编辑后传递 |
| F6 | 仅稿件有限审查；有证据审查；重复标题定位；quote 不存在；空问题与 decision 冲突；未核验范围 |
| F7 | 单篇、多篇、摘要、节选；未知来源；多篇无比较；单篇不得冒充横向比较 |
| 接入层 | 原始附件/知识库转换；独立七入口；材料准备缺失；用户编辑结构化报告后的同步失败；版本切换；旧 v1 不静默混用 |

附件九个 JSON 是完整格式样例，全部标记 is_demo=true，不是已经运行真实算法的结果。无需算法生成相同措辞；应断言结构、引用和计算不变量，而不是逐字匹配正文。

## 十 与当前代码的差异及落地 TODO

| 工作包 | 当前 v1 | v2 要做的事 | 本轮状态 |
| --- | --- | --- | --- |
| 共享契约 | 简单 dict 和文本校验 | 根据本文实现严格模型及跨字段校验、错误协议；保留 run 单参数 | 仅规范完成 |
| 数据适配 | columns/rows；原始附件未完整接入 | 补列类型/单位、文件转换、冲突与容量检查 | 仅规范完成 |
| 论文及证据 | Paper 仅 id/title/text/url；综述字符串 | 补元信息、覆盖范围、结构化 Evidence 和引用校验 | 仅规范完成 |
| 模块算法 | 七个演示实现 | F1/F2/F4/F6/F7 返回结构化结果；F3 增加执行约束；F5 保留 Markdown | 未改代码 |
| 运行适配 | 通过 FIELDS/OUTPUTS 取材料并保存文本 | 补 constraints、scope、mode 等参数选择；多图、渲染模板、用户编辑回写、新旧契约隔离 | 未改代码 |
| 文件与展示 | 单图 SVG、文本投影 | 多图落盘和稳定引用替换，结构化报告继续用原写作控件 | 未改代码 |
| 回归 | 仓库内九份 v1 fixtures | 更新完整输入输出及本规范反例，再做 UI/跨模块联调 | 未执行 v2 算法验收 |

最小实施顺序：共享类型与契约测试 → 接入归一化和 Runtime 投影/编辑适配 → 九份 v2 演示替身 → 验证入口及反馈 → 实习生分别替换核心算法。契约变更由骨架负责人统一维护，算法负责人不得为了通过测试私改字段。

当前 contracts.py 和仓库 fixtures 仍描述 v1，明确与本规范分开。不得直接把本附件覆盖仓库 fixtures 后宣称代码升级完成，也不修改旧 academic_research_pipeline 来强行适配新流程。

## 十一 代码出处

- [当前契约](../../../algorithm/lazymind/document_tools/paper/contracts.py)、[运行适配](../../../algorithm/lazymind/document_tools/paper/workflow.py)
- [Writer 数据模型](../../../algorithm/lazyllm/lazyllm/tools/writer/data_models/writer_ir.py)、[InputResource 与 WritingTask](../../../algorithm/lazyllm/lazyllm/tools/writer/data_models/task.py)
- [公共写作能力](../../../algorithm/lazymind/document_tools/writing.py)、[修订能力](../../../algorithm/lazymind/document_tools/revision.py)、[产物工具](../../../algorithm/lazymind/document_tools/artifacts.py)
- [搜索结果格式](../../../algorithm/lazyllm/lazyllm/tools/tools/search/base.py)、[Arxiv](../../../algorithm/lazyllm/lazyllm/tools/tools/search/arxiv_search.py)、[Semantic Scholar](../../../algorithm/lazyllm/lazyllm/tools/tools/search/semantic_scholar_search.py)
- [旧论文参数与检查](../../../workflows/academic_research_pipeline/scripts/pipeline_tools.py)、[旧流程复用调查](reused.md)
