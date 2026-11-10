"""Deterministic presentation planning shared by native and external tools."""
import json
from typing import Any


def plan_timing(total_minutes: float, pages_json: str) -> dict[str, Any]:
    """Allocate a talk's exact time budget across ordered pages.

    pages_json is [{title, weight}], with positive weights: brief cover/closing
    pages ~0.4, ordinary pages 1, important method/result pages ~2. The requested
    total excludes Q&A unless the user explicitly includes it as a page.
    """
    import math
    pages = json.loads(pages_json)
    if (type(total_minutes) not in (int, float) or not math.isfinite(total_minutes)
            or not 0 < total_minutes <= 480 or not isinstance(pages, list) or not 1 <= len(pages) <= 200):
        raise ValueError('Provide a positive talk duration up to 480 minutes and 1-200 pages')
    weights = []
    for page in pages:
        weight = page.get('weight', 1) if isinstance(page, dict) else None
        if (not isinstance(page, dict) or not isinstance(page.get('title'), str)
                or not page['title'].strip() or type(weight) not in (int, float)
                or not math.isfinite(weight) or not 0 < weight <= 100):
            raise ValueError('Each page needs a title and a positive weight up to 100')
        weights.append(weight)
    total = round(total_minutes * 60)
    if total < len(pages) * 5:
        raise ValueError('Time budget must allow at least five seconds per page')
    shares = [(total - 5 * len(pages)) * weight / sum(weights) for weight in weights]
    seconds = [5 + math.floor(share) for share in shares]
    for index in sorted(range(len(pages)), key=lambda i: (-(shares[i] % 1), i))[:total - sum(seconds)]:
        seconds[index] += 1
    result, elapsed = [], 0
    for index, (page, duration) in enumerate(zip(pages, seconds), 1):
        end = elapsed + duration
        label = f'{elapsed // 60:02d}:{elapsed % 60:02d}–{end // 60:02d}:{end % 60:02d}'
        result.append({'page': index, 'title': page['title'], 'seconds': duration,
                       'start_seconds': elapsed, 'end_seconds': end,
                       'notes_prefix': f'【建议用时 {duration // 60}分{duration % 60:02d}秒｜累计 {label}】'})
        elapsed = end
    return {'total_seconds': total, 'pages': result}


def plan_paper_order(papers_json: str, dependencies_json: str = '[]') -> dict[str, Any]:
    """Order selected papers so prerequisites are explained before dependent methods.

    Call after reading ALL selected papers, before writing the outline.
    papers_json: JSON list of {source_id, title}, in preferred narrative order for
        independent papers. Include every selected paper exactly once.
    dependencies_json: JSON list of {prerequisite, dependent, evidence} using
        those source_ids. Evidence must cite the source section/page and explain
        the reused method/concept. A citation or benchmark alone is NOT an edge.
    Returns an ordered list and evidence; copy it into material_summary. This tool
    checks graph consistency, not the truth of the supplied evidence. Resolve
    uncertain/cyclic relationships from sources rather than inventing an order.
    """
    papers = json.loads(papers_json)
    dependencies = json.loads(dependencies_json)
    if not isinstance(papers, list) or not papers:
        raise ValueError('papers_json must contain all selected papers')
    if not isinstance(dependencies, list):
        raise ValueError('dependencies_json must be a list')
    by_id = {}
    for paper in papers:
        if not isinstance(paper, dict):
            raise ValueError('Each paper needs a source_id and title')
        source_id, title = paper.get('source_id'), paper.get('title')
        if not isinstance(source_id, str) or not source_id.strip() or not isinstance(title, str) or not title.strip():
            raise ValueError('Each paper needs a non-empty source_id and title')
        if source_id in by_id:
            raise ValueError('Duplicate source_id: ' + source_id)
        by_id[source_id] = {'source_id': source_id, 'title': title}
    prerequisites = {source_id: set() for source_id in by_id}
    edges = []
    for edge in dependencies:
        if not isinstance(edge, dict):
            raise ValueError('Each dependency needs prerequisite, dependent and evidence')
        before, after, evidence = edge.get('prerequisite'), edge.get('dependent'), edge.get('evidence')
        if not isinstance(before, str) or not isinstance(after, str) or before not in by_id or after not in by_id:
            raise ValueError('Dependency endpoints must be selected source_ids')
        if before == after:
            raise ValueError('A paper cannot depend on itself')
        if not isinstance(evidence, str) or not evidence.strip():
            raise ValueError('Dependencies require source evidence; omit unconfirmed relationships')
        prerequisites[after].add(before)
        edges.append({'prerequisite': before, 'dependent': after, 'evidence': evidence.strip()})
    ordered = []
    remaining = dict(prerequisites)
    while remaining:
        # Stable tie-break: the first ready paper in the supplied narrative order.
        ready = next((source_id for source_id, parents in remaining.items() if not parents), None)
        if ready is None:
            raise ValueError('Cyclic dependencies; recheck source evidence for: ' + ', '.join(remaining))
        ordered.append(by_id[ready])
        del remaining[ready]
        for parents in remaining.values():
            parents.discard(ready)
    return {'paper_order': ordered, 'dependencies': edges, 'paper_count': len(ordered),
            'ordering_policy': 'prerequisites first; supplied narrative order breaks ties'}
