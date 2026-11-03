"""One independent drawing request per figure, sharing all results, data and style."""
import copy
from concurrent.futures import CancelledError, as_completed

from .model import call_model, call_code_model, PlotModelError
from .code_response import CODE_INSTRUCTION, REPAIR_INSTRUCTION, code_response, repair_response, numbered_source
from .code import ALLOWED_IMPORTS, FORBIDDEN, FORBIDDEN_ATTRIBUTES
from .skills import PlotSkills
from .labels import display_unit
from .pipeline import draw_one, limits, FigureRenderError
from ..analysis.results import result_rows
from ..analysis.schema import prompt_tables, table_map
from ..analysis.model import facts
from ..model_context import model_prompt
from ..analysis import trace as diagnostics
from ..execution.threads import ThreadPoolExecutor


def generate_code(data, statistics, spec, reference, previous, error):
    prompt = '''Implement def draw(data, statistics, spec): for ONE figure and return a Matplotlib Figure.
Use the plain function declaration without type annotations; no annotation-only imports are needed.
Import names actually used in executable statements explicitly; an alias binds only that alias.
Prefer 1 or 2 data panels, with at most 4 in this figure/script. Implement only this figure's planned purpose;
do not expand the full analysis context into a large dashboard or add unrelated panels and duplicated plotting code.
Use loops and the supplied helpers for related series. Additional planned figures have their own independent scripts.
data.analysis_results is the COMPLETE executed result table, with values, meaning, units, intervals, source columns,
scope, coordinates and comparison direction in each row. display_unit is an optional formatted unit symbol with the
original numeric scale; unit preserves source metadata. At execution data.tables contains the COMPLETE original
source tables, each with columns, column_types, units and rows (lists of cells). This request shows a table marked
rows_omitted only by row_count, column_profiles and sample_rows; draw still receives all of its rows.
data.facts supplies the research topic and context.
spec.purpose/requirements describe this figure; spec.finding_ids identifies its focus. Other results are context.
These requirements are generated planning hints, not additional observations. Actual result rows are authoritative
if a hint implies an incorrect value/direction. Derive numeric annotations from runtime values, not literal planning text.
Use actual runtime values, never hardcoded example numbers. Do not recompute estimates/tests/intervals.
Keep data/statistics unchanged; construct local label lists or copied frames for presentation.
Raw rows can be used for real distributions/paired observations when appropriate; do not fabricate missing observations.
Write readable English axis/tick/legend labels yourself from scientific meaning. Use shared terminology consistently.
Source IDs and filter expressions are provenance, not display labels. Keep every selected comparison and its direction;
do not drop adverse values or imply unrelated x/y scopes are paired. State subtraction direction with concise level
names and define their full meanings in the caption. Shared terminology identifies scientific meaning; if its full
wording is too long, use consistent concise equivalents and explain them in the caption.
The relevant skill source and actual helper signatures are provided below. Helpers are injected as globals: call their
names directly, without importing skill modules or copying/redefining helpers. Reuse helpers when they fit; ordinary
Matplotlib code can implement a needed geometry. There is no mandatory result_grid or host-prepared panel protocol.
spec.style_contract provides style-specific appearance/layout; data.shared_style supplies common terminology,
series_order and group colors for ALL figures. Preserve those shared choices across independently generated figures.
series_order and series_colors use ORIGINAL coordinate values; terminology maps those keys to display labels.
For each panel derive its actual groups from the selected result rows and chosen coordinate. Filter the global order
to these observed groups, then append any remaining observed groups. The global order is not a data inventory for
every panel. Never filter observations by translated labels or key summaries by display labels.
Keep summaries indexed by actual group keys and pass display names only where a helper signature accepts them.
Identify every displayed series with a legend or direct label. Build legend handles and labels together from the
actual plotted artists so a shared legend does not silently omit a series.
Ticks identify categories, not full condition descriptions. Avoid repeating factor names and shared sample sizes on
every row. For multifactor groups use short single-line level combinations, aligned factor annotation columns,
grouped headings or facets rather than one multiline paragraph per row. Put full definitions in the external caption.
State uniform sample support once; omit identical per-row n labels and retain per-group n when it differs or affects
interpretation. Reserve multiline ticks for names that cannot be shortened, with enough row spacing for their text.
Do not truncate words, collapse distinct conditions, or drop comparison direction.
Choose the label hierarchy before applying line breaks; wrapping full repeated descriptions does not simplify them.
Use brief high-level axis titles. Keep long factor-order keys and metric definitions/relationships in a horizontal
key or external caption, not in a concatenated vertical title or an overloaded tick label.
Fonts are applied at export
by the host; use fontweight for emphasis and leave font families/rcParams to the host. Use line breaks or horizontal
comparisons for long category names; move complete definitions into the caption. The host can wrap long category ticks.
All-positive bars start at zero. Differences have a zero reference. Do not change numeric scales/units silently;
do not append redundant dimensionless 0-1 unit text. Preserve actual interval meaning; null means unavailable.
In dense plots, individual observations do not need numeric labels. Keep any summary-value annotation clear of
points, lines and other text; omit redundant value labels when the axis and accompanying analysis already convey them.
Include a figure-specific analysis in the caption, in the paper language and within 200 characters.
Lead with the main observed pattern and what it implies for the relevant empirical research question, supported
by at most one or two decisive numerical comparisons from actual executed results. Explain the implication:
for example a conditional benefit, an adverse result, a resource-efficiency conflict or an unresolved interaction,
only when supported by this figure. A list of values or 'this shows a tradeoff' alone is insufficient.
Use only the minimal conditions/symbol definitions, sample and uncertainty notes needed to understand that
interpretation; do not spend the whole caption restating axes, formulas, every interval or repeated provenance.
Keep the explanation specific to this figure; the final narrative synthesizes across figures. Preserve material
limits and simulation disclosure. Do not invent mechanisms, significance or causal effects, or put hardcoded
result judgments on the canvas.
In-figure numeric labels use the actual values.
Keep full figure titles/captions outside the canvas; axis names, panel identifiers and scientific annotations belong inside.
No file/network/process access, installation, save/show calls or extra statistical computation. Return the figure; the host exports it.
Use only the actual allowed imports below. The three function arguments are dictionaries. No attribute-compatibility readers,
schema guards or validation frameworks. On repair use the specific error and preserve evidence; a simpler honest layout is allowed.
'''
    request = call_model if previous else call_code_model
    rows = [{key: value for key, value in row.items() if key != 'display_unit'} for row in data['analysis_results']]
    answer = request(model_prompt(
        prompt, facts=data['facts'], tables=prompt_tables(data['tables']),
        shared=dict(analysis_results=rows, statistics=statistics, shared_style=data['shared_style'],
                    execution_rules=dict(allowed_imports=sorted(ALLOWED_IMPORTS), forbidden_names=sorted(FORBIDDEN),
                                         forbidden_attributes=sorted(FORBIDDEN_ATTRIBUTES)),
                    skill_reference=reference),
        task=dict(spec={key: value for key, value in spec.items() if key != 'caption'},
                  display_units={row['id']: row.get('display_unit') for row in data['analysis_results']},
                  previous_code=numbered_source(previous) if previous else '', error=error,
                  caption=spec.get('caption', '')),
        response_instruction=REPAIR_INSTRUCTION if previous else CODE_INSTRUCTION))
    source = repair_response(answer, previous) if previous else code_response(answer)
    if isinstance(answer.get('caption'), str) and answer['caption'].strip():
        spec['caption'] = answer['caption'].strip()
    return source


