# Model execution boundary v1

## Sole permitted nested model path

Only execution of a Workflow step may invoke another model, and only through the
framework's deterministic Executor Supervisor creating a SubAgent after Runtime
accepts `advance_step` (or the LazyMind-only handoff variant). The SubAgent receives
the fixed Attempt Context and permitted Host capabilities, produces step outputs,
and returns. This is the only nested model boundary in the Agent Kit.

The Supervisor—not the model—claims the Attempt, maintains lease/heartbeat,
forwards progress, validates required outputs, saves Artifacts, and commits exactly
one terminal result. A SubAgent cannot publish a Workflow, alter projection state,
or decide that missing required output is success.

## Tools that must never invoke a model

- connection and discovery;
- Workflow definition and projection reads;
- prepare/start, input binding, stop/resume, and state transitions themselves;
- Ready computation and Runtime execute/retry/rewind resolution;
- Input Resource import/read/bind and Artifact list/read/patch/delete/persistence;
- Skill snapshot and conversion context reads;
- draft create/update, compile, diagnostics, script audit, and publish;
- permission, capability, contract-version, and idempotency checks.

`advance_step` is a composite boundary: its Runtime transition and Supervisor
operations are deterministic, while the explicitly created SubAgent may use the
Host model to perform the accepted step. No other tool may hide a generation,
classification, repair, review, or routing model call.

## Agent behavior

The main Agent performs interpretation and authors text using its already-active
Host model. It must not ask infrastructure tools to “AI generate”, “AI repair”,
or “AI decide”. For authoring, the main Agent writes file content, then submits
that exact content to deterministic tools. For lifecycle decisions, it reads the
projection and applies this Skill. For output review, the main Agent applies
acceptance criteria; a separate review SubAgent is permitted only when it is an
explicit Workflow step executed through the same Supervisor boundary.
