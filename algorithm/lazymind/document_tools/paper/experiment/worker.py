"""Standalone trusted worker loaded by the existing sandbox before access is restricted."""
import contextlib
import io
import json
import sys


def run(payload):
    namespace = {'__name__': 'generated_experiment'}
    log = io.StringIO()
    with contextlib.redirect_stdout(log), contextlib.redirect_stderr(log):
        exec(compile(payload['code'], '<generated-experiment>', 'exec'), namespace)
        records = namespace['run_experiment'](payload['config'], payload['data'])
    return {'records': records, 'logs': log.getvalue()[-6000:]}


if __name__ == '__main__':
    try:
        print(json.dumps(run(json.load(sys.stdin)), ensure_ascii=False, allow_nan=False))
    except Exception as exc:
        print(json.dumps({'error': f'{type(exc).__name__}: {str(exc)[:4000]}'}, ensure_ascii=False))
        sys.exit(1)
