"""Conservative, model-free extraction of explicit whole-paper requirements."""
import copy
import re

from ..contracts import normalize_constraints

SECTION_NAMES = {
    'abstract': ('摘要', r'\babstract\b'),
    'introduction': ('引言', '绪论', r'\bintroduction\b'),
    'methods': ('方法', r'\bmethods?\b', r'\bmethodology\b'),
    'evaluation': ('实验', '评估', r'\bexperiments?\b', r'\bevaluation\b', r'\bexperimental\b'),
    'results': ('结果', r'\bresults?\b'),
    'discussion': ('讨论', r'\bdiscussion\b'),
    'limitations': ('限制', '局限', r'\blimitations?\b'),
    'conclusion': ('结论', '结语', r'\bconclusions?\b'),
}
_LENGTH = re.compile(r'(?<![A-Za-z0-9_.])(?P<number>\d[\d,]*(?:\.\d+)?)\s*(?P<scale>[kK千万]?)\s*'
                     r'(?P<unit>字|words?\b|Chinese\s+characters?\b)', re.I)
_LOCAL = re.compile(r'摘要|引言|绪论|方法|实验|评估|讨论|结论|章节|段落|abstract|section|paragraph', re.I)
_WHOLE = re.compile(r'全文|整篇|论文|初稿|成稿|paper|manuscript|draft|total', re.I)


def _explicit(request):
    value = {}
    for match in _LENGTH.finditer(request):
        prefix = re.split(r'[。；;，,\n（）()]', request[:match.start()])[-1][-48:]
        if re.search(r'[\d–—~至到-]\s*$', prefix) or re.search(r'不超过|至少|至多|以内|at most|at least', prefix, re.I):
            continue
        local = max((item.end() for item in _LOCAL.finditer(prefix)), default=-1)
        whole = max((item.end() for item in _WHOLE.finditer(prefix)), default=-1)
        if local > whole:
            continue
        scale = {'': 1, 'k': 1000, '千': 1000, '万': 10000}[match['scale'].lower()]
        number = float(match['number'].replace(',', '')) * scale
        if number.is_integer() and number > 0:
            value['word_target'] = int(number)
            if match['unit'].lower().startswith('word'):
                value['paper_language'] = 'en'
    if re.search(r'中文(?:论文|初稿|成稿|稿件)|(?:write|paper|draft)\s+in\s+Chinese|(?:用|以|语言[:：]?)\s*中文', request, re.I):
        value['paper_language'] = 'zh-CN'
    elif re.search(r'英文(?:论文|初稿|成稿|稿件)|(?:write|paper|draft)\s+in\s+English', request, re.I):
        value['paper_language'] = 'en'
    sections = []
    for match in re.finditer(r'(?<!不)(?:含|包括|包含|必须有|保留|\binclude\b|\bincluding\b)\s*([^。；;\n]{1,400})',
                             request, re.I):
        if re.search(r'(?:不|不包|不要|无需|不需要|\bnot\s*)$', request[max(0, match.start() - 12):match.start()], re.I):
            continue
        for key, names in SECTION_NAMES.items():
            if any(re.search(name, match[1], re.I) for name in names) and key not in sections:
                sections.append(key)
    if sections:
        value['required_sections'] = sections
    return value


def resolve_constraints(constraints, request, *, current_request=''):
    """Structured values win initially; explicit current follow-up requirements win later.

    Call before applying default length. Local section lengths and ambiguous ranges
    remain in the natural-language request rather than becoming a full-paper budget.
    """
    original = copy.deepcopy(constraints)
    result = normalize_constraints(original)
    extracted = _explicit(str(request or ''))
    for key, value in extracted.items():
        if original.get(key) in (None, []):
            result[key] = value
    update = _explicit(str(current_request or ''))
    required = update.pop('required_sections', [])
    result.update(update)
    result['required_sections'] = list(dict.fromkeys(result['required_sections'] + required))
    # Removing a requested section is a scope decision, never an instruction to
    # erase that section from an already existing manuscript.
    for key, names in SECTION_NAMES.items():
        if any(re.search(r'(?:不(?:要|需|含|包含|包括)|无需|omit\s+|without\s+)' + name,
                         str(current_request or ''), re.I) for name in names):
            result['required_sections'] = [item for item in result['required_sections'] if item != key]
    return normalize_constraints(result)


def validate_required_sections(markdown, constraints):
    """Required sections must be actual headings, not promises in the body."""
    headings = re.findall(r'^#{2,6}\s+(.+)$', markdown, re.M)
    missing = [key for key in constraints.get('required_sections', [])
               if not any(re.search(name, heading, re.I) for heading in headings for name in SECTION_NAMES[key])]
    if missing:
        raise ValueError('缺少用户明确要求的论文章节：' + ', '.join(missing))
