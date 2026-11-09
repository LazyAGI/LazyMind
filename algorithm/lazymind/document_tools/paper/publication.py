"""Paper publication: validate and render chart resources and literature references."""
from collections import Counter
import hashlib
import logging
import os
from pathlib import Path
import re
import tempfile

from lazyllm.tools.writer.numbering import build_numbering_view_from_markdown, compute_numbering

from ..artifacts import validate_paper_svg, markdown_prose


def figure_catalog(charts, media_assets=None):
    """Adapt Writer assets to the shared placement catalog without changing F4 charts."""
    return [*(charts or []), *[
        {'id': key, 'title': asset.get('caption') or key,
         'caption': asset.get('summary') or asset.get('caption') or key,
         'media_uri': asset['uri']}
        for key, asset in (media_assets or {}).get('assets', {}).items()
    ]]


def chart_uri(chart):
    if chart.get('media_uri'):
        return chart['media_uri']
    digest = hashlib.sha256(chart['svg'].encode('utf-8')).hexdigest()
    return f'/static-files/paper-charts/{digest}.svg'


_OBJECT = re.compile(
    r'<a\s+id="block-([A-Za-z][A-Za-z0-9_-]{0,63})"\s*></a>\s*'
    r'!\[(?:\\.|[^\]\\\r\n])*\]\((?P<source>[^\s)]+)\)')


def validate_chart_references(body, charts):
    """Accept only catalog placeholders or the exact immutable resource for that chart."""
    prose = markdown_prose(body)
    if '[图表:' in prose:
        raise ValueError('Unresolved chart insertion marker.')
    catalog = {chart['id']: chart for chart in charts}
    objects = [match for match in _OBJECT.finditer(prose)
               if match[1] in catalog or match['source'].startswith(
                   ('media-placeholder://', '/static-files/paper-charts/', '/static-files/paper-media/'))]
    ids = [match[1] for match in objects]
    anchors = re.findall(r'<a\s+id="block-([A-Za-z][A-Za-z0-9_-]{0,63})"\s*></a>', prose)
    links = re.findall(r'(?<!!)\[(?:\\.|[^\]\\\r\n])+\]\(#block-([A-Za-z][A-Za-z0-9_-]{0,63})\)', prose)
    placeholders = re.findall(r'media-placeholder://([^\s)]+)', prose)
    # Writer also anchors ordinary headings/paragraphs. Only figure objects,
    # catalog IDs and media placeholders belong to this validator.
    figure_ids = set(catalog) | set(ids) | set(placeholders)
    anchors = [identifier for identifier in anchors if identifier in figure_ids]
    all_anchors = set(re.findall(r'<a\s+id="block-([^"]+)"\s*></a>', prose))
    if set(links) - all_anchors:
        raise ValueError('Writer manuscript contains a dangling object reference.')
    links = [identifier for identifier in links if identifier in figure_ids]
    if set(ids + anchors + links + placeholders) - catalog.keys():
        raise ValueError('Writer manuscript references an unknown chart.')
    if len(ids) != len(set(ids)) or len(anchors) != len(set(anchors)):
        raise ValueError('Duplicate chart object or anchor.')
    if set(ids) != set(anchors) or set(links) != set(ids):
        raise ValueError('Chart objects, anchors and prose references must agree.')
    for match in objects:
        if match['source'] not in {f'media-placeholder://{match[1]}', chart_uri(catalog[match[1]])}:
            raise ValueError('Chart image does not match its registered SVG.')
    if sorted(placeholders) != sorted(
        match[1] for match in objects if match['source'].startswith('media-placeholder://')
    ):
        raise ValueError('Chart placeholder must have an adjacent unique anchor.')
    managed = re.findall(r'!\[(?:\\.|[^\]\\\r\n])*\]\((/static-files/paper-(?:charts|media)/[^\s)]+)\)', prose)
    if sorted(managed) != sorted(
            m['source'] for m in objects
            if m['source'].startswith(('/static-files/paper-charts/', '/static-files/paper-media/'))):
        raise ValueError('Published chart must have an adjacent unique anchor.')
    if not ids:
        return objects
    # Shared Writer owns numbering and general cross-reference validity.
    view = build_numbering_view_from_markdown(body)
    numbering = compute_numbering(view)
    if any(identifier not in numbering or numbering[identifier].kind != 'figure' for identifier in ids):
        raise ValueError('Chart object is not supported by the Writer Markdown parser.')
    return objects


