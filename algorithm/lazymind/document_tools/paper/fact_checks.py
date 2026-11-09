"""Check source-anchored review claims with arithmetic, never model verdicts."""
import json
import math
import re
import statistics
import xml.etree.ElementTree as ET
from decimal import Decimal

from .review.schema import CheckedFact, FactCheck, ReviewIssue

CATEGORY_LABELS = {'numbers': '数字与分母', 'comparison': '比较方向', 'literature': '文献支持范围',
                   'data_status': '模拟/实测状态', 'figure': '图文一致性'}
STATUS_LABELS = {'verified': '已核实', 'conflict': '发现冲突', 'insufficient': '证据不足',
                 'not_applicable': '不适用'}
NUMBER = r'[-+−]?\d+(?:,\d{3})*(?:\.\d+)?(?:[eE][-+]?\d+)?'
NUMERIC = {'value', 'sum', 'mean', 'difference', 'ratio', 'percentage',
           'percentage_points', 'relative_change', 'denominator'}
OPERATIONS = {'numbers': NUMERIC, 'comparison': {'direction'}, 'literature': {'citation_support'},
              'data_status': {'status'}, 'figure': {'figure_value'}}


def _pointer(inputs, path):
    parts = path.split('/')[1:]
    if not path.startswith('/') or not parts or parts[0] not in {
            'manuscript', 'analysis', 'evidence', 'writing_materials', 'charts', 'papers', 'result_meta'}:
        raise ValueError('来源必须指向成稿、已提供文献或图表的 JSON Pointer，不能引用模型结论。')
    if parts[0] == 'manuscript' and path != '/manuscript':
        raise ValueError('成稿来源必须引用可定位的正文摘录，不能当作结构化上游材料。')
    value = inputs
    for part in parts:
        if isinstance(value, str):
            try:
                value = json.loads(value)
            except (ValueError, TypeError) as error:
                raise ValueError('来源文本不是可读取的 JSON：' + path) from error
        part = part.replace('~1', '/').replace('~0', '~')
        try:
            value = value[int(part)] if isinstance(value, list) else value[part]
        except (KeyError, IndexError, TypeError, ValueError) as error:
            raise ValueError('来源路径不存在：' + path) from error
    if value is None:
        raise ValueError('来源值缺失；不能将缺失或超时补为 0：' + path)
    return value


def _number(text):
    if not re.fullmatch(NUMBER, text):
        raise ValueError('数值必须是摘录中的一个完整数字；单位、百分号和分母需另行保留。')
    value = Decimal(text.replace(',', '').replace('−', '-'))
    if not value.is_finite():
        raise ValueError('数值不是有限数，不能核验。')
    return value


def _quantity_token(token):
    # Accept a complete displayed quantity, never a substring of a larger number.
    units = '|'.join(re.escape(label) for label in sorted(ALIASES, key=len, reverse=True))
    match = re.fullmatch(r'\s*(' + NUMBER + r'(?:\s*/\s*' + NUMBER + r')?)\s*(' + units + r')?\s*', token, re.I)
    if match is None:
        raise ValueError('待核对内容必须是一个完整数量，保留百分号、分数和单位。')
    number, unit = match.groups()
    return number, _unit(unit) if unit else ('proportion' if '/' in number else None)


def _numeric_token(text, token):
    number, _ = _quantity_token(token)
    occurrences = [m for m in re.finditer(re.escape(token), text)
                   if (m.start() == 0 or text[m.start() - 1] not in '0123456789.,')
                   and (m.end() == len(text) or text[m.end()] not in '0123456789.,')]
    if not occurrences:
        raise ValueError('待核对数字必须是原文中的完整数值，不能截取数字的一部分。')
    if '/' in number:
        left, right = map(lambda part: _number(part.strip()), number.split('/'))
        if right == 0:
            raise ValueError('分母为 0，不能核对比例。')
        return left / right
    return _number(number)


def _table_values(inputs, source, manuscript):
    table = _pointer(inputs, source.path)
    if (not isinstance(table, dict) or not isinstance(table.get('columns'), list)
            or not isinstance(table.get('rows'), list)):
        raise ValueError('表格核对需要已提供的 columns/rows 原始表。')
    columns = table['columns']
    if len(columns) != len(set(columns)) or source.column not in columns or not set(source.where) <= set(columns):
        raise ValueError('表格列或筛选条件不存在或重复。')
    for value in source.where.values():
        label = re.escape(str(value))
        if not re.search(r'(?<![A-Za-z0-9_])' + label + r'(?![A-Za-z0-9_])', manuscript):
            raise ValueError('筛选组标识未在稿件摘录明确出现；不能将模型猜测的别名当作样本绑定。')
    rows, selected_rows = [], []
    for row in table['rows']:
        if not isinstance(row, list) or len(row) != len(columns):
            raise ValueError('原始表行缺列，不能省略后继续核对。')
        record = dict(zip(columns, row))
        if all(record[key] == value for key, value in source.where.items()):
            value = record[source.column]
            if type(value) not in (int, float) or not math.isfinite(value):
                raise ValueError('所选组含缺失或非数值；不能丢弃记录后改变分母。')
            rows.append(Decimal(str(value)))
            selected_rows.append(record)
    for key in columns:
        labels = {row[key] for row in selected_rows if isinstance(row[key], str)}
        if len(labels) > 1 and not re.search(r'全部|所有记录|all rows', manuscript, re.I):
            raise ValueError('所选表仍混有多个组；需要明确全部记录或完整条件，不能省略分组。')
    if not rows:
        raise ValueError('表格筛选未匹配任何观测记录。')
    return rows


