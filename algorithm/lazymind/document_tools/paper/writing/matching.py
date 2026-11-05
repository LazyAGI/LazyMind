"""Resolve paper revision locators to exact source text without fuzzy wording edits."""
import re
import string

from lazyllm.thirdparty import mistune


def _format_view(text):
    ignored = set()
    for match in re.finditer(r'(\*\*|__|~~|`)(?=\S)(.+?)(?<=\S)\1', text):
        width = len(match[1])
        ignored.update(range(match.start(), match.start() + width))
        ignored.update(range(match.end() - width, match.end()))
    chars, offsets = [], []
    for index, char in enumerate(text):
        if index in ignored or char in '[]':
            continue
        if char == '\\' and index + 1 < len(text) and text[index + 1] in string.punctuation:
            continue
        if char.isspace():
            if not chars or chars[-1] == ' ':
                continue
            char = ' '
        chars.append(char)
        offsets.append(index)
    if chars and chars[-1] == ' ':
        chars.pop()
        offsets.pop()
    return ''.join(chars), offsets


def _source_match(source, locator):
    if not locator or not locator.strip():
        raise ValueError('Paper locator must not be empty.')
    count = source.count(locator)
    if count == 1:
        start = source.index(locator)
        return start, start + len(locator)
    if count > 1:
        raise ValueError('Paper locator is ambiguous in its original scope.')
    visible, offsets = _format_view(source)
    selected, _ = _format_view(locator)
    matches = list(re.finditer('(?=' + re.escape(selected) + ')', visible)) if selected else []
    if len(matches) != 1:
        raise ValueError('Paper locator must match exactly once after Markdown format normalization.')
    start = matches[0].start()
    return offsets[start], offsets[start + len(selected) - 1] + 1


def resolve_locator(source, locator, target_scope):
    """Return an exact locator and scope; a list item becomes one bounded fragment.

    Only whitespace, escapes, emphasis delimiters and citation brackets may differ.
    Words, numbers, signs and citation destinations must still match. This function
    never changes manuscript content or searches outside the supplied section.
    """
    start, end = _source_match(source, locator)
    if target_scope == 'fragment':
        return source[start:end], target_scope
    parser = mistune.create_markdown(renderer='ast', plugins=['table'])
    for match in re.finditer(r'\S[^\n]*(?:\n(?![ \t]*\n)[^\n]*)*', source):
        if not match.start() <= start < end <= match.end():
            continue
        block = match[0].rstrip('\r\n')
        tokens = [token for token in parser(block) if token['type'] != 'blank_line']
        if len(tokens) == 1 and tokens[0]['type'] == 'paragraph':
            return source[start:end], 'paragraph'
        if len(tokens) == 1 and tokens[0]['type'] == 'list':
            items = list(re.finditer(r'(?m)^[ \t]{0,3}(?:[-+*]|\d+[.)])[ \t]+', block))
            for index, item in enumerate(items):
                item_end = items[index + 1].start() if index + 1 < len(items) else len(block)
                if item.start() <= start - match.start() < end - match.start() <= item_end:
                    return block[item.start():item_end].rstrip('\r\n'), 'fragment'
        break
    raise ValueError('Paper locator must select one paragraph or one list item.')
