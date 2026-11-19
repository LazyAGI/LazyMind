# AI PPT Planner workflow

Create a presentation through one shared business flow. The step prompts and
acceptance criteria in state.yml govern all executors; execution.md supplies
workflow-wide quality rules. Tool descriptions own invocation details.

Before launch, preserve the user's complete request and bind supplied template
and source references. Source reading, work-record selection and evidence
analysis happen inside the workflow. Uploads are optional. Reuse confirmed
answers, and do not ask for a quick/full generation mode.

The visible stages are:

1. analyze_requirements: scope, audience, count, style and output choice; inspect
   every uploaded template page and record its content/evidence requirements.
2. collect_materials: when evidence is needed, read required sources, perform
   academic analysis and optionally inspect/select images in the same step.
3. plan_background_prompts: when enabled, one editable text-free scene per page.
4. generate_backgrounds: generate, normalize and publish; visual review is advisory.
5. build_outline: one authored Markdown content outline with the requested count.
6. plan_page_prompts: complete per-page style/layout/copy/assets and separate scripts.
7. generate_ppt: author the selected representation, validate and publish.

Follow the actual declared transitions and skip markers. Self-contained requests
may skip collection. Uploaded templates disable AI backgrounds, while evidence
figures and screenshots remain allowed. Host approval mode controls review pauses.

Confirm the choice of interactive HTML with PPTX export or direct native PPTX at
startup whenever the user has not specified a route. With a template, recreate
its inspected style in HTML or compose editable native pages from its actual
layouts. HTML runs publish slides and notes. The user can export those slides
from the panel. Native delivery completes once the PowerPoint file is published.

For follow-up edits, use workflow.yaml's completed actions and edit policy. Local
edits preserve unaffected pages and current item identities. Regeneration requires
an explicit request and is limited to its requested scope. Keep linked prompts,
backgrounds, HTML and notes aligned when editing an interactive deck. Native
PPTX edits update the PowerPoint file on their own route.
