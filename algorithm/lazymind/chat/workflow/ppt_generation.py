"""Model-free page preparation shared by native and external PPT execution.

Prompts remain owned by the selected workflow package. This module only
assembles its page request and normalizes the model response; it never selects
or calls a model, reads a package, or accesses presentation assets.
"""
from __future__ import annotations

import json
import re


HTML_DOCUMENT = re.compile(r'(?is)(<!doctype\s+html\b[\s\S]*?</html>|<html\b[\s\S]*?</html>)')
_THINK_BLOCK = re.compile(r'<think\b[^>]*>[\s\S]*?</think>', re.IGNORECASE)


def page_generation_query(
    query: str,
    *,
    prompt_mode: str,
    language: str,
    has_background: bool = False,
    has_template: bool = False,
    edit_instruction: str = '',
    current_html: str = '',
) -> str:
    """Apply the native page, language and revision contracts to an authored brief."""
    query = (query or '').strip()
    if not query:
        raise ValueError('Page generation requires a nonempty brief')
    if prompt_mode != 'complete-page-prompt' and not has_background and not edit_instruction and not has_template:
        query += (
            '\n\nNO AI BACKGROUND IMAGE: No generated raster backdrop is supplied. '
            'This does not disable visual design. Apply the deck art direction with editable '
            'HTML/CSS or inline SVG: choose a content-led composition, clear type hierarchy, '
            'and restrained structural shapes/lines rather than defaulting every page to equal cards. '
            'One background layer per element still permits multiple explicit decorative elements. '
            'Respect explicit plain/minimal backgrounds and whitespace; do not invent images, facts, '
            'or filler cards. Treat these as design instructions, never visible slide copy.'
        )
    if edit_instruction:
        if not current_html:
            raise ValueError('Cannot revise a missing page; no original HTML to preserve')
        query += '\n\n' + json.dumps({
            'current_edit_instruction': edit_instruction,
            'current_html': current_html,
            'revision_contract': (
                'Apply the current edit over older style/brief defaults only where they conflict. '
                'Preserve text, facts, images and unaffected properties. For less whitespace, reduce '
                'padding/gaps or increase content area without inventing content. For recoloring, change '
                'the actual visible backgrounds/text/accents, not only unused CSS variables. '
                'A white-background/black-text request applies to the canvas, content panels, tables, '
                'formulas, diagram labels and chart axes/legends; remove conflicting dark fills and '
                'light text in those regions. Preserve source image pixels, data values, series '
                'distinctions and speaker notes. Return complete HTML.'),
        }, ensure_ascii=False)
    if language == 'zh-Hant':
        hint = ('Target language for all visible text: Traditional Chinese (zh-Hant). '
                'Use traditional characters (繁體中文) throughout.')
    elif language.startswith('zh'):
        hint = ('Target language for all visible text: Simplified Chinese (zh-Hans). '
                'Use simplified characters (简体中文) throughout.')
    else:
        hint = 'Target language for all visible text: English (en).'
    if prompt_mode != 'complete-page-prompt' and 'Target language for all visible text:' not in query:
        query = f'{hint}\n\n{query}'
    return query


def page_generation_system(package_prompt: str) -> str:
    """Retain the package prompt and the same native visual revision priority."""
    return package_prompt + (
        '\n\nVISUAL PRIORITY: current_edit_instruction overrides older instructions only for the '
        'requested properties. Then visual_revision_instructions, then explicit_visual_constraints, '
        'then the saved style contract, then catalog recipes/defaults. Preserve every user qualifier '
        'and prohibition. A label like technology does not authorize dark backgrounds, cyan, HUD or '
        'neon when the user requests something else. Never print these instructions on the slide. '
        'For edits, use current_html as the baseline; do not recreate unrelated content.'
    )


def restore_serialized_html(html: str) -> str:
    """Repair legacy response-dict escapes while preserving normal JS/CSS escapes."""
    start = re.search(r'(?i)<!doctype\s+html|<html\b', html)
    if start is None:
        return html
    head = html[start.start():].split('</head>', 1)[0]
    if not (re.search(r'(?i)<html\b[^>]*>\\n\s*<head>', head)
            and re.search(r'(?i)<style[^>]*>\\n', head) and '\n' not in head):
        return html
    escapes = {'n': '\n', 'r': '\r', 't': '\t', '\\': '\\', "'": "'", '"': '"'}
    return re.sub(r'''\\([nrt\\'"])''', lambda match: escapes[match[1]], html)


def sanitize_page_html(raw: str) -> str:
    """Remove model reasoning/wrappers and retain the complete HTML when present."""
    value = (raw or '').strip()
    if not value:
        return value
    value = _THINK_BLOCK.sub('', value).strip()
    value = re.sub(r'(?is)^<think\b[^>]*>[\s\S]*?(?=<!doctype|<html\b|```)', '', value).strip()
    if value.startswith('```'):
        first_newline = value.find('\n')
        if first_newline != -1:
            value = value[first_newline + 1:]
        if value.endswith('```'):
            value = value[:-3]
        value = value.strip()
    fence = re.search(r'(?is)```(?:html|HTML)?\s*\n([\s\S]*?)```', value)
    if fence:
        value = fence.group(1).strip()
    document = HTML_DOCUMENT.search(value)
    return restore_serialized_html(document.group(1).strip()) if document else value.strip()
