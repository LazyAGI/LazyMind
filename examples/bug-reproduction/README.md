# Bug Reproduction Brief: external execution, Workflow synthesis

Reproduce a small synthetic arithmetic bug twice, bind the real evidence, and let the model-generated Workflow produce a [reviewed brief](result.md). This is an assisted developer workflow: the external executor runs the fixture, while LazyMind organizes evidence. It does **not** demonstrate command execution inside LazyMind.

Source: [github/awesome-copilot Bug Reproduction Brief](https://github.com/github/awesome-copilot/blob/61fa1fa8fcc63d6dbd2f07a75aeb8e214df5acfb/skills/bug-reproduction-brief/SKILL.md). MIT; the builder retains both repository and original-author licenses. [ORIGIN.json](ORIGIN.json) pins all source URLs and hashes. No Skill wording is changed and no upstream endorsement is implied.

## Reproduce

Requires Python 3, a running isolated LazyMind with Hosted Conversion, a valid user token and a configured model. Model calls consume quota. Review the small [fixture](repro.py) first: its assertion intentionally fails; do not fix it.

```sh
python3 examples/bug-reproduction/prepare_request.py --output /tmp/bug-example
curl --fail-with-body "$LAZYMIND_BASE_URL/api/core/external-agent/workflow-tasks" \
  -H "Authorization: Bearer $LAZYMIND_TOKEN" -H 'Content-Type: application/json' \
  --data-binary @/tmp/bug-example/request.json
```

The builder executes only the local fixed fixture twice in a temporary directory, with isolated Python and a 10-second timeout, verifying exit 1 and the expected AssertionError. It saves real logs, verifies upstream hashes and builds a deterministic Skill ZIP plus a new conversion request. No hand-authored Workflow YAML is used. Inspect the generated input IDs, step states and final Artifact; check that evidence is attributed to the external executor and two failing test exits are not described as a failed reporting task.

## Verified run and boundary

Core `405f31ae`, Algorithm `59d2a836`, DeepSeek `deepseek-flash`; the fixes are included in PR #921. Task `1cb46fa7-a6e0-4863-b8ee-02d091c74f01`, Session `684bbfd8-350d-5ce1-84aa-176f9afad4d6`. The API actually reused model-generated Revision `61e6c1f6-6019-46df-91b5-9008cc927b89`; fresh conversion is requested by the reproduction script and may yield a different plan.

All six steps succeeded on their first attempt. Task and Session completed. Two actual external fixture runs exited 1 with identical stderr; source SHA256 was independently checked. Reviewed Artifact `68000e67-8842-4643-b778-c0297bcbc742` was read back successfully. The model draft needed editorial cleanup of unsupported uncertainty and unnecessary replay instructions. Raw logs remain outside Git.

Tool audit: published scripts can be loaded by exact Revision through `load_workflow_tools`, but run as Python in the host process; this is not a restricted shell sandbox. The reused Workflow contains no script tools and its repeatability step explicitly does not execute commands. No permissions were broadened and no executor was added. Approval and autonomous in-Workflow command execution are not validated here.

Demo (75 seconds): run fixture twice (15s), show both expected assertion exits (10s), inspect bound evidence and six-step plan (15s), show completed Task and report (25s), explain external-executor attribution (10s).
