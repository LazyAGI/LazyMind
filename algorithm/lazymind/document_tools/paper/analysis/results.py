"""Result rows emitted by executed code; identities are assigned after execution."""
import copy
import json
from typing import Annotated, Literal

from pydantic import Field

from .plan import questions
from .schema import (
    Object, Text, Number, ColumnRef, SampleSize, Interval, TestResult, ProbabilityResult,
    ModelSummary, parse, refs_exist, table_map,
)


class ExecutedResult(Object):
    question_id: Text
    label: Text = Field(description='Concise scientific name for this result, not source filters or an ID.')
    metric: Text
    method: Text
    value: Number
    columns: Annotated[list[ColumnRef], Field(min_length=1)]
    scope: Text = Field(description='Actual observation scope, preparation and comparison direction.')
    coordinates: dict[str, object] = Field(default_factory=dict)
    comparison: str = ''
    kind: Literal['estimate', 'effect', 'probability', 'count'] = 'estimate'
    unit: str | None = None
    sample_size: SampleSize
    interval: Interval | None = None
    test: TestResult | None = None
    probability: ProbabilityResult | None = None
    model_id: str | None = None


class UnavailableQuestion(Object):
    question_id: Text
    reason: Text = Field(description='Concrete missing evidence or unsupported comparison; never a computed result.')
    columns: Annotated[list[ColumnRef], Field(
        min_length=1, description='Actual source columns revealing the limitation.')]


class ExecutedAnalysis(Object):
    results: list[ExecutedResult] = Field(default_factory=list)
    unavailable_questions: list[UnavailableQuestion] = Field(default_factory=list)
    limitations: list[Text] = Field(default_factory=list)
    models: list[ModelSummary] = Field(default_factory=list)


def accept_results(value, plan, inputs):
    """Accept real result rows and source references, without recalculating statistics."""
    output = parse(ExecutedAnalysis, value, 'analysis.execution', extra='ignore')
    known = {item['id'] for item in questions(plan)}
    for index, row in enumerate(output['results'], 1):
        if row['question_id'] not in known:
            raise ValueError(f'Result {index} refers to unknown question {row["question_id"]}; '
                             f'available: {sorted(known)}')
        refs_exist(row['columns'], table_map(inputs), f'analysis.results[{index}].columns')
        row['id'] = f'r{index}'
    unavailable = set()
    covered = {row['question_id'] for row in output['results']}
    for item in output['unavailable_questions']:
        identifier = item['question_id']
        if identifier not in known or identifier in unavailable or identifier in covered:
            raise ValueError('Unavailable question must be a unique known question without numeric results: '
                             + identifier)
        refs_exist(item['columns'], table_map(inputs), 'analysis.unavailable_questions.columns')
        unavailable.add(identifier)
    return output


def result_rows(analysis):
    """Keep meaning, values and source metadata together in every model context."""
    rows = []
    for index, finding in enumerate(analysis['findings']):
        metadata = json.loads(finding['calculation'])
        row = copy.deepcopy({key: value for key, value in finding.items()
                             if key not in ('statement', 'calculation')})
        keys = ('label', 'metric', 'method', 'scope', 'coordinates', 'comparison')
        row.update(index=index, **{key: metadata[key] for key in keys})
        rows.append(row)
    return rows
