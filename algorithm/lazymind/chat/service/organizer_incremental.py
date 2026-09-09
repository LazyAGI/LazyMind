"""Version 2: decide one batch or audit one frozen member page."""
from __future__ import annotations

import hashlib
import json
from types import SimpleNamespace

from . import conversation_organizer as engine


def organize(request, call=None):
    data = request.input.data
    if data.get('protocol_version') != 2:
        raise ValueError('unsupported organizer protocol')
    if request.mode != 'llm' or request.tools or request.skills or request.input.files:
        raise ValueError('invalid task config')
    identity = hashlib.sha256(json.dumps([
        2, engine.ALGORITHM_VERSION, engine.SYSTEM_PROMPT, data['snapshot_hash'],
        request.llm_config.get('llm'),
    ], sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    if data.get('identity') and data['identity'] != identity:
        raise ValueError('organizer identity changed')
    items = [engine.Conversation.model_validate(item) for item in data['conversations']]
    if len(items) > engine.MAX_BATCH_SIZE or len({x.id for x in items}) != len(items):
        raise ValueError('invalid batch')
    calls = 0
    if data['phase'] == 'audit':
        ids = [item.id for item in items]
        verdict = engine._model_json(request, {
            'mode': 'scope_audit', 'scope': data['scope'],
            'instruction': '检查每条旧任务是否被新scope覆盖。完整划分keep/reject，不遗漏或重复。',
            'items': [item.model_dump() for item in items],
            'output_schema': {'keep': ids, 'reject': []},
        }, call=call)
        keep, reject = verdict.get('keep'), verdict.get('reject')
        if (not isinstance(keep, list) or not isinstance(reject, list)
                or len(keep + reject) != len(set(keep + reject)) or set(keep + reject) != set(ids)):
            raise ValueError('scope audit must partition every member')
        return {'identity': identity, 'accepted': not reject, 'processed': len(items)}, engine._usage(1)
    cards = data['directory']
    # Directory cards have bounded examples; assignments from earlier batches never enter here.
    wrapper = SimpleNamespace(snapshot=SimpleNamespace(id=data['snapshot_id']))
    state = {'cursor': data['cursor']}
    last = None
    for repair in range(3):
        try:
            shards = [cards[i:i + engine.MAX_BATCH_SIZE] for i in range(0, len(cards), engine.MAX_BATCH_SIZE)] or [[]]
            responses = []
            for shard in shards:
                payload = engine._batch_payload(wrapper, state, items, shard,
                                                'organize' if len(shards) == 1 else 'directory_scan')
                if data.get('repair') or repair:
                    payload['repair_instruction'] = '上次提案或范围审核失败，重新处理本批；不得将旧成员排除在新scope之外。'
                if len(shards) > 1:
                    payload['instruction'] = '只扫描本目录分块，不得create；最终裁决综合所有分块后统一创建候选。'
                response = engine._model_json(request, payload, call=call)
                calls += 1
                if len(shards) > 1 and any(op.get('op') == 'create' for op in response.get('candidate_operations', [])):
                    raise ValueError('directory scans cannot create candidates')
                responses.append(response)
            while len(responses) > 1:
                reduced = []
                for offset in range(0, len(responses), 2):
                    pair = responses[offset:offset + 2]
                    if len(pair) == 1:
                        reduced.append(pair[0])
                        continue
                    reduced.append(engine._model_json(request, {
                        'mode': 'compare_directory_shards', 'groups': engine._referenced_cards(cards, pair),
                        'conversations': [item.model_dump() for item in items], 'shard_proposals': pair,
                        'instruction': '综合分块，保留准确匹配，完整输出本批assignments；仅在最终归并创建候选。',
                    }, call=call))
                    calls += 1
                responses = reduced
            response = responses[0]
            if set(response) != {'candidate_operations', 'assignments'}:
                raise ValueError('invalid response fields')
            assignments = response['assignments']
            if (not isinstance(response['candidate_operations'], list) or not isinstance(assignments, list)
                    or any(not isinstance(x, dict) or set(x) != {'id', 'group_id'} for x in assignments)
                    or len(assignments) != len(items) or {x['id'] for x in assignments} != {x.id for x in items}):
                raise ValueError('every batch item must be assigned once')
            return {'identity': identity, 'operations': response['candidate_operations'],
                    'assignments': assignments, 'processed': len(items)}, engine._usage(calls)
        except Exception as exc:
            last = exc
            if engine._task_call_error(exc).code != 'invalid_output':
                raise
    raise last
