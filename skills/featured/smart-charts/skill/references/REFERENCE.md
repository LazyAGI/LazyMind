# Reference

Detailed reference for installation and CLI usage. For high-level capability overview, activation triggers, and the chart type table, see [SKILL.md](../SKILL.md).

---

## Installation

```bash
pip install -r requirements.txt
```

Dependencies (pinned with `==`): `pandas==3.0.1`, `numpy==2.4.3`, `openpyxl==3.1.5`, `xlrd==2.0.1`. ECharts JS is bundled in `assets/` (no CDN, fully offline).

Self-test (developer): `python {skill_base}/scripts/ux_regression_check.py` checks chart UX invariants (identity labels, color coding, tooltips, true waterfall).

---

## Data Parsing — CLI Reference

> `{skill_base}` = root directory of this skill (contains `SKILL.md`).

```bash
# Single file
python {skill_base}/scripts/data_parser.py <file_path> [--summary] [--skiprows N] [--header-row N] [--sheet <name|index>]

# Multiple files
python {skill_base}/scripts/data_parser.py <file1> <file2> ... [--summary]

# Multiple files with auto-merge
python {skill_base}/scripts/data_parser.py <file1> <file2> ... [--merge] [--summary]
```

**Flags:**
- `--summary` — output a JSON data summary (shape, columns, dtypes, missing, sample, stats) instead of a text preview.
- `--merge` — attempt to merge multiple files into one DataFrame.
- `--skiprows N` *(single-file only)* — skip the first N rows, then read the next row as the header. Use when the file has leading junk rows (notes, blanks) before any header.
- `--header-row N` *(single-file only)* — treat the 0-indexed row N as the header; rows above N are dropped. Use when the file has multi-row headers (merged cells, sub-headers) and you want one specific row as the column name.
- `--sheet <name|index>` *(single-file only)* — pick an Excel sheet by name or 0-indexed position (default: 0).
- N must be determined by inspecting the actual data (run `data_parser.py` once without flags to see the raw layout). Never assume a fixed N.

