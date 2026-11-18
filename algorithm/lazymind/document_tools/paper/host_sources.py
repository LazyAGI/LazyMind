"""Normalize host-read sources without searching or claiming independent verification."""
import copy
import hashlib
import json
import re
from urllib.parse import urlsplit
from typing import Literal

from pydantic import ConfigDict
from typing_extensions import NotRequired, TypedDict

from . import contracts
from .review.search import identities, same_paper


class HostReading(TypedDict):
    content_type: Literal['abstract', 'excerpt', 'full_text']
    location: str
    truncated: bool


class HostSource(TypedDict):
    title: str
    url: str
    text: str
    reading: HostReading
    doi: NotRequired[str]
    authors: NotRequired[list[str]]
    year: NotRequired[int]
    venue: NotRequired[str]


HostReading.__pydantic_config__ = ConfigDict(extra='forbid', strict=True)
HostSource.__pydantic_config__ = ConfigDict(extra='forbid', strict=True)


def reading_quality(paper):
    reading = paper.get('reading') or {}
    rank = {'full_text': 3, 'fulltext': 3, 'provided_text': 3, 'excerpt': 2, 'abstract': 1}.get(
        reading.get('content_type'), 0)
    if reading.get('truncated'):
        rank = min(rank, 2)
    return rank, len(paper.get('text') or '')


def normalize_sources(papers, existing, policy):
    if not isinstance(papers, list) or not 1 <= len(papers) <= 32:
        raise ValueError('一次登记 1–32 篇宿主实际读取的文献')
    if len(json.dumps(papers, ensure_ascii=False).encode()) > 4 * 1024 * 1024:
        raise ValueError('宿主文献登记内容超过 4 MiB，请按批次提交')
    result = []
    allowed = {'title', 'url', 'doi', 'text', 'reading', 'authors', 'year', 'venue'}
    for raw in papers:
        if not isinstance(raw, dict) or set(raw) - allowed:
            raise ValueError('宿主来源只接受书目信息、url、doi、text 和 reading，不接受自定 ID、身份或回执')
        paper = copy.deepcopy(raw)
        for key in ('title', 'url', 'text'):
            contracts.text(paper.get(key), f'paper.{key}')
        url = urlsplit(paper['url'])
        if url.scheme not in {'http', 'https'} or not url.hostname or url.username or url.password:
            raise ValueError('paper.url 必须是无登录凭证的 HTTP(S) 来源地址')
        if paper.get('doi') is not None:
            if not isinstance(paper['doi'], str):
                raise ValueError('paper.doi 必须是 DOI 文本')
            doi = re.sub(r'^https?://(?:dx\.)?doi\.org/', '', paper['doi'], flags=re.I).strip().lower()
            if not re.fullmatch(r'10\.\d{4,9}/[^\s]+', doi):
                raise ValueError('paper.doi 不是有效的 DOI 标识格式')
            paper['doi'] = doi
        if 'authors' in paper and (not isinstance(paper['authors'], list)
                                   or any(not isinstance(a, str) or not a.strip() for a in paper['authors'])):
            raise ValueError('paper.authors 必须是实际作者的文本列表；未知时省略')
        if 'venue' in paper and not isinstance(paper['venue'], str):
            raise ValueError('paper.venue 必须是文本；未知时省略')
        year = paper.get('year')
        if year is not None and (type(year) is not int or not 1 <= year <= 9999):
            raise ValueError('paper.year 必须是实际出版年份；未知时省略')
        if any(policy.get(key) is not None for key in ('year_from', 'year_to')):
            if year is None or not (policy.get('year_from') or 1) <= year <= (policy.get('year_to') or 9999):
                raise ValueError('宿主来源的年份不满足当前固定来源范围')
        reading = paper.get('reading')
        if (not isinstance(reading, dict) or set(reading) != {'content_type', 'location', 'truncated'}
                or reading['content_type'] not in {'abstract', 'excerpt', 'full_text'}
                or type(reading['truncated']) is not bool):
            raise ValueError('reading 必须提供 content_type=abstract/excerpt/full_text、location 和 truncated')
        contracts.text(reading['location'], 'reading.location')
        if reading['content_type'] == 'full_text' and reading['truncated']:
            raise ValueError('未读完的全文须登记为 excerpt，不能标为完整 full_text')
        keys = identities(paper)
        identity = next((sorted(value for kind, value in keys if kind == candidate)
                         for candidate in ('doi', 'arxiv', 'url') if any(kind == candidate for kind, _ in keys)))
        paper['id'] = 'host-' + hashlib.sha256(json.dumps(identity).encode()).hexdigest()[:20]
        previous = next((item for item in existing if same_paper(paper, item)), None)
        if previous:
            if previous.get('review_role') == 'background':
                raise ValueError('明确的背景材料不能通过宿主登记升级为学术论文')
            paper['id'] = previous['id']
            if reading_quality(previous) > reading_quality(paper):
                result.append(copy.deepcopy(previous))
                continue
        paper['source'] = 'host'
        paper['provenance'] = {'origin': 'host_retrieval', 'verification': 'host_reported',
                               'content_sha256': hashlib.sha256(paper['text'].encode()).hexdigest()}
        if previous:
            paper['provenance']['previous_source_version'] = previous.get('source_version') or hashlib.sha256(
                json.dumps(previous, ensure_ascii=False, sort_keys=True).encode()).hexdigest()
        paper['source_version'] = hashlib.sha256(
            json.dumps(paper, ensure_ascii=False, sort_keys=True).encode()).hexdigest()
        result.append(paper)
    # Prefer the most complete actual reading when duplicate identities occur in a batch.
    selected = []
    for paper in sorted(result, key=reading_quality, reverse=True):
        if not any(same_paper(paper, other) for other in selected):
            selected.append(paper)
    return selected
