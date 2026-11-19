# Research Synthesis — First-Session Project Setup (Synthetic Fixtures, Six Participants)

**Editorial revision.** Model-generated report, shortened and source-corrected after reviewer checks; prior Attempts and Artifacts remain available. All recommendations are proposals.

## Research Overview

**Scope.** Which small onboarding improvement should the team investigate first, within one two-week iteration? Input is one fixture (`research_data`) in which all people, organisations and observations are invented: P01–P06, four on first use (P01, P02, P04, P06) and two experienced weekly users (P03, P05); three project managers, three analysts.

**Evidence base.** Six recorded observations, each with one verbatim quote. No analytics, survey, revenue, pricing, timing or severity data; no participant made a pricing claim. This convenience sample cannot estimate population prevalence, and role/experience cohorts of two to four cannot be compared quantitatively.

**Method.** Count distinct participants, denominator 6; rankings are judgement, not population estimates.

## Evidence Table

| Participant | Recorded observation | Exact quote | Evidence type |
|---|---|---|---|
| P01, new project manager, first use | Failed to locate CSV import, then asked a colleague; completed setup after help | "I expected Import beside New project." | Observed failed search and colleague help; quote is a stated expectation |
| P02, new project manager, first use | Searched the settings menu for CSV import; found it only after a facilitator hint; later setup completion unknown | "I looked in settings because I could not see an import button." | Observed search path and hint; quote is stated rationale |
| P03, experienced analyst, weekly user | Found CSV import immediately; accidentally reimported a file because upload progress was invisible | "I clicked again because nothing seemed to happen." | Observed behaviour plus stated reason for the second click |
| P04, new analyst, first use | Failed to find CSV import; after a hint uploaded successfully but could not tell when it finished | "Now that I found it, I still cannot tell whether the upload finished." | Observed discovery failure; quote reports perceived upload state |
| P05, experienced project manager, weekly user | Completed CSV import without help; used the template gallery successfully. No speed or immediacy is recorded | "The template gallery already gives me what I need." | Observed unaided import; quote is a stated evaluation |
| P06, accessibility-focused analyst, first use | Located import with keyboard navigation; could not read low-contrast validation text; could not correct a rejected row without help. Whether help was supplied, and whether correction or setup completion happened, is unknown | "The error text is too faint for me to read." | Observed keyboard discovery and blocked recovery; quote reports legibility |

## Key Findings (ordinal ranking, not measurement)

### F1 — Import entry point not self-evident to some first-time users (3 of 6)
**Evidence.** P01 failed to locate import and asked a colleague; P02 searched settings and found it only after a facilitator hint; P04 failed and uploaded only after a hint. All three were first-use participants.
**Impact.** High in position: failure at the first step of setup, before any value. Observed consequence is bounded — help was sought or given; time, retries and abandonment are unknown.
**Counterevidence.** P03 and P05 found import; P06 reached it unaided by keyboard, a direct counterexample to a universal first-use failure.
**Confidence.** Moderate on direction; low on magnitude and breadth.

### F2 — Upload progress and completion state invisible (2 of 6)
**Evidence.** P03 found import immediately but reimported a file because upload progress was invisible; P04 uploaded successfully after a hint but could not tell when the upload finished. Two experience levels, same missing state signal. The friction is downstream of discovery, so a discovery fix would not remove it.
**Impact.** Potentially high; magnitude unknown, since whether the second import created duplicate data was not observed.
**Counterevidence.** None recorded. P05's smooth import is not counterevidence: no question about upload feedback is recorded for P05.
**Confidence.** Moderate that the symptom is real; low on consequence.

### F3 — Low-contrast validation text blocks error correction (1 of 6)
**Evidence.** P06 found import unaided by keyboard but could not read the low-contrast validation text and could not correct a rejected row without help. Whether help was supplied, and whether the row was corrected or setup completed, is unknown.
**Impact.** A complete block at the recovery step for that participant; breadth unknown. Low frequency is not grounds for dropping it, so it stays ranked and scheduled regardless of count.
**Confidence.** Moderate that the blocker is real for P06; low on breadth. Only legibility is established — wording, message structure and assistive-technology context are not recorded.

### F4 — Assistance dependency limits what success means (4 of 6)
P01 asked a colleague, P02 received a facilitator hint, P04 received a hint, and P06 could not correct a row without help. P02 and P06 completion is unknown. This is a measurement concern, not proof that all sessions completed.

### F5 — Placement expectation is stated preference, not observed behaviour (2 of 6)
P01 expected Import beside New project; P02 explained looking in settings because no import button was visible. These statements motivate testing placement and wording; they do not establish a preferred design. Confidence is low.

### F6 — Keyboard route to import already works and must be preserved (1 of 6)
P06 found import using keyboard navigation. Preserve that observed route; this does not establish end-to-end keyboard accessibility.

## User Segments
Tentative failure-mode groups: discovery P01/P02/P04; upload-state feedback P03/P04; recovery P06. P04 overlaps. These are not validated personas or population segments.

## Opportunity Areas

- **O1 (F1, F5, F6):** an entry point matching first-use expectations — to be tested before anything is built.
- **O2 (F2):** visible upload state and an unambiguous finished state.
- **O3 (F3, F6):** legible, recoverable validation errors, keyboard route preserved.
- **O4 (F4):** a measure of unaided progress needed to interpret any later change.

## Recommendations (all proposed; none implemented)

1. **Define the baseline first.** Write one definition of unaided first-attempt progress and record it before any change. Success signal: a written definition plus a captured baseline for the next round.
2. **Check validation-text legibility as an engineering task,** including the keyboard route through a rejected row. Success signal: a recorded legibility finding and a pass/fail on the keyboard path, raised regardless of participant count.
3. **Test placement versus wording** in a small new convenience round on static mocks (current placement, import beside the new-project action, label-only), recording first click. Success signal: a clear ordering or an explicit null result; no prevalence claim.
4. **Probe upload feedback** with a scripted repeated-upload scenario, recording product behaviour and participant expectation. Success signal: a written record of double-submit behaviour.
5. **Decide rather than ship:** at the end of two weeks, propose at most one user-facing change with acceptance criteria, or record a documented decision not to change. No effort or cost was observed; any schedule is a proposal only.

## Open Questions and Evidence Gaps
Placement versus wording, repeated-import consequences, and validation recovery need investigation. P02 completion and P06 assistance/correction/completion remain unknown. No timing, revenue, prevalence, statistical significance or validated persona claims are supported. Proposed tests have not run and recommendations have not been implemented.
