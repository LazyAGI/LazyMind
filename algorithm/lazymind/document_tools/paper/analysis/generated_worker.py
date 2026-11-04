"""Isolated worker for generated preparation and statistics programs."""
import __future__
import contextlib
import importlib.metadata
import io
import json
import sys
import traceback


def check_coordinate_snapshots(output):
    """Reject mutable coordinate aliases before JSON erases their object identity."""
    seen = {}
    for index, row in enumerate(output.get('results', []) if isinstance(output, dict) else [], 1):
        if not isinstance(row, dict) or not isinstance(row.get('coordinates'), dict) or not row['coordinates']:
            continue
        comparison = row.get('comparison')
        if not comparison:
            continue
        key = (id(row['coordinates']), row.get('metric'), row.get('unit'), row.get('kind', 'estimate'))
        previous = seen.get(key)
        if previous and previous[1] != comparison:
            raise ValueError(
                f'SHARED_RESULT_COORDINATES: results {previous[0]} ({previous[1]!r}) and {index} '
                f'({comparison!r}) share mutable coordinates for metric {row.get("metric")!r}. '
                'Copy/freeze coordinates inside the result-record helper BEFORE later mutations. '
                'Repair analysis code; preserve values, units, scope and separate estimands, '
                'rather than changing the plot grouping to hide the conflict.')
        seen[key] = (index, comparison)


def run(payload):
    import numpy as np
    original = json.dumps(payload['tables'], sort_keys=True, allow_nan=False)
    plan = json.dumps(payload['plan'], sort_keys=True, allow_nan=False)
    namespace = {'__name__': 'generated_analysis'}
    with contextlib.redirect_stdout(io.StringIO()):
        exec(compile(payload['code'], '<generated-analysis>', 'exec',
                     flags=__future__.annotations.compiler_flag, dont_inherit=True), namespace)
        results = namespace['analyze'](payload['tables'], payload['plan'], np.random.default_rng(payload['seed']))
        check_coordinate_snapshots(results)
    if (original != json.dumps(payload['tables'], sort_keys=True, allow_nan=False)
            or plan != json.dumps(payload['plan'], sort_keys=True, allow_nan=False)):
        raise ValueError('生成代码修改了原始数据或锁定计划')
    versions = {}
    for name in ('numpy', 'scipy', 'pandas', 'statsmodels', 'pymc', 'arviz'):
        try:
            versions[name] = importlib.metadata.version(name)
        except importlib.metadata.PackageNotFoundError:
            pass
    return {'results': results, 'versions': versions, 'seed': payload['seed']}


if __name__ == '__main__':
    try:
        print(json.dumps(run(json.load(sys.stdin)), ensure_ascii=False, allow_nan=False))
    except Exception as exc:
        frames = [dict(file=frame.filename, line=frame.lineno, function=frame.name)
                  for frame in traceback.extract_tb(exc.__traceback__)
                  if frame.filename == '<generated-analysis>']
        print(json.dumps({'error': f'{type(exc).__name__}: {str(exc)[:4000]}',
                          'frames': frames[-3:]}, ensure_ascii=False))
        sys.exit(1)
