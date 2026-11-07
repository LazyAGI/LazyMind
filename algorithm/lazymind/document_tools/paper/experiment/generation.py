"""Generate research content; the host owns filenames and the downloadable package."""
import json
import logging
import time

from pydantic import ConfigDict, Field, ValidationError, create_model, field_validator

from lazymind.common.token_estimation import estimate_tokens
from ..research_topic import _prompt_token_limit
from ..prompts.experiment_prompt import DESIGN_REPAIR_PROMPT
from .preview_repair import repair_preview
from .schema import ExperimentDraft, ExperimentPlan, ExperimentPreview, StrictModel, Text, code_column_types

LOG = logging.getLogger(__name__)


class GeneratedDesign(ExperimentPlan):
    # Ignore only extra model fields, not missing fields or invalid types.
    model_config = ConfigDict(extra='ignore', strict=True, allow_inf_nan=False)
    code: str = Field(min_length=1, max_length=60000)
    config: dict
    dependencies: list[Text] = Field(max_length=30)

    @field_validator('code')
    @classmethod
    def check_code(cls, value):
        try:
            code_column_types(value)
        except SyntaxError as error:
            raise ValueError(f'Python syntax error: {error}') from error
        return value

    @field_validator('config')
    @classmethod
    def check_config(cls, value):
        json.dumps(value, allow_nan=False)
        return value

    @field_validator('dependencies')
    @classmethod
    def check_dependencies(cls, values):
        if any(not v.strip() or '\n' in v or '\r' in v for v in values):
            raise ValueError('Each dependency must be one nonempty line')
        return values

    @field_validator('plan', 'explanation')
    @classmethod
    def check_text(cls, value):
        if not value.strip():
            raise ValueError('Research content cannot be blank')
        return value


class GeneratedExperiment(GeneratedDesign, ExperimentPreview):
    """Model-facing schema; unlike the public output it contains no filenames."""


def assemble(candidate):
    """Validate design independently of preview and construct the fixed four files."""
    design = GeneratedDesign.model_validate(candidate)
    fields = design.model_dump(exclude={'code', 'config', 'dependencies'})
    readme = ('# 实验运行说明\n\n' + design.explanation
              + '\n\n## 运行入口\n```bash\npython run_experiment.py\n```\n'
              + '\n配置见 config.json；可选输入见 input.csv。输出为 results.json/results.csv，'
              + '字段和类型见 result_schema.json。\n'
              + '\n依赖见 requirements.txt。服务不会自动安装依赖。'
              + '\n实际执行状态和数据来源以 结果来源.json 为准；模拟数据不代表真实系统效果。\n')
    files = [
        {'path': 'experiment.py', 'content': design.code},
        {'path': 'config.json', 'content': json.dumps(design.config, ensure_ascii=False, indent=2)},
        {'path': 'requirements.txt', 'content': '\n'.join(design.dependencies)},
        {'path': 'README.md', 'content': readme},
    ]
    return {**{k: v for k, v in candidate.items() if k in ExperimentPreview.model_fields},
            **fields, 'code_files': files}


def repair_design(writer, candidate, error, on_event=None):
    """One patch for invalid design fields, preserving all other generated content."""
    if not isinstance(candidate, dict):
        raise ValueError('实验输出必须是 JSON 对象，未重新生成整步') from error
    names = sorted({issue['loc'][0] for issue in error.errors() if issue['loc']})
    if not names or any(name not in GeneratedDesign.model_fields for name in names):
        raise error
    schema = create_model('ExperimentDesignRepair', __base__=StrictModel, **{
        name: (GeneratedDesign.model_fields[name].annotation, Field(...)) for name in names
    })
    request = DESIGN_REPAIR_PROMPT + '\n\nInput JSON:\n' + json.dumps({
        'fields_to_replace': names, 'candidate': candidate,
        'validation_errors': error.errors(include_url=False, include_context=False, include_input=False),
    }, ensure_ascii=False)
    system = writer._structured_output_prompt(schema)
    if estimate_tokens(request + system) > _prompt_token_limit():
        raise ValueError('实验局部修复超过预算；未重新生成整步') from error
    if on_event:
        on_event({'tag': 'phase', 'phase': '正在局部修正实验字段：' + ', '.join(names)})
    # Repair JSON is not an append to the original artifact stream.
    model = writer._build_structured_llm(
        system, stream_output={'_stream_sink': lambda _event: None}, apply_formatter=False)
    started = time.monotonic()
    response = model(request)
    LOG.info('paper_experiment design_repair fields=%s elapsed=%.3fs', names, time.monotonic() - started)
    patch = schema.model_validate(writer._parse_structured_response(response, schema)).model_dump()
    return {**candidate, **patch}


def finish_design_repair(candidate):
    """Validate the complete repaired result without opening another repair budget."""
    return ExperimentDraft.model_validate(assemble(candidate))


def validate_generation(writer, candidate, inputs, on_event=None):
    """One shared repair budget: design OR preview, never both or a whole-step retry."""
    try:
        packaged = assemble(candidate)
    except ValidationError as error:
        repaired = repair_design(writer, candidate, error, on_event)
        return finish_design_repair(repaired)
    try:
        return ExperimentDraft.model_validate(packaged)
    except ValueError as error:
        return repair_preview(writer, packaged, inputs, error, on_event)
