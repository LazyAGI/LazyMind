# Basic Workflow self-test

Use the short request and fixed fixtures; keep this test small and deterministic.
Call each step's declared smoke tool: exercising tool invocation is part of this
workflow's acceptance criteria. To read returned files and saved inputs,
use the host's own capabilities first. Files returned by tools must be materialized
before publication; preserve their exact bytes and newlines.

Use actual saved inputs and publication acknowledgements as evidence. Do not
reconstruct missing inputs or infer a revision number from a filename. On retry,
reuse acknowledged publications; a resumed run can have revision numbers above two.

The only human review follows update. Verification follows the user's approval.
