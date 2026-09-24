# Multi-source document search: implementation and verification

## PR history cleanup: 2026-09-23

PR #771 is rebuilt on current main, preserving the already merged #710 Workspace
and #737 MCP OAuth implementations rather than replaying their original commits.
The remaining diff contains only the experiment's additional behavior. The mainline
ModelScope skill sources, Skill Retrieval, remote Skill readers, MCP diagnostics,
and cloud file read limits remain intact. OpenAPI output is regenerated from the
combined registry. The LazyLLM dependency is rebased on main with only unified
Feishu search and persistent tool configuration/activation changes, hosted in the
Yuang-Deng fork rather than the former YuZou experiment branch.

## Skill source update: 2026-09-23

The repository Skill is now
[`personal-document-search`](../skills/search/personal-document-search/SKILL.md),
covering six personal information Sources. It is registered in the builtin source
catalog and lock, and the complete package passes strict bundle verification.
The updated installed package was published on 8091 and direct Codex transcript
retrieval was verified in a fresh conversation. This does not migrate other users
from installed `enterprise-search` packages.
See the [validation record](personal-document-search-validation.md) for the earlier
offline checks, subsequent live verification and remaining coverage limits. Older names and runtime
results below describe their dated snapshots.

## Branch consolidation: 2026-09-21

The experiment is now maintained on `dya/enterprise-search-poc`, tracking the
same branch in the `Yuang-Deng/LazyMind` fork. The local
`dya/notion-mcp-search-experiment` branch is retained as a backup. The old POC
commit is preserved in the merged history; the current workspace-based file
authorization and Notion MCP implementation remain the experiment baseline.
Machine-specific model configuration is excluded from this update. This branch
consolidation does not restart services or add chat-history retrieval features.

## Status update: 2026-09-18, Notion MCP experiment

8091 now runs the complete code in `/Users/dengyuang/workspace/LazyMind_dev`
on `dya/notion-mcp-search-experiment`. The prior experiment was snapshotted and
MCP OAuth changes from LazyMind #737 / LazyLLM #1330 were integrated locally.
The older checkout and verification sections below describe historical snapshots.
They are not the current deployment or Skill installation status.

The installed `enterprise-search` Skill now routes Notion through the official
OAuth MCP connection. The legacy NotionFS connection remains available for rollback,
but its Chat exposure is disabled. The current account's `fetch self` reports ordinary
search and fetch as available, and AI search as `plan_required`; only `notion-search`
and `notion-fetch` are enabled in LazyMind.

A real model-driven 8091 conversation successfully loaded the Skill, searched Notion,
fetched two pages, read a local project document, and produced an answer with source
links. This verifies the Notion + local path, not Feishu, Windows or PDF/Office coverage.
The model did not perform the instructed `fetch self` preflight; account capability
was verified separately through the product OAuth adapter and MCP client.

Observed limitations: each generic MCP call still requires runtime approval; a large
Notion tool result was spilled as a long single-line payload and caused repeated reads;
the answer reused historical deployment claims as present-tense facts. Completion of
this run does not establish answer correctness, exhaustive retrieval or stable latency.
The actual configured model was Qwen/Qwen3.8-Flash-Next; this integration did not change
model settings. Both 8091 and 8090 returned HTTP 200, and the 8091 session was rejected
by 8090 with HTTP 401. Runtime data and logs remain under the isolated 8091 root.

## Checkout and baseline

Implementation is in the independent `lazymind-multi-source-search` worktree on
`dya/multi-source-document-search`. The parent HEAD remains `264cf7b7`; upstream
`main@35ea7181` is merged into the index/worktree, with conflicts resolved but no
merge commit. The staged upstream integration and unstaged feature changes are
separate. LazyLLM has its own worktree/branch at `47a43df9`, with uncommitted changes.
No service was deployed or restarted, and no commits or pushes were made.

The upstream integration includes five commits (Organizer recovery, search budgets,
external capabilities, internal proxy exclusions, and document learning). Conflict
resolution preserves the authorization branch's declarations, projects and grants,
and the upstream APIs, migrations and generated translations. The migration down
file explicitly restores the shared SQL dialect after the grant-table drops.

## Interfaces

