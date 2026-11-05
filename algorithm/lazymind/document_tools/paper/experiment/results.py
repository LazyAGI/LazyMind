"""Shared, standard-library result validation; also shipped as run_experiment.py."""
import csv
import json
import math
from pathlib import Path


def validate_column_types(value):
    if not isinstance(value, dict) or not 1 <= len(value) <= 30:
        raise ValueError('RESULT_COLUMNS 必须是 1—30 列的名称/类型对象')
    for name, kind in value.items():
        if not isinstance(name, str) or not name.strip() or name != name.strip() or len(name) > 100:
            raise ValueError('结果列名必须是无首尾空格的非空字符串，最多 100 字符')
        if kind not in ('string', 'integer', 'number', 'boolean'):
            raise ValueError(f'结果列 {name} 类型必须是 string/integer/number/boolean')
    return value


def validate_table(table, column_types=None, *, input_data=False):
    """Validate JSON scalars and exact column names/types; JSON objects have no order."""
    if not isinstance(table, dict) or set(table) != {'columns', 'rows'}:
        raise ValueError('结果表必须且只能包含 columns/rows')
    columns, rows = table['columns'], table['rows']
    if (not isinstance(columns, list) or not columns or (not input_data and len(columns) > 30)
            or any(not isinstance(c, str) or not c.strip()
                   or (not input_data and (c != c.strip() or len(c) > 100)) for c in columns)
            or len(set(columns)) != len(columns)):
        raise ValueError('结果列名不能为空、重复或包含首尾空格，最多 30 列')
    if not isinstance(rows, list) or not rows:
        raise ValueError('结果 rows 必须是非空列表')
    if column_types is not None:
        validate_column_types(column_types)
        if set(columns) != set(column_types):
            raise ValueError('结果列名必须与 RESULT_COLUMNS 一致')
    for number, row in enumerate(rows, 1):
        if not isinstance(row, list) or len(row) != len(columns):
            raise ValueError(f'结果第 {number} 行长度必须与列数相同')
        for name, cell in zip(columns, row):
            if cell is not None and type(cell) not in (str, int, float, bool):
                raise ValueError(f'结果第 {number} 行 {name} 必须是 JSON 标量')
            if isinstance(cell, float) and not math.isfinite(cell):
                raise ValueError(f'结果第 {number} 行 {name} 不允许 NaN/Infinity')
            if not input_data and isinstance(cell, str) and len(cell) > 1000:
                raise ValueError(f'结果第 {number} 行 {name} 文本过长')
            if column_types is not None:
                allowed = {'string': (str,), 'integer': (int,),
                           'number': (int, float), 'boolean': (bool,)}[column_types[name]]
                if type(cell) not in allowed:
                    raise ValueError(f'结果第 {number} 行 {name} 必须是 {column_types[name]}，'
                                     f'实际为 {type(cell).__name__}')
    return table


def result_schema(column_types):
    """Describe the actual names, order and types, rather than a generic table."""
    validate_column_types(column_types)
    count = len(column_types)
    return {
        '$schema': 'https://json-schema.org/draft/2020-12/schema',
        'type': 'object', 'required': ['columns', 'rows'], 'additionalProperties': False,
        'properties': {
            'columns': {'const': list(column_types)},
            'rows': {'type': 'array', 'minItems': 1, 'items': {
                'type': 'array', 'minItems': count, 'maxItems': count,
                'prefixItems': [{'type': kind} for kind in column_types.values()],
                'items': False,
            }},
        },
    }


def records_to_table(records, column_types):
    """Generated algorithms return named records; the host owns column ordering."""
    validate_column_types(column_types)
    if not isinstance(records, list) or not records:
        raise ValueError('run_experiment 必须返回非空的记录列表')
    for number, record in enumerate(records, 1):
        if not isinstance(record, dict) or set(record) != set(column_types):
            raise ValueError(f'结果第 {number} 条记录字段必须与 RESULT_COLUMNS 一致')
    table = {'columns': list(column_types), 'rows': [
        [record[name] for name in column_types] for record in records]}
    return validate_table(table, column_types)


def main():
    """User-invoked CLI only. LazyMind never invokes this entry during generation."""
    from experiment import RESULT_COLUMNS, run_experiment

    root = Path(__file__).resolve().parent
    schema = json.loads((root / 'result_schema.json').read_text(encoding='utf-8'))
    column_types = dict(zip(schema['properties']['columns']['const'], [
        item['type'] for item in schema['properties']['rows']['items']['prefixItems']]))
    validate_column_types(column_types)
    if RESULT_COLUMNS != column_types:
        raise ValueError('代码 RESULT_COLUMNS 与 result_schema.json 不一致')
    config = json.loads((root / 'config.json').read_text(encoding='utf-8'))
    if not isinstance(config, dict):
        raise ValueError('config.json 必须是对象')
    json.dumps(config, allow_nan=False)
    input_path = root / 'input.csv'
    data = None
    bundle_path = root / 'input.json'
    if bundle_path.is_file():
        data = json.loads(bundle_path.read_text(encoding='utf-8'))
        if isinstance(data, dict) and 'tables' in data:
            if not isinstance(data['tables'], list) or not data['tables']:
                raise ValueError('input.json 必须是非空表集合')
            for entry in data['tables']:
                validate_table({key: entry['table'][key] for key in ('columns', 'rows')}, input_data=True)
        else:
            validate_table(data, input_data=True)
    elif input_path.is_file():
        with input_path.open(encoding='utf-8-sig', newline='') as stream:
            reader = csv.reader(stream)
            columns = next(reader, [])
            data = {'columns': columns, 'rows': list(reader)}
        validate_table(data, input_data=True)
    table = records_to_table(run_experiment(config, data), column_types)
    # Validate everything before writing either result file.
    encoded = json.dumps(table, ensure_ascii=False, indent=2, allow_nan=False)
    (root / 'results.json').write_text(encoded, encoding='utf-8')
    with (root / 'results.csv').open('w', encoding='utf-8', newline='') as stream:
        writer = csv.writer(stream)
        writer.writerow(table['columns'])
        writer.writerows(table['rows'])


if __name__ == '__main__':
    main()
