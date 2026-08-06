# Database Migration Development Rules

The migrations in this directory must support both PostgreSQL and SQLite. Follow these rules whenever adding or updating a migration.

In this document, an **aggregate** means the consolidated SQL in `version_mode/v0_N`, including its matching up and down files.

## 1. Purpose of `dev_mode`

`dev_mode/v0_N` contains the incremental SQL produced while developing the current version.

A dev migration that has been merged into a shared Git branch is immutable:

- Do not modify its SQL.
- Do not delete it.
- Do not rename it.
- Do not change its version number or timestamp.
- Do not move it to another version directory.

If a merged dev migration needs to be fixed, add a new migration with a new unique version number and provide both up and down files.

Do not change an existing migration's version number, timestamp, or SQL merely because it was merged late and its timestamp is earlier than the aggregate.

If it is unclear whether a dev migration has already been merged into a shared branch, ask the developer before modifying it.

Every newly generated migration version must use a UTC timestamp with second-level precision in `YYYYMMDDHHMMSS` format.

## 2. Purpose of `version_mode`

`version_mode/v0_N` describes the complete schema and data state that must exist after upgrading from the previous released version to the current version.

By default, each version has at most one aggregate up/down pair.

For an existing aggregate:

- Its SQL may be updated to consolidate dev migrations added later for the same version.
- It must not be deleted.
- It must not be renamed.
- Its existing version number must not be changed.

Do not create a new aggregate, an additional `version_mode` SQL file, or a new `version_mode` directory without explicit developer confirmation.

If the current version does not yet have an aggregate, adding a dev migration does not authorize creating one.

## 3. Consolidate dev migrations into the aggregate

When an aggregate already exists for the corresponding version, every new dev migration must be accompanied by a review and any necessary update of:

- The aggregate up SQL.
- The aggregate down SQL.

A dev migration merged after the aggregate was created must still be consolidated into that aggregate.

This also applies when the late migration's version number or timestamp is earlier than the aggregate. Keep the dev migration unchanged and update the existing aggregate to include its final effect.

Never add a dev migration without checking the corresponding existing aggregate.

The aggregate up SQL must describe the current version's final state. The aggregate down SQL must correctly restore the previous released version's state.

## 4. Aggregates must describe the final state

An aggregate is not a chronological concatenation of all dev SQL. It must directly describe the final schema and data state for the version.

For example, if development first creates a table:

```sql
CREATE TABLE users (
    id VARCHAR(64) PRIMARY KEY
);
```

and later adds a column:

```sql
ALTER TABLE users ADD COLUMN name VARCHAR(255);
```

the aggregate should instead contain:

```sql
CREATE TABLE users (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255)
);
```

Do not preserve the intermediate "create the table, then add the column" sequence in the aggregate.

Apply the same consolidation rule to:

- Adding, removing, or changing columns after creating a table.
- Repeated changes to the same column.
- Repeated index or constraint changes.
- Table or column renames.
- Corrections to seed or initialization data.
- Temporary schema states created during development of the same version.

For tables that already exist in the previous released version, the aggregate may use the necessary `ALTER TABLE`, data transformation, or SQLite table-rebuild operations.

The aggregate must perform the upgrade without losing existing data.

## 5. Support both PostgreSQL and SQLite

Every dev migration and aggregate must account for both PostgreSQL and SQLite.

Portable SQL may be shared. When syntax or database capabilities differ, use the project's existing dialect directives:

```sql
-- +migrate Dialect postgres
-- PostgreSQL SQL

-- +migrate Dialect sqlite
-- SQLite SQL
```

Both up and down files must support both databases.

Do not:

- Implement or verify only PostgreSQL.
- Implement or verify only SQLite.
- Assume that ORM `AutoMigrate` will fill SQLite migration gaps.
- Ignore differences in data types, constraints, indexes, or DDL capabilities.

When SQLite requires rebuilding a table, explicitly preserve and recreate:

- Existing data.
- Old-to-new column mappings.
- Column defaults.
- Not-null constraints.
- Primary keys and foreign keys.
- Unique constraints.
- Regular and unique indexes.
- Required data type conversions.

## 6. Required review and verification

When adding or updating a migration, verify at least the following:

- The dev migration has both up and down files.
- PostgreSQL up and down execute correctly.
- SQLite up and down execute correctly.
- Any existing aggregate has been updated as needed.
- The aggregate describes the final state instead of concatenating dev SQL.
- The aggregate path and the complete dev path produce the same final schema and data.
- Upgrading an existing database preserves its data.
- Down correctly restores the corresponding previous state.

Every change must be directly related to the current migration. Do not modify other dev migrations that have already been merged into Git.
