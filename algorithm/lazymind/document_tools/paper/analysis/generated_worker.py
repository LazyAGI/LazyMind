"""Isolated worker for generated preparation and statistics programs."""
import __future__
import contextlib
import importlib.metadata
import io
import json
import sys
import traceback


def run(payload):
    import numpy as np
    original = json.dumps(payload['tables'], sort_keys=True, allow_nan=False)
    plan = json.dumps(payload['plan'], sort_keys=True, allow_nan=False)
    namespace = {'__name__': 'generated_analysis'}
    with contextlib.redirect_stdout(io.StringIO()):
        exec(compile(payload['code'], '<generated-analysis>', 'exec',
                     flags=__future__.annotations.compiler_flag, dont_inherit=True), namespace)
        results = namespace['analyze'](payload['tables'], payload['plan'], np.random.default_rng(payload['seed']))
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
