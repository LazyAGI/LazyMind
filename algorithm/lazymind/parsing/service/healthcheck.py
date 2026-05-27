import json
import os
import sys
import urllib.request

ALGO_ID = 'general_algo'


def _env_int(name: str, default: int) -> int:
    value = os.getenv(name)
    if value in (None, ''):
        return default
    return int(value)


def _get_bytes(url: str) -> bytes:
    with urllib.request.urlopen(url, timeout=3) as response:
        if response.status < 200 or response.status >= 300:
            raise RuntimeError(f'GET {url} returned HTTP {response.status}')
        return response.read()


def main() -> int:
    algo_port = _env_int('LAZYMIND_ALGO_SERVER_PORT', _env_int('LAZYMIND_DOCUMENT_SERVER_PORT', 8000))
    processor_url = os.getenv('LAZYMIND_DOCUMENT_PROCESSOR_URL', 'http://localhost:8000').rstrip('/')

    _get_bytes(f'http://127.0.0.1:{algo_port}/docs')
    payload = json.loads(_get_bytes(f'{processor_url}/algo/list').decode('utf-8'))
    items = payload.get('data', [])
    if not any(item.get('algo_id') == ALGO_ID for item in items):
        raise RuntimeError(f'algo_id not registered yet: {ALGO_ID}')
    return 0


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
