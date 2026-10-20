# Live Work and pinned Workflow compatibility — 8091

All calls used the existing authenticated 8091 test user. No database writes were performed outside normal application APIs. No provider credentials, permissions, or 8090 settings were changed.

## Ordinary Work

- Research task `45e6faf8-938e-447c-abba-da7e2282ef90` genuinely created and exactly listed `KM-E2E-20261020-WORK-400-FIX`, ID `ds_e54373b98a6ef317a32e76d0a1fd5683`. Its final model round stalled after the successful readback. An authorized public conversation stop propagated immediately to task `interrupted` and parent `cancelled/user_cancelled`; the complete task is therefore not claimed as succeeded.
- Writer task `d540facf-65a0-4512-ac20-72b6c9e23fdb` loaded `WriterCreateToolkit_build_writing_task` and actually called knowledge.list to match that ID. Terminal: `succeeded`.
- Image task `d881441d-3ff0-4b1d-9259-41111c254403` loaded `image_generator` and actually called knowledge.list to match that ID. Terminal: `succeeded`. The image tool was not invoked and no image generation occurred. Their parent conversation completed normally.

## Current published PPT revision

The built-in `ppt-workflow` revision 5, `0ec2d959-8418-46fd-a84d-f4e6c68bdd14`, was loaded through the public runtime API and consumed into real pinned sessions. Its materials node declares both `kb` and `knowledge_management`.

1. Session `aa1d93c1800843589c32820f1aea676b`: analyze task `fddcb5fc-c905-4c34-a22b-dd1b56f6f390` and material task `770187aa-0b4b-4c2d-a401-e6de78690735` both succeeded. Material collection actually listed the new empty test library and its documents through MCP, then saved the result. This conversation had no selected retrieval KB, so it does not establish native KB tool execution.
2. Session `6cafc3f5d7e847609d7e9146c424e159`: the normal step command explicitly selected the authorized CHAT-A fixture library for retrieval. Analyze task `b015920d-8ea9-4e6e-834f-1679f87788a0` and material task `5b3eedc6-73fd-4319-9058-304c3c7d9e26` both succeeded. The latter actually called `knowledge_document_list_mcp_5b2509589c45`, then `KBToolkit_read_document`, then `save_artifacts`. It read document `doc_6f9bc58d7269759181e5bfbb505ef21e` from `ds_09b10f333dd36b049f016609bfc22977` and returned the fixture marker `KM-E2E-20261020-ORIGINAL`.

Both partial PPT sessions were stopped through the public lifecycle API after the requested material step. Full PPT rendering and paid background generation were intentionally outside this smoke test.

## Old pinned KB-only revision after a new publication

A dedicated one-step read-only fixture, `km-e2e-pinned-kb-01efb2ad`, was created and published through normal authoring APIs. It did not copy or modify the user's PPT package.

The first fixture revision declared no output slots. Its old KB listing and native document read succeeded, but the model attempted to save an undeclared output; Core correctly rejected it with `OUTPUT_SLOT_UNDECLARED`. This initial task is recorded as failed, not a successful terminal.

The fixture was then corrected to declare a text `report` output:

- Published KB-only revision 3: `9a26aab9-7a20-4f8f-ae91-798cee2cecb3`.
- Created session `1fc7c5c83b7b48b98045d7da18c98231`, pinned to revision 3.
- Published revision 4 with `kb` plus `knowledge_management`: `197dd532-8fc1-4e2f-b938-fce8880bbb5d`.
- Dispatched the existing revision-3 session through the normal step API. Task `0bb57ca0-bc02-4d07-99ba-2e6d3367ecd8` retained `revision_id=9a26aab9-...` and `legacy_tools=["kb"]`.
- It actually called `KBToolkit_list_knowledge_bases`, `KBToolkit_read_document`, and `save_artifacts`, read the same fixture content, and ended `succeeded`. No new management tool was substituted for the old compatibility surface.

## Temporary model preference and exact restoration

Workflow execution uses the owner's Models and Services selection rather than a Chat-only fixed model. Each isolated verification window therefore temporarily changed only this test user's `llm` selection via `PUT /api/core/model_providers/selected_models`, from existing own model `1adadad01cac4b3084910ee5820bd44b` to existing own Kimi model `484d4214991246168cdf7213ef8fe802`.

Each window used a `finally` restoration, including the failed initial fixture. The final GET was compared with the saved pre-test selections across every `model_key`, `model_id`, `source`, and `share`: exact equality was true. Original `llm` selection was restored; other roles and sharing were unchanged. The final read-only task query found zero pending/running Work tasks for this user.

The original full selection snapshot, restore payload, each switch/restore response and GET, request/response evidence, task tool results, and summaries are private files under `/tmp/lazymind-kb-e2e-20261020/`, notably `workflow-all-smokes-final-models.private.json`, `workflow-smoke-readback.private.json`, and `pinned-kb-v3-readback.private.json`.
