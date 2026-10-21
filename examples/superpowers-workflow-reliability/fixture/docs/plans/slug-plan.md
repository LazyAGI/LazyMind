# Slug utility implementation plan

Goal: implement a deterministic ASCII slug function. One task, three enumerable requirements.
Architecture: one pure Python function with unittest, no network or dependencies.

### Task 1: Implement slugify

Files:
- Modify: `slug.py`
- Verify only, do not edit: `test_slug.py`

Requirements and coverage IDs:
- spaces: Trim surrounding whitespace, lowercase ASCII letters, collapse whitespace runs into one hyphen.
- punctuation: Remove characters other than ASCII letters, digits, whitespace and hyphens; collapse hyphens and strip leading/trailing hyphens.
- empty: Raise ValueError when the normalized result is empty.

Run `python3 -m unittest -v` before implementation (expected failure), implement the function, run again (all three tests pass). Preserve the tests unchanged. Write `.eval/coverage.json` as an array with exactly one `{ "id": "...", "status": "DONE", "evidence": "test name" }` per requirement. A SKIPPED entry requires a reason and is partial completion. Commit only the task code. Do not push. Report test commands, results and concerns.