- LazyMind `native_search(query, match="either", path="", kind="any", limit=30)`:
  macOS Spotlight / Windows Search, metadata and absolute paths only. Empty scope
  means the system index. `kind` is any/file/directory. Maximum 100 hits, eight-second
  search deadline and 1 MiB subprocess search output. Outcomes distinguish success,
  partial results, error and index unavailability. Only host-filesystem tool sets on
  supported platforms expose it; no HTTP bridge or container-to-host index access.
- Shared `FeishuFS` / `FeishuWikiFS.search(query, space_id="", node_id="",
  page_size=20, page_token="", *, source_type="all")`: all/document/wiki, one page
  of 1–20 results. All ignores implicit Wiki scope; explicit scope narrows it to Wiki.
  Document mode rejects Wiki scope; a node requires an explicit space. Wiki mode
  honors an existing configured space. Return type is now a dictionary containing
  source, source_type, scope, results, has_more, page_token and coverage, replacing
  the old list. Provider result metadata and URLs are preserved.
- The combined endpoint selects sources by including doc_filter, wiki_filter or both
  in the request. No result-side type filtering is performed. See the
  [official Lark CLI request builder](https://github.com/larksuite/cli/blob/main/shortcuts/doc/docs_search.go).
- Scoped Wiki requests use `/wiki/v1/nodes/search`, correcting the previous v2 path
  according to the [official SDK request definition](https://github.com/larksuite/oapi-sdk-python/blob/main/lark_oapi/api/wiki/v1/model/search_node_request.py).
- Local text continues through LazyLLM read/glob/grep. Existing read_file_resource
  and search_file_resource accept local PDF/supported Office absolute paths in a
  host-filesystem runtime, declaring read access before parsing. Existing safe input
  staging supplies parsers a private copy. Resource-only readers remain restricted;
  no additional parser or directory authorization is introduced.
- The enterprise-search Skill chooses sources, source_type, readers, pagination and
  actual citations. File discovery is not evidence of body content.

## Verification (2026-09-17)

- 157 targeted LazyMind tests passed in the existing Chat container, loading this
  worktree and its LazyLLM with PYTHONPATH. Coverage includes native discovery,
  source scope/truncation/empty/error states, tool declarations and visibility,
  document read authorization, resource-reader regression, tool execution and
  upstream search result budgets.
- 81 Feishu tests plus three subtests passed in the same container, covering source
  filters, one-page continuation, explicit and implicit Wiki scope, OAuth-only
  behavior, provider errors, supplier schemas and existing URL resolution/read tests.
  These use provider mocks, not live OAuth requests.
- Core migrate and localworkspace tests passed in the existing Go container using
  a temporary source copy. Migration verification also passed with PostgreSQL
  enabled, using disposable test databases; SQLite is covered by the same package.
- Core OpenAPI tests and syntax checks for the three merged frontend locale files passed.
- Python lint, whitespace/conflict checks and generated error-code checks passed.
- Real macOS host tool execution: Spotlight found
  `/Users/dengyuang/workspace/lazymind/docs/workspace-file-authorization.md` within
  the specified docs scope; LazyLLM read returned its first three lines and
  next_offset=4. A separate result-limited query returned partial/result_limit, and
  a deliberately absent keyword returned ok with no hits. No index rebuild was run.

## Pending acceptance

- Live Feishu OAuth searches for ordinary documents/Wiki, pagination and body reads.
  The deployed request returned Feishu code 99991679: the existing user grant lacks
  `search:docs:read`. Backend default OAuth scopes and frontend scopes now include
  this permission, and the setup guide shares the frontend scope list. The Feishu
  app must enable the permission and the user must reauthorize the existing connection.
  Scope-list parity and generated authorization URL checks passed. The existing
  OAuth owner suite has 23 passes and 7 failures, identical with the old scope list.
- Full Chat + Skill answers combining local and Feishu evidence, including a failed
  or disconnected source. 8091 now runs this worktree; real Chat native search,
  text reading and a source-path answer passed. Skill installation remains pending.
- PDF/Office parsing through real parser services and the complete Chat flow.
  Authorization/order/cache regression tests use deterministic parser fixtures.
- Real Windows Search execution. Mock and bounded-subprocess tests do not replace it.

The existing parser's format/size/service limitations remain. A missing file,
OS permission denial, unavailable index, parser failure or provider authorization
failure must remain visible rather than being reported as successful empty search.
