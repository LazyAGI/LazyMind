"""Normalize initial Python answers and apply repairs to numbered source lines."""
from dataclasses import dataclass
import json
import re


PYTHON_RESPONSE = re.compile(
    r'```(?:python|py)\s*\n([\s\S]*?)\n```'
    r'(?:\s*```(?:text|caption)\s*\n([\s\S]*?)\n```)?')


CODE_INSTRUCTION = '''Return Python source with actual physical newlines, preferably in ONE ```python``` block.
Do not JSON-encode the source or put literal \\n between statements. Do not wrap it in a Python string.
An optional figure caption may follow in ONE ```text``` block. No other explanation or reasoning text.
Code may also be returned as a JSON code field. Return the complete initial script.
'''


@dataclass(frozen=True)
class ModelResponse:
    body: str | dict
    reasoning: str = ''


def json_body(text):
    text = text.strip()
    match = re.fullmatch(r'```(?:json)?\s*([\s\S]*?)\s*```', text)
    return match[1] if match else text


def _reasoning_text(message):
    for field in ('reasoning_content', 'reasoning'):
        text = message.get(field)
        if isinstance(text, str) and text:
            return text
    details = message.get('reasoning_details')
    if isinstance(details, dict):
        details = [details]
    texts = []
    for item in details if isinstance(details, list) else []:
        if not isinstance(item, dict):
            continue
        text = item.get('text') or item.get('summary')
        if isinstance(text, str):
            texts.append(text)
    return ''.join(texts)


def normalize_response(raw):
    """Read provider fields verbatim; never recover executable text from reasoning."""
    if isinstance(raw, dict):
        if ('content' not in raw and not {'reasoning_content', 'reasoning_details'} & raw.keys()
                and not (raw.get('role') == 'assistant' and 'reasoning' in raw)):
            return ModelResponse(raw)
        return ModelResponse(str(raw.get('content') or ''), _reasoning_text(raw))
    return ModelResponse(str(raw or ''))


def parse_code_response(raw):
    """Read ordinary Python text without guessing how to repair escaping or source syntax."""
    if isinstance(raw, dict):
        return raw
    text = normalize_response(raw).body.strip()
    if not text:
        raise ValueError('模型未返回代码')
    if text.startswith('{'):
        result = json.loads(text)
        if not isinstance(result, dict):
            raise ValueError('代码响应必须为对象或 Python 文本')
        return result
    match = PYTHON_RESPONSE.fullmatch(text)
    if match:
        result = {'code': match[1]}
        if match[2] is not None:
            result['caption'] = match[2]
        return result
    if '```' in text:
        match = re.fullmatch(r'```json\s*\n([\s\S]*?)\n```', text)
        if match:
            return parse_code_response(match[1])
        raise ValueError('代码响应需要单个完整 Python 块和可选图注块')
    return {'code': text}


REPAIR_INSTRUCTION = '''Repair the supplied previous_code using the actual error and full numbered source.
Return ONE JSON object: {"replacements":[{"start_line":12,"end_line":13,"code":"    corrected_statement\\n"}]}.
Each inclusive, 1-based range refers to the SAME supplied source version. The "N | " prefixes are line numbers,
not source text. Return raw replacement code as JSON strings, preserving indentation; do not include line prefixes.
Replace coherent complete statements, loops or functions needed to fix the root cause. When dictionary keys or
variable dependencies change, update the entire affected logical block and all its uses together, not just the
throwing line. For a repeated binding error, replace the affected block/function instead of accumulating one-line
edits. Keep definitions needed by later uses and include matching brackets. Use the current numbered source to
choose ranges; an error line is not automatically the right replacement range.
A traceback may point inside an injected helper: fix the incorrect arguments in previous_code, not the
read-only helper. Do not blindly replace only the throwing line. Multiple ranges must not overlap; the host applies
them from bottom to top. To insert code, replace an adjacent source line with that line plus the insertion; use an
empty code string to delete a range. Preserve all other lines, scientific methods, input scope and calculations.
Do not return a complete script, an old/new text patch, a diff or explanation. A figure caption, only if changed,
may be included as a "caption" string in the same JSON object. Do not wrap Python or captions in separate blocks.
'''


def numbered_source(source):
    return '\n'.join(f'{number} | {line}' for number, line in enumerate(source.splitlines(), 1))


def repair_response(answer, previous, error=''):
    """Apply nonoverlapping inclusive ranges against one immutable source version."""
    replacements = answer.get('replacements') if isinstance(answer, dict) else None
    if not isinstance(replacements, list) or not replacements:
        raise ValueError('修复请返回非空 replacements 列表，每项含 start_line、end_line、code；不要返回完整脚本')
    lines = previous.splitlines(keepends=True)
    ranges = []
    for item in replacements:
        if not isinstance(item, dict):
            raise ValueError('每项 replacements 必须为对象，含 start_line、end_line、code')
        start, end, code = item.get('start_line'), item.get('end_line'), item.get('code')
        if type(start) is not int or type(end) is not int or not 1 <= start <= end <= len(lines):
            raise ValueError(f'替换行号无效：{start!r}–{end!r}；当前源码共 {len(lines)} 行，使用 1 起始的闭区间')
        if not isinstance(code, str):
            raise ValueError(f'替换 {start}–{end} 行的 code 必须是字符串（删除时为空字符串）')
        ranges.append((start, end, code))
    ranges.sort(key=lambda item: item[0])
    for left, right in zip(ranges, ranges[1:]):
        if left[1] >= right[0]:
            raise ValueError(f'替换范围重叠：{left[0]}–{left[1]} 与 {right[0]}–{right[1]}；行号必须基于同一份源码')
    for start, end, code in reversed(ranges):
        # Keep adjacent untouched lines separate, including when a replacement omits its final newline.
        ending = '\r\n' if lines[end - 1].endswith('\r\n') else '\n'
        if code and not code.endswith(('\n', '\r')) and (end < len(lines) or lines[end - 1].endswith(('\n', '\r'))):
            code += ending
        lines[start - 1:end] = [code] if code else []
    source = ''.join(lines)
    if source == previous:
        message = '修复补丁未改变源码'
        if error:
            message += '；原错误仍未解决：\n' + error
        raise ValueError(message)
    return source


def code_response(answer):
    """Read the complete initial source."""
    if not isinstance(answer, dict) or not isinstance(answer.get('code'), str) or not answer['code'].strip():
        raise ValueError('请返回完整 Python 脚本：单个 python 代码块或非空 code 字段')
    return answer['code']
