"""Short outline -> execute analysis -> choose figures -> parallel drawing -> interpretation."""
from contextlib import contextmanager
from contextvars import ContextVar
import copy
import hashlib
import json
import os
import traceback

from . import model, generated_executor as executor, trace as diagnostics
from .plan import validate_plan, questions
from .figure_plan import bind_figures
from .results import accept_results
from .schema import (
    ContractError, binding, digest, table_map, validate_analysis, validate_charts, VERSION,
)
from ..plotting.model import PlotModelError

_trace_sink = ContextVar('paper_analysis_trace_sink', default=None)


@contextmanager
def capture_execution():
    records = []
    token = _trace_sink.set(records)
    try:
        yield records
    finally:
        _trace_sink.reset(token)


def build_analysis(inputs, plan, results):
    output = accept_results(results, plan, inputs)
    unavailable = {item['question_id']: item for item in output['unavailable_questions']}
    covered = {row['question_id'] for row in output['results']} | unavailable.keys()
    missing = [f"{question['id']}: {question['question']}" for question in questions(plan)
               if question['id'] not in covered]
    if missing:
        raise ContractError('INVALID_OUTPUT', 'analysis.results',
                            '以下研究问题没有执行结果或明确不可估说明：' + '；'.join(missing))
    if not output['results']:
        raise ContractError('MISSING_MATERIAL', 'analysis.results',
                            '当前材料没有可估计的数值结果：' + '；'.join(
                                f"{item['question_id']}: {item['reason']}" for item in unavailable.values()))
    findings = []
    for row in output['results']:
        metadata = {key: row[key] for key in ('label', 'metric', 'method', 'scope', 'coordinates', 'comparison')}
        findings.append(dict(
            id=row['id'], target_id=row['question_id'], kind=row['kind'],
            statement=f'{row["label"]}: {row["value"]:.8g}', columns=row['columns'],
            calculation=json.dumps(metadata, ensure_ascii=False, allow_nan=False, separators=(',', ':')),
            **{key: row[key] for key in (
                'value', 'unit', 'sample_size', 'interval', 'test', 'probability', 'model_id')}))
    targets, limitations = [], list(output['limitations'])
    for question in questions(plan):
        unavailable_item = unavailable.get(question['id'])
        refs = {(ref['table_id'], ref['column']): ref for row in findings
                if row['target_id'] == question['id'] for ref in row['columns']}
        if unavailable_item:
            limitations.append(f"{question['question']}：{unavailable_item['reason']}")
        targets.append(dict(**question, estimand=question['question'],
                            scope=unavailable_item['reason'] if unavailable_item else '实际执行的分析结果及其记录范围',
                            columns=unavailable_item['columns'] if unavailable_item else list(refs.values()),
                            status='unavailable' if unavailable_item else 'answered',
                            reason=unavailable_item['reason'] if unavailable_item else None))
    return validate_analysis(dict(
        **binding(inputs), summary='；'.join(row['statement'] for row in findings), targets=targets,
        methods=list(dict.fromkeys(row['method'] for row in output['results'])), findings=findings,
        models=output['models'], limitations=list(dict.fromkeys(limitations)),
        diagnostics=[dict(id='execution', model_id=None, name='实际代码执行', outcome='passed', value=None,
                          threshold=None, details='使用实际输入执行分析；未进行后台独立数值复算。')]), inputs)


def execute_analysis(inputs, plan, attempts):
    code, error, history = '', '', []
    material_check_error = ''
    execution_error = ''
    executable_plan = model.execution_plan(inputs, plan)
    for attempt in range(1, attempts + 1):
        generated = False
        try:
            with diagnostics.stage('analysis_code_generation', attempt=attempt):
                code = model.generate_program(inputs, copy.deepcopy(plan), code, error)
                diagnostics.save(f'analysis-code-{attempt}.py', code)
            generated = True
            with diagnostics.stage('analysis_execution', attempt=attempt):
                rendered = executor.execute(dict(tables=table_map(inputs), plan=copy.deepcopy(executable_plan),
                                                 code=code, seed=42))
                diagnostics.save(f'computed-results-{attempt}.json', rendered['results'])
            with diagnostics.stage('result_contract_validation', attempt=attempt):
                analysis = build_analysis(inputs, plan, rendered['results'])
            history.append(dict(attempt=attempt, outcome='passed'))
            return code, history, rendered, analysis
        except (executor.AnalysisEnvironmentError, PlotModelError):
            raise
        except (ValueError, TypeError, KeyError, SyntaxError, RuntimeError) as exc:
            if isinstance(exc, ContractError) and exc.code == 'MISSING_MATERIAL':
                if material_check_error or attempt == attempts:
                    raise
                # Empty generated output is not independent evidence that the source lacks observations.
                material_check_error = str(exc)
                error = material_check_error + (
                    '\nALL_UNAVAILABLE_SELF_CHECK: One code review only. Recheck actual source rows against the '
                    'previous code: dictionary key/value direction, filter/group values, question IDs, copied '
                    'coordinate snapshots, and formula/unit consistency. Repair only demonstrable implementation '
                    'errors. Preserve source data, scientific questions and unsupported comparisons. Do not '
                    'manufacture matches, observations, zeros, intervals or results. If evidence really is absent, '
                    'keep the explicit unavailable reasons. A second empty output ends as MISSING_MATERIAL.')
                diagnostics.save('all-unavailable-self-check.json', dict(
                    attempt=attempt, error=material_check_error, code_sha256=hashlib.sha256(code.encode()).hexdigest()))
            else:
                error = ''.join(traceback.format_exception_only(type(exc), exc)).strip()[-6000:]
                if material_check_error:
                    error = material_check_error + '\n单次空结果代码自查失败：' + error
            if generated:
                execution_error = error
            elif execution_error and execution_error not in error:
                error = execution_error + '\nCode repair response: ' + error
            history.append(dict(attempt=attempt, outcome='failed', error=error))
            if material_check_error and not (isinstance(exc, ContractError) and exc.code == 'MISSING_MATERIAL'):
                break
    error = '\n'.join(f'Attempt {item["attempt"]}: {item["error"]}' for item in history)[-12000:]
    raise ContractError('EXECUTION_FAILED', 'analysis.code', '统计执行失败，已保留错误记录: ' + error)


