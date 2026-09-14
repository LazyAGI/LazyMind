"""Decide one batch or audit one frozen member page."""
from __future__ import annotations

import hashlib
import json
import re

from . import grouping as engine


def _validate_response(response, cards, items, allow_create, preserve_existing=False):
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
    counts = {card['short_id']: card.get('count', 0) for card in cards if card['kind'] == 'candidate'}
    aliases = {candidate: candidate for candidate in candidates}

    def target(candidate):
        seen = set()
        while candidate in aliases and aliases[candidate] != candidate:
            if candidate in seen:
                raise ValueError('cyclic candidate target')
            seen.add(candidate)
            candidate = aliases[candidate]
        return candidate

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
            counts[op['id']] = 0
            aliases[op['id']] = op['id']
        elif preserve_existing:
            raise ValueError('existing candidates are read-only for this batch')
        elif op['op'] == 'merge':
            if (not isinstance(op['source_ids'], list)
                    or not 2 <= len(op['source_ids']) <= 5
                    or any(not isinstance(x, str) or x not in candidates for x in op['source_ids'])
                    or op['target_id'] not in candidates):
                raise ValueError('unknown or formal candidate target')
            sources = [target(source) for source in op['source_ids']]
            merge_target = target(op['target_id'])
            if (len(sources) != len(set(sources)) or merge_target not in sources
                    or any(counts[source] > 5 for source in sources)):
                raise ValueError('candidate is not mergeable')
            counts[merge_target] = sum(counts[source] for source in sources)
            for source in sources:
                aliases[source] = merge_target
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
        raise engine.ConversationCallError('model_config_changed')
    items = [engine.Conversation.model_validate(item) for item in data['conversations']]
    if len(items) > engine.MAX_BATCH_SIZE or len({x.id for x in items}) != len(items):
        raise engine.ConversationCallError('invalid_task_config')
    calls = 0
    if data['phase'] == 'audit':
        ids = [item.id for item in items]
        scope_change = data.get('scope_change')
        if not isinstance(scope_change, dict):
            raise engine.ConversationCallError('invalid_task_config')
        for attempt in range(3):
            try:
                verdict = engine._model_json(request, {
                    'mode': 'scope_audit', 'scope': data['scope'],
                    'scope_change': scope_change,
                    'instruction': (
                        '检查新scope是否覆盖全部旧任务，并判断原候选组、本批新成员是否存在稳定、可检索的共同业务场景。'
                        '新scope必须是最小且有意义的共同上位场景，不能机械拼接两个边界、使用简单或关系或宽泛兜底分类。'
                        '完整划分keep/reject，不遗漏或重复；只返回限定reason。'),
                    'items': [item.model_dump() for item in items],
                    'output_schema': {'keep': ids, 'reject': [], 'reason': 'accepted'},
                }, call=call)
                keep, reject = verdict.get('keep'), verdict.get('reject')
                reason = verdict.get('reason')
                reasons = {'accepted', 'coverage_gap', 'no_shared_scenario', 'boundary_too_broad'}
                if (set(verdict) != {'keep', 'reject', 'reason'}
                        or not isinstance(keep, list) or not isinstance(reject, list)
                        or any(not isinstance(x, str) for x in keep + reject)
                        or len(keep + reject) != len(set(keep + reject)) or set(keep + reject) != set(ids)):
                    raise ValueError('scope audit must partition every member')
                if (reason not in reasons or (reason == 'accepted' and reject)
                        or (reason == 'coverage_gap' and not reject)):
                    raise ValueError('invalid scope audit reason')
                return {'identity': identity, 'accepted': reason == 'accepted',
                        'audit_reason': reason, 'rejected_ids': reject,
                        'processed': len(items)}, engine._usage(1)
            except Exception as exc:
                if attempt == 2 or engine.call_error(exc).code != 'invalid_output':
                    raise
    cards = data['directory']
    preserve_existing = data.get('preserve_existing_candidates') is True
    scope_repair = data.get('scope_repair')

    def repair_instruction(retrying):
        instructions = []
        if preserve_existing:
            instructions.append(
                '已有候选组保持只读，本批只能复用已有组、create新候选或选择free；'
                '禁止rename、update、merge。' + (
                    '参考scope_repair证据避免重复已被拒绝的方案。' if scope_repair is not None else ''))
        elif scope_repair is not None:
            instructions.append(
                '依据审核证据寻找原组与新成员的真实共同业务场景并重写准确边界；'
                '不能机械拼接边界或扩大成兜底类别。证据中的ID仅用于说明上次方案，'
                '重新输出时只引用当前目录ID；找不到真实共性时取消对应update/merge。')
        if retrying:
            instructions.append('上一次输出未通过校验，请重新生成合法结果。')
        return '\n'.join(instructions)
    # Internal cards are projected through a strict model-facing allowlist.
    last = None
    for repair in range(3):
        try:
            shards = [cards[i:i + engine.MAX_BATCH_SIZE] for i in range(0, len(cards), engine.MAX_BATCH_SIZE)] or [[]]
            responses = []
            for shard in shards:
                payload = {'mode': 'organize' if len(shards) == 1 else 'directory_scan',
                           **engine._model_directory(shard), 'conversations': [item.model_dump() for item in items]}
                if scope_repair is not None:
                    payload['scope_repair'] = scope_repair
                if preserve_existing:
                    payload['preserve_existing_candidates'] = True
                instruction = repair_instruction(bool(data.get('repair') or repair))
                if instruction:
                    payload['repair_instruction'] = instruction
                if len(shards) > 1:
                    payload['instruction'] = '只扫描本目录分块，不得create；最终裁决综合所有分块后统一创建候选。'
                response = engine._model_json(request, payload, call=call)
                calls += 1
                _validate_response(response, shard, items, len(shards) == 1, preserve_existing)
                responses.append(response)
            while len(responses) > 1:
                reduced = []
                for offset in range(0, len(responses), 2):
                    pair = responses[offset:offset + 2]
                    if len(pair) == 1:
                        reduced.append(pair[0])
                        continue
                    reduction = {
                        'mode': 'compare_directory_shards',
                        **engine._model_directory(engine._referenced_cards(cards, pair)),
                        'conversations': [item.model_dump() for item in items], 'shard_proposals': pair,
                        'instruction': '综合分块，保留准确匹配，完整输出本批assignments。' + (
                            '本次是最终归并，可以统一创建候选。' if len(responses) == 2 else '本次不是最终归并，不得create。'),
                    }
                    if scope_repair is not None:
                        reduction['scope_repair'] = scope_repair
                    if preserve_existing:
                        reduction['preserve_existing_candidates'] = True
                        reduction['instruction'] += '已有候选组只读，禁止rename、update、merge。'
                    instruction = repair_instruction(bool(data.get('repair') or repair))
                    if instruction:
                        reduction['repair_instruction'] = instruction
                    reduced.append(engine._model_json(request, reduction, call=call))
                    calls += 1
                    _validate_response(reduced[-1], engine._referenced_cards(cards, pair), items,
                                       len(responses) == 2, preserve_existing)
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
