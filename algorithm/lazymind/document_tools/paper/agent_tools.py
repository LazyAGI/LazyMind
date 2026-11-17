"""Model-free paper operations shared by native and external step agents.

Only adapters construct PaperContext. Candidates never choose an owner, step,
input version, filesystem path or execution permission.
"""
from __future__ import annotations

import base64
import copy
import hashlib
import hmac
import json
import os
import tempfile
from functools import wraps
from collections.abc import Mapping
from contextlib import contextmanager
from contextvars import ContextVar
from dataclasses import dataclass
from pathlib import Path

from . import contracts
from .workflow import (STEP_NODE, add_material, business_input, material,
                       paper_source_ids, pool_from_business, pool_from_inputs,
                       render_material, validate_pool, with_source_notice)

VALIDATOR = 'paper-agent-v1'
_context = ContextVar('paper_agent_context', default=None)
_operation_cancel = ContextVar('paper_operation_cancel', default=None)
MAX_BYTES = 20 * 1024 * 1024


def json_arguments(function):
    """Native authorization freezes JSON containers; keep the business boundary JSON-shaped."""
    def mutable(value):
        if isinstance(value, Mapping):
            return {key: mutable(item) for key, item in value.items()}
        if isinstance(value, (tuple, list)):
            return [mutable(item) for item in value]
        return value

    @wraps(function)
    def call(*args, **kwargs):
        return function(*(mutable(item) for item in args),
                        **{key: mutable(item) for key, item in kwargs.items()})
    return call


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()


def digest(value):
    return hashlib.sha256(canonical(value)).hexdigest()


@dataclass(frozen=True)
class PaperContext:
    session_id: str
    attempt_id: str
    revision: str
    step_id: str
    bindings: dict
    values: dict
    request: str = ''
    launch_request: str = ''
    lease_hash: str = ''
    check_liveness: bool = False
    knowledge_scope: dict | None = None

    @property
    def scope(self):
        return {'session_id': self.session_id, 'attempt_id': self.attempt_id,
                'revision': self.revision, 'step_id': self.step_id,
                'input_versions': self.bindings, 'request': self.request,
                'launch_request': self.launch_request, 'lease_hash': self.lease_hash,
                'knowledge_scope': self.knowledge_scope}

    @classmethod
    def parse(cls, value):
        value = copy.deepcopy(value)
        value['values'] = {key: decode_value(item) for key, item in value['values'].items()}
        result = cls(**value)
        if (not all((result.session_id, result.attempt_id, result.revision))
                or result.step_id not in {*STEP_NODE, 'finish'}):
            raise ValueError('可信论文执行上下文不完整')
        return result


@contextmanager
def paper_scope(value):
    token = _context.set(PaperContext.parse(value) if isinstance(value, dict) else value)
    try:
        yield
    finally:
        _context.reset(token)


def native_context():
    """The native adapter resolves genuine Runtime bindings; no fabricated context."""
    from lazymind.chat.engine.subagent.context import require_context
    current = require_context()
    identity = current.params.get('_paper_execution')
    if not identity:
        raise ValueError('当前 revision 没有论文 Agent 执行契约')
    values = {}
    value_slots = current.params.get('remote_input_value_slots') or []
    for slot, bound in (current.params.get('remote_inputs') or {}).items():
        items = bound if isinstance(bound, list) else [bound]
        decoded = []
        for index, item in enumerate(items, start=1):
            if slot in value_slots:
                decoded.append(item)
                continue
            path = Path(item)
            raw = path.read_bytes()
            if len(raw) > MAX_BYTES:
                raise ValueError('论文输入超过 20 MiB')
            name = path.name.removeprefix(f'{index:04d}_') if isinstance(bound, list) else path.name
            decoded.append(decode_file(raw, name))
        values[slot] = decoded if isinstance(bound, list) else decoded[0]
    return PaperContext.parse({**identity, 'values': values})


def context():
    return _context.get() or native_context()


def decode_file(raw, name):
    suffix = Path(name).suffix.lower()
    if suffix in {'.pdf', '.docx'}:
        from .entry import read_manuscript
        with tempfile.TemporaryDirectory(prefix='paper-input-') as directory:
            path = Path(directory) / ('source' + suffix)
            path.write_bytes(raw)
            return {'filename': name, 'source_sha256': hashlib.sha256(raw).hexdigest(),
                    'text': read_manuscript(str(path))}
    try:
        text = raw.decode('utf-8-sig')
    except UnicodeDecodeError as exc:
        raise ValueError('当前材料需提供 UTF-8 文本/CSV/TSV/JSON，或 PDF/DOCX 文档') from exc
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return {'filename': name, 'source_sha256': hashlib.sha256(raw).hexdigest(), 'text': text}


def decode_value(value):
    if isinstance(value, list):
        return [decode_value(item) for item in value]
    if isinstance(value, dict):
        if {'filename', 'source_sha256', 'text'} <= value.keys():
            return value
        if '__file_base64' in value:
            return decode_file(base64.b64decode(value['__file_base64'], validate=True), value['filename'])
        if value.get('storage') == 'inline_base64':
            return decode_file(base64.b64decode(value['content_base64'], validate=True),
                               value.get('name') or value.get('filename') or 'source.json')
        from .workflow import _unwrap
        return _unwrap(value)
    return value


def value_text(value):
    if isinstance(value, str):
        return value
    if isinstance(value, dict):
        for key in ('text', 'data', 'value'):
            if isinstance(value.get(key), str):
                return value[key]
    raise ValueError('输入必须提供实际文本')


def base_pool(ctx):
    saved = {key: value for key, value in ctx.values.items() if key.endswith(('_pool', '_document'))}
    if any(key.endswith('_pool') for key in saved):
        for key, value in list(saved.items()):
            if key.endswith('_document'):
                saved[key] = value_text(value)
        pool = pool_from_inputs(saved)
    else:
        business = copy.deepcopy(ctx.values.get('paper_input') or {})
        if not isinstance(business, dict):
            raise ValueError('paper_input 必须是业务 JSON')
        for slot, field in (('paper_topic', 'topic'), ('manuscript_text', 'manuscript'),
                            ('manuscript_file', 'manuscript'), ('experiment_question', 'question')):
            if slot in ctx.values:
                incoming = value_text(ctx.values[slot])
                if field in business and business[field] != incoming:
                    raise ValueError(f'{field} 存在冲突输入')
                business[field] = incoming
        business.setdefault('topic', (ctx.launch_request or ctx.request).strip()[:120])
        business.setdefault('request', ctx.launch_request or ctx.request)
        pool = pool_from_business(business)
    pool = copy.deepcopy(pool)
    from .source_materials import markdown_tables, typed_table
    import csv
    import io
    papers = copy.deepcopy((material(pool, 'papers') or {}).get('content', []))
    tables = copy.deepcopy((material(pool, 'source_data') or {}).get('content', {}).get('tables', []))
    known = {paper['id'] for paper in papers}
    attachments = ctx.values.get('research_materials') or []
    for source in attachments if isinstance(attachments, list) else [attachments]:
        data = source.get('data', source) if isinstance(source, dict) else source
        if isinstance(data, dict) and ('tables' in data or {'columns', 'rows'} <= data.keys()):
            from .analysis.schema import DataBundle, DataTable
            if 'tables' in data:
                entries = DataBundle.model_validate(data).model_dump()['tables']
            else:
                table = (DataTable.model_validate(data).model_dump() if 'column_types' in data
                         else typed_table(data['columns'], data['rows']))
                entries = [{'id': 't_' + digest(data)[:20], 'description': 'Bound JSON table', 'table': table}]
            for table in entries:
                previous = next((item for item in tables if item['id'] == table['id']), None)
                if previous is not None and previous != table:
                    raise ValueError('数据表 ID 冲突，不能静默覆盖或丢弃数据')
                if previous is None:
                    tables.append(table)
            continue
        text = value_text(source)
        identifier = 'input-' + digest(source)[:20]
        if identifier in known:
            continue
        name = source.get('filename', 'research-material') if isinstance(source, dict) else 'research-material'
        paper = {'id': identifier, 'title': name, 'text': text, 'source': 'input',
                 'source_version': digest(source), 'reading': {'content_type': 'provided_text', 'truncated': False}}
        papers.append(paper)
        known.add(identifier)
        if Path(name).suffix.lower() in {'.csv', '.tsv'}:
            rows = list(csv.reader(io.StringIO(text), delimiter='\t' if name.endswith('.tsv') else ','))
            if len(rows) < 2:
                raise ValueError('数据文件没有完整表格')
            extracted = [{'id': 't_' + identifier, 'description': name,
                          'table': typed_table(rows[0], rows[1:])}]
        else:
            extracted, _ = markdown_tables(text, identifier, name)
        for table in extracted:
            previous = next((item for item in tables if item['id'] == table['id']), None)
            if previous is not None and previous != table:
                raise ValueError('数据表 ID 冲突，不能静默覆盖或丢弃数据')
            if previous is None:
                tables.append(table)
    for kind, content in (('papers', papers), ('source_data', {'tables': tables})):
        old = material(pool, kind)
        if content != (old['content'] if old else ([] if kind == 'papers' else {'tables': []})):
            add_material(pool, kind, content, [pool['current'][kind]] if old else [], paper_source_ids(papers))
    validate_pool(pool)
    return pool


