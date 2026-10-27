# Media runtime identity fix and highlight reopening

Status: complete.

Implemented the review follow-up:

- Added identity-aware backend runtime support using the existing vLLM/LMDeploy template identity helpers. A custom provider saved with `ProtocolName=OpenAI` now keeps vLLM/LMDeploy's restricted runtime adapters, while actual OpenAI providers retain image/video generation support.
- Applied the identity-aware check to model validation, probing, role selection, and catalog selection repair paths without changing request wire source or provider keys.
- Extended frontend runtime support checks with an optional provider identity and passed it through model capability selection, custom/catalog model add/edit validation, saved-model status, and model editor options.
- Added regression coverage for vLLM/LMDeploy templates using OpenAI protocol and for reopening a manually collapsed voice group when a new highlight targets `tts`.

Validation:

- `frontend`: focused model capabilities and default panel grouping/visibility suites: 3 files, 67 tests passed.
- `frontend`: focused ESLint passed.
- `frontend`: `pnpm typecheck` passed.
- `backend/core`: `go test ./modelprovider` passed.
- `backend/core`: `go test ./modelconfig` passed.
- `git diff --check` passed.

Concern: existing Sass, Ant Design, and macOS Security deprecation warnings remain in test/build output; no new failures or warnings were introduced by this fix.
