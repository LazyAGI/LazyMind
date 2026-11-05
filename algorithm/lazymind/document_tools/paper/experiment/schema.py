"""Bounded experiment deliverables; generated code remains data."""
import ast
import json
from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, model_validator

from .results import validate_column_types, validate_table

Text = Annotated[str, Field(min_length=1, max_length=30000)]
Cell = str | int | float | bool | None


class StrictModel(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True, allow_inf_nan=False)


class ResultTable(StrictModel):
    columns: list[Annotated[str, Field(min_length=1, max_length=100)]] = Field(min_length=1, max_length=30)
    rows: list[list[Cell]] = Field(min_length=1, max_length=200)

    @model_validator(mode='after')
    def check_table(self):
        validate_table(self.model_dump())
        return self


class CodeFile(StrictModel):
    path: Literal['experiment.py', 'config.json', 'requirements.txt', 'README.md']
    content: str = Field(max_length=60000)


def validate_code_files(files, *, require_entry=False):
    parsed = [CodeFile.model_validate(item) for item in files]
    content = {item.path: item.content for item in parsed}
    if len(parsed) != 4 or set(content) != {'experiment.py', 'config.json', 'requirements.txt', 'README.md'}:
        raise ValueError('代码包必须且只能包含 experiment.py/config.json/requirements.txt/README.md')
    if sum(len(item.content.encode('utf-8')) for item in parsed) > 200000:
        raise ValueError('代码包内容超过 200 KB')
    if any(not content[name].strip() for name in ('experiment.py', 'config.json', 'README.md')):
        raise ValueError('代码、配置和运行说明不能为空')
    # Syntax only: no import, eval, exec, subprocess or dependency installation.
    ast.parse(content['experiment.py'], filename='experiment.py')
    if require_entry:
        code_column_types(content['experiment.py'])
    configuration = json.loads(content['config.json'])
    if not isinstance(configuration, dict):
        raise ValueError('config.json 必须是对象')
    json.dumps(configuration, allow_nan=False)
    return parsed


def code_column_types(source):
    """Read a literal declaration and entry signature without importing any code."""
    tree = ast.parse(source, filename='experiment.py')
    declarations = [node.value for node in tree.body if isinstance(node, ast.Assign)
                    and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name)
                    and node.targets[0].id == 'RESULT_COLUMNS']
    if len(declarations) != 1 or not isinstance(declarations[0], ast.Dict):
        raise ValueError('experiment.py 必须声明一次字面量 RESULT_COLUMNS 对象')
    literal = declarations[0]
    try:
        names = [ast.literal_eval(key) for key in literal.keys]
        if any(not isinstance(name, str) for name in names) or len(set(names)) != len(names):
            raise ValueError('RESULT_COLUMNS 列名不能为空或重复')
        columns = validate_column_types(ast.literal_eval(literal))
    except (TypeError, ValueError) as exc:
        raise ValueError(f'RESULT_COLUMNS 无效: {exc}') from None
    entries = [node for node in tree.body if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
               and node.name == 'run_experiment']
    if len(entries) != 1 or not isinstance(entries[0], ast.FunctionDef):
        raise ValueError('experiment.py 必须定义同步 run_experiment(config, input_data)')
    args = entries[0].args
    if (args.posonlyargs or [arg.arg for arg in args.args] != ['config', 'input_data']
            or args.defaults or args.kwonlyargs or args.vararg or args.kwarg or entries[0].decorator_list):
        raise ValueError('run_experiment 必须且只能接受 config、input_data 两个位置参数')
    return columns


class Metric(StrictModel):
    column: Annotated[str, Field(min_length=1, max_length=100)]
    description: Text
    unit: str | None

    @model_validator(mode='after')
    def check_text(self):
        if not self.description.strip() or (self.unit is not None and not self.unit.strip()):
            raise ValueError('指标描述/单位不能为空白；无单位请用 null')
        return self