def step_inputs(ctx, pool):
    if ctx.step_id == 'finish':
        return {}
    if ctx.step_id == 'write_document' and material(pool, 'outline') is None:
        raise contracts.MissingMaterials('请先生成或提供已确认的大纲，再继续写作')
    if ctx.step_id == 'data_analysis':
        prepared = next((entry for entry in reversed(pool['history']) if entry['node'] == 'F3'), None)
        if prepared is None:
            raise contracts.MissingMaterials('请先进入 F3 整理并确认实际数据')
        if any(pool['current'].get(key) and pool['current'][key] not in prepared['inputs']
               for key in ('papers', 'source_data', 'data')):
            raise contracts.MissingMaterials('材料已更新，请先回到 F3 整理并确认')
    value = business_input(ctx.step_id, pool, ctx.request or pool.get('request', ''))
    if 'constraints' in value:
        from .writing.requirements import resolve_constraints
        value['constraints'] = resolve_constraints(value['constraints'], ctx.launch_request,
                                                   current_request=ctx.request)
    if ctx.step_id == 'revise_document':
        import re
        request = ctx.request or ''
        reviewing = bool(pool.get('selected_issue_ids')) or bool(re.search(
            r'根据.*(?:审稿|评审)|按.*(?:审稿|评审)|according to.*review|address.*review', request, re.I))
        if reviewing:
            review = material(pool, 'review_data')
            if review is None:
                raise contracts.MissingMaterials('缺少当前稿件的审稿意见')
            value['review_data'] = review['content']
            value['review'] = (material(pool, 'review') or {}).get('content')
            explicit = re.findall(r'(?<![A-Za-z0-9])R[1-9][0-9]*(?![0-9])', request)
            value['selected_issue_ids'] = pool.get('selected_issue_ids') or explicit or [
                issue['id'] for issue in review['content']['issues'] if issue['action_type'] == 'text_revision']
    result = contracts.validate_input(ctx.step_id, value)
    if ctx.step_id == 'write_outline' and result['constraints']['word_target'] is None:
        result['constraints']['word_target'] = 5000 if result['constraints']['paper_language'] == 'en' else 8000
    return result


def _key():
    key = os.environ.get('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', '')
    if not key:
        raise ValueError('论文工具签名服务未配置，不能生成可信回执')
    return key.encode()


def seal(ctx, kind, value):
    payload = {'scope': ctx.scope, 'kind': kind, 'value': copy.deepcopy(value)}
    return {'payload': payload, 'signature': hmac.new(_key(), canonical(payload), hashlib.sha256).hexdigest()}


def receipt_reference(ctx, kind, value):
    """Keep signed source/compute facts server-side, so models only round-trip a short reference."""
    signed = seal(ctx, kind, value)
    raw = canonical(signed)
    identifier = hashlib.sha256(raw).hexdigest()
    path = execution_directory() / ('receipt-' + identifier + '.json')
    try:
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    except FileExistsError:
        if path.is_symlink() or path.read_bytes() != raw:
            raise ValueError('工具回执存储冲突')
    else:
        with os.fdopen(descriptor, 'wb') as output:
            output.write(raw)
    return {'receipt_id': identifier, 'kind': kind}


def resolve_receipt(receipt):
    if isinstance(receipt, dict) and 'receipt_id' in receipt:
        identifier = receipt.get('receipt_id')
        if (set(receipt) != {'receipt_id', 'kind'} or not isinstance(identifier, str)
                or len(identifier) != 64 or any(char not in '0123456789abcdef' for char in identifier)):
            raise ValueError('无效的工具回执引用')
        path = execution_directory() / ('receipt-' + identifier + '.json')
        if path.is_symlink() or not path.is_file():
            raise ValueError('工具回执引用不可用')
        raw = path.read_bytes()
        if hashlib.sha256(raw).hexdigest() != identifier:
            raise ValueError('工具回执引用内容不一致')
        signed = json.loads(raw)
        if signed.get('payload', {}).get('kind') != receipt['kind']:
            raise ValueError('工具回执引用类型不一致')
        return signed
    return receipt


def unseal(ctx, receipt, kind):
    receipt = resolve_receipt(receipt)
    if not isinstance(receipt, dict) or set(receipt) != {'payload', 'signature'}:
        raise ValueError('缺少可信工具回执')
    payload = receipt['payload']
    expected = hmac.new(_key(), canonical(payload), hashlib.sha256).hexdigest()
    if (not isinstance(receipt['signature'], str) or not hmac.compare_digest(receipt['signature'], expected)
            or payload.get('scope') != ctx.scope or payload.get('kind') != kind):
        raise ValueError('工具回执不属于当前输入版本和执行步骤')
    return copy.deepcopy(payload['value'])


