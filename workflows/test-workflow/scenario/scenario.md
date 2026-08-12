# Workflow Runtime End-to-End Self-Test

This workflow has six fast stages. Every stage performs only one small operation
and uses no network or external product integration.

`prompt → script → typed_artifacts → rewrite → list_artifacts → verify → end`

It covers:

- prompt-only SubAgent execution and text artifacts;
- a workflow-local Python tool returning structured JSON;
- file attachments and a visible, text-labelled mock image URL;
- two writes to one single-cardinality slot, producing artifact revisions;
- ordered list-cardinality attachments;
- cross-step input loading and a final script-based verification report.

Suggested trigger: `运行测试工作流，内容是 hello`.
