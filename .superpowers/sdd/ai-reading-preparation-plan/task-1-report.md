# Task 1 — Backend reading preparation

Implemented personal durable reading policy, capability metadata, policy enforcement, source-bound shared generation services, future-document enrollment, bounded durable execution, progress/failure reporting and read-only chat parse observation.

## API contract

All routes below are under `/api/core`. Existing gateway identity and ACL middleware remain authoritative.

### GET /user/reading-preferences
### PUT /user/reading-preferences

GET returns the usual `{code,message,data}` envelope. PUT replaces the editable policy and returns the same representation. Example PUT:

```json
{"preset":"full","modes":{"pdf_parse":"automatic","chat_prepare":"on_demand","formula":"automatic","figure":"automatic","table":"automatic"},"limits":{"daily_documents":5,"study_items_per_document":10,"automatic_concurrency":1}}
```

`preset`: `demand`, `advance`, `full`, `custom`. Presets normalize modes server-side. Demand makes all five on-demand; advance makes only structural PDF parsing automatic; full makes structural parsing, formulas, figures and tables automatic. Chat preparation stays on-demand for all presets. Custom requires all five known keys. Modes: `on_demand`, `automatic`, `disabled`. Automatic studies require `pdf_parse=automatic`; the server returns a useful Chinese 400 otherwise. Unknown modes/keys, invalid presets/limits and multiple JSON documents are rejected. Limits are 1–100 daily documents, 1–100 total study items per document, and fixed automatic concurrency 1. Defaults are 5/10/1.

Response `data` contains the policy plus server-owned `automatic_eligible_after` and `capabilities`. Each capability contains `id`, `name`, `dependencies`, `supported_modes`, `triggers`, `granularity`, `cost_unit`, `result_version`, `available`, `effective_mode`, and optional `unavailable_reason`. Dynamic checks resolve configured MinerU/chat model or installed local document component. `overview` is named 论文速览, unavailable with no supported triggers or modes. No overview generation is claimed. These availability checks do not test network connectivity. Source-bound saved results do not require currently configured services.

Policy writes never enqueue history. Enabling automatic modes or changing their scope resets the future-document eligibility epoch. A setting change cannot enroll prior documents into newly automatic capabilities.

### POST /datasets/{dataset}/documents/{document}:prepare-reading

Requires document write access, original PDF and a JSON body `{"scopes":["pdf_parse","formula","figure","table"]}`. At least one unique scope is required; users can select any subset. Chat preparation stays on its existing explicit ensure endpoint. Returns envelope `data`:

```json
{"job_id":"...","status":"pending","progress_current":0,"progress_total":0,"model_requests":0,"scopes":["formula"]}
```

Explicit operation idempotency includes caller, document, source and canonical scope list. Pending/running identical requests join; explicitly retrying a terminal result can enqueue fresh work, reusing valid cached artifacts. It does not force-regenerate saved explanation results. Failed preparation does not delete existing outputs.

### GET /datasets/{dataset}/documents/{document}/reading-preparation

Requires document read access. Returns the latest preparation for this caller and document, using the same envelope/status shape above. Status values: `not_requested`, `pending`, `running`, `succeeded`, `failed`, `canceled`. Includes `error_message` on failure and completed/total progress. `model_requests` counts reserved outbound structured-generation transport attempts, including failed/backup calls; it is not a token or currency measure.

### GET /datasets/{dataset}/documents/{document}/parse-status

Read-only observation of existing chat parse artifacts/tasks. Same raw JSON shape as the existing ensure endpoint: `{status,task_id?,error?}`, status `not_requested|parsed|parsing|failed|unknown`. HTTP 202 while an existing task is parsing; otherwise 200. Uses an explicit `ObserveOnly` service option and cannot create parser tasks. Original `POST ...:ensure-parsed` remains the explicit trigger.

### Existing endpoints

PDF IR create/worker, formula explanation, visual explanation, paper chat parsing and reparse submission/worker enforce policy before configuration/model generation. Formula and visual shared services resolve ACL/source/current IR and stable cache before consulting prohibition. Therefore current saved explanations remain readable with disabled generation and without a current model. Force generation is blocked. Formula retry/backup checks policy and source/ACL again. Nonpaper imports retain their prior behavior. Translation is unchanged (outside the five preparation capabilities).

## Durable execution / accounting

