# 模型列表与能力图标控制实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让模型列表的减号统一移出模型，并把视觉、工具调用、推理改为可访问的启用图标，同时保留旧配置和供应商 key 的前向兼容。

**Architecture:** 复用现有删除接口和本地刷新逻辑，移除前端 `enabled` 切换入口；能力编辑器只控制 `feature_enabled`，但在未触碰时原样 round-trip 旧 `feature_support`，在用户主动覆盖旧“不支持”声明时生成后端可接受的显式设置。后端模型设置校验与 key 存储不变。

**Tech Stack:** React 18, TypeScript, Ant Design 5, Vitest Testing Library, Go modelsettings/modelprovider tests.

## Global Constraints

- 已加入列表和目录抽屉中的减号必须调用现有 `onRemove`/DELETE 流程。
- 历史 `enabled=false` 模型只允许减号移出，不增加 Plus 恢复入口。
- 不修改供应商分组 API key、模型 ID、embedding 配置和后端兼容字段。
- 未触碰能力图标时必须保留旧 `feature_support` / `feature_enabled` JSON。
- 只在用户主动打开旧 `feature_support=false` 的能力时移除该能力的旧不支持声明。

---

### Task 1: 统一模型移出动作

**Files:**
- Modify: `frontend/src/modules/modelProvider/components/SavedProviderModels.tsx`
- Modify: `frontend/src/modules/modelProvider/components/ProviderModelCatalogDrawer.tsx`
- Modify: `frontend/src/modules/modelProvider/components/ProviderWorkspace.tsx`
- Modify: `frontend/src/modules/modelProvider/pages/ModelProvidersPage.tsx`
- Modify: `frontend/src/i18n/locales/en-US.ts`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Test: `frontend/src/modules/modelProvider/components/SavedProviderModels.test.tsx`
- Test: `frontend/src/modules/modelProvider/components/ProviderModelCatalogDrawer.test.tsx`
- Test: `frontend/src/modules/modelProvider/pages/ModelProvidersPage.catalog.test.tsx`

**Interfaces:**
- Consumes: existing `onRemove(model): Promise<boolean>` and `deleteCustomModel` flow.
- Produces: `SavedProviderModels` and `ProviderModelCatalogDrawer` with no `onSetEnabled` prop and no delete button duplicate; all existing-model minus actions call `onRemove`.

- [ ] **Step 1: Write failing list tests**

  In `SavedProviderModels.test.tsx`, change the remove assertion to query the minus action by the existing remove/delete aria label, click it, and assert `onRemove` receives the row model. Add a row with `enabled: false` and assert its only model action is still the remove minus; assert no Plus or `disableModelAria`/`enableModelAria` button exists. Keep the probe-lock assertion and assert the minus is disabled while probing.

  In `ProviderModelCatalogDrawer.test.tsx` and `ModelProvidersPage.catalog.test.tsx`, update existing-model row, group, and batch removal cases to assert `onRemove`/`mocks.remove` and assert `mocks.setEnabled` is never called. Add an `enabled: false` saved model case and assert its row uses the remove aria label and calls `onRemove`.

- [ ] **Step 2: Run the focused tests to verify the old behavior fails**

  Run:

  ```bash
  pnpm --dir frontend vitest run src/modules/modelProvider/components/SavedProviderModels.test.tsx src/modules/modelProvider/components/ProviderModelCatalogDrawer.test.tsx src/modules/modelProvider/pages/ModelProvidersPage.catalog.test.tsx
  ```

  Expected: failures show the current minus path calls `onSetEnabled`, the catalog renders enable/disable branches, or the tests still find the separate trash button.

- [ ] **Step 3: Simplify `SavedProviderModels`**

  Remove `DeleteOutlined`, `PlusOutlined`, and the optional `onSetEnabled` prop. Keep the existing pending/probe guards and `remove(model)` helper. Render one text `Button` with `MinusOutlined`, `loading={removing.has(model.id)}`, `aria-label={t("modelProvider.deleteModelAria", { name: model.name })}`, and `onClick={() => void remove(model)}`. Use the existing `removeModelHint` tooltip. Do not change the edit or probe contracts.

