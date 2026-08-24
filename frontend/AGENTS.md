# Frontend Development Rules

These rules apply to every file under `frontend/`.

## 1. Keep changes focused

- Make the smallest change that satisfies the requested behavior.
- Follow the existing component, state-management, styling, and localization patterns before introducing a new pattern or dependency.
- Do not refactor, reformat, or rename unrelated code.
- Preserve unrelated working-tree changes and stage only files that belong to the task.

wei she
## 3. Keep API clients and user-facing text synchronized

- Do not hand-edit files under `frontend/src/api/generated/`; regenerate them with `pnpm gen:openapi` after changing an API contract.
- Run `pnpm gen:openapi:check` after changes that affect API contracts or generated clients.
- Put new user-facing text through the existing i18n system instead of hard-coding one language in a component.

## 4. Test behavior, not implementation accidents

- Add or update focused tests whenever behavior changes or a bug is fixed.
- Prefer assertions on user-visible behavior and stable contracts over fragile source-text or internal-structure assertions.
- During implementation, run the narrowest relevant test first; before commit, run the complete frontend CI checks below.

## 5. Run the frontend CI checks before every commit

Before committing any change under `frontend/`, run the local equivalent of the GitHub Actions `test-frontend` job from the repository root:

```bash
(cd frontend && node scripts/openapi/check-stale.mjs)
(cd tests/frontend && npm install && npm install --no-save c8 && npx c8 --reporter=html --reporter=cobertura npm test && npx c8 check-coverage --lines 10 --functions 10 --branches 10)
```

The workflow in `.github/workflows/ci.yml` is authoritative. If its `test-frontend` commands change, follow the workflow and update this section in the same change.

- Do not commit when any required check fails.
- Fix failures caused by the current change. If a failure is pre-existing or environmental, report the exact command and error instead of bypassing it silently.
- Do not use `--no-verify` unless the developer explicitly approves it after the failure has been diagnosed.

## 6. Keep generated artifacts out of feature commits

- Do not commit `dist/`, coverage output, screenshots, caches, or other generated artifacts unless the task explicitly requires them.
- After verification, check `git status` and remove only artifacts created by the verification commands; never discard unrelated user changes.