def _source(inputs, source, *, numeric=True):
    if source.column is not None or source.where:
        raise ValueError('表格来源只能通过经过组别绑定的聚合核对。')
    value = _pointer(inputs, source.path)
    if isinstance(value, str):
        if source.path.startswith('/charts/') and source.path.endswith('/svg'):
            try:
                value = ' '.join(''.join(node.itertext()) for node in ET.fromstring(value).iter()
                                 if node.tag.rsplit('}', 1)[-1] == 'text' and node.get('display') != 'none')
            except ET.ParseError as error:
                raise ValueError('图源不是可解析的 SVG，无法核验图中标注。') from error
        if source.quote is None or source.quote not in value:
            raise ValueError('来源摘录未在指定材料中找到：' + source.path)
        if not numeric:
            return source.quote
        if source.value_text is None or source.value_text not in source.quote:
            raise ValueError('文本来源需要可定位的数值 token：' + source.path)
        return _numeric_token(source.quote, source.value_text)
    if not numeric and isinstance(value, dict) and isinstance(value.get('data_origin'), str):
        return value['data_origin']
    if numeric and type(value) in (int, float) and math.isfinite(value):
        return Decimal(str(value))
    raise ValueError('来源不是可核验的有限数值或明确文本标签：' + source.path)


def _close(actual, expected, token, scale=1):
    # A displayed rounded value represents half a unit in its final decimal place.
    number, _ = _quantity_token(token)
    if '/' in number:
        return abs(actual - expected) <= Decimal('1e-12')
    exponent = _number(number).as_tuple().exponent
    tolerance = Decimal('.5') * Decimal(10) ** exponent
    return abs(actual - expected) <= tolerance * scale + Decimal('1e-12')


def _direction(text):
    if re.search(r'是否|可能|尚未|并非|未必|不能|not |no ', text, re.I):
        raise ValueError('比较含否定或不确定限定，不能按方向词机械判定。')
    words = {'higher': r'高于|大于|增加|上升|提高|增至|升至|增到|升到|higher|greater|increase[ds]?|above',
             'lower': r'低于|小于|减少|下降|降低|降至|降到|lower|less|decrease[ds]?|below',
             'equal': r'相等|不变|equal|unchanged'}
    matched = [kind for kind, pattern in words.items() if re.search(pattern, text, re.I)]
    if len(matched) != 1:
        raise ValueError('比较需要明确且唯一的数量方向；“更好”需要指标优劣定义。')
    return matched[0]


def _status(text):
    patterns = {
        'simulated': r'模拟|仿真|simulat(?:ed|ion)|synthetic',
        'measured': r'实测|真实(?:数据|实验|测量|模型.*?(?:对照|实验|测试))|measured|real[- ]world|observed',
    }
    positive = set()
    for kind, pattern in patterns.items():
        for match in re.finditer(pattern, text, re.I):
            prefix, suffix = text[:match.start()], text[match.end():]
            negated = re.search(r'(?:不是|并非|而非|没有|缺乏|缺少|尚无|暂无|不属于|非|未经|未|不能证明|不能确认|not|no|without)'
                                r'(?:现实|真实|进行|开展|的|\s)*$', prefix, re.I)
            planned = re.search(r'(?:后续)?(?:将|计划|需要)(?:进行|开展|采用|使用|补充)?\s*$', prefix)
            unfinished = re.match(r'\s*(?:尚未|未进行|未开展|尚未完成)', suffix)
            if not (negated or planned or unfinished):
                positive.add(kind)
    if len(positive) == 1:
        return positive.pop()
    raise ValueError('材料没有唯一明确的正面模拟/实测标签；非模拟、执行成功或 is_demo=false 均不证明实测。')


def _calculate(operation, values):
    if operation in {'value', 'denominator'} and len(values) == 1:
        return values[0]
    if operation == 'sum':
        return sum(values)
    if operation == 'mean':
        return statistics.mean(values)
    if len(values) != 2:
        raise ValueError('该运算必须有两个有序来源：左/右（或分子/分母、后/前）。')
    left, right = values
    if operation == 'difference':
        return left - right
    if operation == 'percentage_points':
        if any(not 0 <= value <= 1 for value in values):
            raise ValueError('百分点运算需要 [0,1] 比例来源；百分数端点应直接相减，不能再乘 100。')
        return (left - right) * 100
    if right == 0:
        raise ValueError('分母为 0，不能计算率或相对变化。')
    if operation == 'ratio':
        return left / right
    if operation == 'percentage':
        return left / right * 100
    if operation == 'relative_change':
        return (left - right) / right * 100
    raise ValueError('运算与来源数量不匹配。')