def _prepared_inputs(ctx, pool=None) -> dict:
    """Build the complete input and schemas for scoped, lossless paging."""
    if ctx.step_id == 'finish':
        return {'step': 'finish', 'input': {}, 'expected_output_fields': [],
                'inputs_digest': digest(ctx.scope), 'launch_request': ctx.launch_request,
                'current_request': ctx.request}
    pool = base_pool(ctx) if pool is None else pool
    value = step_inputs(ctx, pool)
    result = {'step': ctx.step_id, 'input': value, 'pool_sequence': pool['sequence'],
              'expected_output_fields': list(contracts.OUTPUTS.get(contracts.ALIASES.get(ctx.step_id, ctx.step_id), ())),
              'launch_request': ctx.launch_request, 'current_request': ctx.request,
              'material_catalog': [{'material_id': kind, 'version': ref['version']}
                                   for kind, ref in pool['current'].items()
                                   if ctx.step_id != 'peer_review' or kind in value],
              'candidate_contract': ('Pass output as the business object itself. '
                                     'Never pass a JSON string or artifact manifest. '
                                     'Pass receipts as an array of actual tool receipts; use [] when none are needed.'),
              'selected_knowledge': bool(ctx.knowledge_scope and (ctx.knowledge_scope.get('dataset_ids')
                                         or ctx.knowledge_scope.get('documents'))),
              'source_handoff': ('For retrieved papers, prefer selected_paper_ids to copying source facts; '
                                 'in literature_review omit papers to resolve selected IDs from their actual receipts. '
                                 'Never change source facts.'),
              'inputs_digest': digest(ctx.scope), 'checks': 'Program checks do not establish scientific correctness.'}
    if ctx.step_id == 'write_outline':
        result['candidate_example'] = {
            'output': {'outline': '# Paper title\n\n## Introduction\nArgument and budget.'
                                  '\n\n## Methods\nApproach and budget.'}, 'receipts': []}
    result['table_catalog'] = [
        {'table_id': item['id'], 'description': item.get('description'),
         'columns': item['table']['columns'], 'column_types': item['table']['column_types'],
         'units': item['table'].get('units'), 'row_count': len(item['table']['rows'])}
        for original in (value.get('source_data'), value.get('data')) if isinstance(original, dict)
        for item in original.get('tables', [])]
    if ctx.step_id == 'experiment':
        from .experiment.schema import ResultMeta, CodeFile
        result['result_meta_schema'] = ResultMeta.model_json_schema()
        result['code_file_schema'] = CodeFile.model_json_schema()
        result['experiment_shape'] = {'executed': 'boolean from actual receipt',
                                      'plan': 'Markdown string',
                                      'results': ('For existing data use {table_ids:[actual IDs]} '
                                                  'to preserve every bound value; alternatively exact '
                                                  'DataTable/DataBundle, actual executed table, or null'),
                                      'report': 'Markdown string explaining evidence, method and limitations'}
    if ctx.step_id == 'experiment' and result['table_catalog']:
        ids = [entry['table_id'] for entry in result['table_catalog']]
        result['existing_data_candidate_example'] = {
            'output': {'experiment': {'executed': False, 'plan': 'Replace with a concise data preparation plan.',
                                      'results': {'table_ids': ids},
                                      'report': 'Replace with the actual preparation findings and limitations.'},
                       'code_files': [], 'result_meta': {
                           'execution_status': 'not_run', 'data_origin': 'user_supplied', 'method': 'empirical',
                           'reason': 'Bound observations only; no new execution.', 'assumptions': [],
                           'metrics': [], 'seed': None, 'input_table_ids': ids}}, 'receipts': []}
        result['submission_guidance'] = (
            'For preparation of existing observations without new execution, use the example types and actual '
            'table IDs. The server restores complete original tables; never copy rows into the submission. '
            'Keep plan and report concise (normally 100-300 words each); do not repeat the source documents, '
            'research review or future analysis in both fields. Use JSON false, [], null and arrays of strings '
            'as declared, not quoted booleans or {item:...} objects. A cut-off JSON response must be regenerated '
            'with shorter prose, never completed by guessing missing results.')
    if ctx.step_id == 'data_analysis':
        previous = material(pool, 'analysis')
        if previous is not None:
            result['existing_analysis_receipt'] = receipt_reference(ctx, 'analysis', {'analysis': previous['content']})
            result['existing_charts'] = (material(pool, 'charts') or {}).get('content', [])
        from .analysis.plan import Plan
        from .analysis.generated_executor import generation_contract
        from .analysis.schema import AnalysisOutput
        result.update(plan_schema=Plan.model_json_schema(), output_schema=AnalysisOutput.model_json_schema(),
                      code_contract=generation_contract())
        from .analysis.results import ExecutedAnalysis
        from .analysis.schema import Chart
        from .plotting.code import ALLOWED_IMPORTS, FORBIDDEN, FORBIDDEN_ATTRIBUTES
        result['code_contract'].update(
            tables='Mapping of actual table_id to complete DataTable dict: columns, column_types, units, rows.',
            return_value=('Return an ExecutedAnalysis object, not final Analysis: results is a list of actual '
                          'ExecutedResult rows. Use plan.questions question_id values and actual column references.'),
            return_schema=ExecutedAnalysis.model_json_schema(),
            serialization='Return plain finite JSON values; no NumPy scalars, NaN or Infinity.')
        result['plot_code_contract'] = {
            'required_function': 'draw(data, statistics, spec)', 'return_value': 'matplotlib.figure.Figure',
            'data': 'One complete bound DataTable dict (columns, column_types, units, rows), not a DataFrame.',
            'statistics': ('Frozen Analysis plus results: actual flattened rows when encoded metadata is available; '
                           'historical findings may retain prose calculation only and results will be empty. '
                           'Rows include coordinates, label, metric, method, scope, value and source columns. '
                           'Do not recompute estimates.'),
            'finding_metadata': ('Use statistics.results rows for coordinates; findings have calculation JSON, '
                                 'not top-level coordinates. Reject unmatched series; '
                                 'never default missing data to zero.'),
            'specification': 'Chart schema fields except svg, which the renderer supplies.',
            'specification_schema': Chart.model_json_schema(), 'allowed_import_roots': sorted(ALLOWED_IMPORTS),
            'forbidden_names': sorted(FORBIDDEN), 'forbidden_attributes': sorted(FORBIDDEN_ATTRIBUTES),
            'rules': 'Keep original data and statistics unchanged; no savefig/show/network/filesystem access.'}
        result['candidate_analysis_shape'] = (
            'Use output.analysis={receipt: actual paper_analyze receipt, summary: your evidence-based interpretation, '
            'limitations: your honest limitations}. All numerical findings are restored exactly; never retype them.')
        result['candidate_chart_shape'] = (
            'Use output.charts=[actual paper_plot receipt objects] and include '
            'the same receipts in receipts. The server restores exact SVG and export bytes.')
    if ctx.step_id in {'peer_review', 'revise_document'}:
        result['expected_output_fields'] = (['review_result'] if ctx.step_id == 'peer_review'
                                            else ['modify_plan', 'replacements', 'review_result'])
        from .review.schema import ReviewResult, LiteratureReadings
        result['literature_readings_schema'] = LiteratureReadings.model_json_schema()
        result['review_schema'] = ReviewResult.model_json_schema()
        if ctx.step_id == 'revise_document':
            from lazyllm.tools.writer.data_models.revision import MarkdownModifyPlan, StringReplaceSet
            result['modify_plan_schema'] = MarkdownModifyPlan.model_json_schema()
            result['replacements_schema'] = StringReplaceSet.model_json_schema()
            result['revision_contract'] = ('Supply modify_plan and replacements from your own reasoning. '
                                           'Use exact original locators; no model is called to repair a bad patch. '
                                           'Return unchanged manuscript if no valid optional edit is possible.')

    # Native spill previews must show exact-reference guidance before long requests and material bodies.
    first = ('step', 'expected_output_fields', 'candidate_contract', 'experiment_shape', 'table_catalog',
             'source_handoff', 'candidate_analysis_shape', 'candidate_chart_shape',
             'code_contract', 'plot_code_contract')
    result['input'] = result.pop('input')
    return {**{key: result[key] for key in first if key in result}, **result}


def _material_text(value):
    return value if isinstance(value, str) else canonical(value).decode()


def _input_reference(identifier, value):
    text = _material_text(value)
    return {'material_id': identifier, 'total_characters': len(text),
            'source_sha256': hashlib.sha256(text.encode()).hexdigest(),
            'format': 'text' if isinstance(value, str) else 'json'}


def paper_prepare_inputs() -> dict:
    """Read the compact input/schema index. Resolve material_id references with paper_read_material."""
    result = _prepared_inputs(context())
    compact = {}
    for key, value in result.items():
        if key == 'input':
            compact[key] = {name: _input_reference('input:' + name, item)
                            if len(_material_text(item)) > 1000 else item for name, item in value.items()}
        elif key.endswith('_schema') or key in {'code_contract', 'plot_code_contract'}:
            compact[key] = _input_reference('contract:' + key, value)
        elif len(_material_text(value)) > 1000:
            compact[key] = _input_reference('contract:' + key, value)
        else:
            compact[key] = value
    compact['reference_reading'] = (
        'Objects with material_id are exact bound references, not the material/schema itself. '
        'Read them with paper_read_material(material_id=the returned ID, offset=0, limit=4000), '
        'continuing next_offset until null. Parse json references only after reading all pages. '
        'Read the required field schemas and actual evidence before submitting.')
    return compact


@json_arguments
def paper_read_material(material_id: str, offset: int = 0, limit: int = 16000) -> dict:
    """Read a bound material or returned input:/contract: reference. Continue until next_offset is null."""
    if offset < 0 or not 1 <= limit <= 64000:
        raise ValueError('读取范围不合法')
    ctx = context()
    pool = base_pool(ctx)
    if material_id.startswith(('input:', 'contract:')):
        namespace, key = material_id.split(':', 1)
        values = step_inputs(ctx, pool) if namespace == 'input' else _prepared_inputs(ctx, pool)
        if key not in values:
            raise ValueError('只能读取本步骤绑定的输入或契约引用')
        value = values[key]
        text = _material_text(value)
        return {'source': {'material_id': material_id, 'inputs_digest': digest(ctx.scope)},
                'offset': offset, 'text': text[offset:offset + limit],
                'next_offset': offset + limit if offset + limit < len(text) else None,
                'total_characters': len(text), 'source_sha256': hashlib.sha256(text.encode()).hexdigest()}
    # A reviewer sees the submitted manuscript and its references/figures, not upstream research.
    visible = step_inputs(ctx, pool) if ctx.step_id == 'peer_review' else None
    papers = (visible.get('papers', []) if visible is not None
              else (material(pool, 'papers') or {}).get('content', []))
    source = next((paper for paper in papers if paper['id'] == material_id), None)
    if source is None:
        item = (material(pool, material_id) if material_id in pool['current']
                and (visible is None or material_id in visible) else None)
        if item is None:
            raise ValueError('只能读取本步骤绑定的材料；请使用 material_catalog 的 material_id 或真实 paper.id')
        content = visible[material_id] if visible is not None else item['content']
        text = content if isinstance(content, str) else canonical(content).decode()
        source = {'kind': item['kind'], 'version': item['version'], 'source_refs': item['source_refs'], 'text': text}
    text = source.get('text') or source.get('abstract') or ''
    return {'source': {key: value for key, value in source.items() if key not in {'text', 'abstract'}},
            'offset': offset, 'text': text[offset:offset + limit],
            'next_offset': offset + limit if offset + limit < len(text) else None,
            'total_characters': len(text), 'source_sha256': hashlib.sha256(text.encode()).hexdigest()}


