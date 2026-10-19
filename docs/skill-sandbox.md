# Skill Bash and CLI sandbox

LazyMind runs Skill Python, Bash, JavaScript and argv-based CLI commands through an isolated runner. Production and shared environments must use the `runner` backend. The runner is unavailable by default unless its Unix socket is mounted; LazyMind fails closed and returns `SANDBOX_UNAVAILABLE` instead of executing the command in the chat container.

## Start locally with Docker Compose

```bash
docker compose up -d --build sandbox-egress sandbox-runner chat
```

Compose creates a private Unix-socket volume between `chat` and `sandbox-runner`. The runner has a read-only root filesystem, bounded tmpfs workspaces, dropped capabilities, a PID limit and memory/CPU limits. Skill packages are uploaded and kept read-only; execution uses a per-session writable workspace. No host workspace, Docker socket, service credential, database volume or user home is mounted.

The root supervisor only prepares workspaces and launches each execution under a dedicated non-root session UID. A single-threaded launcher applies rlimits and a child seccomp filter before `exec`; the filter denies bind/listen/accept syscalls, so Skills cannot expose services. The runner can only reach an internal egress proxy. That proxy is the only sandbox component attached to the external Compose network. It requires a short-lived per-execution token, permits only declared HTTP/HTTPS domains on ports 80/443, pins HTTP Host and HTTPS SNI to the approved destination, rejects IP literals and private/rebound DNS results, and reports policy rejections back as structured `NETWORK_DENIED` failures.

## Skill metadata

The runner recognizes both LazyMind fields and compatible OpenClaw metadata:

```yaml
---
name: report-builder
description: Build a report with Python, Bash and jq.
required_env: [REPORT_API_KEY]
required_bins: [bash, python3, jq]
runtime_profile: base
input_files: [templates/report.md]
declared_outputs: [output/*.pdf]
metadata:
  openclaw:
    requires:
      bins: [bash, python3, jq]
      env: [REPORT_API_KEY]
  security:
    allowed_domains: [api.example.com]
---
```

`allowed-tools: Bash` is treated only as a model-routing hint and never grants execution permission. `run_skill_command` accepts an argv array, not a shell string. Inline `bash -c` and `sh -c` are denied; reusable shell logic must be stored under `scripts/` and invoked with `run_skill_script`.

Network access is denied by default. A Skill may declare exact domains or wildcard subdomains such as `*.example.com`; the declaration is policy input, not blanket network access. Direct IPs, undeclared domains, arbitrary TCP, SSH tunnels and listening ports remain unavailable.

The `base` image profile provides Bash/sh, Python 3, Node.js, Git, curl, jq, ripgrep/grep/sed/awk/find, zip/unzip/tar, ffmpeg and pandoc. Runtime package installation and privileged/system commands (`sudo`, `apt`, `brew`, `docker`, `mount`, `systemctl`, SSH) are not supported. CLI calls must use an argv array. Shell operators, pipes, redirects and expansion belong in a declared `.sh`/`.bash` script; inline `bash -c` and `sh -c` are rejected.

## Execution contract

Both `run_skill_script` and `run_skill_command` are translated to the same versioned request. It carries the runtime, entrypoint or argv, cwd, package tree hash, Skill identity/revision, declared inputs and outputs, dependency/environment requirements, network policy and resource limits. `cwd`, input paths and output globs must remain relative to the Skill workspace; absolute paths, `..`, archive traversal, symlinks and device files are rejected.

Results include `status`, `reason_code`, `exit_code`, stdout/stderr, duration, dependency or blocked resource details, and only the declared output files. Exported files include their relative path, byte size, SHA-256 and base64 content. Undeclared files never leave the runner. Sessions let Python, Bash and CLI steps share intermediate files; the executor releases the session after a run, terminates the entire process group on timeout/cancellation/completion, and removes its workspace.

Each script or CLI invocation has a 180-second wall-clock timeout by default. The CPU-time limit remains 60 seconds and is enforced independently. Deployments may override these values with `SKILL_SANDBOX_TIMEOUT` and `SKILL_SANDBOX_CPU_SECONDS`; the runner caps either limit at 300 seconds.

## Development fallback

`LAZYMIND_SKILL_SANDBOX_BACKEND=local` enables the compatibility backend for tests and single-user development. It runs commands in a temporary host directory and does not provide OS-level network or filesystem isolation. LazyMind logs a warning whenever this backend is selected. Do not enable it in production.

## Structured failures

The runner reports machine-readable failures including `RUNTIME_NOT_SUPPORTED`, `RUNTIME_NOT_FOUND`, `COMMAND_NOT_FOUND`, `COMMAND_DENIED`, `FILESYSTEM_DENIED`, `NETWORK_DENIED`, `MISSING_ENV`, `TIMEOUT`, `RESOURCE_LIMIT`, `OUTPUT_LIMIT`, `EXIT_NON_ZERO` and `SANDBOX_UNAVAILABLE`. The original stderr/stdout and exit code are retained when available.