def _save_svg(chart):
    from lazymind.chat.service.utils.static_file_url import _upload_root

    data = chart['svg'].encode('utf-8')
    root = Path(_upload_root()).resolve()
    directory = root / 'paper-charts'
    directory.mkdir(parents=True, exist_ok=True)
    if directory.resolve() != directory:
        raise ValueError('Chart storage must not be a symbolic link.')
    target = directory / chart_uri(chart).rsplit('/', 1)[-1]
    # Publish complete bytes atomically, never overwrite an existing resource.
    with tempfile.NamedTemporaryFile(dir=directory, suffix='.svg') as staged:
        staged.write(data)
        staged.flush()
        # Core may serve the shared upload directory under a different UID.
        if hasattr(os, 'fchmod'):
            os.fchmod(staged.fileno(), 0o644)
        else:
            os.chmod(staged.name, 0o644)
        try:
            os.link(staged.name, target)
        except FileExistsError:
            if target.is_symlink() or target.read_bytes() != data:
                raise ValueError('Stored chart resource does not match its content digest.')


def publish_charts(body, charts):
    objects = validate_chart_references(body, charts)
    catalog = {chart['id']: chart for chart in charts}
    used = {match[1] for match in objects}
    for identifier in used:
        if 'svg' in catalog[identifier]:
            validate_paper_svg(catalog[identifier]['svg'])
    for identifier in used:
        if 'svg' in catalog[identifier]:
            _save_svg(catalog[identifier])
    return _resolve_chart_sources(body, charts, objects)


def refresh_chart_sources(body, previous, current):
    """Rebind only validated figure destinations after a visual-only redraw."""
    objects = validate_chart_references(body, previous)
    updated = _resolve_chart_sources(body, current, objects)
    return updated


def resolve_chart_sources(body, charts):
    """Resolve validated destinations without changing captions or display notices."""
    objects = validate_chart_references(body, charts)
    return _resolve_chart_sources(body, charts, objects)


def _resolve_chart_sources(body, charts, objects):
    catalog = {chart['id']: chart for chart in charts}
    # Rewrite only validated object destinations, not prose or fenced examples.
    for match in reversed(objects):
        start, end = match.span('source')
        body = body[:start] + chart_uri(catalog[match[1]]) + body[end:]
    validate_chart_references(body, charts)
    if 'media-placeholder://' in markdown_prose(body):
        raise ValueError('Unresolved chart media after publication.')
    return body


# Literature references: stable source identities and deterministic numbering.
START = '<a id="paper-references-start"></a>'
END = '<a id="paper-references-end"></a>'
_ID = r'[\w.-]+'
_CITATION = re.compile(r'\[文献:(?P<new>' + _ID + r')\]|'
                       r'(?<!\[)\[(?:\\\[(?P<number>[1-9][0-9]*)\\\]|\[[1-9][0-9]*\]|[1-9][0-9]*)\]'
                       r'\(#paper-ref-(?P<saved>' + _ID + r')\)')
_BOUNDARY = re.compile(r'<a\s+id="paper-references-(start|end)"\s*(?:></a>|/>)')


def prose(text):
    masked = markdown_prose(text)
    return re.sub(r'(`+)([^`]|(?!\1)`)*?\1', lambda m: ' ' * len(m[0]), masked)


def split_references(text):
    """Only explicitly delimited generated regions belong to the publisher."""
    matches = list(_BOUNDARY.finditer(prose(text)))
    if not matches:
        return text, ''
    if len(matches) != 2 or [m[1] for m in matches] != ['start', 'end']:
        raise ValueError('参考文献区域边界缺失或重复')
    start, end = matches[0].start(), matches[1].end()
    if text[end:].strip():
        raise ValueError('程序生成的参考文献区域必须位于文末')
    return text[:start].rstrip(), text[start:end]