@json_arguments
def paper_search(query: str, count: int = 20, page: int = 1,
                 year_from: int | None = None, year_to: int | None = None) -> dict:
    """Search real academic sources. Returns signed source facts, not a model-written plan."""
    ctx = context()
    if ctx.step_id not in {'research_topic', 'refine_topic', 'literature_review', 'reading_reflection'}:
        raise ValueError('当前步骤不允许扩展检索')
    if not query.strip() or not 1 <= count <= 50 or not 1 <= page <= 3:
        raise ValueError('检索 query/count/page 超出范围')
    if year_from is not None and year_to is not None and year_from > year_to:
        raise ValueError('检索年份范围不合法')
    policy = ctx.values.get('paper_source_policy') or {}
    request = ctx.launch_request + '\n' + ctx.request
    import re
    if (policy.get('allow_academic_search') is False
            or re.search(r'仅(?:使用|分析|综述|基于)?(?:上传|附件|这些|所选)|'
                         r'只(?:使用|分析|基于)(?:上传|附件|这|所选)|'
                         r'only (?:the )?(?:attachments|uploaded|selected|these papers|this knowledge)', request, re.I)):
        raise ValueError('本次输入范围禁止扩展学术检索')
    if policy.get('year_from') is not None:
        year_from = max(year_from or policy['year_from'], policy['year_from'])
    if policy.get('year_to') is not None:
        year_to = min(year_to or policy['year_to'], policy['year_to'])
    if year_from is not None and year_to is not None and year_from > year_to:
        raise ValueError('检索年份与固定来源范围冲突')
    from .review.materials import _api_keys
    from .review.retrieval import unique_candidates
    from .review.search import search_papers
    result = search_papers(query, limit=min(count, 20), api_keys=_api_keys(),
                           year_from=year_from, year_to=year_to)
    warnings = [f'{run["source"]} 检索状态：{run["status"]}，仅使用实际返回的来源。'
                for run in result['sources'] if run['status'] != 'ok']
    start = (page - 1) * count
    more = len(result['papers']) > start + count
    papers = copy.deepcopy(result['papers'][start:start + count])
    for paper in papers:
        paper.update(text=paper.get('abstract') or '', reading={
            'content_type': 'search_preview' if paper['source'] == 'google_scholar' else 'abstract',
            'truncated': False, 'more': False})
    existing = (material(base_pool(ctx), 'papers') or {}).get('content', [])
    papers = unique_candidates(existing, papers)
    return {'papers': papers, 'warnings': warnings, 'more': bool(more),
            'receipt': receipt_reference(ctx, 'papers', papers)}


@json_arguments
def paper_read_paper(source_receipt: dict, paper_id: str) -> dict:
    """Fetch actual content for a source registered by paper_search, preserving reading scope."""
    ctx = context()
    papers = unseal(ctx, source_receipt, 'papers')
    paper = next((paper for paper in papers if paper['id'] == paper_id), None)
    if paper is None:
        raise ValueError('论文不属于本次检索回执')
    from .review.retrieval import sciverse_provider, read_sciverse
    warnings = []
    if paper.get('doc_id') or (paper.get('extra') or {}).get('doc_id'):
        result = read_sciverse(sciverse_provider(), paper, warnings)
    else:
        result = copy.deepcopy(paper) if paper.get('text') or paper.get('abstract') else None
        if result is not None and not result.get('text'):
            result['text'] = result['abstract']
            result.setdefault('reading', {'content_type': 'abstract', 'truncated': False, 'more': False})
    if result is None:
        raise contracts.MissingMaterials('未取得可读正文或摘要')
    return {'paper': result, 'warnings': warnings, 'receipt': receipt_reference(ctx, 'papers', [result])}


def _knowledge_call(name, arguments):
    import httpx
    from lazymind.config import config
    ctx = context()
    if name.startswith('knowledge_') and not (ctx.knowledge_scope and (
            ctx.knowledge_scope.get('dataset_ids') or ctx.knowledge_scope.get('documents'))):
        return {'status': 'no_selected_knowledge', 'documents': [],
                'message': 'No knowledge sources are bound to this attempt.'}
    response = httpx.post(str(config['core_service_url'] or config['core_api_url']).rstrip('/')
                          + '/internal/workflow-attempts/' + ctx.attempt_id + '/paper-data',
                          headers={'X-LazyMind-Internal-Token': _key().decode()},
                          json={'tool_name': name, 'arguments': arguments, 'lease_hash': ctx.lease_hash},
                          timeout=2 if name == 'execution_status' else 60)
    response.raise_for_status()
    envelope = response.json()
    if not envelope.get('ok'):
        raise ValueError('知识库读取失败')
    return envelope['result']['result']


@json_arguments
def paper_knowledge_documents(keyword: str = '', page_size: int = 20, page_token: str = '') -> dict:
    """List only documents in the frozen selected knowledge scope; no model or embedding call."""
    return _knowledge_call('knowledge_list_documents',
                           {'keyword': keyword, 'page_size': page_size, 'page_token': page_token})


@json_arguments
def paper_knowledge_read(document_id: str) -> dict:
    """Read prepared text from the fixed selected knowledge scope, with source version and receipt."""
    result = _knowledge_call('knowledge_read_document', {'document_id': document_id})
    if result.get('status') != 'ok':
        return result
    paper = {'id': 'kb-' + digest(result['document_id'])[:20], 'title': result['name'],
             'text': result['content']['text'], 'source': 'knowledge_base',
             'source_version': str(result['resource_version']),
             'reading': {'content_type': 'knowledge_base_document',
                         'truncated': result['content']['truncated']}}
    return {'paper': paper, 'receipt': receipt_reference(context(), 'papers', [paper])}


def cancellation_check(ctx):
    import time
    last_checked = 0.0

    def cancelled():
        nonlocal last_checked
        cancellation = _operation_cancel.get()
        if cancellation is not None and cancellation.exists():
            return True
        if not ctx.check_liveness or time.monotonic() - last_checked < 0.5:
            return False
        last_checked = time.monotonic()
        try:
            _knowledge_call('execution_status', {})
            return False
        except Exception:
            # Uncertain authority is not permission to keep executing user code.
            return True
    return cancelled


def execution_directory():
    root = Path(os.environ.get('LAZYLLM_HOME', str(Path.home() / '.lazyllm'))) / 'paper-execution-receipts'
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    if root.is_symlink():
        raise ValueError('执行回执目录不能是符号链接')
    return root


def operation_identifier(operation_id, tool_name, arguments):
    if operation_id and (tool_name or arguments is not None):
        raise ValueError('只能使用 operation_id 或原始工具参数查询')
    if not operation_id:
        import inspect
        if tool_name not in {'paper_analyze', 'paper_plot', 'paper_run_experiment'} or arguments is None:
            raise ValueError('请提供 operation_id 或原始 tool_name/arguments')
        function = globals()[tool_name]
        bound = inspect.signature(function).bind(**arguments)
        bound.apply_defaults()
        operation_id = digest({'scope': context().scope, 'tool': tool_name, 'arguments': bound.arguments})
    return operation_id


def operation_record(operation_id):
    import re
    if not re.fullmatch(r'[0-9a-f]{64}', operation_id):
        raise ValueError('操作 ID 不合法')
    path = execution_directory() / (operation_id + '.json')
    try:
        saved = json.loads(path.read_bytes())
    except FileNotFoundError as exc:
        raise ValueError('未找到当前操作，请核对原始代码和参数；不能自动重跑未知操作') from exc
    if saved.get('scope_digest') != digest(context().scope):
        raise ValueError('操作不属于当前步骤和输入版本')
    return saved


@json_arguments
def paper_execution_status(operation_id: str = '', tool_name: str = '', arguments: dict | None = None) -> dict:
    """Query an actual computation after an interrupted response, without re-executing it."""
    operation_id = operation_identifier(operation_id, tool_name, arguments)
    saved = operation_record(operation_id)
    result = {'operation_id': operation_id, 'state': saved['state']}
    if saved['state'] == 'completed':
        result['result'] = unseal(context(), saved['result'], 'operation')
    elif saved['state'] in {'failed', 'cancelled'}:
        result['error'] = unseal(context(), saved['error'], 'operation_error')
    else:
        result['state'] = 'running_or_unknown'
    return result