def prepare_figures(analysis, figures, inputs, shared=None):
    """No per-row slicing or speculative coordinate inference; every figure sees the same evidence."""
    skills = PlotSkills()
    shared = shared or {}
    base = copy.deepcopy(skills.paper_profile['base'])
    base.update(terminology=copy.deepcopy(shared.get('terminology', {})),
                series_order=copy.deepcopy(shared.get('series_order', [])))
    base['series_colors'] = {name: base['colors'][index % len(base['colors'])]
                             for index, name in enumerate(base['series_order'])}
    data = dict(analysis_results=result_rows(analysis), tables=table_map(inputs),
                facts=facts(inputs), shared_style=base)
    for row in data['analysis_results']:
        row['display_unit'] = display_unit(row['unit'], base['terminology'])
    statistics = {key: copy.deepcopy(analysis[key]) for key in ('methods', 'models', 'limitations', 'targets',
                                                               'data_sha256', 'input_sha256')}
    prepared = []
    for figure in figures:
        contract = skills.style_contract(figure['style'], figure.get('base_style'))
        spec = dict(copy.deepcopy(figure), columns=[ref['table_id'] + '.' + ref['column'] for ref in figure['columns']],
                    style_contract=contract, data_contract={'results': 'executed', 'tables': 'complete_original'})
        reference = skills.reference(figure['style'], figure.get('base_style'))
        # Each task receives private copies; generated code/repair cannot alter another figure's context.
        prepared.append((copy.deepcopy(figure), copy.deepcopy(data), copy.deepcopy(statistics),
                         spec, reference, dict(skills.hashes)))
    return prepared


