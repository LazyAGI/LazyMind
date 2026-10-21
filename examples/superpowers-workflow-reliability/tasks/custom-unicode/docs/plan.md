# Caller-authored project
### Task 7: normalize-labels
Implement normalize_labels(values) in 标签.py. Input is a list of strings.
Coverage ids and requirements:
- normalize: strip whitespace and lowercase each label; omit blank labels.
- dedupe: keep only the first occurrence of each normalized label, preserving order.
- validation: raise ValueError if any element is not a string. Empty input returns [].
Only change 标签.py. Keep test_标签.py and this Plan unchanged. Use Python standard library only.
Run python3 -m unittest -v test_labels. Write .eval/coverage.json with exactly the declared IDs,
DONE and evidence text; write .eval/implementer-report.md. Commit 标签.py only; do not push.
