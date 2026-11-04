"""Common unit symbols and exact terminology; scientific labels are model-authored."""
import re


def display_text(value, aliases):
    """Apply exact display aliases in any language, without rewriting longer IDs or alias outputs."""
    text = str(value)
    if text in aliases:
        return aliases[text]
    names = [name for name in sorted(aliases, key=len, reverse=True) if name]
    if not names:
        return text
    pattern = r'(?<![A-Za-z0-9_])(?:' + '|'.join(re.escape(name) for name in names) + r')(?![A-Za-z0-9_])'
    return re.sub(pattern, lambda match: aliases[match.group()], text)


def display_unit(unit, aliases=None):
    """Format a unit for the figure without converting values or changing source metadata."""
    if unit is None:
        return ''
    raw = str(unit).strip()
    dimensionless = re.sub(r'[\s()（）]', '', raw).lower().replace('–', '-').replace('—', '-')
    if dimensionless in {'', '1', 'dimensionless', 'unitless', 'ratio', 'proportion', 'probability',
                         '比例', '概率', '无量纲', 'proportion0-1', 'ratio0-1', 'probability0-1', '比例0-1', '概率0-1'}:
        return ''
    # Common unit spellings are presentation vocabulary, not a statistical-method registry.
    symbols = {'millisecond': 'ms', 'milliseconds': 'ms', '毫秒': 'ms',
               'second': 's', 'seconds': 's', '秒': 's',
               'minute': 'min', 'minutes': 'min', '分钟': 'min',
               'hour': 'h', 'hours': 'h', '小时': 'h', '天': 'd',
               '米': 'm', '厘米': 'cm', '毫米': 'mm', '千米': 'km',
               '克': 'g', '千克': 'kg', '毫克': 'mg', '微克': 'µg', '升': 'L', '毫升': 'mL',
               '次': 'count', '个': 'count', '任务': 'task', '样本': 'sample',
               '试验': 'trial', '受试者': 'participant', '标准化成本单位': 'cost units',
               '百分比': '%', '百分数': '%', 'percent': '%', 'percentage': '%'}
    symbols.update({symbol.lower(): symbol for symbol in list(symbols.values())})
    raw_parts = [part.strip() for part in raw.split('/')]
    # A free-text suggestion must not replace a known source unit with a different scale.
    known = all(part.lower() in symbols or part in symbols for part in raw_parts)
    translated = raw if known else display_text(raw, aliases or {})
    parts = [part.strip() for part in translated.split('/')]
    parts = [symbols.get(part.lower(), symbols.get(part, part)) for part in parts]
    if len(parts) == 2 and parts[0] in ('count', 'counts', '1'):
        return 'per ' + parts[1]
    return '/'.join(parts)
