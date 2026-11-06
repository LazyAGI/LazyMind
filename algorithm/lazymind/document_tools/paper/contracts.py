"""Business-only contracts. Storage, versions and workflow routing stay outside."""
import copy
import hashlib
import json
import re
import math

from .analysis.validation import validate_svg

from .review.schema import SavedReview, text_digest
from .experiment.schema import ResultMeta, code_column_types, validate_code_files, validate_result_meta
from .experiment.results import validate_table

FIELDS = {
    'research_topic': ('papers', 'question', 'evidence', 'writing_materials'),
    'literature_review': ('papers', 'question', 'evidence'),
    'experiment': ('papers', 'question', 'evidence', 'data', 'source_data', 'writing_materials'),
    'data_analysis': ('data', 'experiment', 'data_context', 'study_design', 'result_meta'),
    'write_document': ('papers', 'question', 'evidence', 'analysis', 'charts', 'outline', 'manuscript',
                       'review', 'review_data', 'selected_issue_ids', 'writing_materials', 'media_assets'),
    'peer_review': ('manuscript', 'evidence', 'analysis', 'charts', 'papers', 'result_meta', 'writing_materials'),
    'reading_reflection': ('papers', 'evidence'),
}
FIELDS['write_outline'] = FIELDS['write_document']
FIELDS['revise_document'] = (*FIELDS['write_document'], 'result_meta')
OUTPUTS = {
    'research_topic': ('question',), 'literature_review': ('evidence', 'papers'),
    'experiment': ('experiment', 'code_files', 'result_meta'), 'data_analysis': ('analysis', 'charts'),
    'write_outline': ('outline',), 'write_document': ('manuscript',), 'peer_review': ('review', 'review_data'),
    'reading_reflection': ('reflection',),
}
OUTPUTS['revise_document'] = ('manuscript', 'review', 'review_data')
ALIASES = {'refine_topic': 'research_topic'}
MODES = {'research_topic': 'create', 'refine_topic': 'refine',
         'write_outline': 'draft', 'write_document': 'draft', 'revise_document': 'revise'}
KINDS = set().union(*FIELDS.values(), *OUTPUTS.values()) - {'selected_issue_ids'}
# The workflow uses these capabilities when assembling inputs and reading requests.
CONSTRAINED_MODULES = {'write_outline', 'write_document', 'revise_document', 'data_analysis'}
CURRENT_REQUEST_MODULES = {'write_outline', 'write_document', 'revise_document', 'peer_review'}
DEFAULT_CONSTRAINTS = {
    'paper_language': 'zh-CN', 'word_target': None, 'paper_type': None,
    'citation_style': 'GB/T 7714', 'focus': [], 'required_sections': [],
}

REQUIRED_SECTIONS = {'abstract', 'introduction', 'methods', 'evaluation', 'results',
                     'discussion', 'limitations', 'conclusion'}


class MissingMaterials(ValueError):
    """The caller should ask for business materials, never a pool file."""


def text(value, label):
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f'{label} 必须是非空文本')


def unique(values, label):
    if len(values) != len(set(values)):
        raise ValueError(f'{label} 不允许重复')
    return set(values)


def normalize_constraints(value):
    if not isinstance(value, dict) or set(value) - DEFAULT_CONSTRAINTS.keys():
        raise ValueError('constraints 字段不合法')
    result = copy.deepcopy(DEFAULT_CONSTRAINTS)
    result.update(copy.deepcopy(value))
    if result['paper_language'] not in ('zh-CN', 'en'):
        raise ValueError('paper_language 必须是 zh-CN 或 en')
    target = result['word_target']
    if target is not None and (type(target) is not int or target <= 0):
        raise ValueError('word_target 必须是正整数或 null')
    if result['paper_type'] not in (None, 'research', 'empirical', 'literature_review',
                                    'theoretical', 'case_study', 'conference'):
        raise ValueError('paper_type 不合法')
    text(result['citation_style'], 'citation_style')
    if not isinstance(result['focus'], list):
        raise ValueError('focus 必须是文本列表')
    for item in result['focus']:
        text(item, 'focus')
    sections = result['required_sections']
    if (not isinstance(sections, list) or any(not isinstance(item, str) or item not in REQUIRED_SECTIONS
                                              for item in sections)):
        raise ValueError('required_sections 必须是支持的论文章节列表')
    unique(sections, 'required_sections')
    return result


