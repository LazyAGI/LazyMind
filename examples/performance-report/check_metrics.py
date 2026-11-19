"""Independently check the report appendix against exact synthetic input arithmetic."""
from decimal import Decimal, ROUND_HALF_UP
from fractions import Fraction
import json
from pathlib import Path
import sys


def check(path):
    source = {
        'search': [[1000, 50000, 2000, 100, 5000], [1200, 60000, 2400, 120, 6000]],
        'social': [[1000, 100000, 1000, 20, 1000], [800, 80000, 800, 16, 800]],
        'combined': [[2000, 150000, 3000, 120, 6000], [2000, 140000, 3200, 136, 6800]],
    }
    expected = {}
    for channel, periods in source.items():
        for period, values in enumerate(periods):
            spend, impressions, clicks, conversions, revenue = map(Fraction, values)
            metrics = dict(zip(['spend_usd', 'impressions', 'clicks', 'conversions', 'revenue_usd'],
                               map(Fraction, values)))
            metrics.update(cpm_usd=spend * 1000 / impressions, ctr_pct=clicks * 100 / impressions,
                           cpc_usd=spend / clicks, conversion_rate_pct=conversions * 100 / clicks,
                           cpa_usd=spend / conversions, roas=revenue / spend)
            for metric, value in metrics.items():
                expected.setdefault(channel + '_' + metric, {})[period] = value
    expected['combined_search_share_of_combined_spend_pct'] = {0: Fraction(50), 1: Fraction(60)}
    issues, seen = [], set()
    checked = 0
    for line in path.read_text(encoding="utf-8").splitlines():
        cells = [cell.strip() for cell in line.strip('|').split('|')]
        if not line.startswith('| ') or len(cells) != 5:
            continue
        if not cells[0].startswith(('Search_', 'Social_', 'Combined_')):
            continue
        name = cells[0].lower()
        if name in seen or name not in expected:
            issues.append(f'Duplicate or unexpected metric: {name}')
            continue
        seen.add(name)
        prior, current = expected[name][0], expected[name][1]
        values = [current, prior, current - prior, (current - prior) * 100 / prior]
        for index, exact in enumerate(values, 1):
            wanted = (Decimal(exact.numerator) / Decimal(exact.denominator)).quantize(
                Decimal('.01'), rounding=ROUND_HALF_UP)
            checked += 1
            if Decimal(cells[index]) != wanted:
                issues.append(f'{name} column {index}: {cells[index]} != {wanted}')
    issues.extend('Missing metric: ' + name for name in sorted(set(expected) - seen))
    return {'rows': len(seen), 'numeric_fields_checked': checked,
            'issues': issues, 'all_passed': not issues,
            'scope': 'Numeric appendix only; narrative and task state require separate review.'}


if __name__ == '__main__':
    report = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).with_name('result.md')
    result = check(report)
    print(json.dumps(result, indent=2))
    sys.exit(0 if result['all_passed'] else 1)
