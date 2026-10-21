# AI Image Production V2

## Node index

The executable nodes are `classify_request`, `ordinary_prepare`,
`ordinary_generate`, `edit_prepare`, `edit_candidate`, `meme_brief`,
`meme_preflight`, `canonical_character`, `build_identity_anchors`,
`understand_states`, `design_best_performances`, `plan_static`,
`render_static`, `caption_static`, `plan_dynamic`, `render_keyframes`,
`video_canary`, `render_dynamic`, `caption_dynamic`, and `review_pack`.

This workflow follows the final deliverable. It has exactly three top-level
routes: ordinary generation, authoritative-source local editing, and meme or
reaction-sticker production. A meme deliverable always uses the meme route,
even when its character begins as an upload, web image, or generated image.
Ordinary generation and authoritative local editing use this workflow's
`baoyu_image_generator` and `baoyu_image_editor` adapters, pinned to Seedream
5.0 without external skill folders or runtime package downloads. Meme production
continues to use its existing native media tools with the optimized planning,
identity-lock, review, caption, and delivery stages defined by this package.
The classification step saves exactly one supported route in `route_plan`, exits
cleanly, and lets the workflow engine select the sole matching transition. The
ChatAgent never chooses among all three candidates itself. The performance-design
step follows the same contract: after saving `best_performances`, it exits cleanly
and lets the workflow engine select exactly one of `plan_static` or `plan_dynamic`
from the approved brief.

Meme production has exactly two delivery modes: `static` and `dynamic`. If the
user has not selected one, stop in the brief step and ask. Do not generate both.
"No text" disables the local caption but does not create a third delivery mode.
Dynamic delivery defaults to captions unless the user explicitly requests no
text. When exact captions are absent, propose one concise, theme-appropriate
caption for every state and ask the user to approve the complete concrete set.
Do not ask the user to invent subtitle content before making that proposal. If
the user rejects any proposed caption, preserve accepted captions and only then
request exact replacement text for the rejected states. Production remains
blocked until the final ordered caption set is approved.

The meme brief is committed through the workflow-owned
`save_validated_meme_brief` boundary.
Before the write is accepted, the runtime compares the complete brief with the
immutable launch request, enforces all required fields and count relationships,
and preserves any explicit caption list verbatim and in order. A reduced,
malformed, reordered, or placeholder brief is rejected before media preflight
and therefore cannot silently reduce the final pack size. The validator writes
the approved object through LazyMind's standard artifact store after validation.

Before pack production, present the complete ordered meme scheme with each
state's caption, communication purpose, and action design, and stop for the first
human approval. Establish one canonical character and one reusable identity lock
grounded in that image. The lock records immutable visual
traits, including facial and eye construction independently from the current
expression, and separates them from pose, expression, camera, background, and
scene props. After producing the four eye-and-mouth anchors and the 2x2 sheet,
stop for the second human approval. Every downstream key-image plan copies the
same lock unchanged.
Each communication state first receives one broad state-performance proposition,
then one complete BestPerformance adapted to the character. A BestPerformance
uses one readable action and emotional progression without redefining identity.

Static production creates one decisive hero frame per state and calls an image
model exactly once per state by default. Dynamic production also creates exactly
one decisive, text-free key image per state, using the canonical image as the sole
source and the same identity lock in every call. That exact key image is the strict
hard first-frame reference for exactly one video task. Its motion-only prompt
describes the action, emotional progression, and readable payoff without
restating or reinterpreting appearance. No additional still frames are generated
or required for dynamic production. Stop for the third human approval after all
static key images are available. Generate the canary and remaining videos, then
stop for the fourth human approval of the complete video set. Continuing from
that checkpoint starts GIF conversion. Rejection at any checkpoint rewinds the
earliest affected producer, applies the user's latest correction, and preserves
accepted outputs unchanged.

All media-model prompts prohibit overlay captions. Exact captions are rendered
locally only after the actual still or GIF has been inspected for stable natural
whitespace. Run preflight before any paid media call. Do not automatically retry,
rewind, regenerate, or silently switch models. Review the whole pack once after
GIF conversion and report any failed state or asset without adding a fifth human
approval checkpoint or starting another paid media call.
