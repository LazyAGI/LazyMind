"""Small, source-preserving HTML layout patches for screenshot refinement.

No model-authored HTML or executable code is evaluated. Only inspected anchors
and a constrained set of inline layout properties can change.
"""
import hashlib
from html import escape
from html.parser import HTMLParser
import re


LAYOUT_PROPERTIES = frozenset({
    'position', 'left', 'top', 'right', 'bottom', 'width', 'height',
    'min-width', 'min-height', 'max-width', 'max-height', 'margin',
    'margin-top', 'margin-bottom', 'margin-left', 'margin-right', 'padding',
    'padding-top', 'padding-bottom', 'padding-left', 'padding-right',
    'gap', 'row-gap', 'column-gap', 'display', 'flex', 'flex-direction',
    'flex-grow', 'flex-shrink', 'flex-basis', 'flex-wrap', 'align-items',
    'align-self', 'justify-content', 'grid-template-columns', 'grid-template-rows',
    'font-size', 'line-height', 'letter-spacing', 'text-align', 'white-space',
    'overflow-wrap', 'word-break', 'box-sizing',
})
_ATTR = re.compile(r'''\s+([^\s=/>]+)(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+))?''')


class _Anchors(HTMLParser):
    def __init__(self, source):
        super().__init__(convert_charrefs=True)
        self.source = source
        self.starts = [0] + [i + 1 for i, char in enumerate(source) if char == '\n']
        self.nodes = {}
        self.feed(source)
        self.close()

    def handle_starttag(self, tag, attrs):
        if tag in {'html', 'head', 'body', 'script', 'style', 'meta', 'link', 'title'}:
            return
        attributes = dict(attrs)
        anchor = attributes.get('data-el') or attributes.get('id')
        if not anchor:
            return
        if anchor in self.nodes or len(attributes) != len(attrs):
            raise ValueError('Ambiguous element anchors or attributes; no layout patch applied')
        line, col = self.getpos()
        self.nodes[anchor] = {'el': anchor, 'tag': tag, 'style': attributes.get('style') or '',
                              'start': self.starts[line - 1] + col, 'raw': self.get_starttag_text()}

    handle_startendtag = handle_starttag


def snapshot(source):
    return [{key: node[key] for key in ('el', 'tag', 'style')} for node in _Anchors(source).nodes.values()]


def validate_review(response, elements):
    if not isinstance(response, dict) or not isinstance(response.get('issues'), list):
        raise ValueError('Visual review requires an issues list')
    ids = {element['el'] for element in elements}
    for issue in response['issues']:
        if not isinstance(issue, dict) or any(not isinstance(issue.get(k), str) or not issue[k].strip()
                                              for k in ('problem', 'fix')):
            raise ValueError('Visual review requires concrete problem and fix descriptions')
        targets = issue.get('element_ids')
        if (not isinstance(targets, list) or not targets
                or any(not isinstance(target, str) or target not in ids for target in targets)):
            raise ValueError('Visual review must reference inspected element_ids')
    return response['issues']


def apply_patch(source, plan, *, source_sha256, allowed_ids):
    if hashlib.sha256(source.encode('utf-8')).hexdigest() != source_sha256:
        raise ValueError('Page changed; inspect the current revision again')
    if not isinstance(plan, dict) or set(plan) != {'ops'}:
        raise ValueError('Layout patch accepts only an ops object')
    ops = plan['ops']
    if not isinstance(ops, list) or not 1 <= len(ops) <= 100:
        raise ValueError('Layout patch requires 1–100 operations')
    nodes = _Anchors(source).nodes
    replacements, seen = [], set()
    for op in ops:
        if (not isinstance(op, dict) or set(op) != {'op', 'el', 'styles'}
                or op['op'] != 'set_style' or not isinstance(op['el'], str)):
            raise ValueError('Only set_style(el, styles) is allowed for visual repair')
        anchor, styles = op['el'], op['styles']
        if anchor not in nodes or anchor not in allowed_ids or anchor in seen:
            raise ValueError('Unknown, duplicate or out-of-scope element ID')
        seen.add(anchor)
        if not isinstance(styles, dict) or not styles:
            raise ValueError('Layout patch requires styles')
        clean = []
        for name, value in styles.items():
            if name not in LAYOUT_PROPERTIES or not isinstance(value, str):
                raise ValueError('Only layout styles are allowed')
            value = value.strip()
            if (not value or len(value) > 160 or not re.fullmatch(r'[a-zA-Z0-9 .,%()+*/-]+', value)
                    or re.search(r'url|expression|var\s*\(|env\s*\(', value, re.I)):
                raise ValueError('Invalid layout style value')
            if name == 'display' and value not in {'block', 'inline', 'inline-block', 'flex', 'grid', 'inline-flex'}:
                raise ValueError('Layout repair cannot hide content')
            if name == 'font-size' and not re.fullmatch(r'(?:[1-9]\d(?:\.\d+)?|\d{3})(?:px|pt)', value):
                raise ValueError('Use a readable absolute font size (at least 10px/pt)')
            clean.append(f'{name}: {value} !important')
        node = nodes[anchor]
        style = node['style'].rstrip().rstrip(';')
        style = (style + '; ' if style else '') + '; '.join(clean) + ';'
        attribute = ' style="' + escape(style, quote=True) + '"'
        raw = node['raw']
        style_attr = next((m for m in _ATTR.finditer(raw) if m.group(1).lower() == 'style'), None)
        if style_attr:
            updated = raw[:style_attr.start()] + attribute + raw[style_attr.end():]
        else:
            end = raw.rfind('/>') if raw.rstrip().endswith('/>') else raw.rfind('>')
            updated = raw[:end] + attribute + raw[end:]
        replacements.append((node['start'], node['start'] + len(raw), updated))
    for start, end, updated in sorted(replacements, reverse=True):
        source = source[:start] + updated + source[end:]
    return source
