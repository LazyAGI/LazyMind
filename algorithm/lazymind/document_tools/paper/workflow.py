"""Paper Workflow state and Runtime adapter; algorithms use the separate business API."""
from __future__ import annotations

import base64
import copy
import json
import re
import tempfile
from concurrent.futures import CancelledError
from pathlib import Path
from typing import Any

from lazyllm import LOG

from ..artifacts import markdown_filename, persist_artifact_json
from . import contracts, reading_reflection, research_topic
from .api import run_algorithm
from .contracts import MissingMaterials
from .entry import MAX_MANUSCRIPT_CHARS, read_manuscript
from .review.schema import text_digest
from .analysis import trace as diagnostics

SCHEMA = 'paper-material-pool.v2'
ROUTES = {
    'F1': ['F2'], 'F2': ['F1', 'F3', 'F5', 'F7'], 'F3': ['F4'],
    'F4': ['F3', 'F5'], 'F5': ['F6'], 'F6': ['F5'], 'F7': [],
}
STEP_NODE = {
    'research_topic': 'F1', 'literature_review': 'F2', 'experiment': 'F3', 'data_analysis': 'F4', 'write_document': 'F5',
    'peer_review': 'F6', 'reading_reflection': 'F7', 'refine_topic': 'F1',
    'revise_document': 'F5', 'write_outline': 'F5',
}
STEP_LABELS = {
    'research_topic': '研究选题', 'literature_review': '文献综述', 'experiment': '实验研究', 'data_analysis': '数据分析',
    'write_outline': '论文大纲', 'write_document': '论文写作', 'peer_review': '模拟审稿', 'reading_reflection': '阅读心得',
    'refine_topic': '调整选题', 'revise_document': '论文修订', 'prepare': '材料准备', 'finish': '结束说明',
}
KINDS = contracts.KINDS


def validate_pool(pool: Any) -> dict:
    if not isinstance(pool, dict) or pool.get('schema') != SCHEMA:
        raise ValueError(f'材料池 schema 必须为 {SCHEMA}')
    if not isinstance(pool.get('topic'), str) or not pool['topic'].strip():
        raise ValueError('缺少研究主题 topic')
    if type(pool.get('sequence')) is not int or pool['sequence'] < 0:
        raise ValueError('sequence 必须是非负整数')
    if not isinstance(pool.get('materials'), list) or not isinstance(pool.get('current'), dict):
        raise ValueError('materials 必须是列表，current 必须是材料引用表')
    if not isinstance(pool.get('history'), list):
        raise ValueError('history 必须是列表')
    contracts.normalize_constraints(pool.get('constraints', {}))
    selected = pool.get('selected_issue_ids', [])
    if not isinstance(selected, list) or any(not isinstance(i, str) or not i.strip() for i in selected):
        raise ValueError('selected_issue_ids 必须是 ID 数组')
    contracts.unique(selected, 'selected_issue_ids')
    indexed = {}
    for item in pool['materials']:
        if not isinstance(item, dict) or item.get('kind') not in KINDS:
            raise ValueError('未知材料类型')
        if not isinstance(item.get('id'), str) or not item['id']:
            raise ValueError('材料缺少 id')
        if type(item.get('version')) is not int or item['version'] < 1:
            raise ValueError('材料版本必须为正整数')
        key = (item['id'], item['version'])
        if key in indexed:
            raise ValueError('材料 id/version 重复')
        if 'content' not in item:
            raise ValueError('材料缺少 content')
        if item['kind'] == 'analysis' and isinstance(item['content'], dict):
            from .analysis.schema import VERSION, validate_analysis
            if item.get('contract') != VERSION:
                raise ValueError('结构化分析缺少匹配的契约版本，旧结果需要原始材料重新分析')
            validate_analysis(item['content'])
        if not isinstance(item.get('source_refs'), list) or not all(
            isinstance(ref, str) and ref for ref in item['source_refs']
        ):
            raise ValueError('source_refs 必须是来源标识列表')
        if not isinstance(item.get('derived_from'), list) or type(item.get('mock')) is not bool:
            raise ValueError('材料必须声明布尔 mock 和 derived_from')
        indexed[key] = item

    def resolve(ref):
        if not isinstance(ref, dict) or not isinstance(ref.get('id'), str) or type(ref.get('version')) is not int:
            raise ValueError('材料引用必须包含 id 和整数 version')
        key = (ref['id'], ref['version'])
        if key not in indexed:
            raise ValueError(f'材料引用不存在: {key}')
        return indexed[key]

    for kind, ref in pool['current'].items():
        if resolve(ref)['kind'] != kind:
            raise ValueError('材料引用类型不匹配')
    for item in pool['materials']:
        for ref in item['derived_from']:
            if resolve(ref) is item:
                raise ValueError('材料不能派生自自身')
    for execution in pool['history']:
        if not isinstance(execution, dict) or execution.get('node') not in ROUTES:
            raise ValueError('执行记录节点无效')
        if not all(isinstance(execution.get(key), list) for key in ('inputs', 'outputs')):
            raise ValueError('执行记录 inputs/outputs 必须是引用列表')
        for ref in execution.get('inputs', []) + execution.get('outputs', []):
            resolve(ref)
    if pool['sequence'] != len(pool['history']):
        raise ValueError('sequence 与执行历史长度不一致')
    return pool


def material(pool: dict, kind: str) -> dict | None:
    ref = pool['current'].get(kind)
    if ref is None:
        return None
    return next(item for item in pool['materials']
                if item['id'] == ref['id'] and item['version'] == ref['version'])


def add_material(pool: dict, kind: str, content: Any, derived: list[dict], sources: list[str],
                 *, is_demo: bool = False) -> dict:
    identifier = pool['current'].get(kind, {}).get('id', f'material-{kind}')
    version = 1 + max((item['version'] for item in pool['materials'] if item['id'] == identifier), default=0)
    ref = {'id': identifier, 'version': version}
    pool['materials'].append({
        **ref, 'kind': kind, 'title': kind, 'mock': is_demo,
        'content': content, 'source_refs': sorted(set(sources)), 'derived_from': copy.deepcopy(derived),
    })
    if kind == 'analysis' and isinstance(content, dict):
        from .analysis.schema import VERSION
        pool['materials'][-1]['contract'] = VERSION
    pool['current'][kind] = ref
    return ref


