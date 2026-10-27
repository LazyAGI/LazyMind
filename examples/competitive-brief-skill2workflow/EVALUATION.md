# Evaluation: observations, not a benchmark

Date: 2026-10-27. Same [task](evidence/task.md), [seven fact cards](evidence/source-cards.md) and final acceptance criteria. Research was deliberately offline in both modes. Different model environments, different packaging context and assisted retry instructions prevent causal speed/cost/quality comparisons. Three baseline runs and one fresh converted Workflow are not a stability estimate.

## Results

| Measure | Original Skill | Automatic hosted Workflow | Assisted session recovery |
| --- | --- | --- | --- |
| Sample size | 3 complete CLI runs | 1 fresh conversion; separate rejected URL probe | Same session; one failed step retried |
| Conversion | Not applicable | Generated and published successfully | Generated package unchanged |
| Task completion | 3 reports delivered | Failed after 2/5 steps; no final result artifact returned | Session completed 5/5; final report available; original external task still failed |
| Report structure | 7/7 sections in each | No final `competitive_brief` at failure | 7/7 sections, 5 actions, follow-up options |
| Four annual seat subtotals | Correct in all three | Not a final-report result | Correct in recovered report |
| Sources/claim boundaries | Seven source URLs, dated scope, fact/inference/unknown labels; checks below | Intermediate research and analysis retained | URLs/date present; several overclaims and label problems remain |
| Step observability | CLI tool events and final text | Runtime step IDs, states, artifact revisions and failure reason visible | Failed and successful attempts remain visible |
| Human review | Pending; no approval claimed | No generated approval gate | Still pending; operator edit attempts failed |
| Recovery | No injected failure test | Research internally recovered from six tool errors; analysis failure stopped task | Official retry succeeded; original research reused; two downstream advances completed |
| Wall time | 146.286 / 212.477 / 133.378 s | 615.843 s from task creation to terminal failure | 1,190.634 s creation to recovered session end, including diagnosis/idle time |
| Observed tool calls | 2 / 3 / 3 file-reading commands | 28 step-level tool calls | 3 additional `save_artifacts` calls; 31 total |
| Model invocations / money | Not exposed / unavailable | Not exposed / unavailable | Not exposed / unavailable |

Baseline mean wall time is 164.047 s; median 146.286 s. These are observations of this setup, not evidence that the baseline is generally faster. The hosted timing includes conversion and publication; the baseline timing does not. The recovered-session total includes time spent investigating, writing documentation and running checks.

### Available baseline token usage

| Run | Input | Cached input (subset) | Output | Reasoning output reported by CLI |
| --- | ---: | ---: | ---: | ---: |
| 1 | 58,978 | 48,896 | 4,458 | 57 |
| 2 | 59,181 | 49,024 | 5,044 | 83 |
| 3 | 59,178 | 49,024 | 4,460 | 49 |

Do not add cached input to total input. No dollar cost is inferred. Baseline model identity was not exposed in CLI JSON; Workflow connection reported DeepSeek `deepseek-flash`. Stream event counts and task `step_count` are not model invocation counts.

Workflow call totals cover execution-step tool events only. Conversion status/warnings and its generated package are recorded, but the conversion model's internal invocation/token trace was not exposed by the captured API; it is not included in those totals.

## Actual execution and failures

1. `/blob/` source probe failed `SKILL_INSTALL_FAILED` in about 1.54 s. Resubmitting the same pinned directory using `/tree/` installed the Skill. This is source-format recovery, not a successful conversion of the rejected request.
2. Preflight warned about `web_search` and missing configuration. Model generation published `competitive_brief`, revision `617f74c8-30fc-42da-84a6-0893f8fda218`. The generated package has five steps, all without mandatory approval.
3. `collect_request` succeeded. `research_competitors` made 25 calls: six save calls, thirteen patches, five reads and one file-resource search. Six returned explicit errors (invalid JSON/schema, wrong slot content type, unsupported `.draft` lookup). The lookup targeted this step's own generated draft, not a private document. Research ultimately succeeded with two competitors in revision 2. Revision 1's empty array was an intermediate save, **not** the final research result.
4. `analyze_positioning` attempt 1 saved JSON but failed the completion check demanding the final Markdown report. The external task stopped on 2026-10-27 at 03:46:59.243124 UTC. See [issue record](ISSUES.md).
5. An agent-authored, explicitly step-scoped retry used the official advance API with `retry_origin: automatic`. It retained final acceptance for the downstream report. The HTTP recorder timed out after 90 s; projection later confirmed attempt 2 succeeded. No duplicate mutation was sent. The retry reused research revision 2 and request revision 1.
6. Two further official advances ran synthesis with the original full input and the generated no-battlecard-request branch. The session reached `completed` / `end_reached: true` at 03:56:34.034513 UTC. This is assisted recovery, not an unassisted success or hand-written Workflow adaptation.
7. A reversible agent-only annotation was attempted through artifact PATCH: HTTP 503 `ARTIFACT_PATCH_FAILED`. The documented native slot PATCH also returned HTTP 500. Reads confirmed the report remained revision 1 with unchanged content. No successful edit, rollback or human review is claimed.