- Dedicated `document_reading_preparation` runner: one worker, resource serialization by caller for automatic jobs. Auto scanner every 15 seconds considers only creator-owned PDF documents in `academic_papers` datasets newer than the saved eligibility epoch, with readable valid PDF sources. All import paths converge through the persisted document rows; no frontend mount trigger is required.
- Enrollment transaction locks the policy row, reserves at most the daily limit (UTC day), snapshots scopes/item cap, and keeps one automatic job per caller/document including terminal failures. It never silently retries a failed automatic job. Documents beyond today's quota remain eligible on later days, provided policy epoch still allows them.
- Worker rehydrates current account identity rather than fabricating privilege, checks current dataset write ACL/source hash, current policy, date quota and effective item cap. Scope changes make old queued document jobs ineligible. Each study iteration rechecks policy, limit, identity and source through the shared service.
- Structural parsing uses the existing real PDF IR async job and source/revision idempotency key, joins pending work, and waits up to 30 minutes. The durable PDF IR payload records automatic origin, so a queued parser retry rechecks automatic policy. Existing explicit/shared parser jobs are not canceled by a preparation waiter. Chat processing payload likewise retains automatic origin across its queue.
- Preparation job `MaxAttempts=1`. PDF IR retains bounded `MaxAttempts=2`. Each formula item uses the existing primary plus at most one backup/retry (maximum two transport calls); each visual item uses one. With a 10-item limit, formula-only study can issue at most 20 structured model requests plus up to two parser attempts. Failed attempts consume their reservation. The total cap combines formula/image/table items; cached items conservatively consume item slots but no model request.
- `model_requests` is persisted before dispatch and finalized on success/failure, including backups. Provider-side token spending and calls whose transport outcome is unknown are not estimated. No automatic retries of the whole preparation multiply the item budget. Parse attempt counts remain on the existing PDF IR job.
- Progress/failure is persisted by asyncjob. Source/ACL/model failures propagate; existing artifacts are retained.

## Schema / deployment

New table `user_reading_policies(user_id PRIMARY KEY, policy_json TEXT, updated_at TIMESTAMP)`.

New dev pair:
- `backend/core/migrations/dev_mode/v0_3/20261103062341_create_reading_policies.up.sql`
- matching `.down.sql`

Consolidated existing aggregate pair:
- `backend/core/migrations/version_mode/v0_3/20260805000000_workflow_runtime_release.up.sql`
- matching `.down.sql`

Both dialects supported. No earlier dev migration was edited. The local Docker override mounts the runtime migration mirror, so deployment must copy these new dev files and updated aggregate into that mirror, preserving its other history. No local deployment or merge performed by this task.

## Verification

- `go test ./doc ./readingpolicy ./userprefs ./asyncjob`: passed, including existing shared generation/cache/ACL/source and parser tests.
- New tests cover default/validation/isolation, epoch changes, durable future-only enrollment, daily quota, total-item snapshot, no failed automatic reenrollment, caller/scope idempotency, stopped automatic PDF worker before model config, disabled formula cache hit/forced denial, read-only parse polling no task creation, and failed generation transport accounting.
- `go test . -run TestPaperReading -count=1`: passed; core/OpenAPI compile verified.
- `go test ./migrations -run TestReadingPolicyMigrationAndAggregate -count=1`: SQLite complete dev path and aggregate path upgraded, inserted policy data, rolled back and preserved unrelated existing data. PostgreSQL environment test requires a host DSN (container has no published port); direct PostgreSQL migration testing used Docker psql with disposable databases instead.
- New incremental PostgreSQL up/insert/down was verified in an isolated transaction and rolled back.
- `git diff --check`: clean.
- Existing macOS Keychain deprecation compiler warnings remain; no new test failures.

## Explicit limitations / review notes

- No live paid model/MinerU end-to-end smoke run performed. Shared real transport was exercised with a failing local test server; existing generation tests exercise response/schema/cache behavior.
- Capability availability reports configuration/component presence, not live endpoint health. Registry result-version strings must be updated alongside their generating task/parser versions.
- Legacy model-keyed explanation files still follow the pre-existing migration path requiring the matching current model identity; the stable source-keyed cache used by current results is independent of model/policy.
- In-flight provider requests cannot be undone by a policy change. Subsequent queued calls, parser retries and formula backup attempts recheck policy. A shared explicit PDF parse may continue independently.
- Chat automatic preparation can fail visibly if its shared asynchronous parse has not completed within the existing 25-second wait window; its underlying parser task remains observable and saved artifacts are retained. It does not silently declare success.
- Enrollment scans eligible document rows for each opted-in user. This is bounded in paid work, but large long-lived eligible ranges may benefit from a separate scan cursor/index optimization later.
- A user can intentionally submit multiple different explicit preparations; daily limits apply to automatic jobs. Explicit per-document study item cap still applies.