# Units are read from supplied records or adjacent source/manuscript text, never a model verdict.
UNIT_GROUPS = {
    's': ('time', '1', ('s', 'sec', 'second', 'seconds', '秒')),
    'ms': ('time', '.001', ('ms', 'millisecond', 'milliseconds', '毫秒')),
    'min': ('time', '60', ('min', 'minute', 'minutes', '分钟')),
    '%': ('ratio', '.01', ('%', '％', 'percent', 'percentage', '百分比')),
    'proportion': ('ratio', '1', ('proportion', 'ratio', 'fraction', '比例')),
    'pp': ('ratio_difference', '.01', ('pp', 'percentage point', 'percentage points', '百分点', '个百分点')),
    'count': ('count', '1', ('count', 'counts', '个')),
    'record': ('record', '1', ('条', '条记录', 'records')),
    'person': ('person', '1', ('人', 'persons')),
    'trial': ('trial', '1', ('次', 'trials')),
    'token': ('token', '1', ('token', 'tokens')),
}
ALIASES = {alias.lower(): key for key, (_, _, aliases) in UNIT_GROUPS.items() for alias in aliases}


def _unit(value):
    value = value.strip().lower()
    return ALIASES.get(value, value)


def _unit_info(value):
    dimension, scale, _ = UNIT_GROUPS.get(value, (value, '1', ()))
    return dimension, Decimal(scale)


def _adjacent_unit(text, token, extra=()):
    number, embedded = _quantity_token(token) if token else ('', None)
    if embedded:
        return embedded
    token = number
    labels = sorted(set(ALIASES) | set(extra), key=len, reverse=True)
    units = set()
    for match in re.finditer(NUMBER, text):
        if match[0] != token:
            continue
        tail = text[match.end():].lstrip()
        found = next((label for label in labels if tail.lower().startswith(label)
                      and (not label[-1].isascii() or not label[-1].isalpha()
                           or len(tail) == len(label) or not tail[len(label)].isalpha())), None)
        if found:
            units.add(_unit(found))
        elif re.search(r'\bn\s*=\s*$', text[:match.start()], re.I):
            units.add('count')
    if len(units) > 1:
        raise ValueError('相同数值有多个单位，需更短且唯一的数量摘录。')
    if units:
        return next(iter(units))
    # A table row heading applies to all its value columns, unlike unrelated prose.
    heading = text.strip().strip('|').split('|')[0] if '|' in text else text.split(token, 1)[0]
    if re.search(r'tokens?|token数', heading, re.I):
        return 'token'
    return None


def _source_unit(inputs, source):
    value = _pointer(inputs, source.path)
    if source.column is not None:
        units = [item.get('unit') for item in (inputs.get('result_meta') or {}).get('metrics', [])
                 if item.get('column') == source.column and item.get('unit')]
        if len(set(units)) == 1:
            return _unit(units[0])
        return None
    if isinstance(value, str):
        return _adjacent_unit(source.quote or '', source.value_text or '')
    leaf = source.path.rsplit('/', 1)[-1]
    if leaf in {'n', 'count', 'correct', 'successes', 'total', 'excluded_n'} or leaf.endswith('_n'):
        return 'count'
    parent = _pointer(inputs, source.path.rsplit('/', 1)[0])
    if isinstance(parent, dict) and isinstance(parent.get('unit'), str) and parent['unit'].strip():
        return _unit(parent['unit'])
    return None


class UnitConflict(ValueError):
    pass