@json_arguments
def paper_cancel_execution(operation_id: str = '', tool_name: str = '', arguments: dict | None = None) -> dict:
    """Cancel only a bound computation; never stop, review or complete the Workflow."""
    operation_id = operation_identifier(operation_id, tool_name, arguments)
    saved = operation_record(operation_id)
    if saved['state'] == 'running':
        (execution_directory() / (operation_id + '.cancel')).touch(mode=0o600)
    return {'operation_id': operation_id, 'cancel_requested': saved['state'] == 'running'}


def save_operation(path, value):
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as staged:
        staged.write(canonical(value))
        staged.flush()
        os.fsync(staged.fileno())
    os.replace(staged.name, path)


def execute_once(function):
    """A repeated identical computation never starts a second process after a lost response."""
    @wraps(function)
    def call(*args, **kwargs):
        import inspect
        from filelock import FileLock, Timeout
        ctx = context()
        bound = inspect.signature(function).bind(*args, **kwargs)
        bound.apply_defaults()
        key = digest({'scope': ctx.scope, 'tool': function.__name__, 'arguments': bound.arguments})
        root = execution_directory()
        record = root / (key + '.json')
        scope_digest = digest(ctx.scope)
        try:
            with FileLock(str(root / (key + '.lock')), timeout=0):
                if cancellation_check(ctx)():
                    raise ValueError('执行已停止或授权失效')
                if record.exists():
                    saved = json.loads(record.read_text())
                    if saved.get('state') in {'failed', 'cancelled'}:
                        raise ValueError(unseal(ctx, saved['error'], 'operation_error'))
                    if saved.get('state') != 'completed':
                        raise ValueError('相同操作正在执行或结果未知；不得自动重复运行，请查询或重新领取步骤')
                    return unseal(ctx, saved['result'], 'operation')
                save_operation(record, {'state': 'running', 'scope_digest': scope_digest})
                token = _operation_cancel.set(root / (key + '.cancel'))
                try:
                    result = function(*args, **kwargs)
                    result['operation_id'] = key
                except Exception as exc:
                    from concurrent.futures import CancelledError
                    state = 'cancelled' if isinstance(exc, CancelledError) else 'failed'
                    save_operation(record, {'state': state, 'scope_digest': scope_digest,
                                            'error': seal(ctx, 'operation_error', str(exc))})
                    raise
                finally:
                    _operation_cancel.reset(token)
                if (root / (key + '.cancel')).exists() or cancellation_check(ctx)():
                    raise ValueError('执行结果已过期，不得提交')
                save_operation(record, {'state': 'completed', 'scope_digest': scope_digest,
                                        'result': seal(ctx, 'operation', result)})
                return result
        except Timeout as exc:
            raise ValueError('相同操作正在执行，不得启动重复任务；可用输入与步骤契约计算出的 operation_id 查询') from exc
    return call


@json_arguments
@execute_once
def paper_analyze(code: str, plan: dict) -> dict:
    """Run agent-authored analysis on actual bound data in the existing process sandbox."""
    ctx = context()
    if ctx.step_id != 'data_analysis':
        raise ValueError('统计工具仅用于数据分析步骤')
    inputs = step_inputs(ctx, base_pool(ctx))
    from .analysis.plan import validate_plan
    from .analysis.schema import table_map
    from .analysis.model import execution_plan
    from .analysis.generated_executor import execute
    from .analysis.pipeline import build_analysis
    plan = validate_plan(plan, inputs)
    raw = execute({'tables': table_map(inputs), 'plan': execution_plan(inputs, plan), 'code': code, 'seed': 42},
                  cancelled=cancellation_check(ctx))
    analysis = build_analysis(inputs, plan, raw['results'])
    provenance = material(base_pool(ctx), 'result_meta')
    if provenance:
        from .experiment.handoff import mark_analysis
        analysis = mark_analysis({'analysis': analysis, 'charts': [], 'is_demo': False},
                                 provenance['content'])['analysis']
    record = {'analysis': analysis, 'code': code, 'plan': plan, 'code_sha256': hashlib.sha256(code.encode()).hexdigest(),
              'versions': raw['versions'], 'seed': raw['seed']}
    return {'receipt': receipt_reference(ctx, 'analysis', record), **record}


@json_arguments
@execute_once
def paper_run_experiment(code: str, configuration: dict) -> dict:
    """Execute explicitly authorized CPU code on bound data; never grant its own permission."""
    ctx = context()
    if ctx.step_id != 'experiment':
        raise ValueError('实验工具仅用于实验步骤')
    preference = ctx.values.get('experiment_execution')
    if preference is None or value_text(preference) != 'allow_local':
        raise ValueError('本次任务未明确授权运行实验')
    from .experiment.executor import validate_code
    from .execution import container
    validate_code(code)
    inputs = step_inputs(ctx, base_pool(ctx))
    actual_data = inputs.get('source_data') or inputs.get('data')
    if actual_data is None and (ctx.values.get('paper_execution_policy') or {}).get('allow_simulation') is not True:
        raise ValueError('没有绑定真实实验输入，且本次任务未授权模拟数据')
    import time
    started = time.monotonic()
    raw = container.execute({'code': code, 'config': configuration, 'data': actual_data},
                            Path(__file__).with_name('experiment') / 'worker.py', 30, label='实验代码',
                            cancelled=cancellation_check(ctx))
    from .experiment.results import records_to_table
    from .experiment.schema import code_column_types
    result = {'results': records_to_table(raw['records'], code_column_types(code)),
              'code': code, 'configuration': configuration, 'logs': raw['logs'],
              'elapsed_seconds': time.monotonic() - started, 'input_data_sha256': digest(actual_data)}
    return {**result, 'receipt': receipt_reference(ctx, 'experiment', result)}


@json_arguments
@execute_once
def paper_plot(code: str, specification: dict, analysis_receipt: dict) -> dict:
    """Render real SVG/PNG/PDF from bound data and signed executed analysis."""
    ctx = context()
    inputs = step_inputs(ctx, base_pool(ctx))
    record = unseal(ctx, analysis_receipt, 'analysis')
    from .analysis.schema import table_map, Chart
    from .plotting.code import validate_code
    from .plotting.executor import execute_plot
    validate_code(code)
    columns = specification.get('columns') or []
    tables = table_map(inputs)
    table_id = columns[0]['table_id'] if columns else None
    if table_id not in tables:
        raise ValueError('图表必须引用绑定的完整数据表')
    from .analysis.results import result_rows
    # Historical accepted findings may contain prose calculations instead of row metadata.
    try:
        rows = result_rows(record['analysis'])
    except (json.JSONDecodeError, KeyError, TypeError):
        rows = []
    statistics = {**record['analysis'], 'results': rows}
    raw = execute_plot({'code': code, 'table': tables[table_id], 'statistics': statistics,
                        'spec': specification, 'data_sha256': record['analysis']['data_sha256']},
                       cancelled=cancellation_check(ctx))
    chart_fields = {key: specification[key] for key in Chart.model_fields if key in specification}
    chart = Chart.model_validate({**chart_fields, 'svg': raw['svg']}).model_dump()
    result = {'chart': chart, 'exports': raw.get('exports', {})}
    if _context.get() is None and result['exports'].get('png'):
        from lazymind.chat.engine.subagent.context import require_context
        native = require_context()
        directory = tempfile.mkdtemp(prefix='paper-chart-preview-', dir=native.workspace_path)
        preview = Path(directory) / (chart['id'] + '.png')
        preview.write_bytes(base64.b64decode(result['exports']['png'], validate=True))
        result['native_preview_path'] = str(preview)
    receipt = receipt_reference(ctx, 'chart', result)
    public = {'chart': {key: value for key, value in chart.items() if key != 'svg'}, 'receipt': receipt,
              'candidate_handoff': ('Pass this exact receipt object in output.charts and receipts; '
                                    'do not copy SVG bytes.')}
    if 'native_preview_path' in result:
        public['native_preview_path'] = result['native_preview_path']
    else:
        exports = {'svg': base64.b64encode(chart['svg'].encode()).decode(), **result['exports']}
        media = {'svg': 'image/svg+xml', 'png': 'image/png', 'pdf': 'application/pdf'}
        public['files'] = [
            {'name': chart['id'] + '.' + extension, 'content_base64': encoded, 'mime_type': media[extension]}
            for extension, encoded in exports.items()]
    return public