def protect_reference_region(original, revised):
    if split_references(original)[1].replace('\r\n', '\n') != split_references(revised)[1].replace('\r\n', '\n'):
        raise ValueError('不能直接修改程序生成的参考文献区域；请更新 Paper.citation')


def _catalog(papers):
    result = {}
    for paper in papers:
        identifier = paper['id']
        if identifier in result:
            raise ValueError(f'重复 Paper.id: {identifier}')
        result[identifier] = paper.get('citation')
    return result


def _scan(body, papers):
    masked = prose(body)
    matches = list(_CITATION.finditer(masked))
    remaining = list(masked)
    catalog = _catalog(papers)
    for match in matches:
        identifier = match['new'] or match['saved']
        entry = catalog.get(identifier)
        if not isinstance(entry, str) or not entry.strip():
            raise ValueError(f'文献 {identifier} 缺少可用的 Paper.citation')
        # Entries are literal bibliographic text, not a second Markdown document.
        if ('\n' in entry or '\r' in entry or '[文献:' in entry or 'paper-ref-' in entry
                or re.match(r'^\s*\[[0-9]+\]', entry)):
            raise ValueError(f'文献 {identifier} 的 citation 必须为无序号、无标记的单行条目')
        remaining[match.start():match.end()] = ' ' * len(match[0])
    remaining = ''.join(remaining)
    if '[文献' in remaining or '#paper-ref-' in remaining or 'id="paper-ref-' in remaining:
        logging.getLogger(__name__).warning('Invalid paper citation markup: %r',
                                            re.findall(
                                                r'.{0,20}(?:\[文献|#paper-ref-|id="paper-ref-).{0,120}', remaining)[:4])
        raise ValueError('文献引用标记或稳定 ID 链接格式错误')
    if matches and (re.search(r'\[\d+(?:\s*[-,，–]\s*\d+)*\]', remaining)
                    or re.search(r'[（(][^()（）\n]*[,，]\s*(?:19|20)[0-9]{2}[a-z]?[)）]', remaining)
                    or re.search(r'(?mi)^#{1,6}\s+(?:参考文献|references|bibliography)\s*$', remaining)):
        raise ValueError('不能混编旧稿引用或人工参考文献列表；请先明确来源 ID 对应关系')
    return matches, catalog


def validate_citations(text, papers, *, published=False):
    body, _ = split_references(text)
    matches, _ = _scan(body, papers)
    if published and (any(m['new'] for m in matches) or publish_citations(text, papers) != text):
        raise ValueError('文献编号或参考文献列表未同步，请先生成新稿版本')
    return matches


def repair_generated_citation_links(text, papers):
    """Restore one dropped outer bracket in generated links to registered sources."""
    catalog = _catalog(papers)
    pattern = re.compile(r'\[\\\[(?P<number>[1-9][0-9]*)\\\]\(#paper-ref-(?P<id>' + _ID + r')\)')
    for match in reversed(list(pattern.finditer(prose(text)))):
        if match['id'] in catalog:
            canonical = f'[\\[{match["number"]}\\]](#paper-ref-{match["id"]})'
            text = text[:match.start()] + canonical + text[match.end():]
    return text


def _literal(entry):
    # Escape Markdown/HTML without changing the displayed bibliographic text.
    entry = entry.replace('&', '&amp;').replace('<', '&lt;').replace('>', '&gt;')
    return re.sub(r'([\\`*_{}\[\]])', r'\\\1', entry)


def publish_citations(text, papers, *, paper_language=None):
    body, region = split_references(text)
    matches, catalog = _scan(body, papers)
    if not matches:
        return body if region else text
    ids = list(dict.fromkeys(m['new'] or m['saved'] for m in matches))
    numbers = {identifier: index + 1 for index, identifier in enumerate(ids)}
    for match in reversed(matches):
        identifier = match['new'] or match['saved']
        link = f'[\\[{numbers[identifier]}\\]](#paper-ref-{identifier})'
        body = body[:match.start()] + link + body[match.end():]
    papers = [{'id': identifier, 'citation': catalog[identifier]} for identifier in ids]
    return body.rstrip() + '\n\n' + render_references(papers, paper_language=paper_language, existing_region=region)


