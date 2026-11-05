# Update-format fixtures

These are schema-only captures from isolated databases created by the actual owners at source commit `70b5b4835c4e01fce60e87b91ba86b613893df41` and pinned LazyLLM `d53007dd95ce5f9180399a095cf81fb0b643093a`. They contain no user rows, credentials or runtime paths. Core migration version/history is retained because it is part of the compatibility contract.

| Family | Actual producer |
| --- | --- |
| core | `migrate.NewRunner("sqlite", tempDB, "migrations").Up(0)` then `episode.Initialize` |
| core-state | `state.NewSQLiteStore` |
| scan | `SQLRepository.AutoMigrate` |
| scan-state | Scan `state.NewSQLiteStore` |
| auth | Auth Alembic `upgrade head` against a temporary SQLite URL |
| auth-state | Auth `SQLiteStateStore` |
| channel | `SQLiteGatewayStore.initialize` |
| lazyllm | Pinned `DocManager` with only parser health transport stubbed; real SQL/migrations/indexes |
| segments | Pinned `SQLiteStore.connect`, before collections exist |

`producer.py` recreates the Python-owned fixtures in a caller-provided temporary path. Segment operations execute the pinned class AST unchanged, neutralizing only the framework base/import/decorator boundary; SQLite, DDL and serialization remain real. `snapshot` captures SQLite schema objects and Core migration metadata. Do not capture application row contents.

Drift tests live in the owning Core/Scan packages and Auth/Channel/Algorithm test entry points. They require a deliberate recipe/fixture review when producers change. Runtime tests restore each capture into an independent temporary database and use the actual segment producer to create/drop collections. Captures represent these listed producers, not proof that every possible future optional schema is supported.

Resolved-storage snapshot version 1 retains its legacy exact-instance contract. Version 2 is the new stable-format contract tested by `update_format_v2_test.go`; these tests are intentionally red until that capability is implemented. The separate recorded legacy failure probe demonstrates why normal first collection creation changes the existing profile.