def seed_pool() -> dict:
    pool = {'schema': SCHEMA, 'topic': '检索增强生成的证据可追溯性', 'sequence': 0,
            'materials': [], 'current': {}, 'history': []}
    # Demonstration sources retain explicit coverage and provenance.
    add_material(pool, 'papers', [
        {'id': 'ref-demo-1', 'title': '[示例] 检索证据组织方法', 'text': '示例论文讨论检索与引用对齐，不是真实论文。',
         'url': None, 'authors': [], 'year': None, 'doi': None, 'content_type': 'excerpt',
         'source': 'demo', 'truncated': False},
        {'id': 'ref-demo-2', 'title': '[示例] 生成答案的来源检查', 'text': '示例论文讨论主张与来源核验，不是真实论文。',
         'url': None, 'authors': [], 'year': None, 'doi': None, 'content_type': 'excerpt',
         'source': 'demo', 'truncated': False},
    ], [], ['ref-demo-1', 'ref-demo-2'], is_demo=True)
    add_material(pool, 'data', {'columns': ['method', 'score'],
                                'rows': [['baseline', 60], ['evidence', 75]]}, [], [], is_demo=True)
    add_material(pool, 'question', '比较不同证据组织方法对引用可追溯性的影响（演示问题）。',
                 [], [], is_demo=True)
    add_material(pool, 'manuscript', '# [示例] 已有论文\n\n## 方法\n待完善证据定位。\n\n## 局限\n仅为流程测试。', [], [
                 'ref-demo-1'], is_demo=True)
    return validate_pool(pool)


def business_input(step: str, pool: dict, request: str = '') -> dict:
    module = contracts.ALIASES.get(step, step)
    value = {'topic': pool['topic'], 'request': request}
    if module in contracts.CONSTRAINED_MODULES:
        value['constraints'] = copy.deepcopy(pool.get('constraints', {}))
    if step in {'peer_review', 'revise_document'}:
        # New research materials may inform the existing manuscript. Review freshness
        # is checked against manuscript/report digests by the revision contract.
        from .publication import validate_chart_references, figure_catalog
        manuscript, charts = material(pool, 'manuscript'), material(pool, 'charts')
        if manuscript:
            media = material(pool, 'media_assets')
            validate_chart_references(manuscript['content'], figure_catalog(
                charts['content'] if charts else [], media['content'] if media else None))
            from .publication import validate_citations
            papers = material(pool, 'papers')
            validate_citations(manuscript['content'], papers['content'] if papers else [], published=True)
    for kind in contracts.FIELDS[module]:
        if kind == 'selected_issue_ids':
            value[kind] = copy.deepcopy(pool.get(kind, [])) if step == 'revise_document' else []
            continue
        if kind in {'review', 'review_data'} and (step in {'write_outline', 'write_document'}
                                                  or (step == 'revise_document' and not pool.get('selected_issue_ids'))):
            value[kind] = None
            continue
        item = material(pool, kind)
        if item:
            value[kind] = copy.deepcopy(item['content'])
    if module == 'experiment' and any(material(pool, key) and material(pool, key)['mock']
                                      for key in ('data', 'source_data', 'papers')):
        value['input_is_demo'] = True
    experiment = material(pool, 'experiment')
    data = material(pool, 'data')
    if module == 'experiment' and not data and not material(pool, 'source_data') and experiment:
        meta = material(pool, 'result_meta')
        if meta and meta['content']['data_origin'] in {'observed', 'user_supplied'}:
            from .experiment.handoff import to_analysis
            value['data'] = to_analysis({'topic': pool['topic'], 'experiment': experiment['content'],
                                         'result_meta': meta['content']})['data']
    if (module == 'data_analysis' and experiment and any(h['node'] == 'F3' for h in pool['history'])
            and (data is None or pool['materials'].index(data) < pool['materials'].index(experiment))):
        # F3's input dataset is not its output. Never silently analyse the old
        # dataset when the experiment produced no result.
        value.pop('data', None)
    if module == 'data_analysis' and value.get('result_meta') is not None:
        from .experiment.handoff import to_analysis
        value = to_analysis(value)
    return value


def paper_source_ids(papers):
    """Project available provenance without requiring or validating Paper fields."""
    if not isinstance(papers, list):
        return []
    return [paper['id'] for paper in papers if isinstance(paper, dict)
            and isinstance(paper.get('id'), str) and paper['id']]


def pool_from_business(value: dict) -> dict:
    """The entry adapter accepts business values, not a user-authored pool."""
    if not isinstance(value, dict):
        raise ValueError('入口材料必须是 JSON 对象')
    if value.get('schema') == SCHEMA:
        return validate_pool(copy.deepcopy(value))  # Existing pinned integrations.
    export_fields = {'execution_preference', 'execution_available', 'notice'}
    if {'experiment', 'result_meta'} <= value.keys() and export_fields & value.keys():
        # Download metadata is not business data or permission to execute code.
        value = {key: copy.deepcopy(item) for key, item in value.items() if key not in export_fields}
        value.setdefault('topic', '实验结果分析')
    if set(value) - {'topic', 'request', 'demo', 'constraints', 'selected_issue_ids', *KINDS}:
        raise ValueError('入口材料含未知字段')
    if type(value.get('demo', False)) is not bool:
        raise ValueError('demo 必须是布尔值')
    if value.get('demo'):
        if set(value) - {'topic', 'demo'}:
            raise ValueError('演示材料不能与真实材料混用')
        pool = seed_pool()
        if 'topic' in value:
            contracts.text(value['topic'], 'topic')
            pool['topic'] = value['topic']
        return pool
    contracts.text(value.get('topic'), 'topic')
    if not isinstance(value.get('request', ''), str):
        raise ValueError('request 必须是字符串')
    from .writing.requirements import resolve_constraints
    pool = {'schema': SCHEMA, 'topic': value['topic'], 'request': value.get('request', ''),
            'constraints': resolve_constraints(value.get('constraints', {}), value.get('request', '')),
            'requirements_version': 2,
            'selected_issue_ids': copy.deepcopy(value.get('selected_issue_ids', [])),
            'sequence': 0, 'materials': [], 'current': {}, 'history': []}
    value = copy.deepcopy(value)
    if 'papers' in value:
        value['papers'] = contracts.normalize_papers(value['papers'])
        contracts.validate_value('papers', value['papers'])
    paper_ids = paper_source_ids(value.get('papers'))
    meta = value.get('result_meta')
    if meta is not None:
        if value.get('experiment') is not None:
            contracts.validate_value('experiment', value['experiment'])
        contracts.validate_result_meta(meta, value.get('experiment'), value.get('data'))
    for kind in sorted(KINDS):
        if kind in value and value[kind] is not None:
            contracts.validate_value(kind, value[kind])
            add_material(pool, kind, value[kind], [], paper_ids,
                         is_demo=bool(meta and meta['data_origin'] == 'synthetic'
                                      and kind in ('experiment', 'data', 'result_meta')))
    return validate_pool(pool)


