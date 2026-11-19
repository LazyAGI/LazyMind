# Workflow Basic Self-Test

Run only when the user asks to test the Workflow runtime.
Suggested request: `运行测试工作流，内容是 hello`.

`generate → update → human review → verify → end`

The shared tools create deterministic JSON/text files and check saved content.
The flow covers cross-step inputs, two revisions of one file slot, an ordered
two-item list and one review pause. Native and external execution use the same
business rules. No network service, image model or other product integration is needed.
