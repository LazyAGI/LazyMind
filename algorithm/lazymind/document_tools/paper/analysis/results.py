"""Result rows emitted by executed code; identities are assigned after execution."""
import copy
import json
import re
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
    kind: Literal['estimate', 'effect', 'probability', 'observation', 'count'] = 'estimate'
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
    value = copy.deepcopy(value)
    tables = table_map(inputs)
    known = {item['id'] for item in questions(plan)}
    if isinstance(value, dict):
        for key in ('results', 'unavailable_questions', 'limitations', 'models'):
            if value.get(key) is None:
                value.pop(key, None)
        for row in [*(value.get('results') or []), *(value.get('unavailable_questions') or [])]:
            if not isinstance(row, dict):
                continue
            identifier = row.get('question_id')
            ordinal = re.fullmatch(r'[qQ]([1-9][0-9]*)(?:[_:-].+)?', identifier) if isinstance(identifier, str) else None
            if ordinal and 'q' + ordinal[1] in known:
                row['question_id'] = 'q' + ordinal[1]
            refs = row.get('columns')
            for ref in refs if isinstance(refs, list) else []:
                if isinstance(ref, dict) and ref.get('table_id') in (None, ''):
                    matches = [key for key, table in tables.items() if ref.get('column') in table['columns']]
                    if len(matches) == 1:
                        ref['table_id'] = matches[0]
            if row.get('scope') in (None, '') and 'value' in row:
                sample = row.get('sample_size') or {}
                counts = sample.get('source_rows') if isinstance(sample, dict) else None
                if isinstance(counts, list) and counts and all(isinstance(item, dict) for item in counts):
                    # Record only explicit provenance; source row count is not the statistical sample size.
                    row['scope'] = '; '.join(
                        f"{item.get('table_id')}: {item.get('count')} source rows" for item in counts)
                    if sample.get('unit'):
                        row['scope'] += f"; observation unit: {sample['unit']}"
                    if row.get('comparison'):
                        row['scope'] += f"; comparison: {row['comparison']}"
    output = parse(ExecutedAnalysis, value, 'analysis.execution', extra='ignore')
    for index, row in enumerate(output['results'], 1):
        if row['question_id'] not in known:
            raise ValueError(f'Result {index} refers to unknown question {row["question_id"]}; '
                             f'available: {sorted(known)}')
        refs_exist(row['columns'], table_map(inputs), f'analysis.results[{index}].columns')
        row['id'] = f'r{index}'
    unavailable = set()
    covered = {row['question_id'] for row in output['results']}
    planned_unavailable = []
    for item in output['unavailable_questions']:
        identifier = item['question_id']
        refs_exist(item['columns'], tables, 'analysis.unavailable_questions.columns')
        if identifier not in known:
            sources = ', '.join(f"{ref['table_id']}.{ref['column']}" for ref in item['columns'])
            output['limitations'].append(f"{identifier}: {item['reason']} (sources: {sources})")
            continue
        if identifier in unavailable or identifier in covered:
            raise ValueError('Unavailable question must be a unique known question without numeric results: '
                             + identifier)
        unavailable.add(identifier)
        planned_unavailable.append(item)
    output['unavailable_questions'] = planned_unavailable
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
