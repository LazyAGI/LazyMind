# 论文 Workflow 对接

面向接入综述、阅读心得和科研 PPT 的同事。基于 [PR #863](https://github.com/LazyAGI/LazyMind/pull/863) 的论文能力，增加准备确认、四步拓展与独立报告。

论文流程提供主题/种子初步调研、明确确认方向后的四步多来源拓展、分层阅读与质量检查、版本化材料交接和全文读取。**综述、心得、PPT 目前接 Mock，只返回材料接收结果，正式生成能力由下游 Workflow 接入。**

## 1. 接入流程

```text
论文集选文 / 输入研究方向 → 后台初步调研 → 回答必要问题 → 多方向选择（单方向自动继续） → 四步论文拓展 → 冻结所选轮次 → 新 Chat 接收综述任务
论文集选文 → 新 Chat 接收阅读心得 / PPT 任务
PPT 选择先写心得 → 保存心得正文 → 携原论文清单进入 PPT 步骤
```

| 任务 | 当前 Workflow | 交接输入 |
| --- | --- | --- |
| 论文拓展 | `paper_expansion` | `expansion_id` |
| 综述 | `paper_workflow_mock` | `ReviewWorkflowInput` |
| 独立心得 / 直接 PPT | `paper_workflow_mock` | `PaperTaskInput` |
| 心得接续 PPT | `paper_ppt_with_reflection_mock` | `PaperTaskInput`，后续追加已保存的 `reflection_markdown` |

业务启动统一使用 `POST /api/core/academic/workflow-launches`，`kind` 为 `expansion / review / reflection / ppt`，`request_key` 为同次请求的稳定幂等键。从论文集启动时传 `dataset_id`、`document_ids` 和 `user_instruction`；PPT 可传 `reflection_first: true`。

新建 expansion 或“先拓展后综述”返回 `expansion_id` 和准备状态，尚无 `conversation_id`。通过 `POST /academic/expansions/{id}/preparation` 提交 `action=answer/revise/retry`、准备 `revision` 和稳定 `request_key`；通过 `POST /academic/expansions/{id}:confirm` 提交同版本、方向选择和预算才启动正式 Chat。过期版本拒绝，重复确认返回同一正式任务。快速/均衡/深入分别为准备 2/3/5 分钟与正式 10/20/30 分钟，等待用户不计执行预算。

正式步骤为准备计划、拓展筛选、关系分析、脉络与阅读建议。结果仍是四字段 `ReviewWorkflowInput`，证据/阅读覆盖/质量/复核/用量独立在 `expansion_report` 和历史轮次报告。普通拓展按需取文，综述目标为最终清单补取；下载、解析、身份匹配和实际已读分别统计。分析不自动入库。

从已有拓展成果启动综述：

```json
{
  "kind": "review",
  "expansion_id": "<拓展记录 ID>",
  "expansion_version": 1,
  "paper_ids": ["<论文 ID 1>", "<论文 ID 2>"],
  "request_key": "<本次请求唯一键>"
}
```

返回新任务的 `conversation_id` 和 `handoff_id`。所选论文须属于该轮成果，至少两篇不同论文的正文通过身份和解析质量检查（相同 PDF 哈希不重复计数）。直接从论文集发起 `review` 会先运行拓展，满足条件后再建立综述 Chat。

## 2. 输入结构

以下为核心字段，完整可选题录字段见 [PaperRef](../../backend/core/academic/content.go)。

```ts
interface PaperRef {
  paper_id: string;          // 服务端论文身份，不是文档或论文集条目 ID
  title?: string;
  authors?: string[];
  abstract: string | null;
  year?: number;
  venue?: string;
  doi?: string;
  arxiv_id?: string;
  paper_url?: string;
  pdf_url?: string;
  document_id?: string;      // 原文档 ID
  source_provider?: string;
  source_version?: string;  // 沿用服务端返回值，如 v2
}

interface WorkflowPaper {
  paper: PaperRef;
  is_seed: boolean;
  is_important: boolean;
  seed_relation?: string;
  direction_relation?: string;
  key_contribution?: string;
}

interface ReviewWorkflowInput {
  user_instruction: string;
  candidate_directions: string[];
  selected_directions: string[];
  papers: WorkflowPaper[];
}

interface PaperTaskInput {
  user_instruction: string;
  papers: PaperRef[];
  notes?: { id: string; paper_id: string; title: string; content: string }[];
}
```

- 综述输入保留用户要求、研究方向、论文清单、种子、重点、关系和贡献；关系与贡献字段均为纯文本字符串。
- 心得和 PPT 的 `papers` 是原始 `PaperRef[]`。心得及“先心得后 PPT”会附带已有笔记。
- 交接内容按创建时冻结，后续拓展不会改写已交接任务。沿用收到的论文身份、题录和来源版本。
- `handoff_id` 属于外层任务上下文，不加入 `PaperRef` 或上述业务输入结构。

## 3. 获取上下文与全文

### 在下游 Workflow 中

复用 [现有工具](../../algorithm/lazymind/chat/service/academic_handoff.py)，在 `workflow.yaml` 和对应步骤中声明：

```python
from lazymind.chat.service.academic_handoff import (
    academic_handoff_context,
    academic_read_paper,
)
```

```python
context = academic_handoff_context()  # 当前 Chat 的 handoff_id、kind、input 等
page = academic_read_paper(paper_id=paper_id)
page = academic_read_paper(cursor=page["next_cursor"])  # 有下一批时继续
```

工具自动取得当前 Chat 的任务身份与鉴权。保留现有隐藏输入 `task_context`（`text`，内容为任务定位信息 JSON），沿用 Workflow 材料绑定；论文、用户要求和笔记按需通过 `academic_handoff_context()` 取得，不需要模型重抄材料或用户手填内部 ID。拓展 Workflow 使用自己的 `academic_read_paper(expansion_id, paper_id=..., cursor=...)`，见 [拓展工具](../../workflows/paper_expansion/scripts/tools.py)。

`academic_handoff_context()` 返回当前交接的 `handoff_id`、任务类型、导航信息及冻结的 `input`，不返回内部来源快照。提交和正文读取工具使用摘要取得当前 Chat 的交接身份；全文续读仍携带当前交接 ID 校验游标，不能混入其他任务的正文。

### 直接调用 HTTP

使用现有用户登录鉴权，JSON 请求携带 `Authorization: Bearer <token>` 和 `Content-Type: application/json`。

| 用途 | 接口 |
| --- | --- |
| 查询当前论文任务上下文 | `GET /api/core/academic/chat-context?conversation_id=<Chat ID>` |
| 查询页面状态和交接身份 | `GET /api/core/academic/chat-context?conversation_id=<Chat ID>&view=summary` |
| 读取正文 | `POST /api/core/academic/paper-contents:read` |

摘要视图只返回类型、ID、版本、状态及适用的导航字段，不包含 `input` 或 `contents`。默认视图在交接任务中额外返回 `input`；服务端来源快照保留用于全文权限和版本校验。两种视图采用相同的用户、Chat 归属和论文集权限检查。

下游任务首次读取：

```json
{"handoff_id": "<交接 ID>", "paper_id": "<论文 ID>"}
```

拓展任务首次读取：

```json
{"expansion_id": "<拓展记录 ID>", "paper_id": "<论文 ID>"}
```

后续读取只传服务端返回的原样游标：

```json
{"cursor": "<上一批 next_cursor>"}
```

关键返回字段：

| 字段 | 使用方式 |
| --- | --- |
| `status` | `processing`：稍后重试首次请求；`ready`：读取正文；`failed / unavailable`：展示 `warnings`，不要当作全文成功 |
| `content_level` | `fulltext / none`，明确实际获得的内容级别 |
| `pages` | `[{"page": 1, "text": "正文"}]`，每批最多 5 项；`page=0` 表示可靠页码未知 |
| `next_cursor` | 非空则续读，空字符串表示读完 |
| `content_hash / source_version` | 标识当前正文内容及来源版本 |

HTTP 200 也可能处于 `processing`，应同时检查 `status`。读取正文不会自动将外部论文导入论文集。服务端校验当前用户、任务选文范围及正文版本：无效参数/游标返回 400，任务不可访问返回 403/404，论文不属于任务返回 422，正文版本不可用返回 409；409 后重新获取任务上下文，不拼接不同版本正文。

## 4. 接正式生成能力时改哪里

- **启动路由**：[workflows.go](../../backend/core/academic/workflows.go) 的 `createPaperHandoff` 目前选择两个 Mock Workflow；接入正式 Workflow 时同步调整这里。
- **下游步骤**：参考 [通用交接包](../../workflows/paper_workflow_mock) 和 [心得接续 PPT 包](../../workflows/paper_ppt_with_reflection_mock)，保留输入绑定与上述读取工具，替换 Mock 步骤的生成逻辑。
- **输出**：正式成果沿用现有 Workflow artifact 发布机制。当前 `academic_handoff_submit()` 只确认冻结输入并发布 `handoff_receipt / ppt_request`，返回 `awaiting_downstream`，不代表正式成果完成。通用回执展示任务、要求和论文标题；心得接续 PPT 保留可折叠查看的结构化输入。
- **心得接续 PPT**：读取已保存的 `reflection_markdown` 和原论文清单；当前 Mock 的心得正文也是占位内容，正式接入时需一并替换。

`POST /academic/mock-handoffs/{handoff_id}:submit` 的业务结果包含 `kind`、`status`、`feedback`、`input`。论文清单只位于 `input.papers`，没有顶层 `papers`；仅心得接续 PPT 携带非空的 `reflection_markdown`。重复提交使用同一份冻结材料。拓展详情中的 `handoffs` 只返回任务导航字段，不能用该列表代替任务上下文。

完整接口定义见 [academic OpenAPI](../../backend/core/openapi_academic_workflows.go)，通用 Workflow 写法见 [格式规范](../workflow-format.md)。

调研深度（快速／标准／深入）在启动任务时固定，补充范围、重试和确认方向时沿用。深度控制检索广度、阅读和证据核验程度，不承诺整轮耗时，也不按分钟截断研究；单次请求仍有超时及有限重试，调查保留轮次和工具调用上限。候选方向根据真实材料确定，最多 5 个、不设数量下限。只有一个有效方向且没有待补充问题时自动开始正式拓展；多个方向等待用户选择，没有有效方向时说明材料缺口。
