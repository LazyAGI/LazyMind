---
name: smart-charts
description: "Intelligent chart generation and data analysis skill. Reads user-supplied data files (CSV/Excel/JSON), analyzes data characteristics with LLM assistance, auto-recommends and generates interactive ECharts visualizations. Use when the user asks to analyze data, generate charts, create visualizations, or work with tabular data files."
license: MIT
compatibility: "Python 3.11+; requires pandas==3.0.1, numpy==2.4.3, openpyxl==3.1.5, xlrd==2.0.1; no network access needed (ECharts JS bundled offline); install with: pip install -r requirements.txt"
metadata:
  author: smart-charts
  version: 7.0.0
  permissions:
    file_read: true
    file_write: true
    network: false
  safety:
    sandbox: "LLM-generated transform code runs in a restricted sandbox (keyword blacklist + AST whitelist + safe builtins). No user confirmation required."
  input_formats: ["csv", "tsv", "txt", "xlsx", "xls", "json"]
  output_format: html
  max_file_size_mb: 100
---

# Smart Charts

> 将数据文件（CSV/Excel/JSON）转化为交互式 ECharts HTML。支持 21 种图表类型、多文件合并、LLM 数据转换代码（沙箱执行）。
> CLI 细节、flags 语义、错误码表、FAQ 见 [REFERENCE.md](./references/REFERENCE.md)。

---

## 30 秒速查表（先看这里，其余细节按需再读）

**契约速览（5 条）**

1. 列名解析后会被规范化：转小写、特殊字符→`_`（如 `总学时`→`总_学时`），中文保留；`--x-axis`/`--y-axis`/transform 必须引用规范化后的列名
2. transform 沙箱：可用变量仅 `df`/`pd`/`np`（`np.select`/`np.where` 可用），支持多语句（`;` 或换行分隔），必须产出名为 `result` 的 DataFrame；禁止 import/open/try/类定义（黑名单 + AST 白名单强制校验，违规返回带 `suggestion` 的错误）
3. pie/bar 等按「1 个分类列(name) + 1 个数值列(value)」读数据；分类频次图先用 transform 聚合成 name/value 两列，再指定 `--x-axis name --y-axis value`
4. 成功时 stdout：`success`/`html_path`/`chart_type`/`title`/`data_rows`/`data_preview`（绘图数据前 10 行，口径校对用）+ `plot_stats`（绘图数据完整统计摘要，写解读用；21 类全覆盖）
5. **校对口径直接读 stdout 的 `data_preview` + `data_rows`，不要打开 HTML 去搜数据**——预览取自 transform 之后、渲染所用的同一份数据，即被绘制内容的真值

**黄金示例 1：分类频次 → pie/bar**（最高频场景，复制改列名即可）

```bash
python {skill_base}/scripts/cli.py data.xlsx bar --title "标题" \
  --x-axis name --y-axis value \
  --transform-code "result = df['类别列'].fillna('未标注').value_counts().rename_axis('name').reset_index(name='value')"
```

**黄金示例 2：多图批量 + 防转义坑**（≥2 张图 MUST 批量；transform 含中文/引号时不要直接在 shell 传 `--charts`，写进 JSON 文件用 `--charts-file`）

```bash
python {skill_base}/scripts/cli.py data.xlsx --sheet "Sheet1" \
  --charts-file charts.json --output-dir ./out
```

`charts.json` 每项：`type`（必填）+ `title`/`x_axis`/`y_axis`/`transform_code`：

```json
[{"type": "pie", "title": "课程学时结构", "x_axis": "name", "y_axis": ["value"],
  "transform_code": "tmp = df.drop_duplicates('课程'); result = tmp.assign(_t=np.select([tmp['讲授_学时'] == 80, tmp['实验_学时'] == 80], ['讲授型', '实验型'], default='混合型'))['_t'].value_counts().rename_axis('name').reset_index(name='value')"}]
```

**黄金示例 3：分组聚合 → bar**

```bash
--transform-code "result = df.groupby('分组列')['数值列'].sum().rename_axis('name').reset_index(name='value')"
```

**口径陷阱**：聚合前想清楚「按数据行 vs 按去重实体」——统计实体属性（如每门课程的学时结构）先 `drop_duplicates`；生成后对照 `data_preview` 数值与 `data_rows` 检查（若各行 value 之和等于原始行数而非实体数，就是忘了去重）。

---

## Activation Triggers

Load this skill when **any** of the following is met:

