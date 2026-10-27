# Frontend runtime identity propagation for model controls

Status: complete.

Implemented the remaining frontend identity wiring:

- `SavedProviderModels` accepts `runtimeIdentity`, receives it from `ProviderWorkspace`, and uses it when determining whether saved media models can be probed.
- `ProviderModelCatalogDrawer` accepts and uses `runtimeIdentity`; `ModelProvidersPage` passes the catalog provider identity.
- `ModelEditorModal` uses `providerIdentity` for both option disabling and form validation, with the page passing the provider's runtime identity.
- Added focused regression assertions for arbitrary display names using OpenAI protocol with vLLM template identity in saved-model, catalog, and editor paths.

Validation:

- Focused frontend suites (model capabilities, saved models, catalog drawer, editor modal, default panel grouping): 5 files, 146 tests passed.
- Focused ESLint passed.
- `pnpm typecheck` passed.
- Backend `go test ./modelprovider` and `go test ./modelconfig` passed.
- `git diff --check` passed.

Concern: existing Sass, Ant Design, React Router, and jsdom deprecation/not-implemented warnings remain in test output; no new failures were introduced.
