"""One independent drawing request per figure, sharing all results, data and style."""
import copy
from concurrent.futures import CancelledError, as_completed

from .model import call_code_model, PlotModelError
from .code_response import CODE_INSTRUCTION, REPAIR_INSTRUCTION, code_response, repair_response
from .code import ALLOWED_IMPORTS, FORBIDDEN, FORBIDDEN_ATTRIBUTES
from .skills import PlotSkills
from .labels import display_unit
from .pipeline import draw_one, limits, FigureRenderError
from .standard import STANDARD_STYLES, check_encoding, evidence_caption, generate_standard_code, generate_stacked_code
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
Prefer a moderately landscape canvas.
Use loops and the supplied helpers for related series. Additional planned figures have their own independent scripts.
data.analysis_results is the COMPLETE executed result table, with values, meaning, units, intervals, source columns,
scope, coordinates and comparison direction in each row. display_unit is an optional formatted unit symbol with the
original numeric scale; unit preserves source metadata. At execution data.tables contains the COMPLETE original
source tables, each with columns, column_types, units and rows (lists of cells). This request shows a table marked
rows_omitted only by row_count, column_profiles and sample_rows; draw still receives all of its rows.
data.facts supplies the research topic and context.
selected_results highlights this figure's selected rows. Bind plotted values and coordinates to the same result row.
spec.purpose/requirements describe this figure; spec.finding_ids identifies its focus. Other results are context.
These requirements are generated planning hints, not additional observations. Actual result rows are authoritative
if a hint implies an incorrect value/direction. Derive annotations from runtime values, not literal planning text.
Use actual runtime values, never hardcoded example numbers. Do not recompute estimates/tests/intervals.
Keep data/statistics unchanged; construct local label lists or copied frames for presentation.
Raw rows can be used for real distributions/paired observations when appropriate; do not fabricate missing observations.
Write concise axis/tick/legend labels in the requested paper language. Use shared terminology consistently.
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
Use integer indices for palette, edge, marker and size arrays; series_colors maps original group keys to colors.
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
Keep full titles/captions outside the canvas; axis names, panel identifiers and scientific annotations belong inside.
No file/network/process access, installation, save/show or extra computation. Return the figure; the host exports it.
Use only the actual allowed imports below. Arguments are dictionaries. No attribute-compatibility readers,
schema guards or validation frameworks. On repair correct the actual error while preserving evidence and geometry.
'''
    rows = [{key: value for key, value in row.items() if key != 'display_unit'} for row in data['analysis_results']]
    answer = call_code_model(model_prompt(
        prompt, facts=data['facts'], tables=prompt_tables(data['tables']),
        shared=dict(selected_results=[row for row in rows if row['id'] in spec['finding_ids']],
                    analysis_results=rows, statistics=statistics, shared_style=data['shared_style'],
                    execution_rules=dict(allowed_imports=sorted(ALLOWED_IMPORTS), forbidden_names=sorted(FORBIDDEN),
                                         forbidden_attributes=sorted(FORBIDDEN_ATTRIBUTES)),
                    skill_reference=reference),
        task=dict(spec={key: value for key, value in spec.items() if key != 'caption'},
                  display_units={row['id']: row.get('display_unit') for row in data['analysis_results']},
                  previous_code=previous, error=error,
                  caption=spec.get('caption', '')),
        response_instruction=REPAIR_INSTRUCTION if previous else CODE_INSTRUCTION))
    source = repair_response(answer, previous, error) if previous else code_response(answer)
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
    statistics = {key: copy.deepcopy(analysis[key]) for key in (
        'methods', 'models', 'limitations', 'targets', 'data_sha256', 'input_sha256')}
    prepared = []
    for figure in figures:
        contract = skills.style_contract(figure['style'], figure.get('base_style'))
        spec = dict(copy.deepcopy(figure), columns=[ref['table_id'] + '.' + ref['column'] for ref in figure['columns']],
                    style_contract=contract, data_contract={'results': 'executed', 'tables': 'complete_original'})
        standard = figure['style'] in STANDARD_STYLES
        reference = '' if standard else skills.reference(figure['style'], figure.get('base_style'))
        spec['renderer'] = ('standard_estimates_v2' if standard else
                            'native_stacked_v1' if figure['style'] == 'bar_stacked' else 'generated')
        spec['paper_language'] = inputs.get('constraints', {}).get('paper_language', 'zh-CN')
        bundle = (inputs.get('data') or {}).get('tables', [])
        spec['source_names'] = {item['id']: item.get('description', '').split('。', 1)[0].split('\n', 1)[0]
                                for item in bundle if item.get('description')}
        spec['limitations'] = copy.deepcopy(analysis['limitations'])
        if standard:
            selected = [row for row in data['analysis_results'] if row['id'] in spec['finding_ids']]
            check_encoding(spec, selected)
            # Older saved plans contain generated English provenance inventories as captions.
            local_caption = any('\u4e00' <= char <= '\u9fff' for char in spec.get('caption', ''))
            if len(spec.get('caption', '')) > 300 or (spec['paper_language'] == 'zh-CN' and not local_caption):
                spec['caption'] = ''
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
        try:
            generator = {'standard_estimates_v2': generate_standard_code,
                         'native_stacked_v1': generate_stacked_code}.get(spec['renderer'], generate_code)
            fixed = spec['renderer'] != 'generated'
            chart, trace = draw_one(data, statistics, spec, reference, 1 if fixed else attempts, generator)
        except (FigureRenderError, PlotModelError) as exc:
            trace = dict(id=figure['id'], title=figure['title'], status='failed',
                         error=diagnostics.safe_error(exc), attempts=getattr(exc, 'history', []), exports={})
            diagnostics.save(f"figure-{diagnostics.figure_name(figure['id'])}-failed.json", trace)
            return None, trace
        selected = [row for row in data['analysis_results'] if row['id'] in figure['finding_ids']]
        description = spec.get('caption') or ('' if fixed else figure['purpose'])
        caption = '\n\n'.join(filter(None, (description, evidence_caption(
            spec, selected, spec['paper_language']))))
        result = dict(id=figure['id'], title=figure['title'], columns=figure['columns'], svg=chart['svg'],
                      finding_ids=figure['finding_ids'], caption=caption,
                      normalize=figure.get('normalize', False),
                      **{key: figure.get(key) for key in ('style', 'base_style', 'category', 'series', 'facet')})
        trace.update(exports=chart['exports'], skill_resources_sha256=hashes,
                     concurrency=min(concurrency, len(figures)), caption=result['caption'], renderer=spec['renderer'],
                     evidence=figure.get('evidence', []))
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
