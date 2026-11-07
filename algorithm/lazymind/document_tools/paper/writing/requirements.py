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
                     r'(?P<unit>字|词|words?\b|Chinese\s+characters?\b)', re.I)
_LOCAL = re.compile(r'摘要|引言|绪论|方法|实验|评估|讨论|结论|章节|段落|abstract|section|paragraph', re.I)
_WHOLE = re.compile(r'全文|整篇|论文|初稿|成稿|paper|manuscript|draft|total', re.I)


_LEGACY_SECTIONS = copy.deepcopy(SECTION_NAMES)
_LEGACY_LENGTH = re.compile(_LENGTH.pattern.replace('字|词|', '字|'), re.I)
_LEGACY_LOCAL = _LOCAL
_LEGACY_WHOLE = _WHOLE
_LOCAL = re.compile(
    r'文献综述|综述|审稿|大纲|摘要|引言|绪论|方法|实验|评估|评价|实证|讨论|结论|章节|段落|'
    r'abstract|introduction|methods?|evaluation|discussion|conclusion|section|paragraph|literature review|outline', re.I)
_WHOLE = re.compile(r'全文|整篇|正文|论文|初稿|成稿|paper|manuscript|draft|total', re.I)
SECTION_NAMES['evaluation'] += ('评价', '实证', r'\bempirical\b')


def _explicit(request):
    value = {}
    for match in _LENGTH.finditer(request):
        # Commas do not reset the subject: a review's language/length stays local to that review.
        prefix = re.split(r'[。；;\n（）()]', request[:match.start()])[-1]
        if re.search(r'[\d–—~至到-]\s*$', prefix) or re.search(r'不超过|至少|至多|以内|at most|at least', prefix, re.I):
            continue
        stage = max((item.end() for item in re.finditer(
            r'综述(?:正文)?|审稿|大纲|literature review|outline', prefix, re.I)), default=-1)
        subject = max((item.end() for item in re.finditer(
            r'全文|整篇|正文|初稿|成稿|稿件|(?:英文|中文)(?:学术)?论文|\b(?:paper|manuscript)\b', prefix, re.I)),
            default=-1)
        if stage >= 0 and stage >= subject:
            continue
        local = max((item.end() for item in _LOCAL.finditer(prefix)), default=-1)
        whole = max((item.end() for item in _WHOLE.finditer(prefix)), default=-1)
        if local > whole:
            continue
        number = float(match['number'].replace(',', '')) * {'': 1, 'k': 1000, '千': 1000, '万': 10000}[
            match['scale'].lower()]
        if number.is_integer() and number > 0:
            value['word_target'] = int(number)
            if match['unit'].lower().startswith('word'):
                value['paper_language'] = 'en'
    languages = list(re.finditer(
        r'(?P<language>英文|中文)(?:学术)?(?:论文|初稿|成稿|稿件)|'
        r'(?:论文|正文|全文|paper|manuscript|draft)(?:使用|采用|用|以|\s+in\s+|\s+written\s+in\s+)'
        r'(?P<after>英语|英文|中文|汉语|English|Chinese)', request, re.I))
    languages = [match for match in languages if not re.search(
        r'综述|审稿|大纲|摘要|literature review|outline|abstract',
        re.split(r'[。；;\n]', request[:match.start()])[-1], re.I)]
    if languages:
        language = languages[-1]['language'] or languages[-1]['after']
        value['paper_language'] = 'en' if language.lower() in ('英文', '英语', 'english') else 'zh-CN'
    explicit_language = list(re.finditer(r'\bpaper_language\s*[:=]\s*[\"\']?(en|zh-CN)\b', request, re.I))
    if explicit_language:
        value['paper_language'] = 'en' if explicit_language[-1][1].lower() == 'en' else 'zh-CN'
    sections = []
    for match in re.finditer(
            r'(?<!不)(?:含|包括|包含|必须有|保留|\binclude\b|\bincluding\b)\s*([^。；;\n（）()]{1,400})',
            request, re.I):
        if re.search(r'(?:不|不包|不要|无需|不需要|\bnot\s*)$', request[max(0, match.start() - 12):match.start()], re.I):
            continue
        items = re.split(r'[,，、/和及与]|\band\b', match[1], flags=re.I)
        selected = []
        for item in items:
            keys = [key for key, names in SECTION_NAMES.items() if any(
                re.fullmatch(r'\s*(?:' + name + r')(?:部分|章节|章|\s+section)?\s*', item, re.I) for name in names)]
            if not keys:
                break
            selected.extend(keys)
        else:
            sections.extend(selected)
    if sections:
        value['required_sections'] = list(dict.fromkeys(sections))
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


def _legacy_explicit(request):
    value = {}
    for match in _LEGACY_LENGTH.finditer(request):
        prefix = re.split(r'[。；;，,\n（）()]', request[:match.start()])[-1][-48:]
        if re.search(r'[\d–—~至到-]\s*$', prefix) or re.search(r'不超过|至少|至多|以内|at most|at least', prefix, re.I):
            continue
        local = max((item.end() for item in _LEGACY_LOCAL.finditer(prefix)), default=-1)
        whole = max((item.end() for item in _LEGACY_WHOLE.finditer(prefix)), default=-1)
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
        for key, names in _LEGACY_SECTIONS.items():
            if any(re.search(name, match[1], re.I) for name in names) and key not in sections:
                sections.append(key)
    if sections:
        value['required_sections'] = sections
    return value


def refresh_legacy_constraints(pool):
    """Repair only values matching the old parser, leaving distinct explicit overrides intact."""
    if pool.get('requirements_version') == 2:
        return
    request = pool.get('request', '')
    previous, corrected = _legacy_explicit(request), _explicit(request)
    constraints = copy.deepcopy(pool.get('constraints', {}))
    for key, value in previous.items():
        if constraints.get(key) == value and corrected.get(key) != value:
            constraints.pop(key, None)
    pool['constraints'] = resolve_constraints(constraints, request)
    pool['requirements_version'] = 2
