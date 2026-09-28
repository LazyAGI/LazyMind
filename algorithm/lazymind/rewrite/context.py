from __future__ import annotations

from bisect import bisect_left
from collections import defaultdict
from concurrent.futures import as_completed
from typing import Any, Callable

import lazyllm
from lazyllm import ThreadPoolExecutor
from lazyllm.tools.writer.data_models.context import WritingContext

from lazymind.chat.engine.agent_runtime.budget import resolve_max_input_tokens
from lazymind.common.token_estimation import estimate_tokens

from .base import BadRequestError

DEFAULT_REWRITE_INPUT_TOKENS = 12000


def run_parallel_requests(requests: list[Any], worker: Callable[[Any], object]) -> list[object]:
    if len(requests) == 1:
        return [worker(requests[0])]
    results: list[object | None] = [None] * len(requests)
    with ThreadPoolExecutor(max_workers=min(8, len(requests))) as executor:
        futures = {
            executor.submit(worker, request): index
            for index, request in enumerate(requests)
        }
        try:
            for future in as_completed(futures):
                results[futures[future]] = future.result()
        except Exception:
            for future in futures:
                future.cancel()
            raise
    return results


def input_budget(llm_config: dict | None = None) -> int:
    if llm_config is None:
        from lazymind.model_config import _role_entry, load_model_config

        injected = lazyllm.globals['config'].get('dynamic_model_configs', {})
        role = injected.get('llm', {}).get('chat')
        llm_config = {'llm': role or _role_entry(load_model_config().get('llm'))}
    return min(DEFAULT_REWRITE_INPUT_TOKENS, resolve_max_input_tokens(llm_config=llm_config) // 4)


def matching_context(document, contexts: list[dict]) -> dict:
    document_id = document.get('document_id') if isinstance(document, dict) else getattr(document, 'document_id', None)
    if not document_id:
        return {}
    for context in contexts:
        if context.get('doc_id') == document_id:
            return WritingContext.model_validate(context).model_dump(exclude_none=True)
    return {}


def build_requests(document, title: str, blocks: list[dict], targets: list[dict], instruction: str,
                   render: Callable[[dict], str], *, context: dict | None = None,
                   llm_config: dict | None = None, system_prompt: str = '') -> list[dict]:
    if not instruction.strip() or not targets:
        raise BadRequestError('instruction and rewrite targets must not be empty')
    budget = input_budget(llm_config)
    overhead = estimate_tokens(system_prompt)

    def fits(payload):
        return overhead + estimate_tokens(render(payload)) <= budget

    def payload_for(group):
        return {'title': title, 'instruction': instruction, 'paragraphs': group,
                'read_only_context': {'blocks': [], 'block_summaries': []}}

    full = {'instruction': instruction, 'paragraphs': targets, 'read_only_document': document}
    if fits(full):
        return [full]

    groups, current = [], []
    for target in targets:
        if not fits(payload_for([target])):
            raise BadRequestError(f'rewrite target {target["id"]} exceeds the input token budget ({budget})')
        if current and not fits(payload_for([*current, target])):
            groups.append(current)
            current = []
        current.append(target)
    groups.append(current)

    block_index = {block['ref']: index for index, block in enumerate(blocks)}
    all_targets = {target['ref'] for target in targets}
    summaries = (context or {}).get('block_summaries', [])
    requests = []
    for group in groups:
        payload = payload_for(group)
        selected_sections = defaultdict(list)
        for target in group:
            index = block_index[target['ref']]
            selected_sections[blocks[index]['section']].append(index)
        neighbors = []
        for index, block in enumerate(blocks):
            positions = selected_sections.get(block['section'])
            if not positions or block['ref'] in all_targets or block['type'] == 'heading':
                continue
            at = bisect_left(positions, index)
            distance = min(abs(index - position) for position in positions[max(0, at - 1):at + 1])
            neighbors.append((distance, index, block))
        neighbors.sort(key=lambda item: (item[0], item[1]))
        candidates = []
        for distance, _, block in neighbors:
            public = {key: value for key, value in block.items() if key != 'section'}
            candidates.append((0 if distance == 1 else 3, 'blocks', public, block['ref']))

        section_refs = {
            (tuple(block['heading_path']), block.get('occurrence', 1))
            for block in blocks if block['section'] in selected_sections
        }
        scoped_refs = {block['ref'] for block in blocks if block['section'] in selected_sections}
        seen = set(all_targets)
        for summary in summaries:
            ref = summary['content_ref']
            identity = ref.get('node_id') or (tuple(ref.get('heading_path', [])), ref.get('occurrence', 1))
            related = (ref.get('node_id') in scoped_refs if ref.get('node_id') else identity in section_refs)
            if related and identity not in seen:
                seen.add(identity)
                candidates.append((1, 'block_summaries', {
                    'content_ref': ref, 'summary': summary['summary'], 'kind': 'excerpt',
                }, identity))
        document_summary = (context or {}).get('document_summary')
        if document_summary and document_summary.get('summary'):
            candidates.append((2, 'document_summary', {
                'summary': document_summary['summary'], 'kind': (context or {}).get('meta', {}).get(
                    'document_summary_kind', 'excerpt'),
            }, ('document_summary',)))

        admitted = set(all_targets)
        for _, key, value, identity in sorted(candidates, key=lambda item: item[0]):
            if identity in admitted:
                continue
            readonly = payload['read_only_context']
            if key == 'document_summary':
                readonly[key] = value
            else:
                readonly[key].append(value)
            if not fits(payload):
                if key == 'document_summary':
                    del readonly[key]
                else:
                    readonly[key].pop()
            else:
                admitted.add(identity)
        requests.append(payload)
    return requests