def _conversation_papers(files_by_turn: dict | None) -> list[dict]:
    """Resolve paper attachments through the existing conversation text resolver."""
    paths = []
    for turn in sorted(files_by_turn or {}, key=int):
        for raw_path in files_by_turn[turn] or []:
            path = str(raw_path).split('?', 1)[0]
            if Path(path).suffix.lower() in {
                '.pdf', '.doc', '.docx', '.pptx', '.txt', '.md', '.markdown', '.tex', '.rst', '.html', '.htm',
                '.csv', '.tsv', '.json',
            }:
                if not any(existing == path for _, existing in paths):
                    paths.append((int(turn), path))
    if not paths:
        return []

    from lazymind.chat.engine.tools.file_resources.ingest import ingest_pdf_file
    from lazymind.chat.engine.tools.file_resources.resolver import resolve_text_target
    from lazymind.chat.engine.tools.file_resources.store import FileResourceStore, workspace_for_request

    store = FileResourceStore(workspace_for_request())
    papers = []
    known_ids = set()
    for turn, path in paths:
        if Path(path).suffix.lower() in {'.csv', '.tsv', '.json'}:
            from .source_materials import read_tabular
            papers.append(read_tabular(path))
            continue
        try:
            if Path(path).suffix.lower() == '.pdf':
                manifest = ingest_pdf_file(path, turn_seq=turn, store=store)
                if manifest.get('parse_status') != 'ready':
                    raise ValueError('PDF is not ready')
                file_id = manifest['file_id']
                if file_id in known_ids:
                    continue
                resolved = resolve_text_target(file_id, resources_only=True)
                if resolved.kind != 'file_resource' or resolved.file_id != file_id:
                    raise ValueError('not a parsed PDF')
            else:
                resolved = resolve_text_target(path, turn=turn, resources_only=True)
            text = Path(resolved.path).read_text(encoding='utf-8').strip()
        except (OSError, ValueError):
            raise MissingMaterials(f'论文附件 {Path(path).name} 无法读取或尚未解析完成，请检查后重试。') from None
        if not text:
            raise MissingMaterials(f'论文附件 {Path(path).name} 没有可用的解析正文，请重新上传。')
        if re.search(r'\[Attachment truncated after \d+ characters\.\]', text):
            raise MissingMaterials(f'论文附件 {Path(path).name} 的解析正文被截断，请提供完整材料。')
        file_id = resolved.file_id or f'upload-{text_digest(text)}'
        if file_id in known_ids:
            continue
        known_ids.add(file_id)
        papers.append({'id': file_id, 'title': Path(resolved.display_name).stem or file_id, 'text': text,
                       'attachment_key': text_digest(path)})
    return papers


def preparation_document(pool: dict) -> str:
    # Compatibility for already pinned legacy sessions; new workflows have no prepare node.
    names = {'papers': '论文', 'data': '数据表', 'question': '研究选题', 'evidence': '文献综述',
             'experiment': '实验结果', 'analysis': '分析结论', 'chart': '图表',
             'charts': '图表目录', 'outline': '大纲', 'manuscript': '稿件', 'review': '审稿意见', 'review_data': '结构化审稿意见',
             'reflection': '阅读心得'}
    names.update(data_context='数据说明', study_design='观测设计事实')
    lines = ['# 材料准备', '', '## 研究主题', pool['topic'], '', '## 已接收材料']
    for kind in pool['current']:
        item = material(pool, kind)
        detail = f"（{len(item['content'])} 篇）" if kind == 'papers' else ''
        lines.append(f'- {names.get(kind, kind)}{detail}')
    if not pool['current']:
        lines.append('尚未提供论文、数据或稿件；进入后续功能时将检查必要材料。')
    if any(material(pool, kind)['mock'] for kind in pool['current']):
        lines.extend(['', '> 演示数据，仅用于流程测试，不是真实研究结果。'])
    lines.extend(['', '请按目标选择下一步；无需依次完成所有阶段。'])
    return '\n'.join(lines)


def render_material(kind: str, content: Any) -> str:
    """Project structured business data without replacing its canonical value."""
    if isinstance(content, str):
        return content
    if kind == 'experiment':
        return content['report']
    if kind == 'review':
        lines = ['# 模拟审稿', '', content['summary']]
        for issue in content['issues']:
            lines.extend(['', '## ' + issue['id'], issue['problem'], '', issue['suggestion']])
        if content['limitations']:
            lines.extend(['', '## 审稿局限', *['- ' + item for item in content['limitations']]])
        return '\n'.join(lines)
    return '# ' + kind + '\n\n```json\n' + json.dumps(content, ensure_ascii=False, indent=2) + '\n```'


def with_source_notice(document: str, is_demo: bool, provenance: dict | None = None) -> str:
    """Publish and restore documents with the same provenance decoration."""
    if not is_demo:
        return document
    if provenance:
        from .experiment.handoff import source_notice
        notice = '> ' + source_notice(provenance)
    else:
        notice = '> 演示数据，仅用于流程测试，不是真实研究结果。'
    if notice not in document:
        first, _, rest = document.partition('\n')
        return first + '\n\n' + notice + '\n' + rest
    return document


def routes_after(pool: dict) -> list[str]:
    """Refinement can continue to experiments; initial topic selection still requires literature."""
    history = pool['history']
    if not history:
        return []
    last = history[-1]
    refined = last.get('step') == 'refine_topic' or (
        'step' not in last and len(history) >= 2
        and history[-2]['node'] == 'F2' and last['node'] == 'F1')
    return ['F2', 'F3'] if refined else ROUTES[last['node']]


