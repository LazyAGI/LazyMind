# Media runtime support and default capability groups

Status: complete.

Implemented the follow-up capability behavior:

- OpenAI runtime capability maps now support `text2image` and `text2video` in both frontend selection filtering and backend runtime validation.
- vLLM and LMDeploy retain their existing OpenAI-compatible chat/embedding/rerank support but remain excluded from image/video generation and image editing. Their wire source remains `openai` for existing request behavior.
- Keyless custom OpenAI runtime coverage now expects image/video roles to emit keyless runtime configuration; STT, TTS, and image editing remain unsupported.
- Default capability extension groups now render in visual, retrieval/tools, voice, evolution order.
- Every default capability accordion starts expanded. Highlight auto-expansion and manual collapse toggles remain intact.

Validation:

- `frontend`: focused Vitest suites for model capabilities and default panel grouping/visibility: 3 files, 64 tests passed.
- `frontend`: `pnpm exec eslint src/modules/modelProvider/modelCapabilities.ts src/modules/modelProvider/modelCapabilities.test.ts src/modules/modelProvider/components/DefaultModelConfigPanel.tsx src/modules/modelProvider/components/DefaultModelConfigPanel.grouping.test.tsx` passed.
- `frontend`: `pnpm typecheck` passed.
- `backend/core`: `go test ./modelprovider` passed.
- `backend/core`: `go test ./modelconfig` passed.
- `git diff --check` passed.

Concern: Sass and Ant Design emit pre-existing deprecation warnings during the frontend tests; no new warnings were introduced by this change.