def normalize_papers(papers):
    if not isinstance(papers, list):
        raise ValueError('papers 必须是论文列表')
    result = copy.deepcopy(papers)
    used = set()
    for paper in result:
        if not isinstance(paper, dict):
            raise ValueError('论文条目必须是对象')
        identifier = paper.get('id')
        if identifier is not None and identifier != '':
            if not isinstance(identifier, str) or not re.fullmatch(r'[\w.-]+', identifier):
                raise ValueError('paper.id 必须是可用于引用的非空 ID（字母、数字、下划线、点或连字符）')
            if identifier in used:
                raise ValueError('论文 id 重复')
            used.add(identifier)
    for paper in result:
        if paper.get('id'):
            continue
        # Content-derived IDs are deterministic across retries and list reordering.
        identity = {key: paper.get(key) for key in ('title', 'text', 'url', 'doi')}
        if not paper.get('text'):
            identity['abstract'] = paper.get('abstract')
        digest = hashlib.sha256(json.dumps(identity, ensure_ascii=False, sort_keys=True).encode()).hexdigest()[:20]
        identifier = 'ref-' + digest
        if identifier in used:
            raise ValueError('缺失 ID 的论文身份重复或与已有 ID 冲突，请明确来源 ID')
        paper['id'] = identifier
        used.add(identifier)
    return result


def validate_value(kind, value, *, allow_metadata=False):
    if kind == 'media_assets':
        from lazyllm.tools.writer.data_models import MediaAssetLibrary
        library = MediaAssetLibrary.model_validate(value)
        for key, asset in library.assets.items():
            if key != asset.media_asset_id or not re.fullmatch(r'[A-Za-z][A-Za-z0-9_-]{0,79}', key):
                raise ValueError('Invalid uploaded image ID')
            if not asset.uri or not re.fullmatch(
                    r'/static-files/paper-media/[A-Za-z0-9_/-]+\.(?:svg|png|jpg|jpeg)', asset.uri):
                raise ValueError('Uploaded image must reference the saved paper media library')
        return
    if kind == 'code_files':
        if not isinstance(value, list):
            raise ValueError('code_files 必须是列表')
        if value:
            validate_code_files(value)
        return
    elif kind == 'result_meta':
        ResultMeta.model_validate(value)
        return
    elif kind == 'review_data' or (kind == 'review' and isinstance(value, dict)):
        SavedReview.model_validate(value)
        return
    if kind == 'analysis' and isinstance(value, dict):
        from .analysis.schema import validate_analysis
        validate_analysis(value)
        return
    if kind in ('data_context', 'study_design', 'constraints'):
        from .analysis.schema import DataContext, StudyDesign, Constraints, parse
        parse({'data_context': DataContext, 'study_design': StudyDesign, 'constraints': Constraints}[kind], value)
        return
    if kind == 'papers':
        if not isinstance(value, list):
            raise ValueError('papers 必须是论文列表')
        ids = set()
        for paper in value:
            if not isinstance(paper, dict):
                raise ValueError('论文条目必须是对象')
            for key in ('id', 'title'):
                text(paper.get(key), f'paper.{key}')
            for key in ('abstract', 'text'):
                if paper.get(key) is not None and not isinstance(paper[key], str):
                    raise ValueError(f'paper.{key} 必须是字符串或 null')
            if not allow_metadata and not any((paper.get(key) or '').strip() for key in ('abstract', 'text')):
                raise MissingMaterials(f'论文 {paper["id"]} 缺少材料：需要摘要或全文')
            if paper.get('url') is not None and not isinstance(paper['url'], str):
                raise ValueError('paper.url 必须是字符串')
            if 'citation' in paper:
                text(paper['citation'], 'paper.citation')
            if paper['id'] in ids:
                raise ValueError('论文 id 重复')
            ids.add(paper['id'])
    elif kind in ('question', 'evidence', 'analysis') and isinstance(value, dict):
        # Structured upstream materials are opaque to this consumer. Their
        # internal fields and relationships are owned by their producing steps.
        pass
    elif kind == 'charts':
        if not isinstance(value, list):
            raise ValueError('charts 必须是列表')
        for chart in value:
            if not isinstance(chart, dict):
                raise ValueError('chart 必须是对象')
            for field in ('id', 'title', 'svg', 'caption'):
                text(chart.get(field), f'chart.{field}')
            if not re.fullmatch(r'[A-Za-z][A-Za-z0-9_-]{0,63}', chart['id']):
                raise ValueError('chart.id 不符合 F4 图表标识约定')
            refs = chart.get('finding_ids')
            if not isinstance(refs, list) or any(not isinstance(ref, str) or not ref.strip() for ref in refs):
                raise ValueError('chart.finding_ids 必须是结论 ID 列表')
            unique(refs, 'chart.finding_ids')
            if not isinstance(chart.get('columns'), list):
                raise ValueError('chart.columns 必须是列表')
        unique([chart['id'] for chart in value], 'chart.id')
    elif kind == 'selected_issue_ids':
        if not isinstance(value, list):
            raise ValueError('selected_issue_ids 必须是 ID 数组')
        for issue_id in value:
            text(issue_id, 'selected_issue_ids')
        unique(value, 'selected_issue_ids')
    elif kind in ('data', 'source_data'):
        if isinstance(value, dict) and ('column_types' in value or 'tables' in value):
            from .analysis.schema import DataTable, DataBundle, parse
            parse(DataBundle if 'tables' in value else DataTable, value)
        else:
            # Other paper stages keep their original table contract; F4 validates its own spec.
            if not isinstance(value, dict) or set(value) != {'columns', 'rows'}:
                raise ValueError('data 必须包含 columns/rows')
            columns, rows = value['columns'], value['rows']
            if (not isinstance(columns, list) or not columns
                    or any(not isinstance(c, str) or not c.strip() for c in columns)):
                raise ValueError('columns 必须是非空列名列表')
            if len(set(columns)) != len(columns) or not isinstance(rows, list) or not rows:
                raise ValueError('列名不能重复，rows 必须是非空列表')
            for row in rows:
                if not isinstance(row, list) or len(row) != len(columns):
                    raise ValueError('数据行长度必须与列数相同')
                for cell in row:
                    if cell is not None and type(cell) not in (str, int, float, bool):
                        raise ValueError('单元格必须是 JSON 标量')
                    if isinstance(cell, float) and not math.isfinite(cell):
                        raise ValueError('数据不允许 NaN/Infinity')
    elif kind == 'experiment':
        if isinstance(value, dict) and {'code', 'logs'} <= set(value):
            from .analysis.schema import Experiment, parse
            parse(Experiment, value)
            return
        if not isinstance(value, dict) or set(value) != {'executed', 'plan', 'results', 'report'}:
            raise ValueError('experiment 必须包含 executed/plan/results/report')
        if type(value['executed']) is not bool:
            raise ValueError('executed 必须是布尔值')
        text(value['plan'], 'plan')
        text(value['report'], 'report')
        if value['results'] is not None:
            validate_value('data', value['results'])
            if 'tables' not in value['results'] and 'column_types' not in value['results']:
                validate_table(value['results'])
        if value['executed'] and value['results'] is None:
            raise ValueError('已执行实验必须提供结果')
    else:
        text(value, kind)
        if kind == 'chart':
            validate_svg(value)


