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
    findings = []
    for row in output['results']:
        metadata = {key: row[key] for key in ('label', 'metric', 'method', 'scope', 'coordinates', 'comparison')}
        findings.append(dict(
            id=row['id'], target_id=row['question_id'], kind=row['kind'],
            statement=f'{row["label"]}: {row["value"]:.8g}', columns=row['columns'],
            calculation=json.dumps(metadata, ensure_ascii=False, allow_nan=False, separators=(',', ':')),
            **{key: row[key] for key in ('value', 'unit', 'sample_size', 'interval', 'test', 'probability', 'model_id')}))
    targets = []
    for question in questions(plan):
        refs = {(ref['table_id'], ref['column']): ref for row in findings
                if row['target_id'] == question['id'] for ref in row['columns']}
        targets.append(dict(**question, estimand=question['question'], scope='实际执行的分析结果及其记录范围',
                            columns=list(refs.values())))
    return validate_analysis(dict(
        **binding(inputs), summary='；'.join(row['statement'] for row in findings), targets=targets,
        methods=list(dict.fromkeys(row['method'] for row in output['results'])), findings=findings,
        models=output['models'], limitations=output['limitations'],
        diagnostics=[dict(id='execution', model_id=None, name='实际代码执行', outcome='passed', value=None,
                          threshold=None, details='使用实际输入执行分析；未进行后台独立数值复算。')]), inputs)


def execute_analysis(inputs, plan, attempts):
    code, error, history = '', '', []
    executable_plan = model.execution_plan(inputs, plan)
    for attempt in range(1, attempts + 1):
        try:
            with diagnostics.stage('analysis_code_generation', attempt=attempt):
                code = model.generate_program(inputs, copy.deepcopy(plan), code, error)
                diagnostics.save(f'analysis-code-{attempt}.py', code)
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
            error = ''.join(traceback.format_exception_only(type(exc), exc)).strip()[-6000:]
            history.append(dict(attempt=attempt, outcome='failed', error=error))
    raise ContractError('EXECUTION_FAILED', 'analysis.code', '统计执行失败，已保留错误记录: ' + error)


def run(inputs):
    """Execute inputs already normalized by the public F4 entry point."""
    diagnostics.save('input-summary.json', dict(
        **binding(inputs), tables=[dict(id=key, rows=len(table['rows']), columns=len(table['columns']))
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
    analysis['summary'] = summary
    with diagnostics.stage('output_validation'):
        validate_charts(charts, analysis)
        diagnostics.save('analysis.json', analysis)
        diagnostics.save('figures.json', [dict(id=chart['id'], title=chart['title'],
                                              svg_bytes=len(chart['svg'].encode())) for chart in charts])
    trace = dict(contract=VERSION, input_sha256=analysis['input_sha256'], plan=plan, plan_sha256=digest(plan),
                 code=code, code_sha256=hashlib.sha256(code.encode()).hexdigest(), seed=42,
                 versions=rendered['versions'], planning=planning, attempts=history, figure_planning=figure_planning,
                 interpretation=interpretation, figures=figure_trace)
    diagnostics.save('locked-plan.json', plan)
    sink = _trace_sink.get()
    if sink is not None:
        sink.append(trace)
    return dict(is_demo=False, analysis=analysis, charts=charts)
