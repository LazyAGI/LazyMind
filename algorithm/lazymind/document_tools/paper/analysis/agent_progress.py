"""Project Agent-authored scientific work onto the existing F4 phase view."""
import json

from lazymind.common.token_estimation import estimate_tokens

from .live import MAX_OUTPUT, public_error
from .trace import clean, redact


def decoded(value):
    if isinstance(value, str):
        try:
            return json.loads(value)
        except (ValueError, TypeError):
            return value
    return value


def project(task):
    records = task.get('analysis_steps') or []
    calls, results = [], {}
    revision = 0
    for record in records:
        if not isinstance(record, dict):
            continue
        if record.get('execution_id') and record['execution_id'] != task.get('execution_id'):
            continue
        content = decoded(record.get('content'))
        if not isinstance(content, dict):
            continue
        revision = max(revision, record.get('seq', 0))
        if record.get('role') == 'assistant':
            calls.extend(content.get('tool_calls') or [])
        if record.get('role') == 'tool':
            for result in content.get('tool_results') or []:
                if isinstance(result, dict):
                    results[result.get('tool_call_id') or result.get('id')] = decoded(result.get('result'))
    phases, attempts = [], 0
    terminal = task.get('status') in {'succeeded', 'failed', 'canceled', 'interrupted'}

    def add(title, *, content=None, format='text', state='succeeded', summary=''):
        step = dict(step_id=f'f4-agent-{len(phases)}', order=len(phases), revision=revision,
                    title=title, status=state, summary=redact(summary)[:1200],
                    started_at=None, finished_at=None, elapsed_ms=None)
        if content is not None:
            text = content if isinstance(content, str) else json.dumps(clean(content), ensure_ascii=False, indent=2)
            text = redact(text)
            raw = text.encode('utf-8')
            text = raw[:MAX_OUTPUT].decode('utf-8', errors='ignore')
            step['output'] = dict(text=text, format=format, mode='complete',
                                  estimated_tokens=estimate_tokens(text), truncated=len(raw) > MAX_OUTPUT)
        phases.append(step)
        return step

    for call in calls:
        if not isinstance(call, dict) or not call.get('id'):
            continue
        name = call.get('name')
        args = decoded(call.get('args'))
        if not isinstance(args, dict):
            continue
        result = results.get(call.get('id'))
        returned = call.get('id') in results
        ok = isinstance(result, dict) and result.get('ok') is True
        value = decoded(result.get('value')) if isinstance(result, dict) else result
        state = ('succeeded' if ok else 'failed') if returned else 'interrupted' if terminal else 'running'
        error = (public_error(dict(code='ANALYSIS_EXECUTION_FAILED', message=str(value)))['message']
                 if returned and not ok else '')
        if name == 'paper_prepare_inputs' and not phases:
            catalog = value.get('table_catalog') if isinstance(value, dict) else None
            add('准备实验数据与研究背景', content=catalog, format='json', state=state,
                summary=error or ('已载入绑定的数据目录；统计分析沿用这些输入。' if ok else '正在读取绑定的实验数据。'))
        elif name == 'paper_analyze':
            attempts += 1
            plan = args.get('plan')
            questions = plan.get('questions') if isinstance(plan, dict) else None
            add(f'分析方案 · 第 {attempts} 次', content=plan, format='json',
                summary='；'.join(questions)[:1000] if isinstance(questions, list)
                and all(isinstance(item, str) for item in questions) else '本轮提交的分析方案。')
            add(f'统计代码 · 第 {attempts} 次', content=args.get('code'), format='python',
                summary='本轮实际提交的计算代码；执行结果见下一阶段。')
            analysis = value.get('analysis') if isinstance(value, dict) else None
            add(f'统计执行与结果校验 · 第 {attempts} 次', content=analysis, format='json', state=state,
                summary=error or (analysis.get('summary', '统计执行与结果校验通过。')
                                  if isinstance(analysis, dict) else '正在执行本轮统计代码。' if not returned
                                  else '本轮执行已返回，完整结果需进一步读取。'))
        elif name == 'paper_plot':
            spec = args.get('specification')
            title = spec.get('title', '') if isinstance(spec, dict) else ''
            add('图表设计' + (f'：{title}' if title else ''), content={key: val for key, val in spec.items() if key != 'svg'}
                if isinstance(spec, dict) else None, format='json')
            add('绘图代码', content=args.get('code'), format='python')
            chart = value.get('chart') if isinstance(value, dict) else None
            add('图表渲染与校验', state=state,
                summary=error or (chart.get('caption', '图表已渲染并通过校验。')
                                  if isinstance(chart, dict) else '正在渲染并校验图表。' if not returned
                                  else '绘图执行已返回。'))
        elif name == 'paper_prepare_candidate':
            output = args.get('output') or {}
            analysis = output.get('analysis') if isinstance(output, dict) else None
            narrative = ({key: analysis[key] for key in ('summary', 'limitations') if key in analysis}
                         if isinstance(analysis, dict) else None)
            add('解读结果并校验分析报告', content=narrative, format='json', state=state,
                summary=error or ('报告与图表通过发布前校验。' if ok else '正在校验分析报告。'))
    if not phases:
        return None
    if not terminal and not any(step['status'] == 'running' for step in phases):
        add('修正分析方案与代码' if phases[-1]['status'] == 'failed' else '准备下一阶段分析内容',
            state='running', summary='等待 Agent 提交本轮分析内容；已记录的方案、代码和结果保留。')
    remaining = 512 * 1024
    for phase in reversed(phases):
        if 'output' in phase:
            output = phase['output']
            raw = output['text'].encode('utf-8')
            output['text'] = raw[:remaining].decode('utf-8', errors='ignore')
            output['truncated'] = output['truncated'] or len(raw) > remaining
            remaining = max(0, remaining - len(raw))
    failed = next((step for step in reversed(phases) if step['status'] == 'failed'), None)
    return dict(execution_id=task.get('execution_id') or 'agent', revision=revision, process_steps=phases,
                error=dict(code='ANALYSIS_EXECUTION_FAILED', message=failed['summary'])
                if failed and task.get('status') == 'failed' else None)