def _file(slot, name, raw, seq=1):
    if isinstance(raw, str):
        raw = raw.encode()
    return {'slot': slot, 'seq': seq, 'content_type': 'file',
            'value': {'storage': 'inline_base64', 'name': name, 'size': len(raw),
                      'sha256': 'sha256:' + hashlib.sha256(raw).hexdigest(),
                      'content_base64': base64.b64encode(raw).decode()}}


def _manifest(artifacts):
    result = []
    for item in artifacts:
        value = item['value']
        raw = (base64.b64decode(value['content_base64'], validate=True)
               if item['content_type'] == 'file' else canonical(value))
        entry = {'slot': item['slot'], 'seq': item.get('seq', 1),
                 'content_type': item['content_type'], 'sha256': hashlib.sha256(raw).hexdigest()}
        if item['content_type'] == 'file':
            entry['name'] = value.get('name') or value.get('filename')
        result.append(entry)
    return sorted(result, key=lambda item: (item['slot'], item['seq']))


def _accepted_papers(pool, receipts):
    known = {paper['id']: copy.deepcopy(paper) for paper in (material(pool, 'papers') or {}).get('content', [])}
    for receipt in receipts:
        if receipt.get('payload', {}).get('kind') == 'papers':
            for paper in unseal(context(), receipt, 'papers'):
                known[paper['id']] = paper
    return known


def analysis_input_digest(inputs):
    # Presentation instructions may change on redraw; scientific inputs may not.
    return digest({key: value for key, value in inputs.items() if key not in {'request', 'constraints'}})


def resolve_bound_tables(result, inputs):
    """Resolve explicit table IDs without making the model reproduce measurements."""
    if not isinstance(result, dict) or set(result) not in ({'table_id'}, {'table_ids'}):
        return result
    identifiers = [result['table_id']] if 'table_id' in result else result['table_ids']
    if (not isinstance(identifiers, list) or not identifiers
            or any(not isinstance(item, str) for item in identifiers)
            or len(identifiers) != len(set(identifiers))):
        raise ValueError('必须选择唯一的真实绑定数据表 ID')
    for original in (inputs.get('source_data'), inputs.get('data')):
        if not isinstance(original, dict):
            continue
        tables = {item['id']: item for item in original.get('tables', [])}
        if all(identifier in tables for identifier in identifiers):
            return {'tables': [copy.deepcopy(tables[identifier]) for identifier in identifiers]}
    actual = [item['id'] for original in (inputs.get('source_data'), inputs.get('data'))
              if isinstance(original, dict) for item in original.get('tables', [])]
    raise ValueError('选择的数据表不属于当前固定输入，实际表 ID: ' + ', '.join(actual))


def matches_bound_data(result, inputs):
    for original in (inputs.get('source_data'), inputs.get('data')):
        if original is None:
            continue
        if result == original:
            return True
        if isinstance(result, dict) and isinstance(original, dict) and set(result) == {'tables'}:
            tables = original.get('tables', [])
            selected = result['tables']
            if selected and all(item in tables for item in selected):
                return True
    return False


def apply_revision(inputs, plan_value, replacements_value):
    original = inputs['manuscript']
    if plan_value is None and replacements_value is None:
        return original
    from lazyllm.tools.writer.data_models.revision import MarkdownModifyPlan
    from .writing.revise_document import _operation_ranges, _bounded_replacements, _validate_review_plan
    from lazyllm.tools.writer.utils.serialization import markdown_section_range
    plan = MarkdownModifyPlan.model_validate(plan_value)
    selected = inputs.get('selected_issue_ids') or []
    if selected:
        issues = [issue for issue in inputs['review_data']['issues'] if issue['id'] in selected]
        if len(issues) != len(set(selected)):
            raise ValueError('修订选择包含未知或过期 issue')
        _validate_review_plan(plan, issues)
    ranges = [bounds for operation in _operation_ranges(original, plan) for bounds in operation]
    if plan.title_instruction:
        import re
        title = re.search(r'(?m)^# +[^\r\n]+', original)
        if title is None:
            raise ValueError('原稿没有可修改的标题')
        ranges.append(title.span())
    replacements, _ = _bounded_replacements(original, ranges, replacements_value)
    edits = []
    for item in replacements['replacements']:
        ref = item.get('content_ref') or {}
        left, right = ((0, len(original)) if not ref or ref.get('document_root') else
                       markdown_section_range(original, ref['heading_path'], ref.get('occurrence', 1)))
        position = original.index(item['old_string'], left, right)
        edits.append((position, position + len(item['old_string']), item['new_string']))
    result = original
    for left, right, text in sorted(edits, reverse=True):
        result = result[:left] + text + result[right:]
    return result


def normalized_revision(inputs, plan, replacements):
    from .publication import (protect_reference_region, validate_unmanaged_additions,
                              publish_charts, publish_citations, figure_catalog, prose)
    manuscript = apply_revision(inputs, plan, replacements)
    protect_reference_region(inputs['manuscript'], manuscript)
    validate_unmanaged_additions(inputs['manuscript'], manuscript)
    if manuscript == inputs['manuscript']:
        return manuscript
    from .writing.common import validate_result
    validate_result({'manuscript': manuscript}, inputs)
    manuscript = publish_charts(manuscript, figure_catalog(inputs.get('charts', []), inputs.get('media_assets')))
    if '[文献:' in prose(manuscript):
        manuscript = publish_citations(manuscript, inputs.get('papers', []))
    return manuscript


@json_arguments
def paper_preview_revision(modify_plan: dict, replacements: dict) -> dict:
    """Apply exact scoped patches and return the final bytes for the Agent's fresh review; no model or save."""
    ctx = context()
    if ctx.step_id != 'revise_document':
        raise ValueError('修订预览仅用于修订步骤')
    manuscript = normalized_revision(step_inputs(ctx, base_pool(ctx)), modify_plan, replacements)
    return {'manuscript': manuscript, 'sha256': hashlib.sha256(manuscript.encode()).hexdigest(),
            'persisted': False, 'next': 'Review this exact manuscript and supply a fresh review_result.'}


