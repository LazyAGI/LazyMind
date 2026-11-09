"""Select supplied papers and expand F2 materials within the current source scope."""
import copy
import json
import time
from typing import Annotated, Literal

from httpx import HTTPStatusError
from lazyllm import AutoModel
from lazyllm.tools.writer.tools.base import WriterToolBase
from pydantic import BaseModel, ConfigDict, Field, create_model, field_validator, model_validator

from lazymind.config import config

from ..contracts import MissingMaterials
from ..literature_review import _prompt
from ..material_bindings import MaterialBindings
from ..prompts.literature_review_prompt import MATERIAL_SELECTION_PROMPT
from ..runtime_events import report_progress
from .retrieval import RETRIEVAL_ERRORS, read_sciverse, sciverse_provider, search_sciverse, unique_candidates


class SearchPlan(BaseModel):
    model_config = ConfigDict(extra='forbid')
    queries: dict[str, Annotated[int, Field(ge=1, le=200)]] = Field(default_factory=dict)
    external_queries: dict[str, Annotated[int, Field(ge=1, le=200)]] = Field(default_factory=dict)
    requested_paper_count: int | None = Field(
        default=None, ge=1, description='Final total only when the user explicitly specifies a count.',
    )
    year_from: Annotated[int, Field(ge=1, le=9999)] | None = None
    year_to: Annotated[int, Field(ge=1, le=9999)] | None = None

    @field_validator('queries', 'external_queries', mode='before')
    @classmethod
    def normalize_query_counts(cls, value):
        if not isinstance(value, dict):
            return value
        # Some model responses wrap a single count in an array; retain numeric/range validation.
        return {query: count[0] if isinstance(count, list) and len(count) == 1 else count
                for query, count in value.items()}

    @model_validator(mode='after')
    def valid_queries(self):
        if any(not query.strip() for query in (*self.queries, *self.external_queries)):
            raise ValueError('检索关键词不能为空')
        if self.year_from is not None and self.year_to is not None and self.year_from > self.year_to:
            raise ValueError('检索起始年份不能晚于结束年份')
        return self


def current_handoff() -> dict | None:
    from lazymind.chat.service.academic_handoff import academic_handoff_context

    try:
        context = academic_handoff_context()
    except HTTPStatusError as error:
        if error.response.status_code == 404:
            return None
        raise
    return context if context['kind'] == 'review' else None


def handoff_input(context: dict) -> dict:
    from lazymind.chat.service.academic_handoff import academic_read_paper

    value = context['input']
    papers = []
    for item in value['papers']:
        paper = {**copy.deepcopy(item['paper']),
                 **{key: copy.deepcopy(value) for key, value in item.items() if key != 'paper'}}
        result = academic_read_paper(paper_id=paper['paper_id'])
        while result['status'] == 'processing':
            report_progress(10, f'正在等待论文《{paper.get("title", paper["paper_id"])}》解析完成')
            time.sleep(2)
            result = academic_read_paper(paper_id=paper['paper_id'])
        parts, warnings = [], []
        while True:
            if result['status'] != 'ready' or result['content_level'] != 'fulltext':
                detail = '；'.join(result.get('warnings', [])) or result['status']
                raise MissingMaterials(f'论文《{paper.get("title", paper["paper_id"])}》正文尚不可读：{detail}')
            warnings.extend(result.get('warnings', []))
            parts.extend((f'<!-- page:{page["page"]} -->\n' if page['page'] > 0 else '') + page['text']
                         for page in result['pages'] if page['text'].strip())
            if not result.get('next_cursor'):
                break
            result = academic_read_paper(cursor=result['next_cursor'])
        if not parts:
            raise MissingMaterials(f'论文《{paper.get("title", paper["paper_id"])}》没有可用正文。')
        paper.update(result['paper'])
        paper.update(id=paper['paper_id'], text='\n\n'.join(parts), source_version=result['source_version'],
                     reading={'content_type': 'fulltext', 'content_hash': result['content_hash'],
                              'warnings': list(dict.fromkeys(warnings))})
        papers.append(paper)
    directions = {key: value[key] for key in ('candidate_directions', 'selected_directions')}
    request = value['user_instruction'] + '\n\nResearch directions (selected defines scope):\n'
    return {'topic': '；'.join(value['selected_directions']) or '论文综述',
            'request': request + json.dumps(directions, ensure_ascii=False), 'papers': papers}


