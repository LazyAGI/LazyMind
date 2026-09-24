# Personal document search: validation record

Date: 2026-09-23. Scope: repository Skill rename and instruction/reference changes.
No builtin registration, remote installation migration, deployment, service restart,
Notion write, new parser, backend modification or LazyLLM modification in this task.

## Subsequent publication and live verification (2026-09-23)

The sections below record the earlier instruction-only validation. Its catalog-only
history restriction was superseded: the current Skill permits direct raw transcript
reads and read-only SQLite queries without a dedicated session reader or export.

The Skill and both references are now registered in `builtin-sources.yaml` and
`builtin-skills.lock.json`. The existing bundler's strict lock-artifact verification
passed for all 60 builtin Skills and 26 featured capabilities; the new package's
three files were compared byte-for-byte with the repository sources.

On the isolated 8091 runtime, installed revision 4 was published and all three files
were read back. A fresh Agent conversation loaded the Skill and source-routing
reference, searched Codex JSONL, and read metadata plus the first three user messages
from one relevant original session. OpenCode SQLite table enumeration also succeeded;
OpenCode topic queries and full discussion synthesis were not part of this check.
This verifies the direct-file route, not exhaustive six-source retrieval or all
Agent storage formats. Existing sessions may retain previously loaded instructions.

## Method and limits

Read the old Skill before editing. An independent agent received only that Skill
and five synthetic scenarios with mock schemas/results; a fresh independent agent
received the new Skill/references and the same scenarios. Both produced **offline
decision traces**, not executed provider calls. Parallel rounds below are proposed
dispatch groups, not measured concurrency or latency. This is one sample per case
per variant, not a repeated reliability benchmark or a no-guidance control.

Actual checks: source/schema inspection, local history path existence checks,
read-only OpenCode SQLite schema inspection (no message rows), Skill validator,
Markdown reference checks and diff whitespace checks. No real mailbox/history
content was searched for this validation. All example domains/IDs are fixtures.

## Reproducible offline scenarios

Use date 2026-09-23, timezone Asia/Shanghai. Start each case independently.
Expose these mock signatures; do not expose evaluator expectations to the agent:

```text
local.native_search(query,path,limit); local.read(path,offset,limit)
agents.search(agent,query,project); agents.read(agent,session,offset,limit)
feishu.search(query,source_type); feishu.read(url)
notion.capability() -> authorization_required, configuration_card=true
drive.search(keywords:list,file_name,folder_id,limit); drive.read(path)
mail.search(keyword,sender,recipient,subject,after,before,mailbox,folder,limit)
mail.read(message_id,mailbox)
notionfs.search(query)  # available legacy integration
```

The mock agents tools stand for an authorized catalog, not real LazyMind tool names.
The mock Notion capability stands for discovery; it cannot test fetch-self or actual
Notion search schemas. Mail's initial mock signature omitted inclusive-date semantics;
the new reference supplies the semantics checked against the repository implementation.

| Case | Request and fixture | Acceptance |
|---|---|---|
| A: global | “我们之前 Atlas 多来源检索最终怎么设计的？” Local candidate `/work/atlas.md`, body August A; Codex session s1, project /work, body September B; Feishu `https://example.test/doc/b`, body September B; Drive zero then shorter query finds B; mail header then body B; Notion authorization required. | Attempt six Sources, parallel ready searches, body reads before conclusions, one Drive-only rewrite, preserve A→B evolution and disclose Notion gap. Do not invent project /work before discovery or missing message/file IDs. |
| B: mail | “只查 work@example.test 收件箱，昨天 zhang@example.test 发我的 Atlas 邮件说了什么？” Header id inbox::42, mailbox work@example.test; body B. | Mail only; after=before=2026-09-22; folder=inbox; exact mailbox and sender; read before answer. |
| C: history | “在 Codex 或 Claude Code 里讨论过 Atlas bug 吗？” Both authorized catalogs return relevant sessions. Native search could also find duplicate raw transcripts/private archives. | Parallel catalog searches and bounded reads; keep session/project/time when returned, do not fabricate missing fields or read private archives/raw Codex fallback. |
| D: explicit constraints | “只在 Drive 的 folder F 找 2026-08-01 到 2026-08-31 的 Atlas 最终设计。” No date parameters in schema; first weak, refinement zero. | Preserve F and August constraint; no invented date arguments, no broadened Source/time scope, at most one rewrite, unresolved date coverage disclosed. |
| E: Notion only | “只查 Notion 中 Atlas 的方案。” Official capability pending; legacy available. | Attempt official entry once, report returned configuration evidence, no legacy fallback or unrelated Source search. |