@json_arguments
def paper_prepare_candidate(output: dict, receipts: list[dict]) -> dict:
    """Validate agent-authored business output, build lineage/artifacts and sign the exact bundle.

    Does not generate content, run another agent, approve a review or persist a step.
    External controllers publish every returned artifact and complete normally.
    """
    ctx, receipts = context(), [resolve_receipt(item) for item in (receipts or [])]
    pool = base_pool(ctx) if ctx.step_id != 'finish' else None
    output = copy.deepcopy(output)
    selected_ids = output.pop('selected_paper_ids', [])
    source_imports = []
    if selected_ids:
        if ctx.step_id == 'finish':
            raise ValueError('结束步骤不导入新的论文来源')
        if (not isinstance(selected_ids, list) or any(not isinstance(item, str) for item in selected_ids)
                or len(selected_ids) != len(set(selected_ids))):
            raise ValueError('selected_paper_ids 必须是唯一的真实论文 ID 列表')
        known = _accepted_papers(pool, receipts)
        if any(identifier not in known for identifier in selected_ids):
            raise ValueError('选择的论文缺少真实绑定或检索回执')
        if ctx.step_id == 'literature_review' and 'papers' not in output:
            output['papers'] = [copy.deepcopy(known[identifier]) for identifier in selected_ids]
            for paper in output['papers']:
                if not paper.get('citation'):
                    fields = [paper.get('title'), str(paper['year']) if paper.get('year') else None,
                              'https://doi.org/' + paper['doi'] if paper.get('doi') else paper.get('url')]
                    paper['citation'] = '. '.join(' '.join(str(item).split()) for item in fields if item)
        previous = material(pool, 'papers')
        papers = copy.deepcopy(previous['content'] if previous else [])
        by_id = {paper['id']: paper for paper in papers}
        by_id.update({identifier: known[identifier] for identifier in selected_ids})
        if list(by_id.values()) != papers:
            source_imports.append(add_material(
                pool, 'papers', list(by_id.values()),
                [copy.deepcopy(pool['current']['papers'])] if previous else [],
                paper_source_ids(list(by_id.values()))))
    if ctx.step_id == 'finish':
        artifacts = [_file('finish_document', '结束说明.md', '# 本次流程已结束\n已有产物保留。')]
    else:
        inputs = step_inputs(ctx, pool)
        module = contracts.ALIASES.get(ctx.step_id, ctx.step_id)
        generated = copy.deepcopy(output)
        redraw_only = generated.pop('redraw_only', False) if module == 'data_analysis' else False
        if module == 'data_analysis':
            proposed = generated.get('analysis')
            if isinstance(proposed, dict) and ('receipt' in proposed or 'receipt_id' in proposed):
                overrides = {}
                reference = proposed
                if 'receipt' in proposed:
                    if not set(proposed) <= {'receipt', 'summary', 'limitations'}:
                        raise ValueError('分析回执只允许补充 summary 和 limitations，不得替换执行事实')
                    reference = proposed['receipt']
                    overrides = {key: proposed[key] for key in ('summary', 'limitations') if key in proposed}
                generated['analysis'] = {**unseal(ctx, reference, 'analysis')['analysis'], **overrides}
            generated['charts'] = [unseal(ctx, item, 'chart')['chart']
                                   if isinstance(item, dict) and 'receipt_id' in item else item
                                   for item in generated.get('charts', [])]
        if ctx.step_id in {'peer_review', 'revise_document'}:
            from .peer_review import build_output
            from .review.schema import ReviewResult
            from .review.schema import text_digest
            if ctx.step_id == 'revise_document':
                manuscript = normalized_revision(
                    inputs, generated.pop('modify_plan', None), generated.pop('replacements', None))
                proposed = generated.pop('manuscript', manuscript)
                if proposed != manuscript:
                    raise ValueError('修订稿必须与通过范围检查的补丁结果一致')
                contracts.text(manuscript, 'manuscript')
                review_inputs = {**inputs, 'manuscript': manuscript}
            else:
                manuscript, review_inputs = None, inputs
            from .review.schema import LiteratureReadings
            readings = LiteratureReadings.model_validate(
                generated.pop('literature_readings', {'readings': []})).readings
            reviewed = build_output(ReviewResult.model_validate(generated.pop('review_result')),
                                    review_inputs, readings=readings)
            if generated:
                raise ValueError('审稿只接受 review_result 和修订时的 manuscript')
            generated = reviewed if manuscript is None else {'manuscript': manuscript, **reviewed}
            if manuscript is not None:
                if reviewed['review_data']['manuscript_sha256'] != text_digest(manuscript):
                    raise ValueError('修订后稿件摘要不一致')
        else:
            generated.setdefault('is_demo', False)
        if 'papers' in generated:
            from .review.retrieval import unique_candidates
            if len(unique_candidates([], generated['papers'])) != len(generated['papers']):
                raise ValueError('选定文献含重复来源，请按真实身份去重')
            known = _accepted_papers(pool, receipts)
            for paper in generated['papers']:
                original = known.get(paper.get('id'))
                if original is None:
                    raise ValueError('引用论文必须来自绑定材料或真实检索回执')
                for key in ('title', 'text', 'abstract', 'doi', 'url', 'authors', 'year', 'venue',
                            'source', 'source_version', 'reading'):
                    if paper.get(key) != original.get(key):
                        raise ValueError(f'不能修改来源事实 paper.{key}')
                if original.get('review_role') == 'background' and paper.get('review_role') != 'background':
                    raise ValueError('明确的背景材料不能升级为学术论文')
        if module == 'experiment':
            meta = generated.get('result_meta') or {}
            if (meta.get('data_origin') == 'user_supplied' and meta.get('execution_status') == 'not_run'
                    and generated.get('experiment', {}).get('executed') is False
                    and not generated.get('code_files')):
                if generated.get('code_files') == '':
                    generated['code_files'] = []
                generated['experiment']['results'] = resolve_bound_tables(
                    generated['experiment'].get('results'), inputs)
            if meta.get('data_origin') == 'synthetic':
                policy = ctx.values.get('paper_execution_policy') or {}
                supplied = inputs.get('input_is_demo') and matches_bound_data(
                    generated['experiment']['results'], inputs) and not generated.get('code_files')
                if policy.get('allow_simulation') is not True and not supplied:
                    raise ValueError('本次任务未明确允许生成模拟数据')

            if meta.get('execution_status') == 'succeeded':
                recorded = [unseal(ctx, item, 'experiment') for item in receipts
                            if item.get('payload', {}).get('kind') == 'experiment']
                record = next((item for item in recorded if item['results'] == generated['experiment']['results']), None)
                if record is None:
                    raise ValueError('实测结果必须匹配真实实验回执')
                files = {item['path']: item['content'] for item in generated.get('code_files', [])}
                if (files.get('experiment.py') != record['code']
                        or json.loads(files.get('config.json', '{}')) != record['configuration']):
                    raise ValueError('实验代码和配置必须匹配实际执行回执')
                meta['execution_log'] = record['logs']
                meta['elapsed_seconds'] = record['elapsed_seconds']
            elif meta.get('data_origin') == 'user_supplied':
                if not matches_bound_data(generated['experiment']['results'], inputs):
                    raise ValueError('已有数据整理不能改变原始数值；请用 experiment.results={table_ids:[真实ID]} '
                                     '引用 table_catalog，不要重新抄写、转换原表；code_files 应为 JSON 空数组 []')
        if module == 'data_analysis':
            analyses = [unseal(ctx, item, 'analysis')['analysis'] for item in receipts
                        if item.get('payload', {}).get('kind') == 'analysis']
            previous = (material(pool, 'analysis') or {}).get('content')
            if previous is not None:
                analyses.append(previous)
            if redraw_only:
                if previous is None or generated['analysis'] != previous:
                    raise ValueError('仅重绘必须保留冻结分析，不得重新计算或修改统计解释')
                from .analysis.schema import binding
                expected = binding(inputs)
                if any(previous[field] != expected[field] for field in ('data_sha256', 'row_count', 'data_sources')):
                    raise ValueError('数据已更新，旧分析不能用于重绘')
                prior_run = next((item for item in reversed(pool['history']) if item['node'] == 'F4'), {})
                if (prior_run.get('analysis_input_sha256')
                        and prior_run['analysis_input_sha256'] != analysis_input_digest(inputs)):
                    raise ValueError('研究上下文已更新，旧分析不能用于重绘')
                prior = {item['id']: item for item in (material(pool, 'charts') or {}).get('content', [])}
                current = {item['id']: item for item in generated['charts']}
                if prior.keys() != current.keys() or any(
                        current[key][field] != prior[key][field]
                        for key in prior for field in ('columns', 'finding_ids')):
                    raise ValueError('仅重绘必须保留图身份、数据列和统计结论关联')

            def numeric(value):
                result = copy.deepcopy(value)
                for key in ('summary', 'limitations'):
                    result.pop(key, None)
                for finding in result.get('findings', []):
                    finding.pop('statement', None)
                return result
            if not any(numeric(item) == numeric(generated['analysis']) for item in analyses):
                raise ValueError('分析数值必须来自真实执行或冻结的已有分析')
            charts = {(chart['id'], digest(chart)) for chart in (material(pool, 'charts') or {}).get('content', [])}
            for item in receipts:
                if item.get('payload', {}).get('kind') == 'chart':
                    chart = unseal(ctx, item, 'chart')['chart']
                    charts.add((chart['id'], digest(chart)))
            if any((chart['id'], digest(chart)) not in charts for chart in generated['charts']):
                raise ValueError('图表必须来自真实绘图回执或未修改的已有图表')
        contracts.validate_output(module, generated, inputs=None if redraw_only else inputs)
        if module in {'research_topic', 'reading_reflection'}:
            from .publication import validate_citations
            field = 'question' if module == 'research_topic' else 'reflection'
            validate_citations(generated[field], inputs.get('papers', []))
        if module == 'literature_review':
            from .publication import normalize_source_citations, validate_citations, publish_citations
            papers = [paper for paper in generated['papers'] if paper.get('review_role') != 'background']
            body = normalize_source_citations(generated['evidence'], papers,
                                              material_source_ids=paper_source_ids(generated['papers']))
            validate_citations(body, papers)
            generated['evidence'] = publish_citations(body, papers)
        if module in {'write_outline', 'write_document', 'revise_document'}:
            from .writing.common import _heading_signature, validate_result
            if module == 'write_document':
                validate_result(generated, inputs)
            else:
                _heading_signature(generated['outline' if module == 'write_outline' else 'manuscript'])
            from .publication import validate_citations, validate_chart_references, figure_catalog
            from .writing.requirements import validate_required_sections
            body = generated['outline' if module == 'write_outline' else 'manuscript']
            validate_citations(body, inputs.get('papers', []))
            validate_chart_references(body, figure_catalog(inputs.get('charts', []), inputs.get('media_assets')))
            validate_required_sections(body, inputs.get('constraints', {}))
        if module == 'write_document':
            import re
            from .publication import split_references, prose
            target = inputs.get('constraints', {}).get('word_target')
            if target:
                body = prose(split_references(generated['manuscript'])[0])
                count = len(re.findall(r"[A-Za-z0-9]+(?:[-'’][A-Za-z0-9]+)*", body))
                if inputs['constraints']['paper_language'] == 'zh-CN':
                    count += len(re.findall(r'[\u4e00-\u9fff]', body))
                if not 0.9 * target <= count <= 1.1 * target:
                    raise ValueError(f'稿件篇幅 {count} 与目标 {target} 不符，允许 ±10%；请在已有证据范围修复')
            from .publication import publish_charts, publish_citations, figure_catalog
            generated['manuscript'] = publish_charts(
                generated['manuscript'], figure_catalog(inputs.get('charts', []), inputs.get('media_assets')))
            generated['manuscript'] = publish_citations(
                generated['manuscript'], inputs.get('papers', []),
                paper_language=inputs.get('constraints', {}).get('paper_language'))
        if 'constraints' in inputs:
            pool['constraints'] = copy.deepcopy(inputs['constraints'])
        relevant = contracts.FIELDS[module]
        derived = [copy.deepcopy(pool['current'][key]) for key in relevant if key in pool['current']]
        sources = {source for key in relevant if material(pool, key) for source in material(pool, key)['source_refs']}
        sources.update(paper_source_ids(generated.get('papers')))
        main = {'research_topic': 'question', 'literature_review': 'evidence', 'experiment': 'experiment',
                'data_analysis': 'analysis', 'write_outline': 'outline', 'write_document': 'manuscript',
                'peer_review': 'review', 'revise_document': 'manuscript', 'reading_reflection': 'reflection'}[module]
        is_demo = generated['is_demo'] or any(material(pool, key)['mock'] for key in relevant if material(pool, key))
        document = (generated[main] if isinstance(generated[main], str) else render_material(main, generated[main]))
        if main == 'analysis':
            from .analysis.render import render
            document = render(generated[main])
        document = with_source_notice(document, is_demo, generated.get('result_meta')
                                      or (material(pool, 'result_meta') or {}).get('content'))
        if main == 'review':
            generated['review'] = document
            from .review.schema import text_digest
            generated['review_data']['review_sha256'] = text_digest(document)
        if module == 'data_analysis' and redraw_only and material(pool, 'manuscript'):
            from .publication import figure_catalog, refresh_chart_sources, publish_charts
            media = (material(pool, 'media_assets') or {}).get('content')
            generated['manuscript'] = refresh_chart_sources(
                material(pool, 'manuscript')['content'],
                figure_catalog((material(pool, 'charts') or {}).get('content', []), media),
                figure_catalog(generated['charts'], media))
            generated['manuscript'] = publish_charts(
                generated['manuscript'], figure_catalog(generated['charts'], media))
            derived.append(copy.deepcopy(pool['current']['manuscript']))
            for kind in ('review', 'review_data'):
                pool['current'].pop(kind, None)
            pool['selected_issue_ids'] = []
        output_kinds = list(contracts.OUTPUTS[module])
        if redraw_only and 'manuscript' in generated:
            output_kinds.append('manuscript')
        refs = source_imports + [
            add_material(pool, kind, generated[kind], derived, sorted(sources), is_demo=is_demo)
            for kind in output_kinds if generated[kind] is not None]
        pool['sequence'] += 1
        pool['history'].append({'node': STEP_NODE[ctx.step_id], 'step': ctx.step_id,
                                'request': ctx.request, 'inputs': derived, 'outputs': refs})
        if module == 'data_analysis':
            pool['history'][-1]['analysis_input_sha256'] = analysis_input_digest(inputs)
            pool['history'][-1]['analysis_execution'] = [
                {key: record[key] for key in ('code_sha256', 'versions', 'seed') if key in record}
                for item in receipts if item.get('payload', {}).get('kind') == 'analysis'
                for record in [unseal(ctx, item, 'analysis')]]
        if module in {'peer_review', 'revise_document'}:
            pool['selected_issue_ids'] = []
        validate_pool(pool)
        artifacts = [_file(ctx.step_id + '_pool', ctx.step_id + '_pool.json', canonical(pool)),
                     _file(ctx.step_id + '_document', ctx.step_id + '.md', document)]
        if module == 'peer_review':
            artifacts.append(_file('peer_review_data', 'review.json', canonical(generated['review_data'])))
        if module == 'revise_document':
            artifacts.append(_file('revision_fact_check_document', '修订后事实核对.md', generated['review']))
        if module == 'experiment':
            artifacts.extend([
                _file('experiment_code', 'experiment-code.json', canonical(generated['code_files'])),
                _file('experiment_results', 'experiment-results.json', canonical(generated['result_meta'])),
            ])
        if module == 'data_analysis':
            executions = [unseal(ctx, item, 'analysis') for item in receipts
                          if item.get('payload', {}).get('kind') == 'analysis']
            artifacts.append(_file('analysis_execution', 'analysis-execution.json',
                                   canonical({'redraw_only': redraw_only, 'executions': executions})))
            seq = 1
            for chart in generated['charts']:
                artifacts.append(_file('analysis_chart', chart['id'] + '.svg', chart['svg'], seq))
                seq += 1
                for receipt in receipts:
                    if receipt.get('payload', {}).get('kind') != 'chart':
                        continue
                    plotted = unseal(ctx, receipt, 'chart')
                    if plotted['chart'] != chart:
                        continue
                    for extension, encoded in sorted(plotted['exports'].items()):
                        artifacts.append(_file('analysis_chart', chart['id'] + '.' + extension,
                                               base64.b64decode(encoded, validate=True), seq))
                        seq += 1
                    break
            if redraw_only and 'manuscript' in generated:
                artifacts.append(_file('redrawn_manuscript_document', '重画后的论文.md', generated['manuscript']))
    manifest = _manifest(artifacts)
    artifacts.append({'slot': ctx.step_id + '_receipt', 'seq': 1, 'content_type': 'json',
                      'value': seal(ctx, 'publication', manifest)})
    return {'status': 'validated', 'artifacts': artifacts,
            'quality_owner': 'executing_agent', 'notice': '程序校验不代表科学或视觉检查通过。'}