class PreviewDimension(StrictModel):
    column: Annotated[str, Field(min_length=1, max_length=100)]
    values: list[Cell] = Field(min_length=1, max_length=200)


class PreviewMetric(Metric):
    values: list[int | float] = Field(min_length=1, max_length=200)


class Preview(StrictModel):
    """Define each column once; the host derives both rows and metric metadata."""
    dimensions: list[PreviewDimension] = Field(max_length=10)
    metrics: list[PreviewMetric] = Field(min_length=1, max_length=20)

    def table(self, column_order=None):
        columns = [*self.dimensions, *self.metrics]
        if len({len(item.values) for item in columns}) != 1:
            raise ValueError('预览各列必须具有相同的行数')
        # Associate values with names before reordering; never relabel positional data.
        by_name = {item.column: item for item in columns}
        if len(by_name) != len(columns):
            raise ValueError('结果列名不能重复')
        if column_order is not None:
            if set(column_order) != set(by_name):
                raise ValueError('结果列名必须与 RESULT_COLUMNS 一致')
            columns = [by_name[name] for name in column_order]
        return ResultTable(columns=[item.column for item in columns],
                           rows=[list(row) for row in zip(*(item.values for item in columns))])

    @model_validator(mode='after')
    def check_columns(self):
        self.table()
        return self


class ExecutionRequirements(StrictModel):
    device: Literal['cpu', 'gpu'] = 'cpu'
    estimated_seconds: int = Field(default=30, ge=1)
    requires_network: bool = False
    requires_external_data: bool = False
    reason: str = ''


class ExperimentPlan(StrictModel):
    """Research content shared by generation and the host-built deliverable."""
    plan: Text = Field(description='Question, hypotheses, evidence, controls, metrics and procedure')
    explanation: Text = Field(description='Code explanation, data requirements, limitations and run commands')
    execution_requirements: ExecutionRequirements = Field(default_factory=ExecutionRequirements)
    metrics: list[Metric] = Field(default_factory=list, max_length=30)


class ExperimentDesign(ExperimentPlan):
    """The fixed design/code package, independently validatable before preview repair."""
    code_files: list[CodeFile] = Field(min_length=4, max_length=4)

    @model_validator(mode='after')
    def check_design(self):
        validate_code_files([item.model_dump() for item in self.code_files], require_entry=True)
        if not self.plan.strip() or not self.explanation.strip():
            raise ValueError('实验方案和说明不能为空白')
        columns = code_column_types(next(item.content for item in self.code_files if item.path == 'experiment.py'))
        # Models sometimes repeat categorical dimensions in the metric list. Their
        # declared type is authoritative; keep these columns in the table, not as outcomes.
        self.metrics = [item for item in self.metrics if columns.get(item.column) in ('integer', 'number')]
        return self


class ExperimentPreview(StrictModel):
    results: Preview | None
    assumptions: list[Text] = Field(min_length=1, max_length=20)
    seed: int | None
    method: Literal['empirical', 'simulation']
    no_results_reason: Text | None


class ExperimentDraft(ExperimentDesign, ExperimentPreview):
    @model_validator(mode='before')
    @classmethod
    def discard_extra_model_fields(cls, value):
        """Normalize only model output; required fields/types and public contracts stay strict."""
        def known(model, item):
            return {key: val for key, val in item.items() if key in model.model_fields} \
                if isinstance(item, dict) else item

        value = known(cls, value)
        if not isinstance(value, dict):
            return value
        if isinstance(value.get('code_files'), list):
            value['code_files'] = [known(CodeFile, item) for item in value['code_files']]
        preview = known(Preview, value.get('results'))
        if isinstance(preview, dict):
            for field, model in (('dimensions', PreviewDimension), ('metrics', PreviewMetric)):
                if isinstance(preview.get(field), list):
                    preview[field] = [known(model, item) for item in preview[field]]
            value['results'] = preview
            # This explanation is inapplicable when a preview exists. Execution
            # provenance is supplied by the host, never inferred from this field.
            value['no_results_reason'] = None
        return value

    @model_validator(mode='after')
    def check_consistency(self):
        columns = code_column_types(next(item.content for item in self.code_files if item.path == 'experiment.py'))
        if any(not value.strip() for value in self.assumptions):
            raise ValueError('实验假设不能为空白')
        if self.results is None:
            if not self.no_results_reason or not self.no_results_reason.strip():
                raise ValueError('无结果时必须说明原因')
        elif self.no_results_reason is not None:
            raise ValueError('有结果时 no_results_reason 必须为 null')
        if self.results is not None:
            table = self.results.table(columns).model_dump()
            validate_table(table, columns)
            if any(columns[item.column] not in ('integer', 'number') for item in self.results.metrics):
                raise ValueError('结果指标必须声明为 integer 或 number')
        return self