def _quantity_result(claim, inputs, values, units=None):
    units = [_source_unit(inputs, source) for source in claim.sources] if units is None else units
    known = [unit for unit in units if unit is not None]
    if known and len(known) != len(units):
        raise ValueError('输入来源单位不完整，不能确认这些量可运算或比较。')
    dimensions = {_unit_info(unit)[0] for unit in known}
    if len(dimensions) > 1:
        raise ValueError('来源量的单位维度不同，不能合并或比较。')
    normalized = [value * _unit_info(unit)[1] if unit else value for value, unit in zip(values, units)]
    if claim.operation in {'relative_change', 'percentage'}:
        expected_unit = '%'
    elif claim.operation == 'percentage_points':
        if known and dimensions != {'ratio'}:
            raise ValueError('百分点差需要比例或百分数来源。')
        expected_unit = 'pp'
    elif claim.operation == 'ratio':
        expected_unit = 'proportion'
    elif claim.operation == 'denominator':
        expected_unit = ('proportion' if '/' in claim.asserted_text else
                         'record' if known == ['record'] else 'count')
    else:
        # Arithmetic above uses base quantities, including seconds instead of milliseconds.
        expected_unit = {'time': 's', 'ratio': 'proportion', 'ratio_difference': 'pp'}.get(
            next(iter(dimensions), None), known[0] if known else None)
    if claim.operation == 'direction':
        return normalized, None
    asserted_unit = _adjacent_unit(claim.quote, claim.asserted_text, known)
    if (expected_unit == 'proportion' and asserted_unit is None and known
            and set(known) == {'proportion'} and re.search(r'率|比例|ratio|fraction|proportion', claim.quote, re.I)):
        asserted_unit = 'proportion'
    if asserted_unit is None and claim.operation == 'denominator':
        asserted_unit = 'count'
    if expected_unit is not None and asserted_unit is None:
        raise ValueError('原文数量没有可定位的单位；不能只核对数值。')
    if expected_unit is None and asserted_unit is not None:
        raise ValueError('来源没有提供数量单位；不能仅凭原文自行补定来源单位。')
    if expected_unit and _unit_info(asserted_unit)[0] != _unit_info(expected_unit)[0]:
        if asserted_unit not in UNIT_GROUPS or expected_unit not in UNIT_GROUPS:
            raise ValueError('来源与原文单位不同，且没有已知换算；需确认单位含义后再核对。')
        raise UnitConflict(f'原文单位 {asserted_unit} 与运算要求的 {expected_unit} 不同。')
    return normalized, (asserted_unit, expected_unit)


def _change_direction(claim):
    number, _ = _quantity_token(claim.asserted_text)
    directions = []
    for match in re.finditer(NUMBER, claim.quote):
        if match[0] != number:
            continue
        change = re.search(
            r'(下降|降低|减少|降幅|缩短|节省|节约|上升|增加|提高|增幅|变化|快|慢|'
            r'decreas(?:e|ed)|reduc(?:e|ed)|reduction|increas(?:e|ed)|faster|slower)'
            r'(?:了|约|幅度|为|by|of|\s)*$', claim.quote[:match.start()], re.I)
        if change is None:
            # A trailing percent may quantify the immediately preceding explicit change amount.
            units = '|'.join(re.escape(label) for label in sorted(ALIASES, key=len, reverse=True))
            change = re.search(
                r'(增加|上升|提高|增幅|减少|下降|降低|降幅)'
                r'(?:了|约|为|\s)*' + NUMBER + r'\s*(?:' + units + r')?'
                r'\s*[,，]\s*(?:即|约|相当于|相对|幅度为|\s)*$', claim.quote[:match.start()], re.I)
        if change:
            word = change[1].lower()
            if word in {'快', '慢', 'faster', 'slower'}:
                if not re.search(r'耗时|延迟|秒|duration|latency|seconds?', claim.quote, re.I):
                    raise ValueError('快慢没有明确耗时或延迟量，不能猜测数量方向。')
            word = {'降幅': '下降', '缩短': '下降', '节省': '下降', '节约': '下降', '增幅': '增加',
                    'reduced': 'decreased', 'reduce': 'decrease', 'reduction': 'decrease',
                    '快': '下降', 'faster': '下降', '慢': '增加', 'slower': '增加'}.get(word, word)
            directions.append(None if word == '变化' else _direction(word))
    if len(set(directions)) > 1:
        raise ValueError('同一数量对应多个变化方向，不能唯一解释。')
    return bool(directions), next(iter(directions), None)


def _semantic_operation(claim):
    # Prose determines a change percent; a model's quotient operator is not evidence.
    if claim.operation not in {'percentage', 'relative_change', 'percentage_points', 'difference'}:
        return claim
    unit = _adjacent_unit(claim.quote, claim.asserted_text)
    changing, _ = _change_direction(claim)
    if changing and len(claim.sources) == 2:
        if unit == '%':
            return claim.model_copy(update={'operation': 'relative_change'})
        if unit == 'pp':
            return claim.model_copy(update={'operation': 'percentage_points'})
    if (claim.operation == 'percentage' and unit == '%' and not changing
            and re.search(r'增加|减少|提高|降低|变化|增幅|降幅|快|慢', claim.quote)):
        raise ValueError('百分数与变化语句的对应关系不明确，不能把比例商当作增减幅度。')
    return claim


def _change_assertion(claim, actual):
    if claim.operation not in {'difference', 'relative_change', 'percentage_points'}:
        return actual
    _, direction = _change_direction(claim)
    return -actual if direction == 'lower' else actual


