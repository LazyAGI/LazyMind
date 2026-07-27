# Migration layout

```text
migrations/
├── version_mode/
│   ├── v0_1/
│   │   ├── 20260321131500_init.up.sql
│   │   └── 20260321131500_init.down.sql
│   └── v0_2/
│       ├── 20260723183515_squash_post_init.up.sql
│       └── 20260723183515_squash_post_init.down.sql
└── dev_mode/
    └── v0_2/
        ├── 20260506120000_seed_default_model_catalog.up.sql
        ├── 20260703130000_create_plugin_step_intents.up.sql
        └── ...
```

`version_mode/v0_N` contains the stable aggregate for release `v0_N` and must
contain exactly one matching up/down pair. `dev_mode/v0_N` contains the SQL files
accumulated while developing that release. Matching directory names are the
mapping, so no separate mapping file is required. The numeric suffix `N` is the
internal mode version.

Existing migration IDs and filenames stay unchanged when a file is moved into a
release directory. The aggregate does not need a `Supersedes` directive for the
dev files in its matching directory; the directory association performs that
canonicalization. `Supersedes` remains supported for legacy history
compatibility.

## History rules

The existing `schema_migrations`, `schema_migration_history`, and
`schema_migration_lock` tables are reused. No extra migration table or column is
required.

For a dev file, the history version is:

```text
full_version = N * 100000000000000 + file_timestamp
```

For example, `dev_mode/v0_2/20260915100000_create_projects.up.sql` is recorded as
`220260915100000`. This gives every dev migration a single complete ID and avoids
collisions between releases. A sealed release is represented only by its
aggregate migration ID.

For each release, the runner applies these rules:

1. If the aggregate version is already recorded, skip the release. Dev records
   for the same release are an error.
2. If some dev migrations are recorded, continue only the missing dev files.
3. Once all current dev files are recorded and an aggregate directory exists,
   atomically replace all full dev history rows with the aggregate history row.
   The aggregate SQL is not executed.
4. If neither path has started and an aggregate directory exists, execute the
   aggregate SQL.
5. Otherwise execute the dev files in timestamp order.

Different releases may use different paths. For example, `v0_1` may have one
aggregate history row while `v0_2` still has full dev history rows.

Old `Supersedes` squash migrations are canonicalized automatically when all
declared source versions are present. `MIGRATION_FAKE_VERSIONS` is not used.

## Deleting dev SQL

Do not delete dev files while a database can still contain only part of that
release's dev history. If a recorded full dev version no longer has a matching
file, the runner stops before executing any aggregate SQL. Dev files can be
removed after the release is sealed and every maintained database has
canonicalized that release to its aggregate history row.

Create a new dev migration with:

```sh
go run ./cmd/dbmigrate create -name create_users -version v0_2
```

`goto` is intentionally unavailable when dev modes are configured because one
numeric target cannot unambiguously select aggregate versus dev history. Use
`up` and `down`.
