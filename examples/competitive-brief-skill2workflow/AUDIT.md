# Implementation audit

Audited against main `71a0e5a829f1ef8d9a5d9e9ce720386ba3c69d0e`, not just older documentation. The real run used an already running local deployment; its binary has no embedded VCS revision. The manifest records its SHA-256 and Go build metadata. Do not interpret the E2E run as proof that this binary is the audited commit. The focused regression checks run against the audited checkout.

| Boundary | Current implementation | Consequence for this trial |
| --- | --- | --- |
| Install/reuse | `backend/core/workflow/external_agent_skill.go`; `skillv2/sourceurl/github.go` | Hosted tasks accept URL or base64 ZIP. GitHub directory links use `/tree/`; `/blob/` is rejected during asynchronous installation. Reuse matches owner and source key. |
| Preflight | `backend/core/workflow/conversion_handlers.go` | Missing/unpublished Skill or missing SKILL.md blocks. Capability and configuration warnings can proceed. This run returned missing web-search configuration warnings. |
| Real generation | `backend/core/workflow/external_agent_workflow.go`; `generation_service.go` | `POST /external-agent/workflow-tasks` installs, pins the source, runs preflight, queues model-backed generation and publishes the generated draft. This case uses that path, not host-authored YAML. |
| Alternative authoring | `workflow/authoring_handlers.go` | `/workflow-authoring/v1/skill-context` and deterministic draft APIs support a host authoring route. They do not themselves invoke a conversion model. That route was not substituted for automatic conversion here. |
| Publication/reuse | `workflow/external_agent_workflow.go` | Publishes after generation completes, prepares call settings, records immutable revision. `reuse_workflow: false` was used for the measured fresh conversion. |
| Execution/terminal state | `workflow/external_agent_workflow.go`; `external_agent_job.go` | Creates a session, advances ready steps, records progress, collects artifacts. `succeeded`, `failed` and `waiting_user_action` have distinct meanings. A published draft is not completed business work. |
| Observability | `/workflow-sessions/{id}/projection`, `/steps`, `/slots`, `/artifacts` | Read only this experiment's IDs. Capture state changes and actual artifact values rather than model statements about success. |
| Human review | ready nodes with `requires_approval` or human mode trigger handoff | Generated nodes in this run all report `requires_approval: false`. A report saying review is pending is not an enforced gate or a completed human review. |
| Artifact edits/recovery | artifact PATCH/version APIs, session retry/rewind/control APIs | Availability in code is not runtime verification. Distinguish source-link resubmission from recovery of a failed step in the same session. |
| External tools | MCP external mode exposes connection, submit, status and result | HTTP calls below use the same Core task service. A missing injected MCP server does not require rewriting a Workflow. |
| Credentials | `workflow/model_config.go`; SDK connection discovery; owner checks | Generation uses the task owner's model on the explicitly selected instance. Normal authenticated local connection was used; secrets were not exported. No identity or model settings were changed. |

## Findings to preserve

1. **URL compatibility:** A common SKILL.md browser link is accepted as a queued task and then fails installation. The supported repair is a pinned `/tree/<commit>/<directory>` URL. It does not alter Skill text.
2. **Reference boundary:** The source's parent-relative CONNECTORS reference falls outside the imported subdirectory. Preflight reports capabilities but does not report this missing reference in the observed response. The platform also supplies a default `scripts/run.py` absent upstream.
3. **Generated research fallback:** The generated instructions permit a model-knowledge fallback when live tools are unavailable. The frozen task instead requires supplied-source-only work. Inspect the actual artifacts and labels for fidelity; do not equate fallback availability with grounded research.
4. **Review semantics:** All generated steps lack a mandatory approval gate. Human edits and approval need separate evidence; the existence of a Panel cannot establish them.
5. **Deployment provenance:** The serving binary lacks VCS metadata. Tests against current main and execution against this deployment are separate evidence classes.
6. **Execution permissions:** The actual Panel reports full-trust execution with service-process filesystem/code permissions, rather than a Workspace sandbox. That setting was inherited, not changed for the trial. Public-only inputs and inspected tool records bound this experiment; they do not establish sandbox isolation for arbitrary Skills. The baseline separately used Codex's read-only sandbox.

These observations are reported without modifying Core, Runtime, generated Workflow text or the original Skill to make the experiment pass.