- User mentions: "analyze data", "generate chart", "data visualization", "chart", "visualization"
  / 用户提到：「分析数据」「生成图表」「数据可视化」
- User provides a data file and asks for analysis or visualization
- User asks to generate charts or a report from tabular data

---

## Hard Constraints (MUST follow)

1. **MUST follow the CLI workflow**: `data_parser.py` → `cli.py`，不要自写脚本替代 CLI。
2. **Messy headers MUST use CLI flags**（`--skiprows N` / `--header-row N` / `--sheet`，语义见 REFERENCE.md），N 由实际数据决定，不得拍脑袋固定。
3. **列重命名/重塑/聚合 MUST use `--transform-code`**。解析层只解决"哪行是表头"，其余清洗归 transform。
4. **MUST report unsupported scenarios**: CLI 确实不支持的（如嵌套 JSON 超过 1 层），先向用户说明并给建议，不得静默绕过。
5. **MUST NOT hard-code absolute paths** in generated code; resolve paths at runtime.
6. **不要主动传 `--lang`**；CLI 自动跟随数据语言。仅当用户明确要求某种语言时才传。
7. **MUST 附解读交付**：交付图表时必须附由 LLM 写的文字解读，并通过 `--annotation` 注入 HTML（见「交付解读规范」），不得只交付裸图。

---

## Confirmation Policy

生成图表是廉价可逆动作（重生成 1-10s，零外部副作用）。**默认不向用户确认**，直接按数据语义选型生成。

agent 内部完成以下判断，不打断用户：

- **图表类型**：按 21 种图表的 Required Format 匹配数据形态
- **多文件合并策略**：按列重叠率自动决定（见 REFERENCE.md）
- **取值口径**：按列名、单位、数值范围推断最可能语义

**事后审阅代替事前确认**：交付时必须在交付语中显式列出本次关键假设，例如：

- "选了 line 图，因为 month 是时间序列列"
- "多文件按列名完全相同走纵向拼接，已注入 source_file 列"
- "销量按金额口径（列含 ¥/元/amount）"

用户审阅成品后若不同意任一假设，可一句话要求换口径/换类型/换合并方式重生成。

**唯一必须的用户介入点**：见 Exit Criteria 的"仍失败"分支。

---

## Capability Boundaries

**Supported:** CSV (.csv=comma / .tsv=tab / .txt=auto-detect delimiter), Excel (.xlsx/.xls), JSON (.json); 21 chart types (see below); multi-file auto-merge (recommended ≤ 10 files); single file ≤ 100 MB (≤ 50 MB recommended); auto-detects UTF-8/GBK/GB2312/UTF-16/Latin-1.

**Not supported:** Databases (export to CSV first), real-time/streaming data, geo maps, >100 MB files, nested JSON >1 level, non-tabular data (images/audio/video). Auto-merge requires ≥50% column overlap.

**Network requirement:** None. ECharts JS is bundled in `assets/` and inlined into each HTML output; charts render fully offline with no external dependencies.

**Security:** transform 代码由沙箱强制校验（黑名单 + AST 白名单 + 安全 builtins），违规会返回带 `suggestion` 的结构化错误，按提示修正重试即可，无需用户确认。机制细节见 REFERENCE.md。

---

## Execution Workflow

1. **Obtain data** — user uploads file(s) or provides path(s).
2. **Parse data** — call `data_parser.py` on all files; for multiple files, assess merge feasibility.
3. **Recommend & generate** — 按数据语义选型后直接生成，无需确认；交付语显式列出关键假设（见 Confirmation Policy）。
4. **Transform (if needed)** — raw data 不匹配目标图表输入格式时，生成 `--transform-code`。
5. **Generate charts** — call `cli.py` → ECharts HTML.
6. **Present results** — 按下方 Exit Criteria 验收后立即展示。

## Exit Criteria (什么算做完，机械可判定)

- ✅ **成功**: `cli.py` stdout 为 `{"chart": {"success": true, ...}}`（多图模式为 `{"charts": [...], "summary": ...}`），且 `html_path` 指向的文件存在且非空 → 呈现前用同一 stdout 的 `data_preview`/`data_rows` 校对聚合口径（按行 vs 按去重实体），确认无误后**附文字解读**（见「交付解读规范」）再呈现给用户；**不要打开 HTML 文件验证数据**。
- ❌ **失败**: `success: false` 或 exit code 1 → 读 `error.details.suggestion`，修正后重试；**同一环节最多重试 2 次**。
- 🛑 **仍失败（唯一必须的用户介入点）**: 把 `code_name`、`suggestion`、已尝试的修复如实报告用户并给出建议，等待用户决策。**不得**静默改用自写脚本兜底（违反约束 1/4）。

