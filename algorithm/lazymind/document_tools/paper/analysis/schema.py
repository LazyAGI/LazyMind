"""Strict paper-v2.1 analysis boundary; no model, filesystem or execution side effects."""
from __future__ import annotations

from collections import Counter
import copy
from datetime import date, datetime
import hashlib
import json
import re
from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, ValidationError, model_validator

VERSION = 'paper-v2.1'
Number = Annotated[float, Field(allow_inf_nan=False)]
Text = Annotated[str, Field(min_length=1, pattern=r'\S')]
Count = Annotated[int, Field(ge=0)]
Digest = Annotated[str, Field(pattern=r'^[0-9a-f]{64}$')]
Level = Annotated[Number, Field(gt=0, lt=1)]
Probability = Annotated[Number, Field(ge=0, le=1)]


class ContractError(ValueError):
    def __init__(self, code, field, message, retryable=False):
        self.code, self.field, self.message, self.retryable = code, field, message, retryable
        super().__init__(code, field, message, retryable)

    def __str__(self):
        return f'{self.code}: {self.field}: {self.message}'

    def as_dict(self):
        return dict(code=self.code, field=self.field, message=self.message, retryable=self.retryable)


class Object(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True, allow_inf_nan=False)


class Constraints(Object):
    paper_language: Literal['zh-CN', 'en'] = 'zh-CN'
    word_target: Annotated[int, Field(gt=0)] | None = None
    paper_type: Literal[
        'research', 'empirical', 'literature_review', 'theoretical', 'case_study', 'conference'
    ] | None = None
    citation_style: Literal['APA 7', 'Chicago Author-Date', 'MLA 9', 'IEEE', 'Vancouver', 'GB/T 7714'] = 'GB/T 7714'
    focus: list[Text] = Field(default_factory=list)


class DataTable(Object):
    columns: Annotated[list[Text], Field(min_length=1, max_length=200)]
    column_types: list[Literal['string', 'integer', 'number', 'boolean', 'date', 'datetime']]
    units: list[Text | None]
    rows: Annotated[list[list], Field(min_length=1, max_length=100_000)]

    @model_validator(mode='after')
    def cells(self):
        if len(set(self.columns)) != len(self.columns) or any(not c.strip() for c in self.columns):
            raise ValueError('列名须非空且唯一')
        if len(self.columns) != len(self.column_types) or len(self.units) != len(self.columns):
            raise ValueError('columns/column_types/units 长度须相同')
        if len(self.rows) * len(self.columns) > 1_000_000:
            raise ValueError('表格超过 100 万单元格')
        for row_number, row in enumerate(self.rows, 1):
            if len(row) != len(self.columns):
                raise ValueError(f'第 {row_number} 行有 {len(row)} 个字段，但 columns 定义了 {len(self.columns)} 列；请核对原始数据，系统不会自动补值或删列')
            for value, kind in zip(row, self.column_types):
                if value is None:
                    continue
                valid = {'string': type(value) is str, 'integer': type(value) is int,
                         'number': type(value) in (int, float), 'boolean': type(value) is bool,
                         'date': type(value) is str, 'datetime': type(value) is str}[kind]
                if not valid:
                    raise ValueError(f'{kind} 列包含错误类型 {type(value).__name__}')
                if kind == 'date':
                    if not re.fullmatch(r'\d{4}-\d{2}-\d{2}', value):
                        raise ValueError('日期必须为 YYYY-MM-DD')
                    date.fromisoformat(value)
                elif kind == 'datetime':
                    if not re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})', value):
                        raise ValueError('时间必须为带时区的 RFC3339')
                    datetime.fromisoformat(value.replace('Z', '+00:00'))
        digest(self.model_dump())  # Reject non-finite cells, including huge integers/invalid JSON.
        return self


class NamedTable(Object):
    id: Annotated[str, Field(pattern=r'^[A-Za-z][A-Za-z0-9_-]{0,63}$')]
    description: str = ''
    table: DataTable


class DataBundle(Object):
    tables: Annotated[list[NamedTable], Field(min_length=1, max_length=20)]

    @model_validator(mode='after')
    def capacity(self):
        ids = [t.id for t in self.tables]
        if len(set(ids)) != len(ids) or {'data', 'experiment'} & set(ids):
            raise ValueError('表 ID 重复或使用了保留名 data/experiment')
        if (sum(len(t.table.rows) for t in self.tables) > 100_000
                or sum(len(t.table.rows) * len(t.table.columns) for t in self.tables) > 1_000_000):
            raise ValueError('表集合超过累计行数/单元格限制')
        return self


