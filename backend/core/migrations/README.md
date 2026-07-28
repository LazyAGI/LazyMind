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

## Required verification

Every schema or data change in `dev_mode/v0_N` must be reflected in the matching
`version_mode/v0_N` aggregate `up` and `down` files in the same change. The CI
migration tests build isolated databases through the supported paths:

1. all release aggregates in order;
2. release aggregates through `v0_(N-1)`, then every `dev_mode/v0_N` migration.

It compares normalized columns, constraints, indexes, sequences, views, and
non-volatile table data, verifies all ORM tables exist, and checks that the current
aggregate down migration restores the previous release schema. CI also exercises
mixed-history recovery and post-aggregate dev upgrades.

PostgreSQL and SQLite use the same catalog, version/dev directories, history
tables, ordering, and runner. SQLite never runs `AutoMigrate` at application
startup. A fresh database executes the explicit v0.1 and v0.2 release migrations;
an unversioned v0.1 Desktop database executes the same idempotent v0.1 baseline
and then the explicit v0.2 table-rebuild/data migration. Rename, drop, constraints,
indexes, seed data, and preserved columns are therefore part of reviewed migration
files rather than being inferred from the ORM at user startup.

SQL that works unchanged on both engines is written normally. When the engines
need different syntax, keep both implementations in the same migration file:

```sql
-- +migrate Dialect postgres
ALTER TABLE public.items ADD COLUMN payload jsonb;
-- +migrate Dialect sqlite
ALTER TABLE items ADD COLUMN payload text;
```

A file containing dialect directives must contain a matching block for every
supported database on which it will run. The same rule applies to its down file.
CI exercises SQLite from an empty database, upgrades a legacy database while
preserving and transforming data, compares the v0.1→v0.2 aggregate path with the
v0.1→all-v0.2-dev path, checks every ORM table and column, migrates legacy
plaintext credentials, and repeats upgrades for idempotency. With
`MIGRATION_TEST_POSTGRES_DSN` set, CI also builds and compares the PostgreSQL
aggregate and dev paths.

`go run ./cmd/sqliteschema` prints a deterministic SQLite DDL snapshot from the
current ORM for use while authoring a migration. It is a development-only
generator; its output must be reviewed and committed into both the current dev
migration and matching release aggregate. Production does not invoke it.

Run the PostgreSQL verification locally with a disposable server:

```sh
MIGRATION_TEST_POSTGRES_DSN='postgres://user:password@127.0.0.1:5432/postgres?sslmode=disable' \
  go test ./migrate -run TestRepositoryPostgresMigrationPaths -v
```

`goto` is intentionally unavailable when dev modes are configured because one
numeric target cannot unambiguously select aggregate versus dev history. Use
`up` and `down`.