---

## 交付解读规范（Delivery Annotation）

交付每张图表时，必须附一段**由 LLM 写的文字解读**（不是技能自动生成的模板），作为用户写报告的佐证。技能只负责算事实（`plot_stats`），解读文字由 agent 读 `plot_stats` 后自己写，再用 `--annotation` 注入 HTML。

**标准流程（两步）**：
1. 先生成图表，从 stdout 拿到 `plot_stats`（绘图数据的完整统计摘要）。
2. agent 读 `plot_stats`，写一段 2~4 句的解读，然后用 `--annotation "解读文字"` 重新生成，把解读注入 HTML（图表下方「图表说明」区块）。

**事实锚点**：解读的每个数字都必须能在 `plot_stats` 或 `data_preview` 里找到出处——不得凭印象编造。注意 `plot_stats` 里的 `x_cardinality` 是 x 轴去重后的个数，写解读时要结合 x 列语义说清（如 x 是「姓名」则说「59 名学生」，而不是笼统的「59 个类别」）。

**最小结构**（2~4 句）：
1. 这张图是什么：图表类型 + 标题 + 覆盖范围（结合 x 列语义，如「59 名学生」「5 个分数段」）。
2. 最显著的事实：基于 `plot_stats` 挑 1~2 个能支持结论的点（最大/最小/趋势方向/占比/离群/累计），给出具体数值与对应标签。
3. 口径说明：一句话交代取值口径，与 `assumptions` 字段呼应。

**硬边界**：
- 只陈述 `plot_stats`/`data_preview` 能支撑的事实，不夸大、不推测数据之外的原因。
- 深度业务解读（为什么/怎么办）不是硬性要求，属结合上下文的额外发挥。

**示例**（agent 读 `plot_stats` 后写，再用 `--annotation` 注入）：
```bash
python {skill_base}/scripts/cli.py data.csv bar --title "学生总成绩对比" \
  --x-axis 姓名 --y-axis 总成绩 \
  --annotation "本图展示 59 名学生的总成绩分布。韩家芯以 98.84 分居首，马云飞 59.96 分垫底，全班平均 76.91 分。"
```

---

## Data Parsing

```bash
python {skill_base}/scripts/data_parser.py <file1> [file2 ...] [--summary] [--merge] [--skiprows N] [--header-row N] [--sheet <name|index>]
```

- `{skill_base}` = 本 skill 根目录（含 SKILL.md）。
- flags 的精确语义、多编码回退、sheet 选择细节见 REFERENCE.md。
- **Merge 关键 gotcha**: 纵向拼接会注入 `source_file` 列标识来源文件，下游 transform 代码必须考虑到这个额外列。≥50% 列重叠走横向关联；无共同结构报错（建议分开分析）。
- **列名规范化**: 解析后列名会被 `_normalize_col` 规范化——转小写、特殊字符（标点/符号）替换为 `_`、连续空白/下划线合并、空名→`unnamed`；中文字符保留。`--x-axis`/`--y-axis`/transform 代码须引用规范化后的列名。完整映射表见 REFERENCE.md。

---

## Chart Generation

```bash
python {skill_base}/scripts/cli.py \
  <file_path> <chart_type> \
  --title "Chart Title" --x-axis "date" --y-axis "revenue profit" \
  --transform-code "<pandas code>" --skiprows N --header-row N --sheet <name|index> \
  --lang zh|en --output-dir "./output" \
  --label-col "姓名" --color-by "地区"
```

- 成功输出 `{"chart": {"success": true, "html_path": ..., "data_rows": N, "data_preview": [{...}, ...]}}` 到 stdout——`data_preview` 是最终绘图数据的前 10 行（transform 之后、渲染所用），生成当轮直接用它校对聚合口径，无需打开 HTML；失败输出结构化错误 JSON（`details.suggestion` 给出恢复方法）。完整参数与错误码表见 REFERENCE.md。
- `--label-col`（可选）：身份列（如姓名/名称），其值进数据点的 name 和 tooltip，适用于 scatter/bubble/boxplot。不传时自动探测未被占用的字符串列（列名含 姓名/name/id 等优先），自动选择会记入 stdout 的 `assumptions` 字段，交付语中应声明。
- `--color-by`（可选）：着色列，适用于 scatter/bubble。数值列 → visualMap 连续着色；类别列 → 按类别拆 series 分色并进 legend。默认不传——无分析意义的着色只是视觉噪音。

