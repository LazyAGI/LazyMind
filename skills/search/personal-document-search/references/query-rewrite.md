# Source-aware query rewriting

Extract topic/entity, project, person, quoted phrase, time, filename and scope.
Remove wrappers such as “帮我找一下 / 我记得 / 之前是不是”; preserve distinctive names,
code symbols and exact phrases. Translate supported constraints into parameters,
not a long query string. Do not invent filter fields absent from the current schema.

Think in terms of `{source, query, filters, scope, search_mode}`; this is a reasoning
aid, not a tool schema or a class to implement. One request gets one appropriate
initial query per Source, not the same set of subquestions sent to every Source.

## Example: one need, different queries

User: “我们之前 Enterprise Search 多来源检索最终怎么设计的？”
With no source hint, attempt all six Sources. The project name remains an exact
search entity even though this skill is named personal-document-search.

| Source | Initial representation | Why |
|---|---|---|
| Local Files | `query="Enterprise Search 多来源检索"` | Short literal keywords. If a project path is already known, scoped grep/read instead of OS index rediscovery. |
| Agent Conversations | `query="Enterprise Search 多来源检索 最终设计"` using direct history-file search or an available history tool | Discussion/decision terms can help find conversations. Tool-specific project/session filters follow its actual schema. |
| Feishu | `query="Enterprise Search 多来源检索", source_type="all"` | Topic query, Docs + Wiki unless the user narrows it. |
| Notion | `query="Enterprise Search 多来源检索"` after official MCP preflight | Use available search mode and actual filter schema; retain exact project/entity names. |
| Google Drive | `keywords=["Enterprise Search", "多来源检索"]` | Few high-value conjunctive terms, not every word in the question. |
| Mail | `keyword="Enterprise Search 多来源检索"` | No invented sender/date/mailbox constraints. Explicit constraints go in structured fields. |

These are representations, not literal cross-provider tool calls. Inspect actual
schemas and use only supported argument names. Keyword backend matching differs:
if combined terms overconstrain recall, refine only that Source.

## Filename, person and time constraints

- Drive exact filename: use `file_name`; if filename is the only clue, use bounded
  `find` with an escaped regex instead of inventing empty keyword behavior. Preserve
  user-provided folder_id/drive_id. Do not turn a human folder name into an invented ID.
- Mail sender/recipient expects an address filter. Use a known address; if only a
  display name is supplied, resolve through an available authorized capability or
  ask when ambiguous. Do not invent an address or claim exact person matching from
  a body keyword. A literal name filter, if supported, has limited matching coverage.
- Resolve relative dates against the user's timezone/current date. In MailToolkit,
  on 2026-09-23 in Asia/Shanghai, “昨天” is `after="2026-09-22", before="2026-09-22"`;
  “上个月” is `after="2026-08-01", before="2026-08-31"`. Both ends are inclusive.
  Preserve a named mailbox and use `folder="inbox"` for received mail. If backend
  day boundaries differ from the user's timezone, check returned message timestamps
  and explain any unresolved coverage instead of asserting precise day coverage.
- No supported date/filter field: retain the constraint for candidate verification
  using returned metadata or body evidence. Do not add nonexistent arguments or
  substitute modified time for the date of a decision. If verification is impossible,
  report the limitation; do not silently drop the user's constraint.

For “只查 work@example.test 收件箱，昨天 zhang@example.test 发我的 Atlas 邮件说了什么？”:

```text
Mail only
search(keyword="Atlas", sender="zhang@example.test",
       after="2026-09-22", before="2026-09-22",
       mailbox="work@example.test", folder="inbox")
read(message_id=<returned folder::UID>, mailbox=<returned mailbox>)
```

## One targeted refinement

If Drive initially searches `["Enterprise Search", "source routing", "final design"]`
and returns zero/weak results, refine once to `["Enterprise Search", "source routing"]`
or a verified alias. Do not rerun Local, Mail or other successful Sources.

Keep exact phrases and user-specified source, folder, mailbox, project and date
boundaries. Only Agent-inferred qualifiers/scope may be relaxed. Change search mode
only when the provider actually supports it. Pagination of the same query is not
a refinement; continue it only when needed for evidence, preserving query and scope.
Shortening recall keywords does not relax answer requirements: an August “final
design” request still needs evidence of an August final decision even if a query
is shortened to the project name. Keep any already verified initial evidence.
After one rewrite, stop and report remaining gaps. Configuration, authorization,
unavailable index and provider errors require their existing handling, not query
rewrites or legacy-provider fallback.
