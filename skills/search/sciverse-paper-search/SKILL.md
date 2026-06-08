---
name: sciverse-paper-search
description: Use this skill for scientific literature search, evidence retrieval, paper metadata screening, and cited research synthesis with Sciverse. This LazyLLM-adapted version supports SciverseSearch search, meta_search, meta_catalog, and get_content only; it does not assume full Sciverse MCP resource or attachment APIs are available.
---

# Sciverse Paper Search Skill

## Overview

Use this skill when the user needs scientific literature retrieval, paper metadata screening, citation-ready evidence, or a research synthesis grounded in Sciverse search results.

This skill is adapted to the current LazyLLM `SciverseSearch` implementation. It must only rely on the currently supported tool capabilities:

- `sciverse_search.search`
- `sciverse_search.meta_search`
- `sciverse_search.meta_catalog`
- `sciverse_search.get_content`

Do not assume Sciverse MCP tools, resource APIs, binary attachment downloads, figure/table downloads, DianShi, or SeqStudio capabilities are available unless the runtime explicitly exposes those tools.

## When To Use

Use this skill for:

- Finding scientific papers on a research topic.
- Retrieving citable evidence snippets for a scientific question.
- Screening papers by year, venue, DOI, author, title, or metadata fields.
- Building paper lists for literature reviews.
- Reading fuller text for selected Sciverse results when `doc_id` is available.
- Producing cited summaries, comparisons, or evidence tables from Sciverse results.

Do not use this skill for:

- Downloading paper images, figures, tables, PDFs, or binary resources.
- Chemical retrosynthesis, molecule/reaction search, or DianShi workflows.
- Protein sequence/structure annotation or SeqStudio workflows.
- Claims that require full-text access when only abstracts or snippets are available.

## Available Tool Capabilities

### `sciverse_search.search`

Use this for normal Agent retrieval.

Recommended defaults:

```text
query=<research question or paper topic>
topk=5
search_type="agentic"
include_content=true
```

Use `search_type="agentic"` when the user asks a natural-language scientific question and needs evidence passages.

Use `search_type="meta"` when the user mainly needs paper metadata. You may pass `year_from` and `year_to` for year constraints.

Current implementation notes:

- `topk` is capped at 10.
- Results are normalized to title, url, snippet, source, and `extra`.
- `extra` may include `doc_id`, `doi`, `year`, `venue`, `authors`, `score`, `chunk_id`, `page_no`, `offset`, and `content`.

### `sciverse_search.meta_search`

Use this for advanced metadata search, filtering, pagination, and paper-list tasks.

Important constraints:

- Do not use `query` together with `sort`.
- Do not use `cursor` together with `page > 1`.
- `page_size` is capped at 200.
- `freshness_boost` must be `NONE`, `MILD`, or `STRONG`.

Useful parameters:

```text
query
filters
sort
fields
page
page_size
cursor
freshness_boost
include_content
year_from
year_to
```

Use `year_from` and `year_to` for simple publication-year filtering.

### `sciverse_search.meta_catalog`

Call this before constructing complex filters or sort clauses if you are unsure which fields and operators are supported.

Use:

```text
include_sample_values=false
```

Set `include_sample_values=true` only when enum-like sample values are needed.

### `sciverse_search.get_content`

Use this to read fuller text for one search result.

The current implementation:

1. Looks for `doc_id` in the item or `item.extra.doc_id`.
2. Calls Sciverse `/content` with `doc_id`.
3. Supports chunked reading with `offset` and `limit`.
4. Falls back to `extra.content`, `snippet`, or URL fetching when `/content` is unavailable.

Do not claim full text was read unless `get_content` actually returns fuller content. If the result only contains an abstract or snippet, say that the analysis is based on metadata/snippets.

## Workflow

### Phase 1: Clarify Search Intent

Classify the user's request:

- Evidence answer: use agentic search.
- Paper list or screening: use meta search.
- Field-specific filtering: call meta catalog first.
- Deep literature review: combine agentic search and meta search.
- Read one selected paper: use `get_content` on a selected result.

### Phase 2: Retrieve Papers

For natural-language evidence questions:

```text
sciverse_search.search(query="<question>", topk=5, search_type="agentic", include_content=true)
```

For paper screening:

```text
sciverse_search.meta_search(
  query="<topic>",
  fields=["title", "doi", "doc_id", "abstract", "author", "publication_published_year", "publication_venue_name_unified"],
  page_size=25,
  year_from=<optional>,
  year_to=<optional>
)
```

For precise filters:

1. Call `sciverse_search.meta_catalog`.
2. Build filters only from supported fields/operators.
3. Call `meta_search`.

### Phase 3: Inspect and Read

For the most relevant results:

1. Extract title, DOI, year, venue, authors, doc_id, and snippet/content.
2. If the user needs deeper analysis, call `get_content` on selected items.
3. Use `offset` and `limit` for chunked reading when needed.

Example:

```text
sciverse_search.get_content(item=<selected_result>, offset=0, limit=2000)
```

### Phase 4: Synthesize With Source Discipline

When answering:

- Separate confirmed full-text evidence from abstract/snippet-only evidence.
- Cite papers using title, year, venue, DOI, and doc_id when available.
- Do not invent bibliographic fields.
- If Sciverse returns limited content, state the limitation.
- For literature reviews, group papers by theme, method, dataset, finding, limitation, and open question.

## Output Patterns

### Paper Search Results

Use a compact table:

```text
| Paper | Year | Venue | Why relevant | DOI / doc_id |
```

### Evidence Answer

Use:

- Short answer.
- Evidence bullets with paper identifiers.
- Caveats about snippet/full-text availability.
- Suggested next searches if coverage is thin.

### Literature Review

Use:

- Search strategy.
- Included papers.
- Thematic synthesis.
- Method and evidence comparison.
- Limitations and open questions.
- Citation table.

## Safety and Limitations

- Do not promise resource, attachment, figure, table, or PDF downloads.
- Do not use unsupported Sciverse MCP tool names.
- Do not fabricate citations, DOI values, doc IDs, authors, or venues.
- If authentication fails, tell the user Sciverse API access may need a valid API key or dynamic auth entry.
- If `get_content` falls back to snippets, clearly label the source as snippet/abstract-based.