def _select(papers: list[dict], inputs: dict, knowledge_bases: list[str], *, fixed_scope=False,
            plan_search=True, retained=(), count_range=None):
    minimum, maximum = count_range or (config['paper_review_min_paper_count'], config['paper_review_max_paper_count'])
    if minimum > maximum:
        raise ValueError('综述论文数量下限不能大于上限。')
    bindings = MaterialBindings(papers)

    roles = create_model('ReviewMaterialRoles', __config__=ConfigDict(extra='forbid'),
                         **{key: (Literal['paper', 'background', 'exclude'], ...) for key in bindings.materials})
    schema = create_model('ReviewSelection', __base__=SearchPlan, material_roles=(roles, ...))
    selection = WriterToolBase(llm=AutoModel(model='llm'))._call_llm_structured(
        _prompt(MATERIAL_SELECTION_PROMPT, bindings.model_view({
            'research': {key: inputs.get(key) for key in ('topic', 'request', 'question')},
            'knowledge_bases': knowledge_bases,
            'fixed_scope': fixed_scope, 'plan_search': plan_search,
            'paper_count_range': {'min': minimum, 'max': maximum},
            'material_role_keys': list(bindings.materials),
            'retained_papers': [{'title': paper['title'], 'review_role': paper.get('review_role', 'paper')}
                                for paper in retained],
            'papers': [{**{key: value for key, value in paper.items() if key not in ('text', 'abstract')},
                        'snippet': (paper.get('text') or paper.get('abstract') or '')[:1200]} for paper in papers],
        })), schema)
    selected = []
    for key, role in selection.material_roles.model_dump().items():
        if role != 'exclude':
            paper = bindings.materials[key]
            if paper.get('review_role') == 'background':
                role = 'background'
            if role == 'background':
                paper['review_role'] = role
            else:
                paper.pop('review_role', None)
            selected.append(paper)
    if not knowledge_bases or not plan_search:
        selection.queries = {}
    if not plan_search:
        selection.external_queries = {}
    return selected, selection