def draw(analysis, figures, inputs, shared=None):
    attempts, concurrency = limits()
    if not figures:
        return [], []

    def render_one(figure):
        # Preparation is figure-local too: one unavailable style/reference must
        # not discard the other figures' successfully exported files.
        item = prepare_figures(analysis, [figure], inputs, shared)[0]
        figure, data, statistics, spec, reference, hashes = item
        fallback, original_attempts = None, []
        try:
            chart, trace = draw_one(data, statistics, spec, reference, attempts, generate_code)
        except (FigureRenderError, PlotModelError) as exc:
            from .simple import fallback_style, generate_simple_code
            original_attempts = getattr(exc, 'history', [])
            style = fallback_style(figure['style'])
            fallback = dict(reason=str(exc), original_style=figure['style'], style=style,
                            relationship_not_shown=True)
            spec.update(style=style, base_style=None, fallback=fallback,
                        style_contract=PlotSkills().style_contract(style))
            spec['caption'] = ('Simple display of the selected executed estimates; the originally planned geometry '
                               'was not completed. Original units and available intervals are retained.')
            try:
                with diagnostics.stage('figure_simple_fallback', figure_id=figure['id']):
                    chart, trace = draw_one(data, statistics, spec, '', 1, generate_simple_code)
            except (FigureRenderError, PlotModelError) as failure:
                trace = dict(id=figure['id'], title=figure['title'], status='failed',
                             error=diagnostics.safe_error(failure), original_attempts=original_attempts,
                             attempts=getattr(failure, 'history', []), exports={})
                diagnostics.save(f"figure-{diagnostics.figure_name(figure['id'])}-failed.json", trace)
                return None, trace
            diagnostics.save(f"figure-{diagnostics.figure_name(figure['id'])}-fallback.json", fallback)
        result = dict(id=figure['id'], title=figure['title'], columns=figure['columns'], svg=chart['svg'],
                      finding_ids=figure['finding_ids'], caption=spec.get('caption') or figure['purpose'])
        trace.update(exports=chart['exports'], skill_resources_sha256=hashes,
                     concurrency=min(concurrency, len(figures)), caption=result['caption'])
        if fallback:
            trace.update(fallback=fallback, original_attempts=original_attempts,
                         first_attempt_passed_execution_and_file_checks=False)
        return result, trace

    ordered = [None] * len(figures)
    # This executor propagates both request ContextVars and the configured model/session.
    with ThreadPoolExecutor(max_workers=min(concurrency, len(figures))) as pool:
        futures = {pool.submit(render_one, figure): index for index, figure in enumerate(figures)}
        try:
            for future in as_completed(futures):
                index = futures[future]
                try:
                    ordered[index] = future.result()
                except CancelledError:
                    raise
                except Exception as exc:
                    figure = figures[index]
                    trace = dict(id=figure['id'], title=figure['title'], status='failed',
                                 error=diagnostics.safe_error(exc), attempts=[], exports={})
                    diagnostics.save(f"figure-{diagnostics.figure_name(figure['id'])}-failed.json", trace)
                    ordered[index] = None, trace
        except BaseException:
            for future in futures:
                future.cancel()
            raise
    charts = [chart for chart, _ in ordered if chart is not None]
    traces = [trace for _, trace in ordered]
    if not charts:
        errors = '；'.join(f'{item["id"]}: {item["error"]["message"]}' for item in traces)
        raise RuntimeError('全部请求图表生成失败，未发布结果：' + errors)
    return charts, traces
