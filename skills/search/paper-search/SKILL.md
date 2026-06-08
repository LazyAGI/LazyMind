---
name: paper-search
description: "Primary skill for searching, retrieving, and reading academic papers from arXiv."
---

# Paper Search Skill

## Overview

This skill provides a streamlined workflow for searching and reading academic papers from arXiv. You must default to using the system's native tools for searching and reading. Use local scripts only for specific formatting tasks (like BibTeX) or as a fallback mechanism.

## Available Tools & Priority

1. **`arxiv_search` (Primary)**: Use this first for querying papers by keyword, author, or ID.
2. **`url_fetch` (Primary)**: Use this to read abstracts and full-text HTML papers.
3. **`run_script` (Optional/Fallback)**: Use this *only* to generate BibTeX citations OR if `arxiv_search` fails to return valid results. This tool returns a dictionary.

---

## Workflow & Tool Usage Guide

### Phase 1: Search & Discovery

**Primary Method:**
When the user asks for papers on a topic, immediately call the native `arxiv_search` tool with appropriate keywords.
- Example: `arxiv_search(query="large language models")`

**Fallback Method (If `arxiv_search` fails or returns empty/errors):**
If the native tool malfunctions, use `run_script` to execute the fallback Python search script.
- Tool Call Configuration:
  ```json
  {
    "name": "paper-search",
    "rel_path": "scripts/search_arxiv.py",
    "args": ["<your_search_query>"]
  }
  ```

### Phase 2: Content Retrieval

Once you have identified target arXiv IDs, retrieve their content using `url_fetch`.

1. **Read the Abstract**
- URL Format: `https://arxiv.org/abs/<arxiv_id>`
- Example: `url_fetch(url="https://arxiv.org/abs/2402.03300")`
2. **Read the Full Paper (HTML Version)**
To read the actual paper content (Methodology, Experiments, etc.), fetch the HTML version (preserves text and tables better than PDFs):
- URL Format: `https://ar5iv.labs.arxiv.org/html/<arxiv_id>`
- Example: `url_fetch(url="https://ar5iv.labs.arxiv.org/html/2402.03300")`

### Phase 3: BibTeX Generation (Optional)

If the user specifically asks for BibTeX citations, the native tools might not format it correctly. Use `run_script` to execute the BibTeX generator.
- Tool Call Configuration:
  ```json
  {
    "name": "paper-search",
    "rel_path": "scripts/get_bibtex.py",
    "args": ["<arxiv_id>"]
  }
  ```
Example `<arxiv_id>: 2402.03300`

## Constraints & Rules
- **Tool Priority:** Always attempt `arxiv_search` before resorting to `scripts/search_arxiv.py`.
- **Arguments Format:** When using `run_script`, the `args` parameter MUST be a List of strings (`List[str]`).
- **ID Versioning:** `2402.03300` resolves to the latest version. Use unversioned IDs for lookups unless the user specifically requests an older version.
