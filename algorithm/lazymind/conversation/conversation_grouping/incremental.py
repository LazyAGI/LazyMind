"""Decide one batch or audit one frozen member page."""
from __future__ import annotations

import hashlib
import json
import re

from . import grouping as engine


def _validate_response(response, cards, items, allow_create):
    if set(response) != {'candidate_operations', 'assignments'}:
        raise ValueError('invalid response fields')
    assignments = response['assignments']
    operations = response['candidate_operations']
    if (not isinstance(operations, list) or not isinstance(assignments, list)
            or any(not isinstance(x, dict) or set(x) != {'id', 'group_id'}
                   or not all(isinstance(v, str) for v in x.values()) for x in assignments)
            or len(assignments) != len(items) or {x['id'] for x in assignments} != {x.id for x in items}):
        raise ValueError('every batch item must be assigned once')
    known = {card['short_id'] for card in cards}
    candidates = {card['short_id'] for card in cards if card['kind'] == 'candidate'}
    fields = {'create': {'op', 'id', 'name', 'scope'}, 'rename': {'op', 'id', 'name'},
              'update': {'op', 'id', 'scope'}, 'merge': {'op', 'source_ids', 'target_id', 'name', 'scope'}}
    for op in operations:
        if (not isinstance(op, dict) or not isinstance(op.get('op'), str)
                or set(op) != fields.get(op['op'], set())
                or any(not isinstance(v, str) for k, v in op.items() if k != 'source_ids')):
            raise ValueError('invalid candidate operation')
        if op['op'] == 'create':
            if (not allow_create or not isinstance(op['id'], str)
                    or not re.fullmatch(r'new_[1-9][0-9]*', op['id']) or op['id'] in known):
                raise ValueError('invalid temporary candidate ID')
            known.add(op['id'])
            candidates.add(op['id'])
        elif op['op'] == 'merge':
            if (not isinstance(op['source_ids'], list)
                    or any(not isinstance(x, str) or x not in candidates for x in op['source_ids'])
                    or op['target_id'] not in candidates):
                raise ValueError('unknown or formal candidate target')
        elif op['id'] not in candidates:
            raise ValueError('unknown or formal candidate target')
    if any(x['group_id'] not in known | {'free'} for x in assignments):
        raise ValueError('unknown group reference')


def organize(request, call=None):
    data = request.input
    identity = hashlib.sha256(json.dumps([
        engine.SYSTEM_PROMPT, data['snapshot_hash'],
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
    # Internal cards are projected through a strict model-facing allowlist.
    last = None
    for repair in range(3):
        try:
            shards = [cards[i:i + engine.MAX_BATCH_SIZE] for i in range(0, len(cards), engine.MAX_BATCH_SIZE)] or [[]]
            responses = []
            for shard in shards:
                payload = {'mode': 'organize' if len(shards) == 1 else 'directory_scan',
                           **engine._model_directory(shard), 'conversations': [item.model_dump() for item in items]}
                if data.get('repair') or repair:
                    payload['repair_instruction'] = '上次提案或范围审核失败，重新处理本批；不得将旧成员排除在新scope之外。'
                if len(shards) > 1:
                    payload['instruction'] = '只扫描本目录分块，不得create；最终裁决综合所有分块后统一创建候选。'
                response = engine._model_json(request, payload, call=call)
                calls += 1
                _validate_response(response, shard, items, len(shards) == 1)
                responses.append(response)
            while len(responses) > 1:
                reduced = []
                for offset in range(0, len(responses), 2):
                    pair = responses[offset:offset + 2]
                    if len(pair) == 1:
                        reduced.append(pair[0])
                        continue
                    reduced.append(engine._model_json(request, {
                        'mode': 'compare_directory_shards',
                        **engine._model_directory(engine._referenced_cards(cards, pair)),
                        'conversations': [item.model_dump() for item in items], 'shard_proposals': pair,
                        'instruction': '综合分块，保留准确匹配，完整输出本批assignments。' + (
                            '本次是最终归并，可以统一创建候选。' if len(responses) == 2 else '本次不是最终归并，不得create。'),
                    }, call=call))
                    calls += 1
                    _validate_response(reduced[-1], engine._referenced_cards(cards, pair), items, len(responses) == 2)
                responses = reduced
            response = responses[0]
            assignments = response['assignments']
            return {'identity': identity, 'operations': response['candidate_operations'],
                    'assignments': assignments, 'processed': len(items)}, engine._usage(calls)
        except Exception as exc:
            last = exc
            if engine.call_error(exc).code != 'invalid_output':
                raise
    raise last