class ResultMeta(StrictModel):
    schema_version: Literal[1] = 1
    execution_status: Literal['not_run', 'succeeded', 'failed', 'timeout']
    data_origin: Literal['observed', 'synthetic', 'user_supplied', 'none']
    method: Literal['empirical', 'simulation']
    reason: Text | None
    assumptions: list[Text] = Field(max_length=20)
    metrics: list[Metric] = Field(max_length=30)
    seed: int | None
    column_types: dict[str, Literal['string', 'integer', 'number', 'boolean']] | None = None
    input_table_ids: list[str] = Field(default_factory=list)
    execution_log: str = ''
    elapsed_seconds: float = Field(default=0.0, ge=0)

    @model_validator(mode='after')
    def check_metrics(self):
        if self.column_types is not None:
            validate_column_types(self.column_types)
        names = [metric.column for metric in self.metrics]
        if len(names) != len(set(names)):
            raise ValueError('指标不能重复')
        if self.data_origin == 'synthetic' and not self.assumptions:
            raise ValueError('模拟结果必须说明假设')
        if any(not item.strip() for item in self.assumptions):
            raise ValueError('模拟假设不能为空白')
        if self.reason is not None and not self.reason.strip():
            raise ValueError('结果原因不能为空白')
        return self


def validate_result_meta(value, experiment, data=None):
    meta = ResultMeta.model_validate(value)
    if meta.execution_status != 'succeeded' and not meta.reason:
        raise ValueError('未成功执行时必须说明原因')
    if experiment is not None:
        if experiment['executed'] != (meta.execution_status == 'succeeded'):
            raise ValueError('执行状态与 executed 不一致')
    table = experiment['results'] if experiment is not None else data
    if (table is None) != (meta.data_origin == 'none'):
        raise ValueError('结果来源与结果数据不一致')
    if table is None:
        if meta.metrics:
            raise ValueError('无结果时不能声明结果指标')
        return meta
    if meta.data_origin in {'user_supplied', 'synthetic'} and ('tables' in table or 'column_types' in table):
        from ..analysis.schema import DataBundle, DataTable
        (DataBundle if 'tables' in table else DataTable).model_validate(table)
        if meta.execution_status != 'not_run' or (meta.data_origin == 'user_supplied' and meta.method != 'empirical'):
            raise ValueError('用户提供的数据不得声明为本次执行或模拟')
        return meta
    validate_table(table, meta.column_types)
    if not meta.metrics or any(metric.column not in table['columns'] for metric in meta.metrics):
        raise ValueError('来源说明必须提供结果表中的指标')
    for metric in meta.metrics:
        if meta.column_types is not None and meta.column_types[metric.column] not in ('integer', 'number'):
            raise ValueError('结果指标必须声明为 integer 或 number')
        index = table['columns'].index(metric.column)
        for number, row in enumerate(table['rows'], 1):
            if type(row[index]) not in (int, float):
                raise ValueError(f'指标 {metric.column} 第 {number} 行必须为有限数值，'
                                 f'实际为 {type(row[index]).__name__}')
    return meta