class ColumnRef(Object):
    table_id: Text
    column: Text


class ColumnDescription(ColumnRef):
    meaning: Text


class TableRelation(Object):
    left_table: Text
    right_table: Text
    left_keys: Annotated[list[Text], Field(min_length=1)]
    right_keys: Annotated[list[Text], Field(min_length=1)]
    cardinality: Literal['one_to_one', 'one_to_many', 'many_to_one', 'many_to_many']


class DataContext(Object):
    description: str = ''
    column_descriptions: list[ColumnDescription] = Field(default_factory=list)
    relations: list[TableRelation] = Field(default_factory=list)


class StudyDesign(Object):
    description: str = ''
    observation_unit: Text | None = None
    unit_keys: list[ColumnRef] = Field(default_factory=list)
    pairing_keys: list[ColumnRef] = Field(default_factory=list)
    grouping: list[ColumnRef] = Field(default_factory=list)
    repeated_measures: list[ColumnRef] = Field(default_factory=list)
    covariates: list[ColumnRef] = Field(default_factory=list)
    time_column: ColumnRef | None = None
    weight_column: ColumnRef | None = None


class Experiment(Object):
    executed: bool
    plan: Text
    results: DataTable | None = None
    report: Text
    code: Text | None = None
    logs: Text | None = None

    @model_validator(mode='after')
    def actual(self):
        if self.executed and (self.results is None or self.logs is None):
            raise ValueError('已执行实验必须有 results/logs')
        if not self.executed and (self.results is not None or self.logs is not None):
            raise ValueError('未执行实验的 results/logs 必须为 null')
        return self


class AnalysisInput(Object):
    topic: Text
    request: str = ''
    constraints: Constraints = Field(default_factory=Constraints)
    data: DataTable | DataBundle | None = None
    experiment: Experiment | None = None
    data_context: DataContext | None = None
    study_design: StudyDesign | None = None


class DataSource(Object):
    id: Text
    columns: Annotated[list[Text], Field(min_length=1)]
    row_count: Annotated[int, Field(gt=0)]
    data_sha256: Digest


class AnalysisTarget(Object):
    id: Text
    question: Text
    estimand: Text
    scope: Text
    columns: Annotated[list[ColumnRef], Field(min_length=1)]


class PriorSpec(Object):
    parameter: Text
    distribution: Literal['beta', 'normal', 'student_t', 'half_normal', 'exponential']
    parameters: dict[str, Number]
    source: Literal['supplied', 'historical', 'default']
    source_table_ids: list[Text] = Field(default_factory=list)
    rationale: Text

    @model_validator(mode='after')
    def parameterization(self):
        names, positive = {'beta': ({'alpha', 'beta'}, {'alpha', 'beta'}),
                           'normal': ({'mu', 'sigma'}, {'sigma'}),
                           'student_t': ({'nu', 'mu', 'sigma'}, {'nu', 'sigma'}),
                           'half_normal': ({'sigma'}, {'sigma'}),
                           'exponential': ({'rate'}, {'rate'})}[self.distribution]
        if set(self.parameters) != names or any(self.parameters[k] <= 0 for k in positive):
            raise ValueError('先验分布参数无效')
        if (self.source == 'historical') != bool(self.source_table_ids):
            raise ValueError('只有历史先验必须提供 source_table_ids')
        return self


class ModelSummary(Object):
    id: Text
    target_ids: Annotated[list[Text], Field(min_length=1)]
    method: Text
    formula: Text | None = None
    likelihood: Text | None = None
    priors: list[PriorSpec] = Field(default_factory=list)


class SourceRowCount(Object):
    table_id: Text
    count: Count


class SampleSize(Object):
    source_rows: Annotated[list[SourceRowCount], Field(min_length=1)]
    n: Count | None = None
    unit: Text
    excluded_n: Count | None = None


class Interval(Object):
    kind: Literal['confidence', 'credible', 'prediction']
    level: Level
    lower: Number | None = None
    upper: Number | None = None
    method: Text

    @model_validator(mode='after')
    def ordered(self):
        if self.lower is None and self.upper is None:
            raise ValueError('区间不能两端都为空')
        if self.lower is not None and self.upper is not None and self.lower > self.upper:
            raise ValueError('区间端点反转')
        return self