def execute_step(step: str, pool: dict, request: str = '', fail: bool = False, *,
                 on_event=None, allow_execution=False, skip_revision=False) -> dict:
    """Infrastructure owns lineage and atomic validation; algorithms own content."""
    validate_pool(pool)
    node = STEP_NODE[step]
    redraw_only = False
    if step == 'data_analysis' and material(pool, 'analysis') and request.strip():
        from .data_analysis import redraw_requested
        redraw_only = redraw_requested(request)
    previous = pool['history'][-1]['node'] if pool['history'] else None
    if previous and node != previous and node not in routes_after(pool) and not redraw_only:
        raise ValueError(f'不允许的流程路径: {previous}→{node}')
    if fail:
        raise RuntimeError('[示例] 模拟执行失败；可重试。')
    if step == 'write_outline':
        # Commit one paper-length target with the outline so drafting inherits it.
        pool = copy.deepcopy(pool)
        from .writing.requirements import refresh_legacy_constraints, resolve_constraints
        refresh_legacy_constraints(pool)
        pool['constraints'] = resolve_constraints(
            pool.get('constraints', {}), pool.get('request', ''),
            current_request=request if request != pool.get('request', '') else '',
        )
        if pool['constraints']['word_target'] is None:
            pool['constraints']['word_target'] = 5000 if pool['constraints']['paper_language'] == 'en' else 8000
    module = contracts.ALIASES.get(step, step)
    relevant = contracts.FIELDS[module]
    if step == 'revise_document' and not pool.get('selected_issue_ids'):
        relevant = tuple(kind for kind in relevant if kind not in {'review', 'review_data'})
    pool = copy.deepcopy(pool)
    current_papers = material(pool, 'papers')
    if current_papers and 'papers' in relevant:
        normalized_papers = contracts.normalize_papers(current_papers['content'])
        if normalized_papers != current_papers['content']:
            add_material(pool, 'papers', normalized_papers, [pool['current']['papers']],
                         paper_source_ids(normalized_papers), is_demo=current_papers['mock'])
    if module == 'research_topic':
        contracts.validate_input(step, business_input(step, pool, request))
        pool = copy.deepcopy(pool)
        papers = material(pool, 'papers')
        if papers:
            prepared = research_topic.prepare_papers(papers['content'])
            if prepared != papers['content']:
                add_material(pool, 'papers', prepared, [pool['current']['papers']], papers['source_refs'],
                             is_demo=papers['mock'])
    inputs = [copy.deepcopy(pool['current'][kind]) for kind in relevant if kind in pool['current']]
    execution_records = []
    if module == 'data_analysis':
        from . import data_analysis
        from .analysis.pipeline import capture_execution
        with capture_execution() as execution_records:
            current_analysis = material(pool, 'analysis')
            if redraw_only:
                current_charts = material(pool, 'charts')
                inputs.extend(pool['current'][kind] for kind in ('analysis', 'charts') if kind in pool['current'])
                output = data_analysis.redraw(
                    business_input(step, pool, request), copy.deepcopy(current_analysis['content']),
                    copy.deepcopy(current_charts['content']) if current_charts else [])
            else:
                output = run_algorithm(step, business_input(step, pool, request))
        provenance = material(pool, 'result_meta')
        if provenance:
            from .experiment.handoff import mark_analysis
            output = mark_analysis(output, provenance['content'])
    elif module == 'experiment':
        output = run_algorithm(step, business_input(step, pool, request), on_event=on_event,
                               allow_execution=allow_execution)
    elif module == 'revise_document':
        from .writing.revise_document import retain_manuscript
        optional_inputs = business_input(step, pool, request)
        retained_inputs = {**optional_inputs, **{
            kind: (material(pool, kind) or {}).get('content') for kind in ('review', 'review_data')}}
        if skip_revision:
            output = retain_manuscript(retained_inputs)
        else:
            try:
                output = run_algorithm(step, optional_inputs)
            except CancelledError:
                raise
            except Exception as error:
                if not optional_inputs.get('manuscript') or not optional_inputs.get('selected_issue_ids'):
                    raise
                LOG.warning(f'Optional paper step {module} retained the current manuscript '
                            f'{diagnostics.safe_error(error)}')
                output = retain_manuscript(retained_inputs)
        output = {key: output[key] for key in ('is_demo', *contracts.OUTPUTS[module])}
        contracts.validate_output(module, output, inputs=optional_inputs)
    else:
        output = run_algorithm(step, business_input(step, pool, request), on_event=on_event) \
            if on_event is not None else run_algorithm(step, business_input(step, pool, request))
    is_demo = output['is_demo'] or any(material(pool, k)['mock'] for k in relevant if material(pool, k))
    generated = {key: copy.deepcopy(output[key]) for key in contracts.OUTPUTS[module]}
    if redraw_only and material(pool, 'manuscript'):
        from .publication import figure_catalog, refresh_chart_sources
        media = (material(pool, 'media_assets') or {}).get('content')
        old_catalog = figure_catalog((material(pool, 'charts') or {}).get('content', []), media)
        new_catalog = figure_catalog(generated['charts'], media)
        original = material(pool, 'manuscript')['content']
        updated = refresh_chart_sources(original, old_catalog, new_catalog)
        if generated['charts'] != (material(pool, 'charts') or {}).get('content', []):
            generated.update(review=None, review_data=None)
        if updated != original:
            inputs.append(copy.deepcopy(pool['current']['manuscript']))
            generated['manuscript'] = updated
    main = {'F1': 'question', 'F2': 'evidence', 'F3': 'experiment', 'F4': 'analysis',
            'F5': 'manuscript', 'F6': 'review', 'F7': 'reflection'}[node]
    if step == 'write_outline':
        main = 'outline'
    structured_analysis = main == 'analysis' and isinstance(generated[main], dict)
    if structured_analysis:
        from .analysis.render import render
        document = render(generated[main])
    else:
        document = render_material(main, generated[main])
    provenance = generated.get('result_meta') or (material(pool, 'result_meta') or {}).get('content')
    document = with_source_notice(document, is_demo, provenance)
    # Keep canonical manuscripts unchanged by display notices.
    if main == 'review':
        generated[main] = document
        generated['review_data']['review_sha256'] = text_digest(document)
    sources = {source for kind in relevant if material(pool, kind)
               for source in material(pool, kind)['source_refs']}
    sources.update(paper_source_ids(generated.get('papers')))
    result = copy.deepcopy(pool)
    outputs = []
    for kind, content in generated.items():
        if content is None:
            result['current'].pop(kind, None)
        else:
            outputs.append(add_material(result, kind, content, inputs, sorted(sources), is_demo=is_demo))
    if step in {'peer_review', 'revise_document'} or redraw_only:
        result['selected_issue_ids'] = []
    result['sequence'] += 1
    result['history'].append({'node': node, 'step': step, 'request': request, 'inputs': inputs, 'outputs': outputs})
    if execution_records:
        result['history'][-1]['analysis_execution'] = execution_records
    validate_pool(result)
    return {'status': 'completed', 'pool': result, 'document': document,
            'next': routes_after(result) + ['END'], 'mock': is_demo}


def execute_mock(node: str, pool: dict, request: str = '', fail: bool = False) -> dict:
    """Legacy scenario-test helper; production dispatches by semantic step ID."""
    if node not in ROUTES:
        raise ValueError(f'未知节点: {node}')
    step = next(step for step, kind in STEP_NODE.items() if kind == node)
    if node == 'F1' and pool['history'] and pool['history'][-1]['node'] == 'F2':
        step = 'refine_topic'
    if node == 'F5' and pool['history'] and pool['history'][-1]['node'] == 'F6':
        step = 'revise_document'
    return execute_step(step, pool, request, fail)


def _unwrap(value: Any) -> Any:
    # Host-bound JSON/text artifact envelopes, never LLM-invented file paths.
    for _ in range(4):
        if (isinstance(value, dict) and 'topic' not in value
                and ('schema' not in value or 'schema_version' in value)):
            if 'data' in value:
                value = value['data']
                continue
            if 'text' in value:
                value = value['text']
                continue
        break
    return value


def _read_bound(path: str) -> Any:
    text = Path(path).read_text(encoding='utf-8-sig')
    try:
        return _unwrap(json.loads(text))
    except json.JSONDecodeError:
        return text


