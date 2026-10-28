# Runtime identity propagation for media availability

Status: complete.

Implemented the remaining identity propagation fixes:

- Frontend default model loading and model editor validation now pass provider runtime identity through capability checks.
- Backend list, selected-model read, shared-model detail, readiness, multimodal-embedding, credential fallback, and selection candidate paths now load the owning provider and use template-aware runtime support. Existing custom providers with OpenAI protocol and vLLM/LMDeploy templates therefore remain unavailable for image/video generation.
- Provider list responses now expose `runtime_identity` for custom providers backed by vLLM or LMDeploy templates. The frontend preserves display name/protocol while using this stable identity for capability filtering.
- Added a regression test covering an existing custom vLLM row stored with `ProviderName=OpenAI`: candidate lists exclude it, inventory lists mark it unavailable, readiness is false, and selected-model reads mark it unavailable.

Validation:

- `frontend`: focused model capability/default panel suites: 3 files, 67 tests passed.
- `frontend`: focused ESLint passed.
- `frontend`: `pnpm typecheck` passed.
- `backend/core`: `go test ./modelprovider` passed.
- `backend/core`: `go test ./modelconfig` passed.
- `git diff --check` passed.

Concern: existing Sass, Ant Design, and macOS Security deprecation warnings remain in test output; no new failures were introduced.
