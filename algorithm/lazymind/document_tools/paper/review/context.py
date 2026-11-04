"""Retrieve bounded original passages for quote repair, preserving global line numbers."""
import json
import re


def _terms(text):
    words = set(re.findall(r'[a-z0-9_]{2,}', text.lower()))
    for part in re.findall(r'[\u4e00-\u9fff]+', text):
        words.update(part[i:i + 2] for i in range(len(part) - 1))
    return words


def source_lines(manuscript, issue, budget):
    """Rank paragraphs by section and excerpt; ranking never validates a quotation."""
    lines = manuscript.splitlines()
    location = str(issue.get('location') or '').lower()
    terms = _terms(str(issue.get('quote') or '')[:1000])
    blocks, block, headings = [], [], []

    def finish():
        if block:
            text = '\n'.join(lines[i] for i in block)
            section = max((len(title) for title in headings if title in location), default=0)
            overlap = len(terms & _terms(text))
            if section or overlap:
                blocks.append((bool(section), overlap, section, block.copy()))
            block.clear()

    size = 0
    for index, line in enumerate(lines):
        heading = re.match(r'^(#{1,6})\s+(.+)', line)
        if heading:
            finish()
            level, title = len(heading[1]), heading[2].strip().lower()
            title = re.sub(r'^\d+(?:\.\d+)*[.、\s]*', '', title)
            headings = headings[:level - 1] + [title]
            size = 0
        if not line.strip():
            finish()
            size = 0
            continue
        # Keep complete source lines; never splice or truncate a quoted line.
        if size + len(line) > 1800 and block:
            finish()
            size = 0
        block.append(index)
        size += len(line)
    finish()

    chosen = set()
    for _, _, _, indices in sorted(blocks, key=lambda item: item[:3], reverse=True):
        proposed = chosen | set(indices)
        numbered = '\n'.join(f'{i + 1}: {lines[i]}' for i in sorted(proposed))
        if len(json.dumps(numbered, ensure_ascii=False)) <= budget:
            chosen = proposed
    if not chosen:
        raise ValueError('未找到预算内可核验的相关原文片段，需人工核对该条意见。')
    return '\n'.join(f'{i + 1}: {lines[i]}' for i in sorted(chosen)), {i + 1 for i in chosen}