def pool_from_inputs(inputs: dict[str, Any]) -> dict:
    candidates = [(key, validate_pool(value)) for key, value in inputs.items() if key.endswith('_pool')]
    if not candidates:
        raise MissingMaterials('没有绑定材料池，请先执行入口准备或补充材料。')
    key, selected = max(candidates, key=lambda item: (item[1]['sequence'], item[0].startswith('recovery_')))
    pool = copy.deepcopy(selected)
    document = inputs.get(key.removesuffix('_pool') + '_document')
    if document is not None:
        if not isinstance(document, str) or not document.strip():
            raise ValueError('已绑定的产物正文为空或格式错误')
        node = pool['history'][-1]['node'] if pool['history'] else None
        kind = {'F1': 'question', 'F2': 'evidence', 'F3': 'experiment', 'F4': 'analysis', 'F5': 'manuscript',
                'F6': 'review', 'F7': 'reflection'}.get(node)
        if key.removeprefix('recovery_') == 'write_outline_pool':
            kind = 'outline'
        current = material(pool, kind) if kind else None
        if current:
            content = current['content']
            if kind == 'analysis' and isinstance(content, dict):
                from .analysis.render import render
                from .analysis.schema import ContractError
                previous_document = render(content)
                previous_document = with_source_notice(
                    previous_document, current['mock'], (material(pool, 'result_meta') or {}).get('content'))
                if previous_document != document:
                    raise ContractError('STALE_MATERIAL', 'analysis',
                                        '分析报告已编辑；编辑稿保留，请将改动作为本轮要求重新分析，不能将修改后的数字视为已复算结果')
                return validate_pool(pool)
            previous_document = render_material(kind, content)
            if kind == 'manuscript':
                from ..artifacts import strip_paper_chart_numbering
                document = strip_paper_chart_numbering(document)
            notice = '> 演示数据，仅用于流程测试，不是真实研究结果。'
            if current['mock'] and kind != 'review':
                document = document.replace('\n\n' + notice + '\n', '\n', 1)
            if previous_document != document:
                if isinstance(content, (dict, list)) and kind != 'experiment':
                    raise ValueError('结构化材料展示文档已变更，请通过结构化输入更新，不能用 Markdown 覆盖业务对象')
                if kind == 'manuscript':
                    from .publication import protect_reference_region, publish_citations
                    protect_reference_region(content, document)
                    papers = material(pool, 'papers')
                    document = publish_citations(
                        document, papers['content'] if papers else [],
                        paper_language=(pool.get('constraints') or {}).get('paper_language', 'zh-CN'))
                updated = {**content,
                           'report': document} if kind == 'experiment' and isinstance(content, dict) else document
                add_material(pool, kind, updated, [pool['current'][kind]],
                             current['source_refs'], is_demo=current['mock'])
    return validate_pool(pool)


def _business_request(value: Any) -> str:
    """UI retry commands control execution; they are not algorithm requirements."""
    text = str(value or '').strip()
    if text.rstrip('。.!！').lower() in {
        '重试', '重新执行', 'retry', 're-run', 'rerun', 'try again',
        '继续', '继续执行', '下一步', 'continue', 'proceed', 'next',
    }:
        return ''
    if re.fullmatch(r'(?:请重新执行步骤|Please re-run step)\s+[A-Za-z0-9_.:-]+', text, re.IGNORECASE):
        return ''
    return text


def _runtime_request(params: dict) -> tuple[str, str]:
    instruction = str(params.get('retry_hint') or params.get('runtime_instruction') or '').strip()
    # Core restores launch intent in user_input on rewind; the new request is in retry_hint.
    recovery = instruction.partition('Recovery request for this rerun only:')[2].strip()
    if params.get('step_id') == 'data_analysis':
        # Before PR #83 Core did not expose current_user_input to algorithms.
        # Keep that F4 contract; Writer independently consumes the new field.
        values = (recovery, params.get('user_input'), params.get('launch_user_input'))
        request = next((text for value in values if (text := _business_request(value))), '')
    elif 'current_user_input' in params:
        request = _business_request(recovery) or str(params['current_user_input'] or '').strip()
    elif contracts.ALIASES.get(params.get('step_id'), params.get('step_id')) in contracts.CURRENT_REQUEST_MODULES:
        request = recovery
    else:
        request = str(recovery or params.get('user_input') or params.get('launch_user_input') or '').strip()
    return request, instruction


def initialize_pool(bound: dict[str, str], request: str = '') -> dict:
    """Resolve saved materials, or initialize business inputs before the first algorithm."""
    # A later step must use the latest confirmed artifacts, not re-read the original upload.
    saved = {key: _read_bound(path) for key, path in bound.items()
             if key.endswith(('_pool', '_document'))}
    if any(key.endswith('_pool') for key in saved):
        pool = pool_from_inputs(saved)
        if 'experiment_question' in bound and material(pool, 'question') is None:
            question = _read_bound(bound['experiment_question'])
            contracts.text(question, 'question')
            add_material(pool, 'question', question, [], [])
        return pool
    return _pool_from_entry(_read_entry_value(bound), request)


def _load_f7_handoff() -> dict | None:
    from httpx import HTTPStatusError
    from lazymind.chat.service.academic_handoff import academic_handoff_context

    try:
        handoff = academic_handoff_context()
    except HTTPStatusError as exc:
        if exc.response.status_code == 404 and exc.response.json().get('code') == 'PAPER_TASK_NOT_FOUND':
            return None
        raise
    return handoff if handoff.get('kind') == 'reflection' else None


def _read_f7_paper(paper_id: str) -> dict:
    from lazymind.chat.service.academic_handoff import academic_read_paper

    result = academic_read_paper(paper_id=paper_id)
    if result['status'] != 'ready':
        return {'status': result['status']}
    parts = []
    while True:
        for page in result['pages']:
            text = page['text']
            if text.strip():
                parts.append(f'<!-- page:{page["page"]} -->\n{text}' if page['page'] > 0 else text)
        cursor = result.get('next_cursor')
        if not cursor:
            break
        result = academic_read_paper(cursor=cursor)
        if result['status'] != 'ready':
            raise ValueError('论文全文分页读取未完成，请稍后重试。')
    text = '\n\n'.join(parts)
    if not text.strip():
        raise MissingMaterials(f'论文 {paper_id} 的全文为空。')
    return {'status': 'ready', 'text': text}


def _read_entry_value(bound: dict[str, str]) -> dict:
    """Read bound business inputs without creating a pool or selecting paper sources."""
    value = _read_bound(bound['paper_input']) if 'paper_input' in bound else {}
    if not isinstance(value, dict) or {'schema', 'step', 'input', 'output'} & value.keys():
        raise ValueError('paper_input 必须是 JSON 业务对象，不接受附件名、样例包装或材料池')
    value = copy.deepcopy(value)
    for slot, field in (('manuscript_text', 'manuscript'), ('paper_topic', 'topic'),
                        ('experiment_question', 'question')):
        if slot in bound:
            incoming = Path(bound[slot]).read_text(encoding='utf-8-sig')
            contracts.text(incoming, field)
            if field in value and value[field] != incoming:
                raise ValueError(f'{field} 存在冲突输入，请只提供一个版本')
            value[field] = incoming
    if 'manuscript_file' in bound:
        if 'manuscript' in value:
            raise ValueError('同时提供了论文文件和正文，请选择一个待审稿版本')
        value['manuscript'] = read_manuscript(bound['manuscript_file'])
    return value


def _pool_from_entry(value: dict, request: str, *, fallback_topic: str = '') -> dict:
    """Finish the existing initialization after the caller has prepared business materials."""
    if not value and ('[paper:demo]' in request or '默认演示材料' in request):
        return seed_pool()
    if value.get('demo'):
        return pool_from_business(value)
    manuscript = value.get('manuscript')
    if manuscript is not None:
        contracts.text(manuscript, 'manuscript')
        if len(manuscript) > MAX_MANUSCRIPT_CHARS:
            raise ValueError('论文正文超过当前 20 万字符限制，请缩小审查范围')
    if not value.get('topic'):
        if manuscript:
            title = next(line.strip().lstrip('#').strip() for line in manuscript.splitlines() if line.strip())
            value['topic'] = title[:120] or '论文审稿'
        elif request.strip():
            value['topic'] = request.strip()[:120]
        elif fallback_topic:
            value['topic'] = fallback_topic
        else:
            raise MissingMaterials('请提供论文文件、正文或研究主题')
    value.setdefault('request', request)
    return pool_from_business(value)