def _ordered_values(claim, inputs, values):
    if claim.operation not in {'difference', 'relative_change', 'percentage_points', 'direction'}:
        return values
    if len(values) != 2:
        raise ValueError('变化或比较必须有两个来源。')
    unit_pattern = '|'.join(re.escape(label) for label in sorted(ALIASES, key=len, reverse=True))
    quantity = NUMBER + r'(?:\s*/\s*' + NUMBER + r')?(?:\s*(?:' + unit_pattern + r'))?'
    transition = re.compile(r'(' + quantity + r')\s*(?:→|->|(?:提高|降低|增加|减少|上升|下降)?到|降至|增至|升至|降到|增到|升到|变为|变成|至|to)'
                            r'\s*(' + quantity + r')', re.I)
    matches = []
    for match in transition.finditer(claim.quote):
        pair = []
        for token in match.groups():
            _, unit = _quantity_token(token)
            pair.append(_numeric_token(token, token) * (_unit_info(unit)[1] if unit else 1))
        if sorted(pair) == sorted(values):
            matches.append((pair[1], pair[0]))
    if matches:
        if len(set(matches)) != 1:
            raise ValueError('原文存在多个相反的前后关系；不能唯一绑定操作数。')
        return list(matches[0])
    if transition.search(claim.quote):
        raise ValueError('来源量与原文前后端点不对应；需先核对端点，不能由模型排序生成事实指控。')
    roles = [source.path.rsplit('/', 1)[-1].lower() for source in claim.sources]
    for after, before in (('after', 'before'), ('left', 'right')):
        if set(roles) == {after, before}:
            return [values[roles.index(after)], values[roles.index(before)]]
    # Legacy textual arrows carry the source's own before/after relationship.
    excerpts = [source.quote for source in claim.sources]
    if excerpts[0] and excerpts[0] == excerpts[1]:
        pairs = []
        for match in transition.finditer(excerpts[0]):
            pair = [_numeric_token(token, token) * (_unit_info(_quantity_token(token)[1])[1]
                    if _quantity_token(token)[1] else 1) for token in match.groups()]
            if sorted(pair) == sorted(values):
                pairs.append((pair[1], pair[0]))
        if len(set(pairs)) == 1:
            return list(pairs[0])
    raise ValueError('未从原文关系或来源角色键确定操作数；模型数组顺序不是事实依据。')


def _numeric_sources(claim, inputs):
    values, units = [], []
    for source in claim.sources:
        selected = (_table_values(inputs, source, claim.quote) if source.column is not None
                    else [_source(inputs, source)])
        if source.column is not None:
            descriptions = [item.get('description', '').split('：')[0] for item in
                            (inputs.get('result_meta') or {}).get('metrics', [])
                            if item.get('column') == source.column]
            if source.column not in claim.quote and not any(d and d in claim.quote for d in descriptions):
                raise ValueError('原始列与稿件指标没有明确绑定；不能猜测翻译或选择邻近指标。')
        if source.column is not None and claim.operation not in {'mean', 'sum', 'denominator'}:
            raise ValueError('原始表只能用于明确的均值、求和或记录行数核对。')
        if claim.operation == 'denominator' and source.column is not None:
            if not re.search(r'条|行|records?|rows?', claim.quote, re.I):
                raise ValueError('表行数不能自行当作独立样本数；原文需明确称记录行数。')
            values.append(Decimal(len(selected)))
            units.append('record' if '条' in claim.quote else 'count')
        else:
            values.extend(selected)
            units.extend([_source_unit(inputs, source)] * len(selected))
    return _quantity_result(claim, inputs, values, units)


def literature_coverage(paper):
    """Use the same supplied-text coverage for report scope and semantic eligibility."""
    reading = paper.get('reading', {})
    reading = reading[-1] if isinstance(reading, list) and reading else reading
    reading = reading if isinstance(reading, dict) else {}
    coverage = reading.get('content_type', paper.get('content_type', ''))
    if isinstance(paper.get('text'), str) and paper['text'].strip():
        if (coverage in {'full_text', 'fulltext', 'full-text'}
                and not any(paper.get(key) is True or reading.get(key) is True
                            for key in ('truncated', 'fallback', 'more'))):
            return 'full_text'
        return 'abstract' if coverage == 'abstract' else 'excerpt'
    if isinstance(paper.get('abstract'), str) and paper['abstract'].strip():
        return 'abstract'
    return 'metadata'


def _full_text_sources(claim, inputs):
    sources = {}
    for source in claim.sources:
        match = re.fullmatch(r'/papers/(\d+)/text', source.path)
        if not match:
            raise ValueError('该来源未明确为可访问文献全文；需全文及相应位置后核对支持范围。')
        paper = inputs.get('papers', [])[int(match[1])]
        if literature_coverage(paper) != 'full_text':
            raise ValueError('只有摘要、截断或覆盖不明的材料，不能声明全文支持已核实。')
        _source(inputs, source, numeric=False)
        sources[source.path] = _pointer(inputs, source.path)
    return sources


def literature_input(result, inputs):
    """Only accessible, anchored full texts enter an independent semantic reading."""
    claims, papers = [], {}
    for index, claim in enumerate(result.fact_claims):
        if claim.category != 'literature' or inputs['manuscript'].count(claim.quote) != 1:
            continue
        try:
            sources = _full_text_sources(claim, inputs)
        except (ValueError, IndexError):
            continue
        claims.append({'claim_index': index, 'manuscript_quote': claim.quote, 'source_paths': list(sources)})
        papers.update(sources)
    return {'claims': claims, 'full_texts': papers}


