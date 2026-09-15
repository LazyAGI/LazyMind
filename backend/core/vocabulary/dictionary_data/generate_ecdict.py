#!/usr/bin/env python3
'''Generate the compact bundled ECDICT index from the upstream CSV.'''

import csv
import gzip
import json
import sys


def number(value: str) -> int:
    try:
        return int(value or 0)
    except ValueError:
        return 0


def main(source: str, output: str) -> None:
    rows = []
    with open(source, encoding='utf-8', newline='') as stream:
        for row in csv.DictReader(stream):
            term = (row.get('word') or '').strip()
            translation = (row.get('translation') or '').strip()
            ranked = (
                number(row.get('bnc')) > 0
                or number(row.get('frq')) > 0
                or number(row.get('oxford')) > 0
                or number(row.get('collins')) > 0
                or bool((row.get('tag') or '').strip())
            )
            if not term or not translation or not ranked:
                continue
            rows.append(
                {
                    'term': term,
                    'phonetic': (row.get('phonetic') or '').strip(),
                    'pos': (row.get('pos') or '').strip(),
                    'translation': translation,
                    'definition': (row.get('definition') or '').strip(),
                    'examples': [],
                }
            )
    with gzip.open(output, 'wt', encoding='utf-8', compresslevel=9) as stream:
        json.dump(rows, stream, ensure_ascii=False, separators=(',', ':'))
    print(f'wrote {len(rows)} ECDICT entries to {output}')


if __name__ == '__main__':
    main(sys.argv[1], sys.argv[2])