- [ ] **Step 4: Make every catalog removal call `onRemove`**

  Remove `DeleteOutlined` and `onSetEnabled` from `ProviderModelCatalogDrawer` props. In `runChanges`, delete the `enabling` calculation and the `enabled` branches; when `remove && model.existing`, call `await props.onRemove(model.existing)` and throw `copy("removeFailed")` on `false`, otherwise add the candidate. For an existing candidate, always render the minus/remove aria label and call `runChanges([model], true)`, including `enabled=false` rows. Remove the second trash button. Leave add filtering, group/all batch behavior, pending state, partial-failure messages, and refresh behavior unchanged.

- [ ] **Step 5: Remove page wiring for enabled toggles**

  Delete `setSavedModelEnabled` from `ModelProvidersPage.tsx`, remove its `setGroupModelEnabled` import if no other call remains, and stop passing `onSetEnabled` to `ProviderWorkspace` and `ProviderModelCatalogDrawer`. Remove the corresponding `onSetEnabled` prop and adapter from `ProviderWorkspace.tsx`. Keep `deleteCustomModel` and its embedding confirmation path unchanged so API keys and model IDs remain untouched.

- [ ] **Step 6: Remove stale copy only where the UI no longer references it**

  Keep old translation keys available for old clients, but ensure visible labels use the remove/delete copy. If TypeScript or i18n checks identify unused feature-specific keys, do not delete them as part of this task; they are harmless compatibility vocabulary.

- [ ] **Step 7: Run focused tests and commit**

  Run the command from Step 2 and expect PASS. Then commit:

  ```bash
  git add frontend/src/modules/modelProvider/components/SavedProviderModels.tsx frontend/src/modules/modelProvider/components/ProviderModelCatalogDrawer.tsx frontend/src/modules/modelProvider/components/ProviderWorkspace.tsx frontend/src/modules/modelProvider/pages/ModelProvidersPage.tsx frontend/src/modules/modelProvider/components/SavedProviderModels.test.tsx frontend/src/modules/modelProvider/components/ProviderModelCatalogDrawer.test.tsx frontend/src/modules/modelProvider/pages/ModelProvidersPage.catalog.test.tsx
  git commit -m "fix: make model minus action remove models"
  ```

### Task 2: Replace support/enabled table with capability icon toggles

**Files:**
- Modify: `frontend/src/modules/modelProvider/components/ModelFeaturesEditor.tsx`
- Modify: `frontend/src/modules/modelProvider/components/ModelFeaturesEditor.scss`
- Modify: `frontend/src/modules/modelProvider/components/ModelEditorModal.tsx`
- Modify: `frontend/src/modules/modelProvider/modelSettings.ts`
- Modify: `frontend/src/i18n/locales/en-US.ts`
- Modify: `frontend/src/i18n/locales/zh-CN.ts`
- Test: `frontend/src/modules/modelProvider/components/ModelEditorModal.test.tsx`
- Test: `frontend/src/modules/modelProvider/modelSettings.test.ts` (create if the existing model-settings test directory has no helper coverage)

**Interfaces:**
- Consumes: `isModelFeatureEnabled`, current model settings and Ant Design `Form` values.
- Produces: `editableModelSettings(capability, settings, embeddingValues, preserveEmpty, touchedFeatures?)`, where `touchedFeatures` is an optional `ReadonlySet<ModelFeature>` used only to apply explicit user overrides.