def _check(claim, inputs, reading=None):
    manuscript = inputs['manuscript']
    locations = [source.path for source in claim.sources]
    checked = dict(category=claim.category, status='insufficient', location=claim.location,
                   quote=claim.quote, evidence_locations=locations,
                   calculation='尚未执行独立核对。', suggestion='补充可定位的原始记录和计算口径后重新核对。')
    try:
        if claim.quote not in manuscript or claim.asserted_text not in claim.quote:
            raise ValueError('事实摘录或待核对词未在稿件原文找到，不能形成事实指控。')
        if manuscript.count(claim.quote) != 1:
            raise ValueError('事实摘录在稿件中重复，无法唯一定位；需要更长的连续原文后再核对。')
        line = manuscript[:manuscript.index(claim.quote)].count('\n') + 1
        checked['location'] = f'{claim.location}（原文第 {line} 行）'
        locations.insert(0, f'manuscript:L{line}')
        manuscript_sources = set()
        for source in claim.sources:
            if source.path != '/manuscript':
                continue
            _source(inputs, source, numeric=False)
            if manuscript.count(source.quote) != 1:
                raise ValueError('成稿内依据不能唯一定位，需更明确的连续摘录。')
            token = source.value_text or source.quote
            if source.quote.count(token) != 1 or (source.quote, token) in manuscript_sources:
                raise ValueError('成稿内运算依据不能重复使用或含有歧义。')
            manuscript_sources.add((source.quote, token))
            start = manuscript.index(source.quote) + source.quote.index(token)
            target = manuscript.index(claim.quote) + claim.quote.index(claim.asserted_text)
            if start < target + len(claim.asserted_text) and target < start + len(token):
                raise ValueError('不能以待核对语句自身证明其数值或数据状态。')
            if claim.operation in {'value', 'status', 'denominator'}:
                start = manuscript.index(source.quote)
                end = start + len(source.quote)
                target = manuscript.index(claim.quote)
                if start < target + len(claim.quote) and target < end:
                    raise ValueError('不能以待核对语句自身证明其数值或数据状态。')
        if claim.operation not in OPERATIONS[claim.category]:
            raise ValueError('此类别与运算不匹配，未执行核对。')
        if claim.operation in NUMERIC:
            claim = _semantic_operation(claim)
        if claim.category == 'literature':
            sources = _full_text_sources(claim, inputs)
            if reading is None:
                raise ValueError('全文已提供且摘录已定位；尚未完成独立语义核读，不能以字面出现代替支持范围判断。')
            if reading.status != 'insufficient' and not reading.passages:
                raise ValueError('独立核读没有提供可核验的原文依据，不能采纳结论。')
            for passage in reading.passages:
                if passage.path not in sources:
                    raise ValueError('独立核读引用了此主张未绑定的文献。')
                _source(inputs, passage, numeric=False)
                text = sources[passage.path]
                if text.count(passage.quote) == 1:
                    number = text[:text.index(passage.quote)].count('\n') + 1
                    locations.append(f'{passage.path}:L{number}')
            checked.update(status=reading.status, verification_method='semantic_read', reading=reading,
                           calculation='独立模型全文语义核读（不是确定性证明）：' + reading.reason,
                           suggestion='保留此具体支持范围及限制。' if reading.status == 'verified' else
                           '按文献实际支持范围限制或更正该主张；材料未支持的部分保持待核实。')
            return CheckedFact.model_validate(checked)
        if claim.category == 'data_status':
            prefix = claim.quote.split(claim.asserted_text, 1)[0]
            if re.search(r'不是|并非|未进行|未经', prefix[-5:]):
                raise ValueError('稿件标签受否定限定，不能按正面状态核实。')
            actual = _status(claim.asserted_text)
            values = [_status(_source(inputs, source, numeric=False)) for source in claim.sources]
            if len(set(values)) != 1:
                raise ValueError('来源中的实验状态互相冲突，不能自行选择一种当真。')
            expected = values[0]
        elif claim.category == 'comparison':
            values, _ = _numeric_sources(claim, inputs)
            values = _ordered_values(claim, inputs, values)
            if len(values) != 2:
                raise ValueError('比较必须明确给出左、右两个同指标来源。')
            actual = _direction(claim.asserted_text)
            expected = 'higher' if values[0] > values[1] else 'lower' if values[0] < values[1] else 'equal'
        else:
            actual = _numeric_token(claim.quote, claim.asserted_text)
            values, units = _numeric_sources(claim, inputs)
            values = _ordered_values(claim, inputs, values)
            if claim.category == 'figure':
                # Check rendered text, not a chart's self-reported provenance alone.
                if (len(values) != 2 or not (
                        claim.sources[0].path == '/manuscript' or claim.sources[0].path.startswith('/analysis/'))
                        or not claim.sources[1].path.startswith('/charts/')
                        or not claim.sources[1].path.endswith('/svg')):
                    raise ValueError('图文核对需要成稿中的数值及 SVG 实际文字标注；只有图注或元数据不足以核实。')
                expected = values[0]
                if not _close(values[1], expected, claim.sources[1].value_text,
                              _unit_info(_source_unit(inputs, claim.sources[1]))[1]):
                    checked.update(status='conflict',
                                   calculation=f'图中标注 {values[1]:g}，给定文字来源为 {expected:g}，两者不一致。',
                                   suggestion=f'按来源 {claim.sources[0].path} 修正图中标注及对应图注/正文，再核对。')
                    return CheckedFact.model_validate(checked)
            else:
                expected = _calculate(claim.operation, values)
            if not expected.is_finite():
                raise ValueError('派生计算不是有限数，不能核验。')
        display_expected, display_actual, display_unit = expected, actual, ''
        if isinstance(actual, Decimal):
            asserted_unit, expected_unit = units
            if claim.operation in {'percentage', 'percentage_points', 'relative_change'}:
                expected *= _unit_info(expected_unit)[1]
            actual = _change_assertion(claim, actual)
            scale = _unit_info(asserted_unit)[1] if asserted_unit else Decimal(1)
            display_unit = asserted_unit or ''
            display_expected, display_actual = expected / scale, actual
            actual *= scale
            equal = _close(actual, expected, claim.asserted_text, scale)
            if '/' in claim.asserted_text and len(claim.sources) == 1:
                source_token = claim.sources[0].value_text or ''
                if '/' not in source_token or not isinstance(_pointer(inputs, claim.sources[0].path), str):
                    raise ValueError('来源只有比例值，没有可核验分母；不能据此核实分数样本量。')
                fractions = [tuple(_number(part.strip()) for part in _quantity_token(token)[0].split('/'))
                             for token in (source_token, claim.asserted_text)]
                equal = equal and fractions[0] == fractions[1]
        else:
            equal = actual == expected
        checked.update(status='verified' if equal else 'conflict',
                       calculation=f'独立核对 {claim.operation}({", ".join(map(str, values))}) '
                       f'= {display_expected}{display_unit}；'
                       f'原文按单位与方向解释为 {display_actual}{display_unit}。仅核对该摘录与给定来源的对应关系，未证明来源本身真实或因果结论。',
                       suggestion='保留当前事实和来源。' if equal else
                       f'按来源重写此数量和方向：有符号结果为 {display_expected}{display_unit}；'
                       '如表述下降幅度，应使用正的降幅。保留单位、分母与实验限制，再核对。')
    except (ValueError, ArithmeticError, IndexError) as error:
        checked['status'] = 'conflict' if isinstance(error, UnitConflict) else 'insufficient'
        checked['calculation'] = str(error)
        if isinstance(error, UnitConflict):
            checked['suggestion'] = '按来源和运算口径修正原文单位及对应数值，区分百分比与百分点后重新核对。'
    return CheckedFact.model_validate(checked)