def redraw(inputs, analysis, charts):
    """Redraw a requested subset without a statistical execution or manuscript rewrite."""
    from ..plotting.analysis import draw
    frozen, originals = copy.deepcopy(analysis), copy.deepcopy(charts)
    validate_analysis(frozen)
    validate_charts(originals, frozen)
    if not originals:
        raise ContractError('MISSING_MATERIAL', 'input.charts', '没有可重画的已有图表')
    if binding(inputs)['data_sha256'] != frozen['data_sha256']:
        raise ContractError('INVALID_INPUT', 'input.data', '只重画必须使用原分析的数据；数据变更请重新分析')
    before = digest(frozen)
    existing = {chart['id']: chart for chart in originals}
    redraw_all = None
    plan = dict(redraw_only=True, questions=[item['question'] for item in frozen['targets']],
                outline='仅修改指定已有图表的表达；冻结统计结果和未选图表。',
                existing_figures=[dict(
                    {key: value for key, value in chart.items() if key not in {'svg', 'caption'}},
                    caption=chart['caption'].split('\n\n', 1)[0]) for chart in originals])
    error, arrangement, history = '', None, []
    for attempt in range(1, 3):
        try:
            with diagnostics.stage('figure_planning', attempt=attempt):
                candidate = model.plan_figures(inputs, copy.deepcopy(frozen), copy.deepcopy(plan), error)
                bound = bind_figures(candidate, frozen)
                if redraw_all is None:
                    redraw_all = bound['all_existing_figures']
                used = set()
                if not bound['figures']:
                    raise ValueError('只重画必须选择至少一张已有图表')
                for figure in bound['figures']:
                    identifier = figure.get('source_chart_id')
                    if identifier not in existing or identifier in used:
                        raise ValueError(f'source_chart_id 必须为不重复的已有图表 ID: {list(existing)}')
                    if set(figure['finding_ids']) != set(existing[identifier]['finding_ids']):
                        raise ValueError(f'只重画 {identifier} 不得添加或删除已有 finding_ids')
                    figure['id'] = identifier
                    used.add(identifier)
                if redraw_all and used != set(existing):
                    raise ValueError('The user requested every existing figure. Include these missing source_chart_id '
                                     'values: ' + ', '.join(sorted(set(existing) - used)))
            arrangement = bound
            history.append(dict(attempt=attempt, outcome='passed'))
            break
        except PlotModelError:
            raise
        except (ValueError, TypeError, KeyError) as exc:
            error = str(exc)[-4000:]
            history.append(dict(attempt=attempt, outcome='failed', error=error))
    if arrangement is None:
        raise ContractError('INVALID_OUTPUT', 'analysis.figures', error)
    plan.update(arrangement)
    diagnostics.save('figure-plan.json', arrangement)
    rendered, traces = draw(frozen, plan['figures'], inputs=inputs, shared=arrangement)
    if len(rendered) != len(plan['figures']):
        raise ContractError('EXECUTION_FAILED', 'output.charts', '部分重画失败，原分析和图表保留，请重试')
    if before != digest(frozen) or digest(analysis) != before:
        raise ContractError('INVALID_OUTPUT', 'output.analysis', '绘图修改了已冻结分析，未发布')
    replacements = {chart['id']: chart for chart in rendered}
    output = [replacements.get(chart['id'], chart) for chart in originals]
    validate_charts(output, frozen)
    trace = dict(contract=VERSION, mode='redraw', data_sha256=frozen['data_sha256'],
                 input_sha256=frozen['input_sha256'], analysis_preserved_sha256=before,
                 plan=plan, plan_sha256=digest(plan), figure_planning=history, figures=traces)
    diagnostics.save('locked-plan.json', plan)
    sink = _trace_sink.get()
    if sink is not None:
        sink.append(trace)
    return dict(is_demo=False, analysis=frozen, charts=output)


