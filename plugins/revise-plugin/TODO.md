# Revise Plugin TODO

- [x] Add an explicit `build_context` step before document presentation and revision.
  - Split context construction out of `load_document`; loading should only resolve the target and persist the source IR/provider snapshot.
  - Build revision context from the user's request, source IR, target document binding, and available resources.
  - Make the interaction order explicit: load and present source IR -> build and present context -> revise -> present candidate IR -> user edits/adds requirements/confirms -> write back.
  - Preserve `source_ir` as immutable and keep all AI/human versions on the editable candidate slot.
  - Do not modify Feishu before the final explicit confirmation boundary.