**Merge behavior:**
- Identical columns → vertical concat (adds a `source_file` column to indicate each row's origin file).
- ≥50% overlap → horizontal join on shared key.
- No common structure → error (advise analyzing separately).
- `--merge --summary` 模式下 stdout 为纯 JSON，合并方式体现在 JSON 的 `merge_type` 字段（不打印额外文本行，保证机器可读）。

**Formats:** .csv (comma) / .tsv (tab) / .txt (auto-detect delimiter: `,`/`\t`/`;`/`|`) + auto-detect encoding (UTF-8/GBK/GB2312/UTF-16/Latin-1), .xlsx/.xls (first non-empty sheet), .json (array format + 1-level nested objects).

**Column name normalization:** Parsed column names are normalized by `_normalize_col`:
1. Non-word characters (anything except letters, digits, `_`, and whitespace) are replaced with `_`. CJK characters count as word characters and are preserved.
2. Consecutive spaces/underscores collapse into a single `_`.
3. Leading/trailing `_` are stripped, then the name is lowercased.
4. Empty or NaN names become `unnamed`.

| Input | Normalized |
|-------|-----------|
| `Sales Amount` | `sales_amount` |
| `Revenue($)` | `revenue` |
| `销售-额%` | `销售_额` |
| `A/B` | `a_b` |
| `  Total  ` | `total` |
| (blank / NaN) | `unnamed` |

`--x-axis`, `--y-axis`, and transform code must use the **normalized** column names, not the raw header text.

**Error output:** When a `SmartChartsError` occurs, the CLI prints a JSON object to stderr:
```json
{"error": "<message>", "code": <int>, "code_name": "<NAME>", "details": {...}}
```
The `details` field always includes a `suggestion` for recovery. Other exceptions (e.g. `KeyboardInterrupt`) are printed as plain text.

**Error codes:**

| Code | Name | Meaning |
|------|------|---------|
| 1001 | FILE_NOT_FOUND | File path does not exist |
| 1002 | FILE_NOT_REGULAR | Path is not a regular file |
| 1003 | FILE_FORMAT_INVALID | Unsupported file extension |
| 1004 | FILE_SIZE_EXCEEDED | File exceeds 100 MB limit |
| 2001 | DATA_PARSE_ERROR | Parsing failed (encoding, structure, etc.) |
| 2003 | DATA_EMPTY | File or cleaned data is empty |
| 3001 | TRANSFORM_EXEC_ERROR | Transform code execution failed (blacklist/AST/timeout) |
| 3002 | TRANSFORM_NO_RESULT | Transform code did not produce `result` variable |
| 3003 | TRANSFORM_INVALID_RESULT | `result` is not a DataFrame |
| 3004 | TRANSFORM_EMPTY_RESULT | `result` DataFrame is empty |
| 4001 | CHART_GENERATION_ERROR | Chart generation failed |
| 4002 | CHART_TYPE_UNSUPPORTED | Unsupported chart type |
| 4003 | CHART_CONFIG_ERROR | Axis field does not exist in DataFrame |
| 9999 | UNKNOWN_ERROR | Unclassified error |

---

## Chart Generation — CLI Reference

```bash
python {skill_base}/scripts/cli.py \
  <file_path> <chart_type> \
  --title "Chart Title" \
  --x-axis "date" \
  --y-axis "revenue profit" \
  --transform-code "<pandas code>" \
  --skiprows N --header-row N --sheet <name|index> \
  --lang zh|en \
  --output-dir "./output"
```

**Parameters:**
- `file_path` (required) — path to the data file.
- `chart_type` (required) — one of the 21 types listed in SKILL.md.
- `--title` (default follows `--lang` / data language) — chart title.
- `--x-axis` (auto-detected if omitted) — column name for x-axis.
- `--y-axis` (space-separated; defaults to first 5 numeric columns, or first 3 other columns if none numeric) — column name(s) for y-axis.
- `--transform-code` (optional) — LLM-generated pandas code, validated + executed before rendering.
- `--skiprows` / `--header-row` / `--sheet` (optional) — same semantics as `data_parser.py`; passed through to the parsing step so chart generation works directly on messy-header files.
- `--lang zh|en` (optional) — force the chart text language. If omitted, the CLI auto-detects from the data: CJK character ratio > 5% in column names + string cells → `zh`, otherwise `en`. Pass `--lang` only when the user explicitly requests a specific language.
- `--label-col` (optional) — identity column (e.g. name/title). Its values become each point's `name` and appear as the tooltip title. Applies to scatter/bubble/boxplot (boxplot uses it for outlier points). If omitted, an unused string column is auto-detected (columns named 姓名/name/id/... preferred) and the choice is reported in the `assumptions` field of the success output.
- `--color-by` (optional) — color-encoding column for scatter/bubble. Numeric column → continuous coloring via `visualMap`; categorical column → one series per category with legend. Off by default.
- `--output-dir` (default: `./smart_charts_output`) — output directory for HTML files.
- `--charts-file <path>` (multi-chart mode, recommended) — read the `--charts` JSON array from a UTF-8 file instead of a shell argument; avoids shell-escaping corruption when transform code contains CJK text or quotes. Missing/unreadable file returns a structured `FILE_NOT_FOUND` error on stderr (exit 1).

**Output:** On success, prints a JSON object to stdout and exits with code 0:
```json
{"chart": {"success": true, "html_path": "./output/Title_abc123.html", "chart_type": "bar", "title": "Title", "data_rows": 34, "data_preview": [{"name": "兼职", "value": 20}, {"name": "专职", "value": 12}]}}
```
`data_rows` is the number of rows fed into plotting (after transform); `data_preview` is the first 10 rows of the final plotting data (the same data the renderer consumed, with NaN → null). Use it to sanity-check aggregation grain in-band — no need to open the HTML to verify values.
On failure, prints a structured JSON and exits with code 1:
- **File-level errors** (file not found, parse error, etc.): error JSON printed to **stderr**.
- **Chart-level errors** (unsupported type, transform failure, axis field missing, etc.): result JSON with `"success": false` printed to **stdout**.

Both include a `details.suggestion` field for recovery. Other exceptions are printed as plain text to stderr.

**Multi-chart mode:** when generating **2+ charts**, pass `--charts` instead of a positional `chart_type`. The file is parsed once and all charts are generated in a single process (several times faster than repeated single-chart calls):

```bash
python {skill_base}/scripts/cli.py data.csv \
  --charts '[{"type":"bar","title":"Revenue","x_axis":"city","y_axis":["revenue"]},
             {"type":"line","title":"Trend","x_axis":"date","y_axis":["revenue","profit"]}]' \
  --output-dir "./output"
```

- Each item requires `type`; optional per-chart keys: `title`, `x_axis`, `y_axis` (string or array), `transform_code` (per-chart), `label_col`, `color_by`, `width`, `height`.
- A global `--transform-code` (optional) is applied to the DataFrame once before all charts; each chart may also carry its own `transform_code`.
- Output shape: `{"charts": [{...}, ...], "summary": {"total": N, "succeeded": M, "failed": K}}` — each item has the same structure as the single-chart `chart` object (including `success`, `html_path`, and `error.details.suggestion` on failure).
- Exit code 1 only when **all** charts fail; partial failure exits 0 with per-chart errors in the `charts` array.
- Invalid `--charts` JSON (malformed, empty array, or an item missing `type`) prints a `CHART_CONFIG_ERROR` JSON with a `suggestion` to stderr and exits 1.

**Language behavior:** every piece of chart text — title, series names, tooltip labels ("No data" / "无数据"), action buttons ("Save Image" / "Fullscreen" / "保存图片" / "全屏"), scroll hint, footer ("Generated by Smart Charts" / "由 Smart Charts 生成"), and the HTML `lang` attribute — is rendered in a single consistent language. By default that language follows the data; pass `--lang zh` or `--lang en` to override (e.g. when the user explicitly asks for an English chart on Chinese data).

**Overflow behavior:** when data points exceed the zoom threshold (default 15), the HTML enables ECharts `dataZoom` (slider + inside-drag) and a horizontal scrollbar on the chart container. Users can drag the slider, scroll horizontally, or click the fullscreen button to inspect all data points. No agent action needed.

**Inline title editing:** every generated HTML title is `contenteditable`. Users can double-click the title in the browser, type a new name, and press Enter — the ECharts chart title and the saved image filename update immediately. No backend round-trip needed.

---

## Transform Code Generation

When raw data doesn't match the target chart's input format, the LLM should generate pandas code following the template in [SKILL.md](../SKILL.md). The code is validated (keyword blacklist + AST whitelist) and executed in a sandbox before chart rendering.

**Safety rules enforced:**
- Only allowed variables: `df`, `pd`, `np`
- Must produce a `result` variable (pd.DataFrame)
- Do not modify `df` in-place
- No `import`, `open`, `exec`, `eval`, `os`, `sys`, `subprocess`, file I/O, or network calls
- Only safe builtins exposed (`len`, `range`, `sorted`, etc.); `open`/`exec`/`eval`/`__import__` removed
- Execution timeout: 10 seconds
- Max recursion depth: 500

On violation, a `CodeValidationError` is raised with `details.violations` listing the offending keywords or AST nodes, and `details.reason` explaining why.

---

## FAQ — Common Data Issues

**Q: The Excel file has multi-row headers (merged cells, sub-headers). How do I parse it?**

First run `data_parser.py` without flags to inspect the raw layout:
```bash
python {skill_base}/scripts/data_parser.py data.xls
```
Look at the printed `head(5)` to count how many rows are headers. Then re-run with `--header-row N` (0-indexed) where row N is the one you want as column names:
```bash
python {skill_base}/scripts/data_parser.py data.xls --header-row 2
```
Rows above N are dropped. The value of N depends on the actual file — never assume a fixed number.

**Q: After `--header-row`, the columns are still messy (e.g. `score_a`, `unnamed_3`). What next?**

Use `--transform-code` at chart generation to rename columns:
```bash
python {skill_base}/scripts/cli.py data.xls bar \
  --header-row 2 \
  --transform-code "result = df.rename(columns={'unnamed_0':'student_id','unnamed_1':'name','score_a':'homework','score_b':'exam'})" \
  --x-axis student_id --y-axis homework exam
```

**Q: The Excel file has multiple sheets. How do I pick one?**

```bash
python {skill_base}/scripts/data_parser.py data.xlsx --sheet "Sheet2"
# or by index
python {skill_base}/scripts/data_parser.py data.xlsx --sheet 1
```

**Q: Some cells are blank because of merged cells (only the first row of a group is filled).**

Forward-fill in transform code:
```bash
--transform-code "result = df.ffill()"
```

**Q: The data has leading note rows / blank rows before the actual header.**

Use `--skiprows N` to skip the first N rows, then read the next row as the header:
```bash
python {skill_base}/scripts/data_parser.py data.csv --skiprows 2
```

**Q: The chart shows mixed languages (e.g. Chinese data but English buttons, or vice versa).**

The CLI auto-detects the data language and renders all chart text (title, series names, tooltip, buttons, footer, HTML `lang`) in that language. If the auto-detection is wrong (e.g. a Chinese dataset with mostly English column names), force the language explicitly:
```bash
python {skill_base}/scripts/cli.py data.csv bar --lang zh
# or
python {skill_base}/scripts/cli.py data.csv bar --lang en
```
Only pass `--lang` when the user explicitly requests a specific language; otherwise let the data drive the choice.
