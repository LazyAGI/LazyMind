"""Rebuild only an invalid synthetic preview against the generated code's schema."""
import json
import logging
import time
from typing import Annotated, Literal

from pydantic import BeforeValidator, Field, create_model

from lazymind.common.token_estimation import estimate_tokens
from ..research_topic import _prompt_token_limit
from ..prompts.experiment_prompt import PREVIEW_REPAIR_PROMPT
from .schema import ExperimentDesign, ExperimentDraft, Metric, Preview, StrictModel, Text, code_column_types

LOG = logging.getLogger(__name__)


def _strict_seed(value):
    if value is not None and type(value) is not int:
        raise ValueError('seed 必须是整数或 null，不能是布尔值或字符串')
    return value


def repair_base(candidate):
    """Reject broken code/plan first; only a supplied invalid preview is repairable."""
    if not isinstance(candidate, dict) or not isinstance(candidate.get('results'), dict):
        raise ValueError('实验输出不完整，不能仅修正模拟预览')
    base = ExperimentDesign.model_validate({
        key: candidate[key] for key in ExperimentDesign.model_fields if key in candidate
    })
    source = next(item.content for item in base.code_files if item.path == 'experiment.py')
    return base, code_column_types(source)


def repair_schema(columns, candidate=None):
    """Require every code-declared column in every row, with its exact scalar type."""
    scalars = {'integer': int, 'number': int | float, 'string': str, 'boolean': bool}
    numeric = tuple(name for name, kind in columns.items() if kind in ('integer', 'number'))
    if not numeric:
        raise ValueError('代码没有声明数值指标，无法修正模拟结果')
    # Aliases also support column names that are not valid Python identifiers.
    row = create_model('ExperimentPreviewRow', __base__=StrictModel, **{
        f'column_{index}': (scalars[kind], Field(alias=name))
        for index, (name, kind) in enumerate(columns.items())
    })
    metric = create_model('ExperimentPreviewMetric', __base__=Metric,
                          column=(Literal[numeric], ...))
    candidate = candidate or {}
    method_type = Literal['empirical', 'simulation']
    if candidate.get('method') in ('empirical', 'simulation'):
        method_type = Literal[candidate['method']]
    seed_type = int | None
    if 'seed' in candidate and (candidate['seed'] is None or type(candidate['seed']) is int):
        seed_type = Literal[candidate['seed']]
    return create_model(
        'ExperimentPreviewRepair', __base__=StrictModel,
        rows=(list[row], Field(min_length=1, max_length=20)),
        metrics=(list[metric], Field(min_length=1, max_length=20)),
        assumptions=(list[Text], Field(min_length=1, max_length=20)),
        seed=(Annotated[seed_type, BeforeValidator(_strict_seed)], ...), method=(method_type, ...),
    )


def apply_repair(base, columns, schema, response):
    """Convert complete named rows to the unchanged Preview/F4 contract, without filling cells."""
    repaired = schema.model_validate(response).model_dump(by_alias=True)
    rows, metrics = repaired['rows'], repaired['metrics']
    metric_names = {item['column'] for item in metrics}
    if len(metric_names) != len(metrics):
        raise ValueError('修正后的模拟指标重复')
    preview = Preview.model_validate({
        'dimensions': [{'column': name, 'values': [row[name] for row in rows]}
                       for name in columns if name not in metric_names],
        'metrics': [{**metric, 'values': [row[metric['column']] for row in rows]}
                    for metric in metrics],
    })
    # Revalidate the whole deliverable. The code and plan are never regenerated.
    return ExperimentDraft.model_validate({
        **base.model_dump(), 'results': preview.model_dump(),
        'assumptions': repaired['assumptions'], 'no_results_reason': None,
        'seed': repaired['seed'], 'method': repaired['method'],
    })


def repair_preview(writer, candidate, inputs, error, on_event=None):
    """Make at most one bounded model call for the preview; never retry code/plan generation."""
    base, columns = repair_base(candidate)
    schema = repair_schema(columns, candidate)
    request = PREVIEW_REPAIR_PROMPT + '\n\nInput JSON:\n' + json.dumps({
        'question': inputs['question'], 'request': inputs.get('request'),
        'plan': base.plan, 'RESULT_COLUMNS': columns,
        'config': next(item.content for item in base.code_files if item.path == 'config.json'),
        'invalid_preview': candidate['results'],
        'existing_metadata': {key: candidate[key] for key in ('assumptions', 'seed', 'method') if key in candidate},
        'validation_error': str(error)[:1500],
    }, ensure_ascii=False)
    system_prompt = writer._structured_output_prompt(schema)
    if estimate_tokens(request + system_prompt) > _prompt_token_limit():
        raise ValueError('模拟预览修正超过预算，已停止；未重新生成方案或代码。') from error
    if on_event:
        on_event({'tag': 'phase', 'phase': '正在按代码声明修正模拟结果列名与类型'})

    def repair_events(event):
        # Repair JSON is a different schema, not a continuation of the draft.
        if on_event and event.get('tag') == 'think':
            on_event(event)

    model = writer._build_structured_llm(
        system_prompt, stream_output={'_stream_sink': repair_events}, apply_formatter=False)
    started = time.monotonic()
    response = model(request)
    LOG.info('paper_experiment preview_repair elapsed=%.3fs', time.monotonic() - started)
    repaired = writer._parse_structured_response(response, schema)
    return apply_repair(base, columns, schema, repaired)
