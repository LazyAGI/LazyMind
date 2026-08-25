# Skill compatibility patches

This directory is the single source of platform-maintained compatibility patches for bundled and downloaded Skills.

Register active patches in `catalog.yaml`. Each entry points to a self-contained `patch.yaml` directory:

```text
patches/
├── catalog.yaml
└── <builtin-skill-uid>/
    └── <patch-id>/
        ├── patch.yaml
        └── files/
            └── <replacement-or-added-files>
```

The catalog is connected once from `skills/builtin-sources.yaml`:

```yaml
schema_version: 1
patch_catalog: patches/catalog.yaml
```

Example catalog entry:

```yaml
schema_version: 1
patches:
  - bsk_example/fix-output-path-v1/patch.yaml
```

Example `patch.yaml`:

```yaml
schema_version: 1
id: example/fix-output-path-v1
description: Fix an output path that is not writable in LazyMind
target:
  uid: bsk_example
  version: 1.0.0
  origin_tree_sha256: <original-skill-tree-sha256>
operations:
  - op: upsert
    path: scripts/run.py
    file: files/scripts/run.py
    before_sha256: <original-run.py-sha256>
```

Patch definitions use ordered `upsert` and `delete` operations. Every operation must declare the expected previous file SHA256, or `absent` for a new file. The target also pins the original Skill tree SHA256, so stale patches fail closed when their source changes.

The catalog contains only active patches. Remove a patch from the catalog after the upstream Skill includes the fix; Git history remains the audit trail.

Patches only produce immutable distribution artifacts. Upgrading an already installed and user-evolved Skill uses the separate three-way merge workflow documented in `docs/skill-distribution-upgrade.md`.