def render_references(papers, *, paper_language=None, existing_region=''):
    """Render review and manuscript bibliographies in the editor's reference format."""
    title = 'References' if paper_language == 'en' else '参考文献'
    # A stable heading anchor keeps editor rendering out of the protected reference region.
    heading = '<a id="block-paper-references"></a>\n\n## ' + title
    if paper_language is None and existing_region:
        # Citation-only validation also accepts previously published headings.
        existing = re.match(re.escape(START) + r'\n\n((?:<a id="block-paper-references"></a>\n\n)?'
                            r'(?:## References|## 参考文献|\*\*References\*\*|\*\*参考文献\*\*))\n', existing_region)
        if existing:
            heading = existing[1]
    entries = [heading]
    for number, paper in enumerate(papers, 1):
        entries.append(f'<a id="paper-ref-{paper["id"]}"></a>\n\n'
                       f'[{number}] {_literal(paper["citation"])}')
    return START + '\n\n' + '\n\n'.join(entries) + '\n\n' + END


def validate_unmanaged_additions(original, revised):
    """Legacy citations may be retained, but a model cannot create numbered citations."""
    def tokens(text):
        body, _ = split_references(text)
        masked = _CITATION.sub('', prose(body))
        return Counter(re.findall(r'\[[0-9]+(?:\s*[-,，–]\s*[0-9]+)*\]', masked))
    before, after = tokens(original or ''), tokens(revised)
    if after - before:
        raise ValueError('新增文献引用必须使用 [文献:ref-ID]，不能自行编写序号')


def normalize_source_citations(text, papers, *, material_source_ids=()):
    """Import explicit PR #81 source markers; never infer numeric bibliography IDs.

    A trailing source list is removable only when every line exactly matches the
    supplied citation. Code, images, links and ambiguous numeric IDs stay intact.
    """
    body, region = split_references(text)
    if region:
        return text
    catalog = _catalog(papers)
    # Study attachments are evidence provenance, not bibliography entries.
    material_ids = set(material_source_ids) - catalog.keys()
    for match in reversed(list(_CITATION.finditer(prose(body)))):
        if match['new'] in material_ids:
            body = body[:match.start()] + body[match.end():]
    masked = prose(body)
    heading = re.search(
        r'(?mi)^(?:#{1,6}\s+(?:参考文献|references|bibliography)|'
        r'\*\*(?:参考文献|references|bibliography)\*\*)[ \t]*$', masked)
    if heading:
        tail = body[heading.end():].strip()
        entries = [re.sub(r'^[-*]\s+', '', line.strip()) for line in tail.splitlines() if line.strip()]
        expected = {f'[{identifier}] {entry}' for identifier, entry in catalog.items()
                    if isinstance(entry, str) and entry.strip() and not identifier.isdigit()}
        if entries and all(line in expected for line in entries) and len(entries) == len(set(entries)):
            body = body[:heading.start()].rstrip()
            masked = prose(body)
        elif (any(f'[{identifier}]' in tail for identifier in catalog if not identifier.isdigit())
              or re.search(r'\[(?:SRC|KB)-[^\]]+\]', tail)):
            raise ValueError('来源参考文献列表与 papers.citation 不一致，不能自动迁移')
    pattern = re.compile(r'(?<!!)(?<!\\)\[([\w.-]+)\](?![(:])')
    for match in reversed(list(pattern.finditer(masked))):
        identifier = match[1]
        following = re.match(r'\[([^\]\n]*)\]', masked[match.end():])
        if following and following[1] not in catalog:
            continue  # Markdown reference-style link, not adjacent source citations.
        if identifier.isdigit():
            continue
        if identifier not in catalog:
            if identifier.startswith(('SRC-', 'KB-', 'ref-')):
                raise ValueError(f'旧稿引用未知来源: {identifier}')
            continue
        if not isinstance(catalog[identifier], str) or not catalog[identifier].strip():
            raise ValueError(f'文献 {identifier} 缺少可用的 Paper.citation')
        body = body[:match.start()] + f'[文献:{identifier}]' + body[match.end():]
    return body
