"""F3 planning, bounded execution and explicitly labelled preview fallback."""
import json
import logging
import time

from lazyllm import AutoModel
from lazyllm.tools.writer.tools.base import WriterToolBase

from lazymind.common.token_estimation import estimate_tokens
from ..contracts import validate_input, validate_output
from ..research_topic import _prompt_token_limit
from .schema import ExperimentDraft, Metric, ResultMeta, code_column_types
from .generation import GeneratedExperiment, validate_generation
from .stream import generation_progress
from ..prompts.experiment_prompt import EXPERIMENT_PROMPT

LOG = logging.getLogger(__name__)
NO_EXECUTION = '本次未运行实验代码；以下数据为模型生成的模拟示例。'


def build_output(draft: ExperimentDraft, inputs: dict, execution=None) -> dict:
    """Render one validated model result; all execution claims are host-owned."""
    execution = execution or {'status': 'not_run', 'reason': NO_EXECUTION, 'results': None,
                              'logs': '', 'elapsed_seconds': 0.0}
    executed = execution['status'] == 'succeeded'
    columns = code_column_types(next(item.content for item in draft.code_files if item.path == 'experiment.py'))
    table = execution['results'] if executed else draft.results.table(columns).model_dump() if draft.results else None
    reason = execution['reason']
    metrics = draft.metrics or ([Metric.model_validate(item.model_dump(exclude={'values'}))
                                for item in draft.results.metrics] if draft.results else [])
    if table is None:
        metrics = []
    origin = ('synthetic' if draft.method == 'simulation' or not executed else 'observed') if table else 'none'
    meta = ResultMeta(
        execution_status=execution['status'], data_origin=origin,
        method=draft.method, reason=reason, assumptions=draft.assumptions,
        metrics=metrics, seed=draft.seed,
        column_types=columns, execution_log=execution['logs'], elapsed_seconds=execution['elapsed_seconds'],
    )
    from .handoff import source_notice
    notice = source_notice(meta.model_dump())
    execution_text = (f'实际执行耗时 {meta.elapsed_seconds} 秒，返回 {len(table["rows"])} 行。' if executed
                      else (reason or draft.no_results_reason or '本次没有可用结果。'))
    report = '\n\n'.join([
        f'# 实验研究 · {inputs["topic"]}', '> ' + notice,
        '## 实验方案\n' + draft.plan,
        '## 代码与运行说明\n' + draft.explanation,
        '## 实验假设\n' + '\n'.join('- ' + item for item in draft.assumptions),
        '## 指标与单位\n' + '\n'.join(
            f'- {item.column}：{item.description}（{item.unit or "无单位"}）' for item in metrics),
        '## 执行记录\n' + execution_text
        + ('\n\n```text\n' + meta.execution_log + '\n```' if meta.execution_log else ''),
        '## 交付\n实验代码包可下载复现；本次结果可继续交给数据分析步骤生成图表。',
    ]) + '\n'
    return validate_output('experiment', {
        'experiment': {'executed': executed, 'plan': draft.plan, 'results': table, 'report': report},
        'code_files': [item.model_dump() for item in draft.code_files],
        'result_meta': meta.model_dump(), 'is_demo': origin in ('synthetic', 'none'),
    })


def run(inputs: dict, *, on_event=None, allow_execution=False) -> dict:
    """Generate once, then use the Runtime's execution choice within the bounded executor."""
    inputs = validate_input('experiment', inputs)
    prompt = EXPERIMENT_PROMPT + '\n\nInput JSON:\n' + json.dumps(inputs, ensure_ascii=False)
    writer = WriterToolBase(llm=AutoModel(model='llm'))
    system_prompt = writer._structured_output_prompt(GeneratedExperiment)
    prompt_tokens, prompt_limit = estimate_tokens(prompt + system_prompt), _prompt_token_limit()
    if prompt_tokens > prompt_limit:
        raise ValueError(f'实验输入超过当前模型预算（预计 {prompt_tokens} / 可用 {prompt_limit} tokens），'
                         '请缩小研究材料或数据范围；未截断输入。')
    # A private sink keeps generated JSON out of the Agent's chat/event queue.
    stream = {'_stream_sink': on_event or (lambda _event: None)}
    model = writer._build_structured_llm(
        system_prompt, stream_output=stream, apply_formatter=False)
    started = time.monotonic()
    with generation_progress(on_event):
        response = model(prompt)
    LOG.info('paper_experiment generation elapsed=%.3fs', time.monotonic() - started)
    # Reuse Writer's JSON parser (fences and syntax repair); it does not call a
    # model again. The complete strict schema and code syntax still validate next.
    if on_event:
        on_event({'tag': 'phase', 'phase': '校验字段、结果类型与代码接口'})
    parsed = writer._parse_structured_response(response, GeneratedExperiment)
    draft = validate_generation(writer, parsed, inputs, on_event)
    from .executor import execute
    execution = execute(draft, inputs, allowed=allow_execution, on_event=on_event)
    return build_output(draft, inputs, execution)
