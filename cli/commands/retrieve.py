"""Retrieve command: run lazyllm.Retriever against a remote Document."""

import argparse
import json
import os
import subprocess
import sys
from typing import Any, Dict, List, Optional

from cli.context import get as get_context
from cli.context import resolve_algo_dataset, resolve_algo_url, resolve_dataset


DOCKER_RETRIEVE_SCRIPT = r"""
import json
import sys

from lazyllm import Document, Retriever
from lazymind.config import EMBED_MAIN


def run_single(document, payload):
    kwargs = {
        'group_name': payload['group_name'],
        'topk': payload['topk'],
        'similarity': payload['similarity'],
        'output_format': 'dict',
    }
    if payload.get('embed_keys'):
        kwargs['embed_keys'] = payload['embed_keys']
    retriever = Retriever(document, **kwargs)
    return retriever(query=payload['query'], filters=payload['filters'])


def run_config(document, payload):
    retriever_configs = [
        {'group_name': 'line', 'embed_keys': [EMBED_MAIN], 'target': 'block'},
        {'group_name': 'block', 'embed_keys': [EMBED_MAIN]},
    ]
    results = []
    for cfg in retriever_configs:
        cfg = dict(cfg)
        cfg['output_format'] = 'dict'
        retriever = Retriever(document, **cfg)
        result = retriever(query=payload['query'], filters=payload['filters'])
        if isinstance(result, list):
            results.extend(result)
    return results


payload = json.load(sys.stdin)
document = Document(url=f"{payload['url']}/_call", name=payload['algo_dataset'])
if payload.get('config_mode'):
    output = run_config(document, payload)
else:
    output = run_single(document, payload)
print(json.dumps(output, ensure_ascii=False))
"""


def _ensure_lazyllm():
    """Import lazyllm, raising a friendly error if unavailable."""
    try:
        import lazyllm  # noqa: F401
        return lazyllm
    except ImportError:
        print(
            'Error: lazyllm is not installed or not on PYTHONPATH.\n'
            'If running from the repo root, the ./lazymind wrapper '
            'should set PYTHONPATH automatically.',
            file=sys.stderr,
        )
        sys.exit(1)


def _find_local_algo_container() -> Optional[str]:
    override = os.getenv('LAZYMIND_ALGO_CONTAINER')
    if override:
        return override

    try:
        result = subprocess.run(
            ['docker', 'ps', '--filter', 'name=lazyllm-algo', '--format', '{{.Names}}'],
            check=False,
            capture_output=True,
            text=True,
        )
    except OSError:
        return None

    if result.returncode != 0:
        return None

    names = [line.strip() for line in result.stdout.splitlines() if line.strip()]
    return names[0] if names else None


def _build_document(url: str, algo_dataset: str):
    """Create a remote Document for the registered algo endpoint."""
    from lazyllm import Document
    return Document(url=f'{url}/_call', name=algo_dataset)


def _run_single_retriever(
    document,
    query: str,
    filters: Dict[str, str],
    group_name: str,
    topk: int,
    similarity: str,
    embed_keys: Optional[List[str]],
) -> List[Dict[str, Any]]:
    """Run one Retriever and return results as dicts."""
    from lazyllm import Retriever

    kwargs = {
        'group_name': group_name,
        'topk': topk,
        'similarity': similarity,
        'output_format': 'dict',
    }
    if embed_keys:
        kwargs['embed_keys'] = embed_keys

    retriever = Retriever(document, **kwargs)
    return retriever(query=query, filters=filters)


def _run_config_retrievers(
    document, query: str, filters: Dict[str, str], config_path: Optional[str],
) -> List[Dict[str, Any]]:
    """Run the default fixed retriever routes."""
    from lazyllm import Retriever
    from lazymind.config import EMBED_MAIN

    retriever_configs = [
        {'group_name': 'line', 'embed_keys': [EMBED_MAIN], 'target': 'block'},
        {'group_name': 'block', 'embed_keys': [EMBED_MAIN]},
    ]
    all_results = []
    for cfg in retriever_configs:
        cfg = dict(cfg)
        cfg['output_format'] = 'dict'
        retriever = Retriever(document, **cfg)
        results = retriever(query=query, filters=filters)
        if isinstance(results, list):
            all_results.extend(results)
    return all_results