- [ ] **Step 1: Add failing compatibility tests for settings normalization**

  Add tests for these exact cases:

  ```ts
  expect(editableModelSettings("LLM_CHAT", {
    feature_support: { vision: false },
  })).toEqual({ feature_support: { vision: false } });

  expect(editableModelSettings("LLM_CHAT", {
    feature_support: { vision: false },
    feature_enabled: { vision: true },
  }, {}, false, new Set(["vision"]))).toEqual({
    feature_enabled: { vision: true },
  });

  expect(editableModelSettings("LLM_CHAT", {
    feature_support: { vision: true, reasoning: false },
    feature_enabled: { tool_calling: false },
  }, {}, false, new Set(["tool_calling"]))).toEqual({
    feature_support: { vision: true, reasoning: false },
    feature_enabled: { tool_calling: false },
  });
  ```

  These verify untouched old declarations round-trip, an explicit override removes only the contradictory old false declaration, and unrelated flags remain intact.

- [ ] **Step 2: Run the settings tests to verify the new signature/behavior is absent**

  Run:

  ```bash
  pnpm --dir frontend vitest run src/modules/modelProvider/modelSettings.test.ts
  ```

  Expected: the new override case fails until the helper accepts `touchedFeatures` and removes only the overridden false support flag.

- [ ] **Step 3: Extend `editableModelSettings` without changing default round-trip behavior**

  Add `touchedFeatures?: ReadonlySet<ModelFeature>` as the fifth parameter. Build `support` and `enabled` from `declaredFeatureFlags` as today. For each touched feature, read the explicit `settings.feature_enabled[feature]` value; if it is boolean, keep it in `feature_enabled`. If that value is `true` and `support?.[feature] === false`, delete only that key from the copied support object. Continue returning the existing support/enabled objects for untouched values and preserve embedding handling exactly. This function must never mutate the caller's `settings` object.

- [ ] **Step 4: Replace `ModelFeaturesEditor` controls with icons**

  Import `EyeOutlined`, `ToolOutlined`, and `BulbOutlined`. Keep the `Form.useWatch("modelSettings")` source and `modelFeatures` iteration, but remove every `Select`, support column, Switch disabled-by-support branch, and explanatory hint paragraph. For each feature, render a `Form.Item` bound to `modelSettings.feature_enabled.<feature>` with `getValueProps` that uses the explicit value, then the old support value, then the existing vision/legacy fallback. Render an Ant Design text `Button` with the feature icon, `aria-label` from `featureEnabled`, `aria-pressed` equal to the computed boolean, tooltip text from the feature label, and an `onClick`/Form change that writes the inverse boolean. The button remains clickable even when old support is false so an intentional enable can be normalized by Task 2 Step 3. Add a compact label visually hidden or adjacent to the icon for non-color users.

- [ ] **Step 5: Track touched features in `ModelEditorModal`**

  Add `const [touchedFeatures, setTouchedFeatures] = useState<Set<ModelFeature>>(new Set())`, clear it whenever the model ID changes/reset occurs, and inspect `changedValues.modelSettings?.feature_enabled` in the form's `onValuesChange` to add changed feature names. Pass `touchedFeatures` to `normalizeValues` and extend `normalizeValues` to pass it to `editableModelSettings`. Keep initial normalization with an empty set so opening and saving without icon interaction preserves old settings. Ensure form resets do not mark fields touched.

- [ ] **Step 6: Update styles, labels, and component tests**

  Replace the three-column grid in `ModelFeaturesEditor.scss` with a compact flex/grid row of three icon buttons, visible active state, hover/focus outline, and a small screen-reader-friendly label. Add concise tooltip copy for each icon in both locale files; retain old support strings for compatibility. Rewrite `ModelEditorModal.test.tsx` cases that query Select/Switch support columns to query buttons by `featureEnabled.*`, assert `aria-pressed`, and cover: old support-only state initializes correctly; clicking an icon writes explicit enabled; clicking vision on an old `feature_support.vision=false` model saves without that contradictory support flag; editing only the model ID preserves the old support JSON.

