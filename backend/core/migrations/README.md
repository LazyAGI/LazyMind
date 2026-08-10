# Migration layout

```text
migrations/
├── version_mode/
│   ├── v0_1/
│   │   ├── 20260321131500_init.up.sql
│   │   └── 20260321131500_init.down.sql
│   ├── v0_2/
│   │   ├── 20260723183515_squash_post_init.up.sql
│   │   └── 20260723183515_squash_post_init.down.sql
│   └── v0_3/
│       ├── 20260805000000_workflow_runtime_release.up.sql
│       └── 20260805000000_workflow_runtime_release.down.sql
└── dev_mode/
    └── v0_3/
        ├── 20260803120000_expand_workflow_host_refs.up.sql
        ├── 20260803150000_create_workflow_facade_tables.up.sql
        └── ...
```

`version_mode/v0_N` contains the complete aggregate for release `v0_N` and must
contain exactly one matching up/down pair. Its SQL is maintained as the release
changes, while its migration ID stays unchanged. `dev_mode/v0_N` contains the
SQL files accumulated while developing that release. Matching directory names
are the mapping, so no separate mapping file is required. The numeric suffix `N`
is the internal mode version.

Existing migration IDs and filenames stay unchanged when a file is moved into a
release directory. `Supersedes` remains available for compatibility with legacy
flat-layout squash histories. Structured release execution uses the aggregate
version itself as the compatibility cutoff.

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
collisions between releases. History records what actually executed: the
aggregate path has an aggregate row, while the dev path retains its individual
dev rows.

For each release, the runner applies these rules:

1. If the aggregate version is already recorded, skip every dev migration whose
   file timestamp is less than or equal to the aggregate version. Execute only
   missing dev migrations with a greater file timestamp.
2. Otherwise, if the release contains dev migrations, execute only the missing
   dev files in timestamp order. This is also the path for a new database when
   both aggregate and dev files exist.
3. Do not execute or record the aggregate after the dev path has started or
   completed, and do not replace or delete individual dev history rows.
4. Execute the aggregate only when the release contains no dev migration files.

The aggregate cutoff applies to every release that may have been executed by an
older runner. A local deployment may contain only an aggregate history row
because it executed the aggregate directly or because the older runner
canonicalized and deleted its dev history. A late-merged migration with an older
timestamp is therefore treated as already covered. If its change must reach
those databases, keep the original migration unchanged and add an idempotent
repair migration whose timestamp is greater than the aggregate version.

Different releases may use different paths. For example, `v0_1` may have one
aggregate history row while `v0_2` still has full dev history rows.

`Supersedes` remains only for old flat-layout squash-history compatibility. It
is not the structured release's dev migration inventory.
`MIGRATION_FAKE_VERSIONS` is not used.

## Deleting dev SQL

Do not modify, rename, or individually delete a committed dev migration. An old
release's entire dev directory may be archived only after the release is closed,
its aggregate SQL represents the complete release, and the dev path has shipped
for a full release cycle.

When an archived release has no dev directory, the runner uses the combined
history version's release prefix:

1. no dev history for the release: execute the aggregate;
2. any dev history for the release: keep the dev histories and skip the
   aggregate, because the database used the dev path.

Without the archived SQL files or a separate inventory, the runner cannot tell
a complete dev path from a partially applied one. Archiving a partially migrated
release is therefore outside the supported release process. Restore the original
unchanged dev directory to repair or roll back such a database; the runner cannot
reconstruct missing up or down statements from history alone.

Adding a dev migration to a retained release requires updating its aggregate SQL
in the same change.

Create a new dev migration with:

```sh
go run ./cmd/dbmigrate create -name create_users -version v0_3
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
startup. With the current catalog, a fresh database executes the v0.1 aggregate
and then the v0.2/v0.3 dev migrations; an unversioned v0.1 Desktop database
executes the same idempotent v0.1 baseline and then the explicit dev
table-rebuild/data migrations. Rename, drop, constraints, indexes, seed data, and
preserved columns are therefore part of reviewed migration files rather than
being inferred from the ORM at user startup.

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
preserving and transforming data, compares the aggregate path through v0.3 with the
matching development-migration path, checks every ORM table and column, migrates legacy
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
