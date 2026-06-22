# ADR-0001: Use process-compose + lazymind-local wrapper for Local Runtime v1

Status: Accepted
Date: 2026-06-22

## Context

Local Runtime work needs an explicit local entrypoint that:

- keeps Cloud/Server mode as default,
- avoids a one-off script matrix,
- and can evolve from a docker-stack-only local flow to mixed host-process orchestration.

At this stage, only `local/local-runtime-manager` exists as the runtime control layer.

## Decision

For v1 we use:

- `process-compose` as the generic process supervisor/orchestrator.
- `lazymind-local` as a narrow LazyMind-specific wrapper that handles profile selection, path layout, and command mapping.

The wrapper now runs the local flow as one process (`docker-stack`) that executes:

`docker compose -f docker-compose.yml -f local/docker-compose.local.yml ...`

## Rationale

- `process-compose` provides consistent process lifecycle primitives (start/stop/status/logs/health) needed for future non-container process migration.
- A dedicated wrapper preserves separation of concerns:
  - `process-compose` keeps orchestration semantics.
  - `lazymind-local` keeps LazyMind control semantics (profiles, diagnostics, local runtime conventions).
- The Cloud compose baseline remains unchanged for compatibility.

## Consequences

- Local Runtime behavior is explicit and gated behind local targets/commands.
- Existing `make up`/`make down` remain Cloud defaults.
- v1 capability is intentionally limited to docker-stack-only local operation; no Electron or packaging behavior is implied.