def _print_results(results: List[Dict[str, Any]], as_json: bool) -> None:
    if as_json:
        print(json.dumps(results, ensure_ascii=False, indent=2))
        return

    if not results:
        print('No results found.')
        return

    print(f'Retrieved {len(results)} result(s):\n')
    for i, node in enumerate(results, 1):
        content = node.get('content', node.get('text', ''))
        group = node.get('group', '')
        score = node.get('score', '')
        uid = node.get('uid', node.get('id', ''))

        header_parts = [f'[{i}]']
        if score != '':
            header_parts.append(f'score={score}')
        if group:
            header_parts.append(f'group={group}')
        if uid:
            header_parts.append(f'id={uid}')
        print(' '.join(header_parts))

        preview = content[:500] if len(content) > 500 else content
        print(preview)
        if len(content) > 500:
            print(f'  ... ({len(content)} chars total)')
        print()


def _resolve_embed_keys(raw_value: Optional[str]) -> Optional[List[str]]:
    if not raw_value:
        return None
    return [key.strip() for key in raw_value.split(',') if key.strip()]


def _build_filters(dataset_id: str) -> Dict[str, str]:
    return {'kb_id': dataset_id}


def _run_local_retrieve(args: argparse.Namespace) -> List[Dict[str, Any]]:
    _ensure_lazyllm()

    url = resolve_algo_url(args.url)
    algo_dataset = resolve_algo_dataset(args.algo_dataset)
    dataset_id = resolve_dataset(args.dataset)
    document = _build_document(url, algo_dataset)
    filters = _build_filters(dataset_id)

    if args.config:
        return _run_config_retrievers(document, args.query, filters, args.config)
    return _run_single_retriever(
        document,
        query=args.query,
        filters=filters,
        group_name=args.group_name,
        topk=args.topk,
        similarity=args.similarity,
        embed_keys=_resolve_embed_keys(args.embed_keys),
    )


def _run_docker_retrieve(
    container: str, args: argparse.Namespace,
) -> List[Dict[str, Any]]:
    dataset_id = resolve_dataset(args.dataset)
    # Inside the lazyllm-algo container we talk to the algo service on
    # localhost.  Allow override so non-default deployments can point at a
    # different port or host without touching code.
    container_url = os.getenv(
        'LAZYMIND_ALGO_CONTAINER_URL', 'http://127.0.0.1:8000',
    )
    payload = {
        'url': container_url,
        'algo_dataset': resolve_algo_dataset(args.algo_dataset),
        'filters': _build_filters(dataset_id),
        'query': args.query,
        'group_name': args.group_name,
        'topk': args.topk,
        'similarity': args.similarity,
        'embed_keys': _resolve_embed_keys(args.embed_keys),
        'config_mode': bool(args.config),
    }
    result = subprocess.run(
        ['docker', 'exec', '-i', container, 'python', '-c', DOCKER_RETRIEVE_SCRIPT],
        input=json.dumps(payload, ensure_ascii=False).encode('utf-8'),
        capture_output=True,
        check=False,
    )
    if result.returncode != 0:
        stderr = result.stderr.decode('utf-8', errors='replace').strip()
        message = stderr or f'docker exec failed with exit code {result.returncode}'
        raise RuntimeError(f'Failed to run retrieve in {container}: {message}')
    stdout = result.stdout.decode('utf-8', errors='replace').strip()
    if not stdout:
        return []
    # lazyllm's boot-time logging can leak onto stdout; the real JSON is the
    # last non-empty line, so isolate it before parsing to tolerate that.
    last_line = stdout.splitlines()[-1].strip()
    try:
        data = json.loads(last_line)
    except json.JSONDecodeError as exc:
        raise RuntimeError(
            f'Failed to parse retrieve output from {container}: {exc}; '
            f'stdout tail: {last_line[:200]!r}',
        ) from exc
    if not isinstance(data, list):
        raise RuntimeError(f'Unexpected retrieve result type: {type(data).__name__}')
    return data


def _resolve_execution_mode(args: argparse.Namespace) -> Optional[str]:
    """Return container name for docker mode, or None for local mode.

    Collapsed into a single pass so we only issue one `docker ps` per
    invocation and avoid a TOCTOU race where the container vanishes between
    the two original lookups.
    """
    if args.url:
        return None
    if get_context('algo_url') or os.getenv('LAZYMIND_ALGO_SERVICE_URL'):
        return None
    return _find_local_algo_container()


def cmd_retrieve(args: argparse.Namespace) -> int:
    container = _resolve_execution_mode(args)
    if container:
        results = _run_docker_retrieve(container, args)
    else:
        results = _run_local_retrieve(args)

    _print_results(results, args.as_json)
    return 0