def _step_request(step: str, pool: dict, params: dict, bound: dict | None = None) -> str:
    """Keep requirements step-scoped, while restoring saved requirements on retry."""
    if step == 'data_analysis':
        request, instruction = _runtime_request({**params, 'step_id': step})
        recovery = instruction.partition('Recovery request for this rerun only:')[2].strip()
        if _business_request(recovery):
            return request
        if material(pool, 'analysis'):
            current = _business_request(params.get('current_user_input'))
            if current:
                return current
        # Restore the pre-PR input contract: only recovery can override materials.
        previous = ''
        if recovery:
            previous = next((_business_request(item.get('request')) for item in reversed(pool['history'])
                             if item['node'] == STEP_NODE[step] and _business_request(item.get('request'))), '')
        return previous or _business_request(pool.get('request')) or request
    bound = bound if bound is not None else params.get('remote_inputs', {})
    current, instruction = _runtime_request(params)
    current = _business_request(current)
    recovery = instruction.partition('Recovery request for this rerun only:')[2].strip()
    retry = bool(params.get('current_user_input') or recovery) and not current
    saved = next((_business_request(item.get('request')) for item in reversed(pool['history'])
                  if item['node'] == STEP_NODE[step] and _business_request(item.get('request'))), '')
    initial = not any(key.endswith('_pool') for key in bound)
    if initial and not current and not retry and not (step == 'peer_review' and 'review_request' in bound):
        current = _business_request(params.get('user_input') or params.get('launch_user_input'))
    base = _business_request(pool.get('request')) if initial or step == 'experiment' else ''
    if step == 'peer_review' and 'review_request' in bound:
        base = Path(bound['review_request']).read_text(encoding='utf-8-sig')
        contracts.text(base, 'review_request')
    if step == 'write_document' and 'write_outline_pool' in bound and saved:
        base = saved
    if retry and saved:
        base = saved
    if retry and not base:
        base = _business_request(params.get('user_input'))
    if not base and not current:
        base = saved or _business_request(pool.get('request'))
    if not base or not current:
        return base or current
    if base.strip() == current.strip():
        return base
    label = '本次启动补充要求' if initial and step == 'write_document' else '本轮补充要求（优先于上述要求）'
    return base + '\n\n' + label + '：\n' + current


@diagnostics.workflow_trace
def run_paper_step() -> dict:
    """Publish one scheduled step, with failure-safe attempt-scoped experiment previews."""
    from lazymind.chat.engine.subagent.context import require_context

    context = require_context()
    from .runtime_events import bind_runtime_events

    step = context.params.get('step_id')
    with bind_runtime_events(context.emit, f'{step}_document') as events:
        events.progress(5, '正在读取研究材料')
        stream = None
        if step == 'experiment':
            from .experiment.stream import ExperimentStream
            stream = ExperimentStream(context.emit, '实验研究', context.output_slots)
        from .writing.revise_document import RevisionInputRequired
        try:
            result = _run_paper_step(context, stream)
        except RevisionInputRequired as error:
            question = str(error)
            request, _ = _runtime_request(context.params)
            events.wait_for_input(question)
            return {'status': 'needs_input', 'step': step,
                    'control': {'awaiting_input': {'question': question, 'request_sha256': text_digest(request)}},
                    '_agent_control': {'stop': True, 'reason': 'workflow_input_required', 'final_text': question}}
        events.complete()
        return result


