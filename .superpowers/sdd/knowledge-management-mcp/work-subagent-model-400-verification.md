# Work SubAgent Kimi 400 diagnosis

## Confirmed cause

The real Work task `d1af2986-e2df-4020-a198-a19152d98bd9` failed on its first Kimi `k3` request. A read-only task/config reconstruction of the actual SubAgent plan, stopped before any tool execution, reproduced HTTP 400. The upstream error was:

> tools.function.parameters is not a valid moonshot flavored json schema, details: <At path 'properties.artifacts.items': detected infinite recursion without termination condition>

`save_artifacts` emitted a local `$ref` to `ArtifactSaveItem` in its array item schema. The following real upstream differential requests used the existing selected Kimi model and account, without changing user configuration:

- Remove all knowledge management tools, retain `save_artifacts`: HTTP 400 with the same error.
- Retain the full tool list, inline only `ArtifactSaveItem`: HTTP 200.
- Run the updated SubAgent plan construction and send its complete 26-tool request: HTTP 200.

The private evidence is in `/tmp/lazymind-kb-e2e-20261020/work400-*`. Request bodies and provider credentials are not included in this report. These diagnostic calls stopped at the response headers and did not execute model-selected tools.

## Fix and verification

`save_artifacts` now uses Pydantic's `WithJsonSchema` to expose an inline item schema while retaining the original TypedDict runtime validator. `get_type_hints(..., include_extras=True)` preserves this metadata in both LazyMind tool preparation and LazyLLM schema construction. Required item fields, allowed content types, and rejection of unknown fields remain present.

The model-facing schema regression failed before the fix. Including actual LazyMind tool-doc preparation reproduced an additional metadata-stripping failure, also fixed. The final focused run passed 91 tests: 81 LazyLLM ToolManager tests plus 10 artifact-schema/tool-doc tests. LazyLLM commit: `e05839b005ee0ed8cd119bbc0ed7b209cc44464f`.

An earlier wider artifact test selection had 39 assertions pass and a fixture teardown error: `monkeypatch.setitem(lazyllm.globals, ...)` attempts unsupported `__delitem__` when restoring an absent key. This fixture issue is outside this change; the dedicated final 91-test run passed. It does not establish full artifact-suite acceptance.

## Live deployed Work acceptance

After the coordinated READY5 reload, real public Chat requests used the fixed existing Kimi model, without changing user settings:

- Research Work task `45e6faf8-938e-447c-abba-da7e2282ef90` created `KM-E2E-20261020-WORK-400-FIX`, ID `ds_e54373b98a6ef317a32e76d0a1fd5683`, and read it back with exact-match `knowledge.list` (total 1). An initial model-supplied `scene=private` was rejected with `INVALID_ARGUMENT`; the model corrected it to `general` and created exactly one library. Its final model round stalled after successful readback. The authorized public conversation stop immediately propagated to task status `interrupted` and parent SSE `cancelled/user_cancelled`; this is not recorded as a fully successful task terminal.
- Writer Work `d540facf-65a0-4512-ac20-72b6c9e23fdb` loaded `WriterCreateToolkit_build_writing_task`, then actually called knowledge.list and matched the same ID. Task terminal: `succeeded`.
- Image Work `d881441d-3ff0-4b1d-9259-41111c254403` loaded `image_generator`, then actually called knowledge.list and matched the same ID. Task terminal: `succeeded`. Image generation was not called. Both compatibility tasks' parent Chat ended `completed/normal`.

Private evidence: `work400-live.jsonl`, `work-compat-live.jsonl`, `work-smoke-readback.private.json`, and both conversations' public tasks responses in `/tmp/lazymind-kb-e2e-20261020/`. Fixed-revision Workflow/PPT material-step execution and the old KB-only pinned revision were subsequently verified; see workflow-compatibility-live-verification.md for exact successful task IDs and model-preference restoration evidence. No service restart was performed by this reviewer.

Diagnostic-script isolation note: the first offline reconstruction used the repository's default `agentic_workspace`, creating three tool-state files under checkout/workspace. Their scopes belong to the diagnostic script (the original failed task ID and two `work400-diagnostic-*` IDs), not deployed service runs. The private script now explicitly sets `LAZYMIND_AGENTIC_WORKSPACE` before imports; service isolation is being independently checked against the new real tasks.