class TestResult(Object):
    name: Text
    statistic: Number | None = None
    p_value: Probability
    adjusted_p_value: Probability | None = None
    adjustment: Text | None = None
    alternative: Literal['two_sided', 'greater', 'less']
    null_hypothesis: Text
    family_id: Text | None = None


class ProbabilityResult(Object):
    kind: Literal['posterior', 'posterior_predictive']
    event: Text
    operator: Literal['gt', 'ge', 'lt', 'le'] | None = None
    threshold: Number | None = None
    threshold_unit: Text | None = None
    mcse: Annotated[Number, Field(ge=0)] | None = None

    @model_validator(mode='after')
    def event_fields(self):
        if (self.operator is None) != (self.threshold is None):
            raise ValueError('阈值和运算符须同时存在')
        if self.operator is None and self.threshold_unit is not None:
            raise ValueError('复合事件不能填写阈值单位')
        return self


class Finding(Object):
    id: Text
    target_id: Text
    kind: Literal['estimate', 'effect', 'probability', 'observation', 'count']
    statement: Text
    columns: Annotated[list[ColumnRef], Field(min_length=1)]
    calculation: Text
    value: Number | None = None
    unit: Text | None = None
    sample_size: SampleSize
    interval: Interval | None = None
    test: TestResult | None = None
    probability: ProbabilityResult | None = None
    model_id: Text | None = None

    @model_validator(mode='after')
    def semantics(self):
        if self.kind != 'observation' and self.value is None:
            raise ValueError('数值结论缺少 value')
        if self.kind == 'count' and (self.value < 0 or not self.value.is_integer()):
            raise ValueError('计数必须是非负整数')
        if self.probability and (not 0 <= self.value <= 1 or self.unit is not None or self.model_id is None):
            raise ValueError('后验概率须为 0..1、无单位且关联模型')
        return self


class Diagnostic(Object):
    id: Text
    model_id: Text | None = None
    name: Text
    outcome: Literal['passed', 'failed', 'not_applicable', 'not_checked']
    value: Number | None = None
    threshold: Text | None = None
    details: Text


class Analysis(Object):
    data_sha256: Digest
    input_sha256: Digest
    data_sources: Annotated[list[DataSource], Field(min_length=1, max_length=20)]
    summary: Text
    row_count: Annotated[int, Field(gt=0)]
    targets: Annotated[list[AnalysisTarget], Field(min_length=1)]
    methods: Annotated[list[Text], Field(min_length=1)]
    findings: Annotated[list[Finding], Field(min_length=1)]
    models: list[ModelSummary]
    diagnostics: list[Diagnostic]
    limitations: list[Text]


class Chart(Object):
    id: Annotated[str, Field(pattern=r'^[A-Za-z][A-Za-z0-9_-]{0,63}$')]
    title: Text
    columns: Annotated[list[ColumnRef], Field(min_length=1)]
    svg: Text
    finding_ids: list[Text]
    caption: Text


class AnalysisOutput(Object):
    """F4 producer payload; downstream stages implement their own consumers."""
    analysis: Analysis
    charts: list[Chart]
    is_demo: bool


def digest(value):
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'), allow_nan=False)
    return hashlib.sha256(raw.encode()).hexdigest()


def parse(cls, value, field='input', *, extra=None):
    try:
        options = {'extra': extra} if extra is not None else {}
        return cls.model_validate(value, **options).model_dump()
    except (ValidationError, TypeError, ValueError, OverflowError) as exc:
        code = 'INVALID_OUTPUT' if field.startswith('output') else 'INVALID_INPUT'
        if not field.startswith('output') and isinstance(exc, ValidationError):
            if any(e['type'] == 'too_long' or '超过' in e['msg'] for e in exc.errors()):
                code = 'INPUT_TOO_LARGE'
        message = str(exc)
        if isinstance(exc, ValidationError):
            errors = exc.errors(include_input=False, include_url=False)
            message = '; '.join(
                ('.'.join(str(part) for part in item['loc']) + ': ' if item['loc'] else '') + item['msg']
                for item in errors[:5])
        raise ContractError(code, field, message) from exc


def table_map(inputs):
    data = inputs['data']
    if data is None:
        return {'experiment': inputs['experiment']['results']}
    if 'tables' in data:
        return {entry['id']: entry['table'] for entry in data['tables']}
    return {'data': data}


PROMPT_TABLE_BYTES = 48 * 1024


