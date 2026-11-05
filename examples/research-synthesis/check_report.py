"""Check source IDs and exact quotes; factual interpretation still needs review."""
import argparse
import hashlib
import json
from pathlib import Path
import re


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('report', type=Path)
    args = parser.parse_args()
    source = (Path(__file__).parent / 'research-data.md').read_text()
    report = args.report.read_text()
    quotes = re.findall(r'Quote: "([^"\n]+)"', source)
    participants = re.findall(r'^## (P\d+) —', source, re.MULTILINE)
    checks = {
        'all_source_ids_present': all(p in report for p in participants),
        'all_exact_quotes_present': len(quotes) == 6 and all(q in report for q in quotes),
    }
    print(json.dumps({
        'sha256': hashlib.sha256(args.report.read_bytes()).hexdigest(),
        'checks': checks,
        'manual_review_required': ['4 first-use and 2 weekly users',
                                   'theme counts 3/6, 2/6, 1/6 with correct source IDs',
                                   'P06 counterexample and accessibility blocker',
                                   'no invented facts or completed improvements'],
    }, indent=2))
    raise SystemExit(0 if all(checks.values()) else 1)


if __name__ == '__main__':
    main()