def validate_input(step, value):
    if step == 'data_analysis':
        if isinstance(value, dict) and value.get('result_meta') is not None:
            from .experiment.handoff import to_analysis
            return to_analysis(value)
        from .analysis.schema import normalize_input, ContractError
        try:
            return normalize_input(value)
        except ContractError as exc:
            if exc.code == 'MISSING_MATERIAL':
                raise MissingMaterials(str(exc)) from exc
            raise
    module = ALIASES.get(step, step)
    fields = FIELDS[module]
    allowed = {'topic', 'request', 'mode', *fields}
    if module in CONSTRAINED_MODULES:
        allowed.add('constraints')
    if module == 'experiment':
        allowed.add('input_is_demo')
    if not isinstance(value, dict) or set(value) - allowed:
        raise ValueError(f'{step} 输入含未知字段')
    text(value.get('topic'), 'topic')
    if not isinstance(value.get('request', ''), str):
        raise ValueError('request 必须是字符串')
    result = {'topic': value['topic'], 'request': value.get('request', '')}
    if module == 'experiment' and 'input_is_demo' in value:
        if type(value['input_is_demo']) is not bool:
            raise ValueError('input_is_demo 必须是布尔值')
        result['input_is_demo'] = value['input_is_demo']
    if module in CONSTRAINED_MODULES:
        if module in {'write_outline', 'write_document'}:
            from .writing.requirements import resolve_constraints
            result['constraints'] = resolve_constraints(value.get('constraints', {}), result['request'])
        else:
            result['constraints'] = normalize_constraints(value.get('constraints', {}))
    if step in MODES:
        if value.get('mode', MODES[step]) != MODES[step]:
            raise ValueError(f'{step} mode 必须是 {MODES[step]}')
        result['mode'] = MODES[step]
    elif 'mode' in value:
        raise ValueError(f'{step} 不接受 mode')
    for field in fields:
        item = value.get(field, [] if field in ('papers', 'charts', 'selected_issue_ids') else None)
        if field == 'papers':
            item = normalize_papers(item)
        if (module in {'write_outline', 'write_document', 'revise_document'} and item is not None
                and field in ('question', 'evidence', 'review')):
            text(item, field)
        elif (module in {'write_outline', 'write_document', 'revise_document'}
              and field == 'analysis' and item is not None):
            from .analysis.schema import validate_analysis
            validate_analysis(item)
        elif module in {'write_outline', 'write_document', 'revise_document'} and field == 'charts':
            from .analysis.schema import Chart, parse
            if not isinstance(item, list):
                raise ValueError('charts 必须是列表')
            for index, chart in enumerate(item):
                parse(Chart, chart, f'input.charts[{index}]')
            unique([chart['id'] for chart in item], 'chart.id')
        elif item is not None or field in ('charts', 'selected_issue_ids'):
            validate_value(field, item, allow_metadata=module == 'peer_review')
        result[field] = copy.deepcopy(item)
    if module in {'write_outline', 'write_document', 'revise_document'} and isinstance(result['analysis'], dict):
        from .analysis.schema import validate_charts as validate_structured_charts
        validate_structured_charts(result['charts'], result['analysis'])
    if step == 'revise_document' and result.get('manuscript') and not result.get('review_data'):
        from .publication import normalize_source_citations
        result['manuscript'] = normalize_source_citations(result['manuscript'], result['papers'])
    required = {'experiment': (), 'refine_topic': ('evidence',), 'peer_review': ('manuscript',),
                'revise_document': ('manuscript',), 'reading_reflection': ('papers',)}.get(step, ())
    missing = [name for name in required if not result[name]]
    if step == 'experiment' and not any(result.get(k) for k in ('question', 'papers', 'data', 'source_data')):
        missing.append('实验问题或用户数据')
    if step in {'write_outline', 'write_document', 'revise_document'}:
        writing_materials = ('papers', 'question', 'evidence', 'analysis', 'outline', 'manuscript', 'writing_materials')
        if not any(result[k] for k in writing_materials):
            missing.append('研究材料、大纲或稿件')
    if missing:
        raise MissingMaterials('缺少材料: ' + '、'.join(missing))
    if step == 'revise_document' and any((result['review'], result['review_data'], result['selected_issue_ids'])):
        if not result['review'] or not result['review_data']:
            raise MissingMaterials('审稿修订需要完整的审稿报告和结构化意见。')
        data = result['review_data']
        text(result['review'], 'review')
        if (data['manuscript_sha256'] != text_digest(result['manuscript'])
                or data['review_sha256'] != text_digest(result['review'])):
            raise ValueError('STALE_MATERIAL: 稿件或审稿意见已修改，结构化意见已过期，请重新审稿后再修订')
    return result


