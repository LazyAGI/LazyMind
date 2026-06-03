import time
import requests
import urllib.error
import urllib.request

from lazymind.config import config as _cfg
from lazymind.parsing.service.build_document import (
    ALGO_ID,
    build_document,
    drop_lazyllm_tables,
    get_algo_server_port,
    reset_stores,
)


def _wait_for_http_ok(url: str, label: str, timeout: float, interval: float) -> None:
    deadline = time.time() + timeout if timeout > 0 else None
    while True:
        try:
            with urllib.request.urlopen(url, timeout=3) as response:
                if 200 <= response.status < 300:
                    return
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            pass
        if deadline is not None and time.time() >= deadline:
            raise RuntimeError(f'timed out waiting for {label}: {url}')
        time.sleep(interval)


def _wait_for_algorithm_registration(processor_url: str, algo_id: str, timeout: float, interval: float) -> None:
    deadline = time.time() + timeout if timeout > 0 else None
    algo_list_url = f'{processor_url.rstrip("/")}/algo/list'
    while True:
        try:
            response = requests.get(algo_list_url, timeout=3)
            response.raise_for_status()
            data = response.json().get('data', [])
            if any(item.get('algo_id') == algo_id for item in data):
                return
        except requests.exceptions.RequestException:
            pass
        if deadline is not None and time.time() >= deadline:
            raise RuntimeError(f'timed out waiting for algorithm registration: {algo_id}')
        time.sleep(interval)


def main() -> None:
    processor_url = _cfg['document_processor_url'].rstrip('/')
    retry_interval = float(_cfg['startup_retry_interval'])
    startup_timeout = float(_cfg['startup_timeout'])

    _wait_for_http_ok(f'{processor_url}/health', 'DocumentProcessor', startup_timeout, retry_interval)

    if _cfg['reset_algo_on_startup']:
        drop_lazyllm_tables()
        reset_stores()

    docs = build_document()
    docs.start()

    _wait_for_http_ok(
        f'http://127.0.0.1:{get_algo_server_port()}/docs',
        'lazyllm-algo local service',
        startup_timeout,
        retry_interval,
    )
    _wait_for_algorithm_registration(processor_url, ALGO_ID, startup_timeout, retry_interval)

    try:
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        pass


if __name__ == '__main__':
    main()
