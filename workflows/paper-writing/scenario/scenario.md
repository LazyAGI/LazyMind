# Paper writing

Start when the user explicitly requests paper writing or academic manuscript review.
For uploaded manuscripts, bind the exact attachment filename to manuscript_file; do not transcribe the manuscript.
Bind pasted text to manuscript_text; paper_topic and review_request are optional plain text.
If no topic is supplied, code derives a default from the manuscript title or filename.
For new writing, use uploaded Markdown materials/outlines and SVG/PNG/JPG images; write_outline reads the attachments directly.
Run write_outline and stop for user confirmation of its editable artifact.
Only after confirmation run write_document using the selected outline revision. Choose peer_review only when the user requests a review; uploading or having a manuscript does not request review.
Use write_document only to draft from the confirmed outline. Use revise_document for all existing-manuscript edits.
Pass the current editing request unchanged through the existing request_context/user_input mechanism.
Direct edits go to revise_document and use the public Writer to plan changes from that request.
For review followed by revision, run peer_review first and stop for the user's revision instructions.
When the user requests revision according to the review, use all current issues by default; honor explicit issue numbers, exclusions, or categories for selective revision.
Direct edits must ignore historical review advice. Never broaden a partial-review request to all issues; clarify only ambiguous selections. Missing materials or experiments remain pending and must not block other executable text edits.
A manuscript or historical review alone does not request review-driven revision.
If the request does not distinguish review from editing, clarify the intended action before execution. Keep the file's request and pass additions through request_context;
review_request remains a peer-review requirement.
Never treat filenames, paths, or summaries as manuscript text. If multiple attachments make the target ambiguous,
ask which manuscript to review.
Use fictional examples only for demo=true or an explicit request for default demonstration materials.
Otherwise ask for missing real materials; never silently fall back to fabricated examples.
F3 is an explicit exception for labeled synthetic previews, not for research evidence: it designs an
experiment from the supplied question/evidence, generates downloadable code, and runs simple CPU
experiments within the existing bounded executor when authorized. Complex or failed experiments
return labelled synthetic previews. If real results only are requested, do not substitute a preview.
Use the package's framework ask_user execution preference before startup. No separate preparation
node or permission UI is needed. Only explicit consent may bind allow_local. The experiment tool
owns execution and status; never run its generated code separately through Chat tools.
For direct experiment entry bind experiment_question from the user's stated goal; ask if missing.
After F3, the adapter converts final experiment.results and result_meta into F4 typed data and context;
never analyse the original input dataset as if it were the experiment output.
When paper_input is bound, use it; validation failure must not trigger a fallback to default examples.

Select the algorithm node matching the user's goal without requiring preceding steps.
Review requests enter peer_review directly. Code initializes materials before execution; there is no prepare step.
Wait for the user's selection at each step; finish is available at any point.
Literature and review feedback use forward nodes:
literature_review → refine_topic, then rewind literature_review when the user asks to continue;
peer_review → revise_document, then rewind peer_review when the user asks for another review.
Native rewind preserves confirmed feedback snapshots before invalidating downstream state, allowing multiple rounds.
Do not reinitialize materials. Starting over differs from continuing with feedback; use a new session to clear feedback.

Allowed routes: F1→F2; F2→F1/F3/F5/F7; F3→F4; F4→F5; F5→F6; F6→F5; finish after F7.
Do not add other execution edges. refine_topic is a feedback operation of F1; revise_document handles direct and review-driven F5 revisions,
not additional algorithm capabilities. Within F5, write_outline → write_document is an approval boundary,
with separate outline and manuscript nodes in the frontend. Do not continue from outline to body without user confirmation.
This workflow produces manuscripts or reading reflections. It does not generate or hand off to external workflows,
ingest into the knowledge base, or execute experiments.
Surface failures and ask for materials or a retry; never fabricate success.

## Chat completion

After the steps requested in this turn succeed and their artifacts are published, acknowledge completion in one
short sentence in the user's language and direct them to the workflow.
Do not repeat the document, review, or JSON, or list issues, conclusions, or next-step options.
Do not call list_artifacts, read_artifact, read_file_resource, or similar tools merely to summarize, repeat,
or reconfirm successfully published artifacts.
Read results when explicitly asked to explain them; continue subsequent steps when explicitly requested,
then acknowledge briefly once the current request is fulfilled.
For a review-only request, stop after successful review without automatically revising or finishing the workflow.
On failure or missing required input, explain the actual cause and wait for user action; do not claim success.
Always relay necessary user_notice messages from tools.

Use the configured Chinese stage labels when addressing users; do not expose F numbers or internal identifiers.
Do not show material preparation as a node or checklist document. On parsing failure, ask for a readable manuscript.
finish is an internal control node. Users may request it in Chat; existing artifacts are preserved.
Subsequent actions must select nodes that actually exist in the execution graph.