def verify_publication(ctx, artifacts):
    receipts = [item for item in artifacts if item['slot'] == ctx.step_id + '_receipt']
    if len(receipts) != 1:
        raise ValueError('缺少唯一的论文验收回执')
    manifest = unseal(ctx, decode_value(receipts[0]['value']), 'publication')
    actual = _manifest([item for item in artifacts if item is not receipts[0]])
    if manifest != actual:
        raise ValueError('提交产物与校验通过的候选内容不一致')
    return {'valid': True}


@json_arguments
def paper_submit_candidate(output: dict, receipts: list[dict]) -> dict:
    """Native-only publication adapter; external agents use prepare and controller publication."""
    if _context.get() is not None:
        raise ValueError('外部 worker 不能保存或完成步骤')
    result = paper_prepare_candidate(output, receipts)
    from lazymind.chat.engine.subagent.context import require_context
    from lazymind.chat.engine.subagent.tools import save_artifacts
    native = require_context()
    artifacts = []
    directory = tempfile.mkdtemp(prefix='paper-candidate-', dir=native.workspace_path)
    # Core consumes artifact events asynchronously; Runtime owns workspace cleanup.
    if directory:
        for item in result['artifacts']:
            value = item['value']
            if item['content_type'] == 'file':
                path = Path(directory) / value['name']
                path.write_bytes(base64.b64decode(value['content_base64'], validate=True))
                value = str(path)
            artifacts.append({'key': item['slot'], 'content_type': item['content_type'], 'value': value,
                              })
        saved = [save_artifacts(artifacts[start:start + 50]) for start in range(0, len(artifacts), 50)]
    return {'status': 'completed', 'saved': saved,
            '_agent_control': {'stop': True, 'reason': 'paper_candidate_submitted',
                               'final_text': '产物已保存，请在工作流中查看。'}}


@json_arguments
def paper_read_result(reference: str, offset: int = 1, limit: int = 2000) -> dict:
    """Page an exact native tool-spill URI. Offset is a 1-based line number; limit is 1..4000 lines."""
    if _context.get() is not None or not reference.startswith('workspace://tool_spills/'):
        raise ValueError('只能读取当前原生执行返回的 tool_spills 引用')
    if offset < 1 or not 1 <= limit <= 4000:
        raise ValueError('读取范围不合法：offset 是从 1 开始的行号，limit 必须在 1..4000 行之间')
    from lazymind.chat.engine.tools.file_resources.tools import read_file_resource
    return read_file_resource(reference, offset=offset, limit=limit)