### Multi-Chart Mode（多图批量生成）

需要 **≥2 张图**时 MUST 用 `--charts` 一次生成（单次解析 + 单次进程启动，比逐张调用快数倍）：

```bash
python {skill_base}/scripts/cli.py <file_path> \
  --charts '[{"type":"bar","title":"城市营收","x_axis":"city","y_axis":["revenue"]},
             {"type":"line","title":"趋势","x_axis":"date","y_axis":["revenue","profit"]}]' \
  --output-dir "./output"
```

- 每项字段：`type`（必填）+ `title`/`x_axis`/`y_axis`（字符串或数组）/`transform_code`（单图级）/`label_col`/`color_by`。
- **`--charts-file <path>`（推荐）**：把 `--charts` 的 JSON 数组写进文件传入；transform 含中文/引号时避免 shell 转义损坏，文件不存在返回结构化 FILE_NOT_FOUND 错误。
- 全局 `--transform-code`（可选）：先对 df 应用一次，再供所有图使用；各图也可带自己的 `transform_code`。
- 输出 `{"charts": [{...}, ...], "summary": {"total": N, "succeeded": M, "failed": K}}`，每项结构与单图 `chart` 一致（含 `success`/`html_path`/`error.details.suggestion`）。
- exit code：全部失败才为 1；部分失败时 exit 0，读 `charts` 中 `success:false` 项的 `suggestion` 修正后重试该图。
- 数据点超过阈值（默认 15）时 HTML 自动启用 dataZoom + 横向滚动，无需 agent 处理。
- 生成的 HTML 标题可双击内联编辑（用户可直接在浏览器修改标题，保存图片时使用新标题）。

### Chart Types

选择图表前先核对原始数据是否匹配 Required Format；不匹配则用 transform 代码适配。

> **量纲提示**：heatmap / boxplot / radar 等多列图表，若各列量纲差异大（如满分 10 与满分 100 混合），需先用 transform 代码归一化，否则小量纲列会被大量纲列主导。

| ID | Best For | Trigger Keywords | y_axis Cardinality | Required DataFrame Format | Example Columns |
|----|----------|------------------|:-------------------:|--------------------------|-----------------|
| `line` | Time-series trends | trend, change, over time, 趋势, 变化, 走势 | 1~N | 1 category/time + 1~N numeric | `month, productA, productB` |
| `bar` | Category comparison | compare, rank, difference, 对比, 比较, 排名, 差异 | 1~N | 1 category + 1~N numeric | `city, revenue, profit` |
| `area` | Cumulative change | cumulative, change, 累计, 变化 | 1~N | 1 category/time + 1~N numeric | `date, uv, pv` |
| `pie` | Composition/share | share, composition, proportion, 占比, 构成, 比例 | 1 | 1 name + 1 value | `category, share` |
| `scatter` | Correlation | correlation, relationship, scatter, 相关, 关系, 散点 | 1 | 2 numeric, or 1 category + 1 numeric | `height, weight` |
| `radar` | Multi-dimension comparison | multi-dimension, comprehensive, radar, 多维, 综合, 雷达 | N | 1 indicator + N numeric | `metric, productA, productB` |
| `heatmap` | Density/cross-tab | density, cross, matrix, heatmap, 密度, 交叉, 矩阵, 热力 | N | 2 category + 1 numeric | `row, col, value` |
| `treemap` | Hierarchical proportion | hierarchy, proportion, nested, 层级, 占比, 嵌套 | 1 | 1 name + 1 value | `category, sales` |
| `graph` | Entity relationships | relationship, network, topology, 关系, 网络, 拓扑 | special | source + target (+ value) | `from, to, weight` |
| `boxplot` | Distribution/outliers | distribution, outlier, quartile, 分布, 离群, 四分位 | N | N numeric | `math, chinese, english` |
| `waterfall` | Incremental change | increment, change, waterfall, 增量, 变化, 瀑布 | 1 | 1 category + 1 numeric (increments) | `month, profit_delta` |
| `gauge` | KPI progress | progress, kpi, achievement, 进度, KPI, 达成 | 1 | 1 numeric (mean used) | `completion_rate` |
| `sankey` | Flow transfer | flow, transfer, sankey, 流向, 流量, 转移 | special | source + target + value | `origin, destination, amount` |
| `funnel` | Conversion rate | conversion, funnel, churn, 转化, 漏斗, 流失 | 1 | 1 name + 1 value | `stage, count` |
| `sunburst` | Single-level proportion | proportion, sunburst, 占比, 比例 | 1 | 1 name + 1 value | `category, value` |
| `wordcloud` | Frequency/keywords | word frequency, keywords, text, 词频, 关键词, 词云 | 1 | 1 name + 1 value | `word, frequency` |
| `histogram` | Distribution shape | distribution, histogram, 分布, 直方图 | 1 | 1 numeric column（`--x-axis` 或 `--y-axis` 均可，数值型 `--x-axis` 优先） | `score` |
| `stacked_bar` | Composition over categories | composition, stacked, 堆叠, 构成 | 1~N | 1 category + 1~N numeric | `quarter, productA, productB` |
| `bubble` | 3-variable correlation | bubble, 3-variable, 气泡, 三变量 | 2 | 2 numeric + 1 size | `price, rating, sales` |
| `pareto` | 80/20 analysis | pareto, 80/20, 帕累托, 二八 | 1 | 1 category + 1 numeric | `defect_type, count` |
| `combo` | Dual-axis comparison | dual-axis, combo, 双轴, 组合 | 1~N | 1 category + 1 bar + 1~N line | `month, revenue, growth_rate` |