def validate_output(module, value, inputs=None):
    if not isinstance(value, dict) or set(value) != {'is_demo', *OUTPUTS[module]}:
        raise ValueError(f'{module} 算法输出字段不符合约定')
    if type(value['is_demo']) is not bool:
        raise ValueError('输出 is_demo 必须是布尔值')
    if module == 'data_analysis':
        from .analysis.schema import AnalysisOutput, parse, validate_analysis
        from .analysis.schema import validate_charts as validate_structured_charts
        parse(AnalysisOutput, value, 'output')
        validate_analysis(value['analysis'], inputs)
        validate_structured_charts(value['charts'], value['analysis'])
        return value
    for key in OUTPUTS[module]:
        validate_value(key, value[key])
    if module == 'literature_review':
        for paper in value['papers']:
            if paper.get('review_role') != 'background':
                text(paper.get('citation'), 'paper.citation')
    if module == 'experiment':
        meta = validate_result_meta(value['result_meta'], value['experiment'])
        if meta.data_origin == 'observed' and meta.execution_status != 'succeeded':
            raise ValueError('未成功执行的实验不能声明实测结果')
        if meta.data_origin in ('synthetic', 'none') and not value['is_demo']:
            raise ValueError('模拟或无结果必须保留来源标记')
        prepared = not value['code_files'] and 'tables' in (value['experiment']['results'] or {})
        if meta.data_origin == 'user_supplied' or (prepared and meta.data_origin == 'synthetic'):
            if (value['experiment']['executed'] or value['code_files']
                    or value['is_demo'] != (meta.data_origin == 'synthetic')):
                raise ValueError('已有数据整理不能声明新实验或改变原始数据来源')
            return value
        columns = code_column_types(next(item['content'] for item in value['code_files']
                                         if item['path'] == 'experiment.py'))
        if meta.column_types != columns:
            raise ValueError('结果类型声明必须与代码 RESULT_COLUMNS 一致')
        table = value['experiment']['results']
        if table is not None and table['columns'] != list(columns):
            raise ValueError('结果列顺序必须与代码 RESULT_COLUMNS 一致')
    if module == 'revise_document' and value['review_data']['manuscript_sha256'] != text_digest(value['manuscript']):
        raise ValueError('修订后核对必须对应当前稿件')
    if (module in {'peer_review', 'revise_document'}
            and value['review_data']['review_sha256'] != text_digest(value['review'])):
        raise ValueError('结构化审稿意见与 Markdown 报告不匹配')
    return value