def table_profile(table, levels=20, samples=5):
    """Columns, level counts, ranges and a few rows: enough to plan from, without every row."""
    rows, profiles = table['rows'], []
    for index, name in enumerate(table['columns']):
        values = [row[index] for row in rows if row[index] is not None]
        counts = Counter(values)
        item = dict(column=name, missing=len(rows) - len(values), distinct=len(counts))
        if values and table['column_types'][index] in ('integer', 'number'):
            item.update(min=min(values), max=max(values))
        if len(counts) <= levels:
            item['level_counts'] = [[value, count] for value, count in counts.items()]
        else:
            item['examples'] = list(counts)[:samples]
        profiles.append(item)
    return dict(columns=table['columns'], column_types=table['column_types'], units=table['units'],
                row_count=len(rows), rows_omitted=True, column_profiles=profiles, sample_rows=rows[:samples])


def prompt_tables(tables):
    """Prompts carry complete tables within a size budget and profiles beyond it; executed code gets every row."""
    shown, used = {}, 0
    for key, table in tables.items():
        size = len(json.dumps(table, ensure_ascii=False, allow_nan=False, separators=(',', ':')).encode())
        if used + size <= PROMPT_TABLE_BYTES:
            shown[key], used = table, used + size
        else:
            shown[key] = table_profile(table)
    return shown


def refs_exist(refs, tables, field):
    pairs = [(r['table_id'], r['column']) for r in refs]
    missing = [(t, c) for t, c in pairs if t not in tables or c not in tables[t]['columns']]
    if len(set(pairs)) != len(pairs) or missing:
        detail = f'不存在的列引用: {missing}。' if missing else '列引用重复。'
        available = {name: value['columns'] for name, value in tables.items()}
        code = 'INVALID_OUTPUT' if field.startswith(('output', 'analysis.results')) else 'INVALID_INPUT'
        raise ContractError(code, field, detail + f'可用表及原始列: {available}')


def normalize_input(value):
    value = copy.deepcopy(value)
    result = parse(AnalysisInput, value)
    result['topic'] = result['topic'].strip()
    if not result['topic']:
        raise ContractError('MISSING_MATERIAL', 'input.topic', '缺少研究主题')
    if result['data'] is not None and result['experiment'] is not None:
        raise ContractError('AMBIGUOUS_INPUT', 'input', '同时提供 data 和 experiment，请选择一个来源')
    if result['data'] is None and not (result['experiment'] and result['experiment']['executed']):
        raise ContractError('MISSING_MATERIAL', 'input.data', '缺少实际数据或已执行实验结果')
    tables = table_map(result)
    context = result['data_context'] or {}
    refs_exist(context.get('column_descriptions', []), tables, 'input.data_context.column_descriptions')
    for relation in context.get('relations', []):
        left, right = relation['left_table'], relation['right_table']
        if left == right or len(relation['left_keys']) != len(relation['right_keys']):
            raise ContractError('INVALID_INPUT', 'input.data_context.relations', '关联表/键长度无效')
        for side in ('left', 'right'):
            table_id, keys = relation[f'{side}_table'], relation[f'{side}_keys']
            refs_exist([dict(table_id=table_id, column=c) for c in keys], tables, 'input.data_context.relations')
            indices = [tables[table_id]['columns'].index(c) for c in keys]
            values = [tuple(row[i] for i in indices) for row in tables[table_id]['rows']]
            if relation['cardinality'].split('_to_')[0 if side == 'left' else 1] == 'one':
                non_null = [v for v in values if None not in v]
                if len(set(non_null)) != len(non_null):
                    raise ContractError('INVALID_INPUT', 'input.data_context.relations', '关联键基数不符，有重复键')
        for lc, rc in zip(relation['left_keys'], relation['right_keys']):
            lt = tables[left]['column_types'][tables[left]['columns'].index(lc)]
            rt = tables[right]['column_types'][tables[right]['columns'].index(rc)]
            if lt != rt and {lt, rt} != {'integer', 'number'}:
                raise ContractError('INVALID_INPUT', 'input.data_context.relations', '关联键类型不兼容')
    design = result['study_design'] or {}
    for key in ('unit_keys', 'pairing_keys', 'grouping', 'repeated_measures', 'covariates'):
        refs_exist(design.get(key, []), tables, 'input.study_design.' + key)
    for key in ('time_column', 'weight_column'):
        if design.get(key):
            refs_exist([design[key]], tables, 'input.study_design.' + key)
    return result