def run(inputs):
    """Execute inputs already normalized by the public F4 entry point."""
    diagnostics.save('input-summary.json', dict(
        **binding(inputs), tables=[
            dict(id=key, rows=len(table['rows']), columns=len(table['columns']))
            for key, table in table_map(inputs).items()]))
    with diagnostics.stage('environment_validation'):
        executor.configuration()
    try:
        attempts = int(os.environ.get('LAZYMIND_PAPER_GENERATED_ANALYSIS_ATTEMPTS', '5'))
        if not 1 <= attempts <= 5:
            raise ValueError
    except ValueError as exc:
        raise ValueError('统计代码总尝试次数必须为 1..5：首次生成计 1 次，最多局部修复 4 次') from exc

    planning, error, plan = [], '', None
    for attempt in range(1, min(attempts, 3) + 1):
        try:
            with diagnostics.stage('plan_generation', attempt=attempt):
                candidate = model.make_plan(inputs, error)
                diagnostics.save(f'plan-attempt-{attempt}.json', candidate)
                plan = validate_plan(candidate, inputs)
            planning.append(dict(attempt=attempt, outcome='passed'))
            break
        except PlotModelError:
            raise
        except (ValueError, TypeError, KeyError) as exc:
            error = str(exc)[-4000:]
            planning.append(dict(attempt=attempt, outcome='failed', error=error))
    if plan is None:
        raise ContractError('INVALID_OUTPUT', 'analysis.plan', error)
    diagnostics.save('analysis-plan.json', plan)
    code, history, rendered, analysis = execute_analysis(inputs, plan, attempts)
    diagnostics.save('executed-analysis.json', analysis)

    # Figures are chosen only now, using real computed values/intervals/scopes.
    error, figure_planning, arrangement = '', [], None
    for attempt in range(1, min(attempts, 2) + 1):
        try:
            with diagnostics.stage('figure_planning', attempt=attempt):
                candidate = model.plan_figures(inputs, copy.deepcopy(analysis), copy.deepcopy(plan), error)
                diagnostics.save(f'figure-plan-attempt-{attempt}.json', candidate)
                arrangement = bind_figures(candidate, analysis)
            figure_planning.append(dict(attempt=attempt, outcome='passed'))
            break
        except PlotModelError:
            raise
        except (ValueError, TypeError, KeyError) as exc:
            error = str(exc)[-4000:]
            figure_planning.append(dict(attempt=attempt, outcome='failed', error=error))
    if arrangement is None:
        raise ContractError('INVALID_OUTPUT', 'analysis.figures', error)
    plan.update(arrangement)
    diagnostics.save('figure-plan.json', arrangement)

    from ..plotting.analysis import draw
    charts, figure_trace = draw(analysis, plan['figures'], inputs=inputs, shared=arrangement)
    failed = [item for item in figure_trace if item.get('status') == 'failed']
    failed_ids = {item['id'] for item in failed}
    plan['failed_figures'] = [dict(id=item['id'], title=item['title'], error=item['error']) for item in failed]
    plan['figures'] = [figure for figure in plan['figures'] if figure['id'] not in failed_ids]
    traces = {item['id']: item for item in figure_trace}
    # Keep the planned style; a disclosed simple fallback records the style it actually drew.
    for figure in plan['figures']:
        item = traces[figure['id']]
        if item.get('fallback'):
            figure['fallback'] = item['fallback']
        figure['caption'] = item['caption']
    summary, interpretation = model.interpret_results(inputs, copy.deepcopy(analysis), copy.deepcopy(plan))
    for chart in charts:
        note = ('原绘图未完成，现用简单图保留各项结果；原计划关系或布局未能完整展示。'
                if next(item for item in figure_trace if item['id'] == chart['id']).get('fallback') else '')
        chart['caption'] = '\n\n'.join(filter(None, (note, chart['caption'])))
    if failed or any(item.get('fallback') for item in figure_trace):
        missing = '未生成：' + '；'.join(item['title'] for item in failed) + '。' if failed else ''
        summary = (f'图表部分完成：已生成 {len(charts)}/{len(figure_trace)} 张图；'
                   '统计结果保留。' + missing + '\n\n') + summary
    unavailable_notes = [f"当前无法回答“{target['question']}”：{target['reason']}"
                         for target in analysis['targets'] if target['status'] == 'unavailable']
    analysis['summary'] = '\n\n'.join([summary, *unavailable_notes])
    with diagnostics.stage('output_validation'):
        validate_charts(charts, analysis)
        diagnostics.save('analysis.json', analysis)
        diagnostics.save('figures.json', [
            dict(id=chart['id'], title=chart['title'], svg_bytes=len(chart['svg'].encode())) for chart in charts])
    trace = dict(contract=VERSION, input_sha256=analysis['input_sha256'], plan=plan, plan_sha256=digest(plan),
                 code=code, code_sha256=hashlib.sha256(code.encode()).hexdigest(), seed=42,
                 versions=rendered['versions'], planning=planning, attempts=history, figure_planning=figure_planning,
                 interpretation=interpretation, figures=figure_trace)
    diagnostics.save('locked-plan.json', plan)
    sink = _trace_sink.get()
    if sink is not None:
        sink.append(trace)
    return dict(is_demo=False, analysis=analysis, charts=charts)