**y_axis cardinality key:** `1` = only first column used; `1~N` = each column becomes a series; `N` = multiple columns expected; `2` = exactly 2 numeric columns required; `special` = auto-detects source/target/value columns. scatter/bubble/boxplot 中未被 x/y 占用的字符串列不会浪费——自动作为身份列进 tooltip（见 `--label-col`）。

### Programmatic API

```python
from scripts.chart_generator import ChartGenerator

# Single chart — returns {'chart': {'success', 'html_path'/'error', ...}}
# lang=None auto-detects from data; pass 'zh'/'en' to override (only when user asks).
result = ChartGenerator(output_dir="./output").generate_chart(
    df=df, chart_type="bar", title="Regional Revenue",
    x_axis="region", y_axis=["revenue"], lang=None,
)

# Batch — returns {'charts': [...]}，每项结构与单图一致
result = ChartGenerator(output_dir="./output").generate_multi_charts(
    df=df,
    chart_configs=[
        {"type": "bar",  "title": "Regional Revenue", "x_axis": "region", "y_axis": ["revenue"]},
        {"type": "line", "title": "Monthly Trend",   "x_axis": "month",  "y_axis": ["revenue", "profit"]},
    ],
    lang=None,
)
```

失败时 `success` 为 `False`、`error` 为结构化错误字典，不抛异常——检查 `success` 决定下一步。

---

## Transform Code Contract

契约（由沙箱强制，违反会收到带 `suggestion` 的错误，按提示修正即可）：

- 可用变量只有 `df`, `pd`, `np`；必须产出名为 `result` 的 `pd.DataFrame`
- 不要原地修改 `df`（用 `df.copy()` 或链式操作）
- 原始数据已匹配目标格式时，不传 `--transform-code`

**Common transform patterns:**
- Long→multi-series: `result = df.pivot_table(index='<time>', columns='<category>', values='<value>', aggfunc='sum').reset_index()`
- Long→pie (filter): `result = df[df['metric']=='revenue'][['category','value']].rename(columns={'category':'name'})`
- Wide→long: `result = df.melt(id_vars=['date'], var_name='name', value_name='value')`
- Aggregate→bar: `result = df.groupby('<category>')['<value>'].sum().reset_index()`
- Rename columns: `result = df.rename(columns={'来源':'source','去向':'target','金额':'value'})`
- Compute delta→waterfall: `tmp = df.copy(); tmp['delta'] = tmp['profit'].diff().fillna(tmp['profit'].iloc[0]); result = tmp[['month','delta']]`
- Rename messy/uninformative column names (after `--header-row` leaves columns like `score_a`, `unnamed_3`): `result = df.rename(columns={'unnamed_0':'student_id','unnamed_1':'name','score_a':'homework_score','score_b':'exam_score'})`
- Forward-fill merged cells (when only the first row of a group is populated): `result = df.ffill()`
- Combine sub-headers into a single column name (when `--header-row N` flattens one row but loses context): `result = df.rename(columns={c: f'{c}_score' for c in df.columns if c not in ['student_id','name']})`