def _run_paper_step(context, stream=None) -> dict:
    """Execute the authoritative current step and save its artifacts in one batch.

    No arguments: step identity and immutable input bindings come from Runtime.
    """
    from lazymind.chat.engine.subagent.tools import save_artifacts

    step = context.params.get('step_id')
    bound = context.params.get('remote_inputs') or {}
    if not isinstance(bound, dict):
        raise ValueError('Runtime remote_inputs 格式错误')
    request, instruction = _runtime_request(context.params)
    handoff_request = None
    review_context = None
    if step == 'literature_review' and 'paper_input' not in bound:
        from .review.materials import current_handoff
        review_context = current_handoff()
    fixed_review_scope = review_context is not None
    if step == 'prepare':
        # Old sessions pin their graph; keep them runnable without exposing this node in new graphs.
        if not bound and '[paper:demo]' not in request and '默认演示材料' not in request:
            raise MissingMaterials('请提供论文文件、正文或研究主题')
        pool = initialize_pool(bound, request)
        artifacts = [
            {'key': 'seed_pool', 'content_type': 'json', 'value': pool},
            {'key': 'prepare_document', 'content_type': 'text', 'value': preparation_document(pool)},
        ]
    elif step == 'finish':
        artifacts = [{'key': 'finish_document', 'content_type': 'text', 'value': '# 本次流程已结束\n已有产物保留，可按需继续或重新执行。'}]
    else:
        if step not in STEP_NODE:
            raise ValueError(f'未知 Workflow 步骤: {step}')
        if step in {'experiment', 'data_analysis', 'write_outline', 'write_document', 'revise_document'}:
            if step in {'experiment', 'data_analysis'}:
                from .analysis.attachments import bind_attachment, _json_files
                from .data_analysis import retry_request
                if step == 'data_analysis':
                    request = retry_request(context.params, request)
            else:
                from .writing.attachments import bind_attachment
            with diagnostics.stage('input_binding'):
                history = context.params.get('history_files_per_turn') or {}
                has_json = _json_files([path for paths in history.values() for path in paths or []]) \
                    if step in {'experiment', 'data_analysis'} else True
                if has_json or 'paper_input' in bound:
                    bound = bind_attachment(bound, context.workspace_path, request)
            with diagnostics.stage('input_validation'):
                pool = initialize_pool(bound, request)
        elif step in {'research_topic', 'reading_reflection'} and not any(
            key.endswith('_pool') for key in bound
        ):
            value = _read_entry_value(bound)
            use_attachments = 'paper_input' not in bound
            if use_attachments:
                handoff = _load_f7_handoff() if step == 'reading_reflection' else None
                if handoff is not None:
                    selected = handoff['input']
                    papers = []
                    for paper in selected['papers']:
                        contracts.text(paper.get('paper_id'), 'paper.paper_id')
                        contracts.text(paper.get('title'), 'paper.title')
                        papers.append({**paper, 'id': paper['paper_id']})
                    papers = reading_reflection.prepare_papers(papers, read_paper=_read_f7_paper)
                    for paper in papers:
                        notes = [note for note in selected.get('notes', []) if note['paper_id'] == paper['id']]
                        if notes:
                            paper['notes'] = notes
                    handoff_request = selected['user_instruction']
                    value['request'] = handoff_request
                    value.setdefault('topic', '；'.join(paper['title'] for paper in papers))
                else:
                    from .source_materials import sync_attachments
                    imported = pool_from_business({'topic': request.strip()[:120] or '研究材料'})
                    sync_attachments(imported, context.params.get('history_files_per_turn'), request,
                                     read_papers=_conversation_papers)
                    papers = (material(imported, 'papers') or {}).get('content', [])
                    if step == 'research_topic' and papers:
                        papers = research_topic.prepare_papers(papers)
                    if material(imported, 'source_data'):
                        value['source_data'] = material(imported, 'source_data')['content']
                if papers:
                    # Real attachments take precedence over a demo marker in the request.
                    value['papers'] = papers
            fallback_topic = ''
            if value.get('schema') != SCHEMA and not value.get('demo'):
                papers = value.get('papers', [])
                if 'papers' in value:
                    contracts.validate_value('papers', papers)
                demo_request = not value and ('[paper:demo]' in request or '默认演示材料' in request)
                if step == 'reading_reflection' and not papers and not demo_request:
                    raise MissingMaterials('请先选择至少一篇论文，或上传论文附件，再生成阅读心得。')
                fallback_topic = '；'.join(paper['title'] for paper in papers)
            pool = _pool_from_entry(value, request, fallback_topic=fallback_topic)
            if use_attachments and handoff is None:
                pool['imported_attachments'] = imported.get('imported_attachments', [])
            if step == 'research_topic' and use_attachments and not any(item['mock'] for item in pool['materials']):
                if 'paper_topic' not in bound:
                    pool['topic'] = research_topic.infer_topic(pool.get('request') or request, papers)
        elif step == 'peer_review':
            from .peer_review import stage
            with stage('pool_initialization', getattr(context, 'task_id', 'peer_review'),
                       bindings=sorted(bound)):
                pool = initialize_pool(bound, request)
        elif review_context is not None and not any(
            key.endswith('_pool') for key in bound
        ):
            from .review.materials import handoff_input
            pool = pool_from_business(handoff_input(review_context))
        else:
            pool = initialize_pool(bound, request)
        if bound.get('_imported_attachments'):
            pool['imported_attachments'] = sorted(set(pool.get('imported_attachments', [])).union(
                bound['_imported_attachments']))
        # Import only new attachments before projecting business inputs. Explicit input selections stay pinned.
        if (not fixed_review_scope and handoff_request is None
                and ('paper_input' not in bound or any(key.endswith('_pool') for key in bound))):
            from .source_materials import pending_attachments, sync_attachments
            history = context.params.get('history_files_per_turn')
            if step == 'data_analysis' and pending_attachments(pool, history):
                raise MissingMaterials('收到新附件，请先回到 F3 提取入池并整理，再进行数据分析。')
            sync_attachments(pool, history, request, read_papers=_conversation_papers)
        if step == 'data_analysis':
            prepared = next((item for item in reversed(pool['history']) if item['node'] == 'F3'), None)
            if prepared is None:
                raise MissingMaterials('请先进入 F3 整理实验或用户数据，再进行数据分析。')
            consumed = prepared['inputs']
            if any(pool['current'].get(kind) and pool['current'][kind] not in consumed
                   for kind in ('papers', 'source_data', 'data')):
                raise MissingMaterials('材料已更新，请先回到 F3 整理并确认最新数据，再进行分析。')
        if stream:
            stream.topic = pool['topic']
        if step == 'reading_reflection':
            papers = material(pool, 'papers')
            if papers is None or not papers['content']:
                raise MissingMaterials('请先选择至少一篇论文，或上传论文附件，再生成阅读心得。')
        request_params = context.params
        if handoff_request is not None:
            launch_request = str(
                context.params.get('launch_user_input')
                or context.params.get('user_input')
                or ''
            ).strip()
            request_params = {
                **context.params,
                'user_input': handoff_request,
                'launch_user_input': handoff_request,
                'current_user_input': handoff_request if request == launch_request else request,
            }
        if step == 'data_analysis' and not material(pool, 'analysis'):
            recovery = _business_request(instruction.partition('Recovery request for this rerun only:')[2])
            current = _business_request(request_params.get('current_user_input'))
            launch = _business_request(request_params.get('launch_user_input') or request_params.get('user_input'))
            followup = recovery or (current if current != launch else '')
            if followup:
                from .data_analysis import redraw_requested
                if redraw_requested(followup):
                    raise MissingMaterials('未绑定已有分析与图表，无法只重画；请补充已有分析材料，'
                                           '或在启用恢复输入的新会话完成分析后再重画。')
        request = _step_request(step, pool, request_params, bound)
        if step == 'literature_review':
            import lazyllm
            from .review.materials import prepare
            knowledge_bases = ((lazyllm.globals.get('agentic_config') or {}).get('filters') or {}).get('kb_id', [])
            current = material(pool, 'papers')
            papers = prepare(business_input(step, pool, request), knowledge_bases,
                             fixed_scope=fixed_review_scope)
            if current is None or current['content'] != papers:
                add_material(pool, 'papers', papers, [pool['current']['papers']] if current else [],
                             paper_source_ids(papers))
        skip_revision = False
        if step == 'revise_document':
            from .writing.revise_document import resolve_review_issues
            review = material(pool, 'review_data')
            manuscript = material(pool, 'manuscript')
            report = material(pool, 'review')
            previous = next((entry.get('step') or entry.get('node') for entry in reversed(pool['history'])), None)
            matches = bool(review and manuscript and report
                           and review['content']['manuscript_sha256'] == text_digest(manuscript['content'])
                           and review['content']['review_sha256'] == text_digest(report['content']))
            continuing_review = (matches and previous in {'peer_review', 'F6'}
                                 and not _business_request(context.params.get('current_user_input', request)))
            try:
                pool['selected_issue_ids'] = resolve_review_issues(
                    request, review['content'] if review else None,
                    workflow_context={'current_step': step,
                                      'previous_completed_step': (
                                          'peer_review' if previous in {'peer_review', 'F6'} else previous),
                                      'review_matches_manuscript': matches},
                )
            except CancelledError:
                raise
            except Exception as error:
                if not continuing_review:
                    raise
                LOG.warning(f'Optional paper revision scope was unavailable {diagnostics.safe_error(error)}')
                pool['selected_issue_ids'] = []
                skip_revision = True
        fail = '[mock:fail]' in request + instruction and '[mock:retry]' not in request + instruction
        if (step == 'write_document' and 'write_outline_document' in getattr(context, 'input_slots', [])
                and not material(pool, 'manuscript') and 'write_outline_document' not in bound):
            raise MissingMaterials('请先生成并确认论文大纲，再继续写作。')
        preference = _read_bound(bound['experiment_execution']) if 'experiment_execution' in bound else 'simulate_only'
        result = execute_step(step, pool, request, fail=fail, on_event=stream,
                              allow_execution=preference == 'allow_local', skip_revision=skip_revision)
        if stream:
            output = {kind: material(result['pool'], kind)['content'] for kind in contracts.OUTPUTS['experiment']}
            output['is_demo'] = result['mock']
            stream.validated(output)
        if step in {'write_document', 'revise_document'}:
            from .publication import publish_citations, publish_charts, figure_catalog
            manuscript = material(result['pool'], 'manuscript')
            charts = material(result['pool'], 'charts')
            papers = material(result['pool'], 'papers')
            original = publish_citations(
                manuscript['content'], papers['content'] if papers else [],
                paper_language=(result['pool'].get('constraints') or {}).get('paper_language', 'zh-CN'))
            media = material(result['pool'], 'media_assets')
            published = publish_charts(original, figure_catalog(
                charts['content'] if charts else [], media['content'] if media else None))
            if step == 'revise_document':
                checked = material(result['pool'], 'review_data')['content']
                if checked['manuscript_sha256'] != text_digest(published):
                    raise ValueError('发布后的修订稿与事实核对版本不一致')
            manuscript['content'] = published
            # Slots and the frontend receive canonical Markdown with resolved image URLs.
            result['document'] = published
            notice = '> 演示数据，仅用于流程测试，不是真实研究结果。'
            if result['mock'] and notice not in result['document']:
                first, _, rest = result['document'].partition('\n')
                result['document'] = first + '\n\n' + notice + '\n' + rest
        artifacts = [
            {'key': f'{step}_pool', 'content_type': 'json', 'value': result['pool']},
            {'key': f'{step}_document', 'content_type': 'text', 'value': result['document']},
        ]
        if (step == 'experiment'
                and {'experiment_code', 'experiment_results'} <= set(getattr(context, 'output_slots', []))):
            from .experiment.artifacts import export_experiment
            output = {kind: material(result['pool'], kind)['content'] for kind in contracts.OUTPUTS['experiment']}
            output['is_demo'] = result['mock']
            data = material(pool, 'data')
            selected_data = data['content'] if data else None
            if output['result_meta'].get('input_table_ids'):
                from .experiment.preparation import candidate_tables
                candidates = candidate_tables(business_input('experiment', result['pool'], request))
                selected = [candidates[key] for key in output['result_meta']['input_table_ids']]
                selected_data = ({key: selected[0]['table'][key] for key in ('columns', 'rows')}
                                 if len(selected) == 1 else {'tables': selected})
            files = export_experiment(output, context.workspace_path, selected_data, preference)
            artifacts.extend({'key': key, 'content_type': 'file', 'value': path} for key, path in files.items())
        # Old pinned graphs still receive the structured result inside their existing pool artifact.
        if step == 'peer_review' and 'peer_review_data' in getattr(context, 'output_slots', []):
            path = Path(context.workspace_path) / '审稿意见.json'
            path.write_text(json.dumps(material(result['pool'], 'review_data')['content'],
                                       ensure_ascii=False, indent=2), encoding='utf-8')
            artifacts.append({'key': 'peer_review_data', 'content_type': 'file', 'value': str(path)})
        if step == 'revise_document' and 'revision_fact_check_document' in context.output_slots:
            artifacts.append({'key': 'revision_fact_check_document', 'content_type': 'text',
                              'value': material(result['pool'], 'review')['content']})
        if step == 'data_analysis':
            updated_manuscript = material(result['pool'], 'manuscript')
            if (updated_manuscript
                    and result['pool']['current'].get('manuscript') != pool['current'].get('manuscript')):
                from .publication import publish_charts, figure_catalog
                media = (material(result['pool'], 'media_assets') or {}).get('content')
                published = publish_charts(updated_manuscript['content'], figure_catalog(
                    material(result['pool'], 'charts')['content'], media))
                if 'redrawn_manuscript_document' in context.output_slots:
                    artifacts.append({'key': 'redrawn_manuscript_document', 'content_type': 'text', 'value': published})
            figure_exports = {}
            for entry in result['pool']['history']:
                for record in entry.get('analysis_execution', []):
                    for figure in record.get('figures', []):
                        figure_exports[figure['id']] = figure.get('exports', {})
            for figure in material(result['pool'], 'charts')['content']:
                chart = Path(context.workspace_path) / f'分析图表-{figure["id"]}.svg'
                chart.write_text(figure['svg'], encoding='utf-8')
                artifacts.append({'key': 'analysis_chart', 'content_type': 'file', 'value': str(chart),
                                  'caption': figure.get('caption') or figure['title']})
                for extension, encoded in figure.get('exports', figure_exports.get(figure['id'], {})).items():
                    exported = chart.with_suffix('.' + extension)
                    exported.write_bytes(base64.b64decode(encoded, validate=True))
                    artifacts.append({'key': 'analysis_chart', 'content_type': 'file', 'value': str(exported),
                                      'caption': f'{figure.get("caption") or figure["title"]} ({extension.upper()})'})
    from .runtime_events import current_events, report_progress
    events = current_events()
    if events:
        for artifact in artifacts:
            if artifact['key'].endswith('_document'):
                events.completed_preview(artifact['value'], artifact['key'])
    # Validate all publication dependencies before writing, and never overwrite a prior run.
    publication_dir = Path(tempfile.mkdtemp(prefix='paper-output-', dir=context.workspace_path))
    # Publish actual named files; the existing document projection supplies the editor.
    for artifact in artifacts:
        if artifact['key'].endswith('_pool'):
            path = publication_dir / f"{artifact['key']}.json"
            persist_artifact_json(artifact['value'], str(path), schema_name=SCHEMA,
                                  created_by='paper-writing')
            artifact.update(content_type='file', value=str(path))
        elif artifact['key'].endswith('_document'):
            label = {'revision_fact_check_document': '修订后事实核对',
                     'redrawn_manuscript_document': '重画后的论文'}.get(artifact['key'], STEP_LABELS[step])
            path = publication_dir / markdown_filename(label)
            path.write_text(artifact['value'], encoding='utf-8')
            artifact.update(content_type='file', value=str(path))
    report_progress(97, '正在保存文档')
    with diagnostics.stage('output_publication'):
        saved = save_artifacts(artifacts)
        diagnostics.save('published-files.json', [
            {'slot': item['key'], 'filename': Path(item['value']).name}
            for item in artifacts if item['content_type'] == 'file'
        ])
    final_text = '产物已保存，请在工作流中查看。'
    if step == 'revise_document':
        review = material(result['pool'], 'review_data')['content']
        pending = [issue['id'] for issue in review['issues'] if issue['action_type'] != 'text_revision']
        final_text += ' 已生成修订后事实核对报告；请以新报告中的问题编号为准。'
        if pending:
            final_text += ' ' + '、'.join(pending) + ' 仍需补充材料或实验；文字修改不代表这些事项已完成。'
    return {
        'status': 'completed', 'step': step, 'saved': saved,
        **({'control': {'next_step': 'write_document'}} if step == 'write_outline' else {}),
        # Stop the node with user-facing text, not a stringified publication receipt.
        '_agent_control': {'stop': True, 'final_text': final_text},
    }
