# Testing and CI Development Rules

These rules apply to changes under `tests/`. They also document shared practices
for tests colocated with application code and for CI changes; placing this file
here does not automatically extend agent instruction scope to those directories.

## Test placement and execution

- Before adding or moving a test, check the owning module's test configuration,
  local command and actual CI command. A passing focused run does not prove CI
  collects the test.
- Put algorithm tests in `tests/algorithm/`, not `algorithm/tests/`. Keep Go unit
  tests beside their packages and cross-module Core integration tests in
  `tests/backend/core/`.
- Frontend component and hook tests belong beside their code under
  `frontend/src/`. `tests/frontend/` is a separate suite with its own dependencies
  and collection rules. Check which suite and Vitest configuration a CI job runs;
  a dedicated configuration may collect only a subset of application tests.
- Reuse the owning module's existing test entry point. If a required regression
  test is outside CI collection, update the relevant collection or entry point
  with the change. Do not move tests into an unrelated suite merely to make CI
  discover them.
- Check the scope of `make test`, module commands and CI separately. Do not
  describe a local entry point as equivalent to the complete CI matrix without
  verifying its coverage.

## Observable behavior and external boundaries

- Test real user paths, public results and important error handling. Preserve
  assertions about permissions, state transitions and submitted parameters;
  avoid tests that only inspect source text or repeat the implementation.
- In ordinary unit tests, fake unrelated external service calls at the existing
  client boundary using the module's current mocking tools. Return data that
  matches the scenario, including resource IDs and revisions where relevant.
- Keep business logic, tool assembly and validation real. Do not mock the
  function whose behavior the test is intended to verify.
- A request that times out and falls back is still an external request. Check for
  unexpected HTTP calls when investigating slow tests; a passing assertion alone
  does not prove the test is isolated.
- Keep SDK transport, retry, timeout and error-handling coverage. Tests intended
  to exercise HTTP should use controlled local or mocked endpoints; real-service
  integration tests must have explicit setup and execution requirements.
- Prefer existing fake timers, events or controlled responses to fixed sleeps
  when testing asynchronous behavior. Do not shorten production timeouts or
  disable retries to accelerate unrelated unit tests.

## Database fixtures

- Reuse existing domain fixtures and `orm.MigrateTestDB`. Before reducing a model
  list, trace the full handler or service call chain, including related queries,
  reconciliation and background work reached by the test.
- Include tables inspected through `HasTable` or similar optional-schema checks
  when their behavior belongs to the scenario. Missing tables must not silently
  bypass the business behavior being tested.
- Ordinary business tests should initialize their required models. Avoid
  full-model initialization and repeated `AutoMigrate` calls when a suitable
  domain fixture already provides the required schema.
- Complete migration, schema-contract and genuinely full-application tests must
  retain the complete schema they verify. Do not replace migration-path
  verification with ORM `AutoMigrate`; follow the
  [migration rules](../backend/core/migrations/AGENTS.md) for migration changes.
- Preserve independent SQLite databases or PostgreSQL schemas and cleanup for
  each test. Do not share mutable databases across tests to reduce setup time.
- After changing a fixture, run the affected tests with both supported database
  drivers and check that required branches still run. Investigate missing-table
  errors even if the final test result passes through a fallback.

## CI dependencies, coverage and caching

- Keep lint and documentation checks as prerequisites for test jobs. Do not
  remove these dependencies to start tests earlier.
- Preserve required test suites, database variants, platform coverage, migration
  verification, coverage reports and coverage thresholds. Do not gain speed by
  deleting scenarios, adding skips or weakening assertions and quality gates.
- Reuse existing dependency download caches and compatible build artifacts
  before introducing another cache or installation step. Cache keys must account
  for the inputs that determine compatibility, such as platform, runtime,
  dependency files and source revision for locally built packages.
- Distinguish download caches, build caches and test-result caches. A restored
  cache or a passing run does not by itself prove the expected work was reused.
  Diagnose restore keys, exact hits, saves and invalidation before changing the
  cache strategy. Never reuse stale artifacts or results across incompatible
  inputs.

## Performance diagnosis and verification

- Measure before changing execution structure. Separate queueing, dependency
  installation, compilation, linking, database setup, seeding, requests, test
  execution, cleanup and cache finalization where they contribute to the delay.
- Do not assume a slow `go test` step means compilation is slow, or that printing
  `ok` means the command has finished. Use available timing output, pytest
  `--durations`, Go JSON events, build activity and cache diagnostics as needed.
- Compare the same suites and instrumentation on comparable platforms. Record
  source revisions, commands and cold or warm cache state; distinguish measured
  change from runner variation and queueing. Assess overall duration through the
  critical path rather than adding parallel package or job times.
- Remove temporary timing instrumentation after diagnosis. Keep permanent
  diagnostics only when they have a continuing purpose, preserve failure exit
  codes and avoid exposing credentials or business data.
- Run lint and documentation checks before affected tests. Broaden validation to
  the relevant database and platform suites when fixture or CI changes require
  it. Do not repeatedly trigger unchanged full CI runs for more measurements.
- Keep performance targets separate from correctness requirements. Report
  remaining bottlenecks and unverified limits without claiming a target has been
  reached. Report validation and timing results outside the PR description,
  which is reserved for the functional change.