def check_facts(result, inputs, readings=None):
    """Return all five coverage rows, including checks the evidence cannot support."""
    readings = {reading.claim_index: reading for reading in (readings or [])}
    checks = [_check(claim, inputs, readings.get(index)) for index, claim in enumerate(result.fact_claims)]
    manuscript = inputs['manuscript']
    manuscript_review = not any(inputs.get(key) for key in ('analysis', 'evidence', 'writing_materials', 'result_meta'))
    applicable = {
        'numbers': bool(re.search(r'\d', manuscript)),
        'comparison': bool(re.search(r'高于|低于|提高|降低|增加|减少|优于|更差|higher|lower|better|increase|decrease',
                                     manuscript, re.I)),
        'literature': bool(re.search(r'\[[^\]]+\]|文献|研究表明|et al\.', manuscript, re.I)),
        'data_status': bool(re.search(r'实验|模拟|实测|真实|simulation|simulated|experiment|measured', manuscript, re.I)),
        'figure': bool(inputs.get('charts') or re.search(r'!\[|图\s*\d|fig(?:ure)?\.?\s*\d', manuscript, re.I)),
    }
    pending = set()
    for note in result.limitations:
        marker = '待核验事实提案（未进入事实修订）：'
        if note.startswith(marker):
            try:
                detail = json.loads(note[len(marker):])
                raw = detail.get('proposal', {}) if isinstance(detail, dict) else None
                category = raw.get('category') if isinstance(raw, dict) else None
                pending.update([category] if category in CATEGORY_LABELS else CATEGORY_LABELS)
            except (ValueError, TypeError):
                pending.update(CATEGORY_LABELS)
    coverage = []
    for category in CATEGORY_LABELS:
        selected = [item for item in checks if item.category == category]
        states = {item.status for item in selected}
        if category in pending:
            states.add('insufficient')
        status = ('conflict' if 'conflict' in states else 'insufficient' if 'insufficient' in states else
                  'verified' if selected else
                  'insufficient' if applicable[category] and not manuscript_review else 'not_applicable')
        reason = ('本类有格式未能修复的事实提案，原始提案及原因保留在限制中；未形成可自动修订事实。'
                  if category in pending else
                  '状态仅覆盖下面已执行的摘录核对；未列出的主张未核实。' if selected else
                  '稿件涉及本类事实，但未取得可独立执行的原文—来源核对；需补充材料或可定位的核对项。'
                  if applicable[category] and not manuscript_review else
                  '本轮未单列本类事实核对项；不据此要求上游实验或分析材料。' if manuscript_review else
                  '未识别出本类适用主张；本类没有执行核对。')
        coverage.append(FactCheck(category=category, status=status, reason=reason,
                                  evidence_locations=list(dict.fromkeys(
                                      path for item in selected for path in item.evidence_locations)), checks=selected))
    return coverage