The [actual recovered Panel](evidence/panel-recovered.png) displays a completed session beside the original failed external-task message. Both are real and refer to different state records. The report is shown as raw Markdown in the observed Panel; the existence of this Panel is not proof of polished rendering or successful editing.

## Report acceptance and groundedness review

Checks apply to the delivered text, not a model's claim that it complied. The checker verifies all seven headings, seven official URLs, source date, review-pending boundary, claim-label vocabulary and four subtotals: Linear Basic $2,400, Linear Business $3,840, Asana Starter $2,637.60, Asana Advanced $5,997.60. These are annual seat arithmetic, not total cost of ownership.

The operator also compared the four plan rows and selected positioning/content statements to S1–S7. Baseline outputs preserve the distinction between sampled vendor claims and unknown market metrics in those inspected areas. This is a bounded content audit, not independent human review or a claim that every sentence is flawless. All raw reports remain available for inspection.

The recovered [Workflow report](evidence/artifacts/competitive_brief.md) passes structural/arithmetic checks but **does not fully pass groundedness acceptance**:

| Location | Observed problem | Needed revision |
| --- | --- | --- |
| Executive Summary | “Neither vendor publishes the numbers a 20-seat budget actually needs” exceeds a seven-card corpus | Say those costs are unavailable in the supplied cards; do not assert absence across vendor publications |
| Linear strengths | “genuinely usable free tier” and reduced trial friction are labelled FACT | Retain entitlement facts, label usability/trial-friction interpretation as an untested inference |
| Linear claimed differentiators | “AI embedded natively rather than added on” lacks support in S2/S3 | Remove the architecture/implementation claim |
| Content formats | “gated long-form authority” assumes access gating not established by S6 | Remove “gated” or collect evidence in a separate run |
| Pricing table | Asana Personal has a $0 entry under the 20-seat column despite its stated two-user limit | Mark 20-person eligibility unavailable/not applicable; the four paid-plan calculations are correct |
| Opportunities/threats | Some implications and future predictions carry FACT citations without clear inference labels | Split supported observations from analysis; remove unsupported quarter-specific forecasts |

The generated schema calls the source mode `model_knowledge`, while the actual task and artifacts explicitly describe offline supplied-source replay. Tool records contain no live search calls. Treat the schema label as misleading metadata, not evidence of live retrieval or a reason to silently relabel the raw artifact.

## Verification and limits

- `scripts/verify.py`: evidence hashes, terminal states, artifact/attempt counts, section/citation/arithmetic smoke checks, redaction patterns and Markdown/internal links. Passing these checks does not override the substantive findings above.
- Current-main focused Go source-URL and hosted conversion/task tests passed. Current-main Python SDK and agent-kit tests: **92 passed**. Logs are under [checks](evidence/checks/); these tests are separate from the live deployment.
- `git diff --check` and Python syntax checks passed during submission preparation.
- No production changes, no model mocks, no fake success state, no generated screenshot. A real local deployment and authenticated model connection were used.
- Not validated: enforced human approval, successful artifact edit/rollback, a second converted run, same-model comparison, dollar cost, independent vendor correctness, live web research or exact deployed source commit.

This supports an honest PM-facing account of what worked, what needed intervention and what still needs review. It does **not** support a promotion claiming stable autonomous end-to-end success, a quality win or cost savings.