- [ ] **Step 7: Run focused editor/settings tests and commit**

  Run:

  ```bash
  pnpm --dir frontend vitest run src/modules/modelProvider/modelSettings.test.ts src/modules/modelProvider/components/ModelEditorModal.test.tsx
  ```

  Expect PASS, then commit:

  ```bash
  git add frontend/src/modules/modelProvider/components/ModelFeaturesEditor.tsx frontend/src/modules/modelProvider/components/ModelFeaturesEditor.scss frontend/src/modules/modelProvider/components/ModelEditorModal.tsx frontend/src/modules/modelProvider/modelSettings.ts frontend/src/modules/modelProvider/modelSettings.test.ts frontend/src/modules/modelProvider/components/ModelEditorModal.test.tsx frontend/src/i18n/locales/en-US.ts frontend/src/i18n/locales/zh-CN.ts
  git commit -m "feat: use capability icons for model features"
  ```

### Task 3: Page-level regression and compatibility verification

**Files:**
- Test: `frontend/src/modules/modelProvider/pages/ModelProvidersPage.asyncState.test.tsx`
- Test: `frontend/src/modules/modelProvider/pages/ModelProvidersPage.catalog.test.tsx`
- Test: `frontend/src/modules/modelProvider/components/ProviderWorkspace.test.tsx`
- Test: `frontend/src/modules/modelProvider/components/ProviderModelCatalogDrawer.test.tsx`
- Test: `frontend/src/modules/modelProvider/components/SavedProviderModels.test.tsx`
- Verify: `backend/core/modelsettings`
- Verify: `backend/core/modelprovider`

**Interfaces:**
- Consumes: completed removal and icon behavior from Tasks 1–2.
- Produces: green focused frontend tests, frontend type/lint/build checks, and backend compatibility evidence.

- [ ] **Step 1: Update page mocks and assertions**

  Remove `setEnabled` expectations from page mocks where no production call remains. Keep mocks for the generated DELETE endpoint and assert the provider/group API key fixtures are unchanged after deletion. Ensure the embedding 409 confirmation test still retries with `confirm_indexed_downgrade=true`.

- [ ] **Step 2: Run all model-provider frontend tests**

  Run:

  ```bash
  pnpm --dir frontend vitest run src/modules/modelProvider/components/SavedProviderModels.test.tsx src/modules/modelProvider/components/ProviderModelCatalogDrawer.test.tsx src/modules/modelProvider/components/ModelEditorModal.test.tsx src/modules/modelProvider/components/ProviderWorkspace.test.tsx src/modules/modelProvider/pages/ModelProvidersPage.asyncState.test.tsx src/modules/modelProvider/pages/ModelProvidersPage.catalog.test.tsx
  ```

  Expected: PASS with no tests asserting the removed enable/disable UI.

- [ ] **Step 3: Run frontend static checks and build**

  Run:

  ```bash
  pnpm --dir frontend typecheck:all
  pnpm --dir frontend lint
  pnpm --dir frontend build
  ```

  Expected: all commands exit 0; generated OpenAPI files remain unchanged.

- [ ] **Step 4: Run backend compatibility tests**

  Run from `backend/core`:

  ```bash
  go test ./modelsettings ./modelprovider -run 'TestSettingsKeepUnknownSeparateFromDisabled|TestSettingsRejectImpossibleEnabledFeatures|TestModelSettingsEditRoundTripAndLegacyRename|TestModelSettingsEditRejectsInvalidCapabilityCombination|TestBatchAddGroupModelsPreservesExistingAndRestoresUUID|TestDeleteGroupModel|TestVisionReadinessUsesSavedModelFeatureGates' -count=1
  ```

  Expected: both packages PASS. These tests prove old settings remain valid, DELETE is soft/reversible by re-add, model UUID/history are retained, and API-key storage is not part of the deletion mutation.

- [ ] **Step 5: Inspect the final diff and commit verification updates**

  Run `git diff HEAD~2..HEAD --stat`, inspect all changed UI and tests, then commit any page-test-only updates with:

  ```bash
  git add frontend/src/modules/modelProvider/pages frontend/src/modules/modelProvider/components
  git commit -m "test: verify model settings compatibility"
  ```

  Leave the branch clean and report the exact test/build commands and results.