def reconcile_precision_advice(result, coverage):
    """Reject a display-precision allegation only after the actual mean was recalculated."""
    mean_quotes = {claim.quote for claim in result.fact_claims if claim.operation == 'mean'}
    verified = {check.quote for group in coverage for check in group.checks
                if check.status == 'verified' and check.quote in mean_quotes
                and check.calculation.startswith('独立核对 mean(')}
    verified = {quote for quote in verified
                if all(check.status == 'verified' for group in coverage
                       for check in group.checks if check.quote == quote)
                and sum(check.quote == quote for group in coverage for check in group.checks)
                == sum(claim.quote == quote for claim in result.fact_claims)}
    changed, issues = False, []
    for issue in result.issues:
        mixed = re.search(r'分母|单位|方向|组别|样本|缺失|高于|低于|denominator|unit|direction|missing|group',
                          issue.problem, re.I)
        if (issue.quote in verified and not mixed and issue.category in {'analysis', 'evidence'}
                and re.search(r'精度|小数|precision|decimal', issue.problem, re.I)
                and re.search(r'无法|不能|没有对应|未给出|cannot|unverif', issue.problem, re.I)):
            issue = issue.model_copy(update={
                'category': 'language', 'severity': 'suggestion', 'action_type': 'text_revision',
                'problem': '该摘录中已列为核实的均值已由原始记录重算。行值保留较少小数不妨碍均值产生更多小数；'
                           '不能仅据小数位数要求新数据。其他组别及主张仍以逐项核对状态为准。',
                'suggestion': '可补充显示舍入规则与测量有效位数说明；这是表达建议，不是否定已核对均值。',
            })
            changed = True
        issues.append(issue)
    if not changed:
        return result
    return result.model_copy(update={
        'issues': issues,
        'summary': (result.summary + '\n\n程序复核更正：其中仅基于行值小数位数而否定均值可核验性的意见不成立，'
                    '已按实际重算结果改为精度表达建议；其他未知事项未据此核实。'),
    })


def review_issues(result, coverage, *, audit=None):
    """Keep advice usable, and turn only independently confirmed conflicts into facts."""
    issues = [(issue, 'advice' if issue.category in {'structure', 'language'} or issue.severity == 'suggestion'
               else 'unverified') for issue in result.issues]
    for group in coverage:
        for check in group.checks:
            if check.status != 'conflict':
                continue
            category = {'data_status': 'evidence', 'literature': 'citation'}.get(check.category, 'analysis')
            issue = ReviewIssue(severity='major', category=category,
                                location=check.location, quote=check.quote,
                                problem=check.calculation + '\n证据位置：' + '、'.join(check.evidence_locations),
                                suggestion=check.suggestion, action_type='text_revision')
            # Replace a matching numerical allegation with the independently checked explanation.
            existing = next((index for index, (old, disposition) in enumerate(issues)
                             if old.quote == check.quote and old.category == issue.category
                             and disposition == 'unverified'), None)
            if existing is not None:
                issues[existing] = (issue, 'fact')
            else:
                issues.append((issue, 'fact'))
    anchors = {claim.quote for claim in result.fact_claims}
    anchors.update(check.quote for group in coverage for check in group.checks)
    guarded = []
    for issue, disposition in issues:
        numeric = re.search(NUMBER, '\n'.join((issue.quote or '', issue.problem, issue.suggestion)))
        if (disposition == 'unverified' and issue.action_type == 'text_revision'
                and (issue.quote in anchors or numeric)):
            if audit is not None:
                audit.append('未核实事实更正（未进入可自动修订意见）：' + json.dumps(
                    {'original_issue': issue.model_dump(),
                     'reason': '事实修改未被独立核对证实；不足或未绑定的事实提案不能自动更改原稿。'},
                    ensure_ascii=False))
            issue = issue.model_copy(update={
                'action_type': 'needs_material',
                'problem': '以下为尚未核实的模型事实更正，不能据此认定原稿错误：' + issue.problem,
                'suggestion': '先补充可定位证据并完成独立核对，当前不得据此更改稿件事实或数值。原模型建议：'
                              + issue.suggestion,
            })
        guarded.append((issue, disposition))
    priority = {'major': 0, 'minor': 1, 'suggestion': 2}
    return sorted(guarded, key=lambda pair: priority[pair[0].severity])