def binding(inputs):
    tables = table_map(inputs)
    return dict(data_sha256=digest([dict(id=k, table=tables[k]) for k in sorted(tables)]),
                input_sha256=digest({'contract': VERSION, 'input': inputs}),
                row_count=sum(len(t['rows']) for t in tables.values()),
                data_sources=[dict(id=k, columns=tables[k]['columns'], row_count=len(tables[k]['rows']),
                                   data_sha256=digest(tables[k])) for k in sorted(tables)])


def unique(items, label):
    indexed = {item['id']: item for item in items}
    if len(indexed) != len(items):
        raise ContractError('INVALID_OUTPUT', label, '重复 ID')
    return indexed


def validate_analysis(value, inputs=None):
    result = parse(Analysis, value, 'output.analysis')
    sources = unique(result['data_sources'], 'output.analysis.data_sources')
    targets = unique(result['targets'], 'output.analysis.targets')
    models = unique(result['models'], 'output.analysis.models')
    findings = unique(result['findings'], 'output.analysis.findings')
    unique(result['diagnostics'], 'output.analysis.diagnostics')
    if result['row_count'] != sum(s['row_count'] for s in sources.values()):
        raise ContractError('INVALID_OUTPUT', 'output.analysis.row_count', '原始总行数不一致')
    if inputs is not None:
        for key, expected in binding(inputs).items():
            if result[key] != expected:
                raise ContractError('INVALID_OUTPUT', 'output.analysis.' + key, '输入摘要或来源不一致')
    for target in targets.values():
        refs_exist(target['columns'], sources, 'output.analysis.targets.columns')
    for model in models.values():
        if len(set(model['target_ids'])) != len(model['target_ids']) or not set(model['target_ids']) <= targets.keys():
            raise ContractError('INVALID_OUTPUT', 'output.analysis.models', '模型引用的目标不存在或重复')
        for prior in model['priors']:
            if not set(prior['source_table_ids']) <= sources.keys():
                raise ContractError('INVALID_OUTPUT', 'output.analysis.models.priors', '历史先验表不存在')
    for finding in findings.values():
        refs_exist(finding['columns'], sources, 'output.analysis.findings.columns')
        mid, tid = finding['model_id'], finding['target_id']
        if tid not in targets or mid is not None and (mid not in models or tid not in models[mid]['target_ids']):
            raise ContractError('INVALID_OUTPUT', 'output.analysis.findings', '目标或模型引用错误')
        if finding['probability'] or finding['interval'] and finding['interval']['kind'] == 'credible':
            if mid is None or not models[mid]['priors'] or not models[mid]['likelihood']:
                raise ContractError('INVALID_OUTPUT', 'output.analysis.findings', '后验结论缺少先验/似然')
        counts = finding['sample_size']['source_rows']
        if len({x['table_id'] for x in counts}) != len(counts):
            raise ContractError('INVALID_OUTPUT', 'output.analysis.findings.sample_size', '来源表重复')
        for count in counts:
            if count['table_id'] not in sources or count['count'] > sources[count['table_id']]['row_count']:
                raise ContractError('INVALID_OUTPUT', 'output.analysis.findings.sample_size', '来源计数超过原始表')
    if {f['target_id'] for f in findings.values()} != targets.keys():
        raise ContractError('INVALID_OUTPUT', 'output.analysis.targets', '有分析目标未产生结果')
    for diagnostic in result['diagnostics']:
        if diagnostic['model_id'] is not None and diagnostic['model_id'] not in models:
            raise ContractError('INVALID_OUTPUT', 'output.analysis.diagnostics', '诊断模型不存在')
    return result


def validate_charts(value, analysis):
    from .validation import validate_svg
    if not isinstance(value, list) or len(value) > 20:
        raise ContractError('INVALID_OUTPUT', 'output.charts', '图表必须是 0..20 项数组')
    charts = [parse(Chart, c, 'output.charts') for c in value]
    unique(charts, 'output.charts')
    sources = {s['id']: s for s in analysis['data_sources']}
    ids = {f['id'] for f in analysis['findings']}
    for chart in charts:
        refs_exist(chart['columns'], sources, 'output.charts.columns')
        if not set(chart['finding_ids']) <= ids or len(set(chart['finding_ids'])) != len(chart['finding_ids']):
            raise ContractError('INVALID_OUTPUT', 'output.charts.finding_ids', '图表发现引用不存在或重复')
        validate_svg(chart['svg'])
    return charts
