"""Ground uploaded-source classification and bibliography in the bound document."""
import copy
import re
from pathlib import Path
from typing import Literal
from urllib.parse import urlsplit

from pydantic import BaseModel, ConfigDict, Field


class Bibliography(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    title: str = Field(min_length=1)
    authors: list[str] = Field(default_factory=list)
    year: int | None = Field(default=None, ge=1, le=9999)
    venue: str | None = None
    doi: str | None = None
    url: str | None = None


class SourcePreparation(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)
    paper_id: str = Field(min_length=1)
    role: Literal['paper', 'background', 'unknown']
    evidence: list[str] = Field(min_length=1, max_length=8)
    bibliography: Bibliography | None = None


def _normalized(value):
    return re.sub(r'\s+', ' ', str(value)).strip().casefold()


def is_citable(paper):
    preparation = paper.get('citation_preparation')
    return (paper.get('review_role') != 'background'
            and (preparation is None or isinstance(preparation, dict) and preparation.get('role') == 'paper')
            and isinstance(paper.get('citation'), str) and bool(paper['citation'].strip()))


def pending_sources(papers):
    pending = []
    for paper in papers:
        if paper.get('review_role') == 'background' or is_citable(paper):
            continue
        preparation = paper.get('citation_preparation')
        pending.append({'paper_id': paper['id'], 'title': paper['title'],
                        'status': preparation.get('role', 'pending') if isinstance(preparation, dict) else 'pending'})
    return pending


def prepare_sources(papers, annotations):
    """Keep identity, source text and versions fixed; accept only grounded reading judgments."""
    if not isinstance(annotations, list) or not 1 <= len(annotations) <= 32:
        raise ValueError('source_preparation 必须包含 1–32 条材料识别结果')
    result = copy.deepcopy(papers)
    known = {paper['id']: paper for paper in result}
    seen = set()
    for value in annotations:
        entry = SourcePreparation.model_validate(value)
        if entry.paper_id not in known or entry.paper_id in seen:
            raise ValueError('source_preparation 必须使用唯一的实际绑定材料 ID')
        seen.add(entry.paper_id)
        paper = known[entry.paper_id]
        source_text = paper.get('text') or paper.get('abstract') or ''
        raw = _normalized(source_text)
        quotes = [_normalized(quote) for quote in entry.evidence]
        if (any(not quote or quote not in raw for quote in quotes)
                or sum(map(len, entry.evidence)) > 12000):
            raise ValueError('材料识别依据必须是该材料实际原文中的片段，合计不超过 12000 字符')
        if paper.get('review_role') == 'background' and entry.role != 'background':
            raise ValueError('明确的背景材料不能升级为可引用论文')
        if entry.role != 'paper' and entry.bibliography is not None:
            raise ValueError('只有识别为论文的材料可以补充书目信息')
        if entry.role == 'paper':
            bibliography = entry.bibliography
            if bibliography is None and not paper.get('citation'):
                raise ValueError('识别为论文后仍需提供原文可确认的书目信息')
            if bibliography is not None:
                facts = bibliography.model_dump(exclude_none=True, exclude_unset=True)
                evidence = ' '.join(quotes)
                own_text = _normalized(re.split(
                    r'(?im)^\s*(?:#{1,6}\s*)?(?:\d+[.)]?\s+)?(?:references|bibliography|参考文献)\s*[:：]?\s*$',
                    source_text, maxsplit=1)[0])
                for key, fact in facts.items():
                    items = fact if isinstance(fact, list) else [fact]
                    if any(not _normalized(item) for item in items):
                        raise ValueError(f'书目字段 {key} 不能包含空值')
                    original = paper.get(key)
                    if key == 'title' and original in {paper.get('filename'),
                                                       Path(paper.get('filename', '')).stem, 'research-material'}:
                        original = None
                    if original:
                        if _normalized(original) != _normalized(fact):
                            raise ValueError(f'不能覆盖已有书目事实 {key}')
                        continue
                    for item in items:
                        if (not _normalized(item) or _normalized(item) not in evidence
                                or _normalized(item) not in own_text):
                            raise ValueError(f'书目字段 {key} 必须在提供的原文依据中可确认')
                effective = {key: paper.get(key) for key in Bibliography.model_fields}
                effective.update(facts)
                if not (effective.get('authors') or effective.get('doi') or effective.get('url')):
                    raise ValueError('论文身份不足：需从原文确认作者、DOI 或来源地址，不能仅用文件名引用')
                if facts.get('doi') and not re.fullmatch(r'(?:https?://doi\.org/)?10\.\d{4,9}/\S+',
                                                         facts['doi'], re.I):
                    raise ValueError('论文 DOI 格式不合法')
                if facts.get('url'):
                    url = urlsplit(facts['url'])
                    if url.scheme not in {'http', 'https'} or not url.hostname or url.username or url.password:
                        raise ValueError('论文来源必须是无登录凭证的 HTTP(S) 地址')
                paper.update(copy.deepcopy(facts))
                if not paper.get('citation'):
                    fields = [', '.join(effective.get('authors') or []), effective['title'], effective.get('venue'),
                              effective.get('year'), effective.get('doi') or effective.get('url')]
                    paper['citation'] = '. '.join(' '.join(str(field).split()) for field in fields if field)
            # The publisher consumes literal, single-line entries only.
            citation = paper['citation']
            if (not isinstance(citation, str) or not citation.strip() or '\n' in citation or '\r' in citation
                    or '[文献:' in citation or 'paper-ref-' in citation or re.match(r'^\s*\[\d+\]', citation)):
                raise ValueError('citation 必须是无序号、无引用标记的单行书目条目')
        if entry.role != 'unknown':
            paper['review_role'] = entry.role
        paper['citation_preparation'] = entry.model_dump(exclude={'paper_id'}, exclude_none=True, exclude_unset=True)
    return result