def _limit_queries(plan, remaining):
    # Search previews need room for duplicates and exclusions; full-text reads have their own cap.
    remaining *= 3
    queries = [(source, query, min(200, count * 3)) for source in (plan.queries, plan.external_queries)
               for query, count in source.items()]
    for index, (source, query, count) in enumerate(queries):
        allowed = min(count, max(1, remaining // (len(queries) - index))) if remaining else 0
        if allowed:
            source[query] = allowed
        else:
            del source[query]
        remaining -= allowed


def _paper_count(papers):
    return sum(paper.get('review_role') != 'background' for paper in papers)


def _search_kb(toolkit, queries, knowledge_bases, warnings):
    documents, more_queries = {}, {}
    for query, count in queries.items():
        try:
            items = toolkit.kb_search(query, k_max=count, kb_ids=knowledge_bases)['items']
        except RETRIEVAL_ERRORS as error:
            warnings.append(f'知识库检索「{query}」失败（{type(error).__name__}）。')
            continue
        if len(items) >= count and count < 200:
            more_queries[query] = min(200, count * 2)
        for item in items:
            identifier = f'kb-{item["kb_id"]}-{item["docid"]}'
            if item['kb_id'] not in knowledge_bases or not item.get('text', '').strip():
                continue
            if identifier in documents:
                if item['text'] not in documents[identifier]['text']:
                    documents[identifier]['text'] += '\n\n' + item['text']
            else:
                documents[identifier] = {
                    'id': identifier, 'title': item.get('file_name') or item['docid'], 'text': item['text'],
                    'source': 'knowledge_base', 'kind': 'knowledge_base',
                    'metadata': {**item.get('metadata', {}), 'kb_id': item['kb_id'], 'doc_id': item['docid']},
                }
    if not documents:
        warnings.append('所选知识库未取得可用的新增文献。')
    return list(documents.values()), more_queries


def prepare(inputs: dict, knowledge_bases: list[str], *, fixed_scope: bool = False) -> list[dict]:
    papers = copy.deepcopy(inputs.get('papers') or [])
    if fixed_scope and not inputs.get('request'):
        return papers
    for paper in papers:
        paper.pop('retrieval_warnings', None)
    report_progress(10, '正在选择本轮综述材料')
    selected, plan = _select(papers, inputs, knowledge_bases, fixed_scope=fixed_scope)
    selected = unique_candidates([], selected)
    target_count = plan.requested_paper_count or config['paper_review_max_paper_count']
    minimum_count = plan.requested_paper_count or config['paper_review_min_paper_count']
    if plan.requested_paper_count is not None:
        selected = selected[:target_count]
    elif not fixed_scope:
        supplied = [paper for paper in selected
                    if paper.get('source') not in {'sciverse', 'crossref', 'knowledge_base'}]
        retrieved = [paper for paper in selected
                     if paper.get('source') in {'sciverse', 'crossref', 'knowledge_base'}]
        selected = supplied + retrieved[:max(0, target_count - _paper_count(supplied))]
    _limit_queries(plan, max(0, target_count - _paper_count(selected))
                   if _paper_count(selected) < minimum_count else 0)
    warnings, seen = [], list(selected)
    expanding = bool(plan.queries or plan.external_queries)
    toolkit, provider = None, None
    if plan.queries:
        from lazymind.chat.engine.tools.lazy_kb import KBToolkit

        toolkit = KBToolkit(kb_scope=knowledge_bases)
    if plan.external_queries:
        provider = sciverse_provider()
        if getattr(provider, 'source_name', 'sciverse') == 'crossref':
            warnings.append('Sciverse 检索凭据未配置，本轮使用 Crossref 元数据与实际取得的论文摘要。')
    kb_queries, external_queries = plan.queries, plan.external_queries
    for page in range(1, 4):
        if not (kb_queries or external_queries):
            break
        report_progress(10 + page * 3, f'正在检索候选文献（第 {page} 轮，已选 {_paper_count(selected)} 篇）')
        candidates = []
        if kb_queries:
            hits, kb_queries = _search_kb(toolkit, kb_queries, knowledge_bases, warnings)
            candidates.extend(hits)
        if external_queries:
            hits, external_queries = search_sciverse(
                provider, external_queries, plan.year_from, plan.year_to, warnings, page=page,
            )
            candidates.extend(hits)
        candidates = unique_candidates(seen, candidates)
        seen.extend(candidates)
        if not candidates:
            continue
        additions, _ = _select(candidates, inputs, [], plan_search=False, retained=selected,
                               count_range=(minimum_count, target_count))
        for paper in additions:
            if _paper_count(selected) >= target_count:
                break
            if paper['source'] == 'knowledge_base':
                metadata = paper['metadata']
                try:
                    content = toolkit.read_document(metadata['kb_id'], metadata['doc_id'])['content']
                    if not content['text'].strip():
                        raise ValueError('文档正文为空')
                    paper.update(text=content['text'], reading={
                        'content_type': 'knowledge_base_document', 'truncated': content['truncated'],
                    })
                    if content['truncated']:
                        warnings.append(f'《{paper["title"]}》知识库正文不完整，仅使用实际读取的内容。')
                except RETRIEVAL_ERRORS:
                    warnings.append(f'《{paper["title"]}》知识库全文读取失败，仅使用检索节选。')
                    paper['reading'] = {'content_type': 'knowledge_base_excerpt', 'truncated': True}
            else:
                paper = read_sciverse(provider, paper, warnings)
            if paper:
                selected.append(paper)
        if _paper_count(selected) >= minimum_count:
            break
    if expanding and _paper_count(selected) < minimum_count:
        warnings.append(f'本轮目标为 {minimum_count}–{target_count} 篇论文，实际仅取得 {_paper_count(selected)} 篇相关可读论文；'
                        '已在最多三轮候选检索内补充，未以不相关文献凑数。')
    if not any(paper.get('review_role') != 'background' for paper in selected):
        detail = '；'.join(dict.fromkeys(warnings))
        raise MissingMaterials('指定范围内没有可综述的论文，请补充论文或调整检索范围。' + detail)
    if warnings:
        selected[0]['retrieval_warnings'] = list(dict.fromkeys(warnings))
    return selected
