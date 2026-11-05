"""Export inert F3 files through the existing Runtime artifact publisher."""
import csv
import io
import json
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

from ..contracts import validate_output
from . import results
from .results import result_schema


def csv_text(table: dict, *, spreadsheet_safe: bool = True) -> str:
    stream = io.StringIO(newline='')
    writer = csv.writer(stream)
    for row in [table['columns'], *table['rows']]:
        # Downloaded previews must not turn model-produced cells into spreadsheet formulas.
        writer.writerow([
            "'" + cell if spreadsheet_safe and isinstance(cell, str)
            and cell.lstrip().startswith(('=', '+', '-', '@', '\t', '\r')) else cell
            for cell in row
        ])
    return stream.getvalue()


def export_experiment(output: dict, workspace: str, data: dict | None = None,
                      preference: str = 'simulate_only') -> dict[str, str]:
    """Package generated source as inert bytes; never import or execute it."""
    validate_output('experiment', output)
    if data is not None:
        from ..contracts import validate_value
        validate_value('data', data)
    if preference not in ('allow_local', 'simulate_only'):
        preference = 'simulate_only'
    root = Path(workspace)
    table = output['experiment']['results']
    manifest = {
        'experiment': output['experiment'], 'result_meta': output['result_meta'],
        'execution_preference': preference,
        'execution_available': output['result_meta']['execution_status'] in ('succeeded', 'failed', 'timeout'),
        'notice': output['result_meta']['reason'],
    }
    result_path = root / '实验结果.json'
    result_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, allow_nan=False), encoding='utf-8')
    archive_path = root / '实验代码.zip'
    with ZipFile(archive_path, 'w', compression=ZIP_DEFLATED) as archive:
        for item in output['code_files']:
            archive.writestr(item['path'], item['content'])
        archive.writestr('结果来源.json', result_path.read_text(encoding='utf-8'))
        if not output['code_files']:
            archive.writestr('README.md', output['experiment']['report'])
            archive.writestr('results.json', json.dumps(table, ensure_ascii=False, indent=2, allow_nan=False))
            entries = table['tables'] if 'tables' in table else [{'id': 'data', 'table': table}]
            for entry in entries:
                archive.writestr(entry['id'] + '.csv', csv_text(entry['table']))
            return {'experiment_code': str(archive_path), 'experiment_results': str(result_path)}
        archive.writestr('run_experiment.py', Path(results.__file__).read_text(encoding='utf-8'))
        archive.writestr('result_schema.json', json.dumps(
            result_schema(output['result_meta']['column_types']), ensure_ascii=False, indent=2))
        from .handoff import source_notice
        archive.writestr('使用前必读.txt',
                         source_notice(output['result_meta']) + '\n'
                         '检查后运行 python run_experiment.py；统一导出器校验字段和类型后保存 results.json/results.csv。\n'
                         '只有选定的输入数据会附在 input.csv 或 input.json。\n')
        if data is not None:
            # Input is machine-readable data, not a preview; preserve original values.
            if 'tables' in data or output['result_meta'].get('input_table_ids'):
                archive.writestr('input.json', json.dumps(data, ensure_ascii=False, allow_nan=False))
            else:
                archive.writestr('input.csv', csv_text(data, spreadsheet_safe=False))
        if table:
            prefix = 'results' if output['experiment']['executed'] else 'preview_results'
            archive.writestr(prefix + '.json', json.dumps(table, ensure_ascii=False, indent=2, allow_nan=False))
            archive.writestr(prefix + '.csv', csv_text(table))
        if output['result_meta'].get('execution_log'):
            archive.writestr('execution.log', output['result_meta']['execution_log'])
    return {'experiment_code': str(archive_path), 'experiment_results': str(result_path)}