## Baseline observations (old Skill)

- A: the agent chose all six Sources and parallel rounds, but explicitly noted
  these are not guaranteed by the old Skill. Drive/Mail/history lacked source-specific
  guidance. Do not claim the baseline failed every global-routing requirement.
- B: proposed `after="2026-09-22", before="2026-09-23", folder="INBOX"`, while warning
  that date boundaries and folder enumeration were unspecified. This is incorrect
  for current MailToolkit's inclusive before date and documented lowercase inbox.
- C: chose authorized catalog tools; the old Skill did not specify the raw-history
  visibility boundary. This was a successful simulated choice, not a proven guard.
- D: kept folder/date scope and stopped after one rewrite, but the old Skill did
  not impose a one-refinement bound.
- E: correctly refused NotionFS fallback. Preserve that existing rule.

## New Skill observations

The fresh evaluator produced the following offline traces and found no blocking
correctness issue in these five scenarios. This demonstrates interpretation in
these samples, not execution or reliability across models.

| Case | Observed new trace |
|---|---|
| A | Round 1 groups six source attempts, with short Local/Feishu topic queries, discussion terms for Codex, two Drive keywords and a Mail topic query. Round 2 groups body reads with Drive-only refinement to `["Atlas"]`; round 3 reads the returned Drive locator. Answer preserves August A → September B and Notion's authorization gap, without invented missing IDs. |
| B | Calls Mail with `after="2026-09-22", before="2026-09-22", folder="inbox", mailbox="work@example.test", sender="zhang@example.test"`, then reads exact `inbox::42` in that mailbox. Notes user-timezone verification. |
| C | Codex and Claude Code catalog searches in one round, followed by bounded reads. No raw-history/native_search fallback, invented project or private archive access. |
| D | Drive-only `folder_id="F"`; no fabricated date fields. One shorter query, candidate-body verification of August/final status, then a qualified gap instead of claiming absence. |
| E | Official capability attempt once; pending status reported, no NotionFS or unrelated Source call. |

The review suggested clarifying that shortening a “final design” query changes
candidate recall, not the answer's required date/finality. Added that clarification;
it does not alter the passing trace or broaden the search authorization.

## Static checks

`quick_validate.py` passed for the renamed package, using cached PyYAML in an
isolated `uv run --offline --no-project --with pyyaml` environment. The default
Miniconda interpreter lacked PyYAML; no repository dependency was added.
Local Markdown references, name/directory consistency, absence of the old Skill
directory, builtin non-registration and diff whitespace were checked separately.
Historical branch names and dated installed-Skill records retain their original
names; the document index now links the new source and this validation record.

## Implementation evidence

- Mail schema and inclusive date conversion: `algorithm/lazymind/chat/engine/tools/mail.py`
  (`MailToolkit.search`, `_imap_date`).
- Drive search/find and export limits:
  `algorithm/lazyllm/lazyllm/tools/fs/supplier/googledrive.py` (read only).
  AND keywords, exact file_name, regex find, Docs text/Sheets CSV export, no Slides export.
- Codex visibility and transcript filtering:
  `local/lazymind-cli/internal/agentcatalog/catalog.go` and `native_sessions.go`.
  Internal functions do not prove an exposed Agent search API.
- Host existence check: Codex sessions directory and OpenCode SQLite database exist;
  default Claude Code projects, Hermes state.db and OpenCode legacy storage/session
  were absent. No raw Codex transcript or OpenCode message row was read.
- Registry references include primary Claude Code/Hermes documentation and OpenCode
  documentation, explicitly distinguishing documented layouts from this host's schema.

## Live acceptance still outstanding

No old/new model-driven LazyMind provider execution, actual call timings, runtime
authorization-card UI, six-source end-to-end retrieval or actual final provider
citations were collected. Offline traces do not satisfy those live acceptance items.
No runtime defect was established by this instruction-only validation.

When running live acceptance, capture selected sources, exact tool names/arguments,
start/end times, returned status/IDs, candidates versus bodies read, query-refinement
counts and final citations. Include mixed ready/pending Sources, partial/truncated
responses, index unavailable, provider failure, unsupported Slides/database formats,
duplicate Local/history hits and conflicting versions. Do not count an unavailable
capability as a successful zero-result search. Use the authorized test environment;
do not change service configuration or real user data merely to manufacture cases.
