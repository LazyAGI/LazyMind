#!/usr/bin/env python3
"""Verify this recorded experiment, not model quality or a live installation.

Requires markdown-it-py. Failure outcomes are asserted, never converted to passes.
"""
import hashlib
import json
from pathlib import Path
import re
from urllib.parse import unquote, urlsplit

from markdown_it import MarkdownIt
from check_recovery import check as check_recovery, load_records

CASE = Path(__file__).resolve().parents[1]
EVIDENCE = CASE / 'evidence'
SECTIONS = ['Executive Summary', 'Competitor Profiles', 'Messaging Comparison Matrix',
            'Content Gap Analysis', 'Opportunities', 'Threats', 'Recommended Actions']
URLS = ['https://linear.app/pricing', 'https://linear.app/features', 'https://linear.app/now',
        'https://asana.com/pricing', 'https://asana.com/product', 'https://asana.com/resources',
        'https://asana.com/whats-new']


def read(name):
    return json.loads((EVIDENCE / name).read_text())


def data(name):
    response = read('http/' + name + '.json')['response']
    return response.get('result', response.get('data', response))


def check(condition, message):
    if not condition:
        raise AssertionError(message)


def report_checks(path):
    text = path.read_text()
    for section in SECTIONS:
        check(re.search(r'^##\s+(?:\d+\.\s*)?' + re.escape(section), text, re.M),
              f'{path.name}: missing section {section}')
    for url in URLS:
        check(url in text, f'{path.name}: missing source URL {url}')
    check('2026-10-27' in text or 'October 27, 2026' in text, f'{path.name}: missing source date')
    for word in ['Human review: pending', 'FACT', 'INFERENCE', 'UNKNOWN']:
        check(word in text, f'{path.name}: missing {word}')
    for amount in ['2400', '3840', '2637.60', '5997.60']:
        check(amount in text.replace(',', ''), f'{path.name}: missing subtotal {amount}')


def main():
    check_recovery(load_records())
    manifest = read('manifest.json')
    actual = {str(p.relative_to(EVIDENCE)) for p in EVIDENCE.rglob('*')
              if p.is_file() and p.name != 'manifest.json'}
    check(actual == set(manifest['files']), 'Manifest file inventory differs')
    for name, meta in manifest['files'].items():
        raw = (EVIDENCE / name).read_bytes()
        check(len(raw) == meta['bytes'], f'{name}: size differs')
        check(hashlib.sha256(raw).hexdigest() == meta['sha256'], f'{name}: hash differs')

    check(data('blob-result')['error_code'] == 'SKILL_INSTALL_FAILED', 'URL probe outcome differs')
    metadata = read('generated/metadata.json')
    check(metadata['generate_status'] == 'done' and metadata['published'], 'Publication missing')
    check(metadata['current_revision_id'] == manifest['workflow_revision'], 'Revision differs')
    original = data('tree-status-8')
    check(original['status'] == 'failed' and original['result_ready'] is False, 'Original failure lost')
    check(original['progress']['completed_steps'] == 2, 'Original step count differs')
    check(data('external-after-recovery')['status'] == 'failed', 'External outcome was rewritten')
    projection = data('projection-completed')
    check(projection['status'] == 'completed' and projection['projection']['completed'], 'Recovery incomplete')
    nodes = projection['projection']['nodes']
    check(len(nodes) == 5 and all(n['execution'] == 'succeeded' for n in nodes.values()), 'Recovered steps differ')
    check(all(not n['requires_approval'] for n in nodes.values()), 'Approval-gate claim differs')
    attempts = projection['attempt_history']['analyze_positioning']
    check([a['status'] for a in attempts] == ['failed', 'succeeded'], 'Retry history missing')
    check(read('http/edit-response.json')['http_status'] == 503, 'Facade edit failure missing')
    check(read('http/slot-edit-response.json')['http_status'] == 500, 'Native edit failure missing')
    slots = data('slots-final')['slots']
    report = next(s for s in slots if s['slot_id'] == 'competitive_brief')
    check(report['revision'] == 1, 'Failed edits changed report revision')
    research = read('artifacts/research-revision-2.json')
    check(len(research['competitors']) == 2, 'Final research differs from the two-product corpus')
    summary = read('tool-summary.json')
    check(sum(sum(t.values()) for t in summary.values()) == 31, 'Step tool-call count differs')
    check(all('web_search' not in t for t in summary.values()), 'Unexpected live search tool')
    for n in range(1, 4):
        run = read(f'baseline/run-{n}.json')
        check(run['exit_code'] == 0 and run['elapsed_seconds'] > 0, 'Baseline did not finish')
        check(run['cost_usd'] is None and run['model_calls'] is None, 'Unavailable metrics fabricated')
        report_checks(EVIDENCE / f'baseline/run-{n}.md')
    report_checks(EVIDENCE / 'artifacts/competitive_brief.md')
    acquisition = read('acquisition.json')
    check(len(acquisition) == 7 and all(r['status'] == 200 for r in acquisition), 'Source acquisition differs')

    parser = MarkdownIt()
    docs = list(CASE.rglob('*.md')) + [CASE.parent / 'README.md', CASE.parent / 'README.CN.md']
    links = 0
    for path in docs:
        text = path.read_text()
        check(not re.search(r'[ \t]+$', text, re.M), f'{path}: trailing whitespace')
        for token in parser.parse(text):
            for child in token.children or []:
                target = child.attrGet('href') if child.type == 'link_open' else child.attrGet('src') if child.type == 'image' else None
                if target is None:
                    continue
                check(bool(target.strip()), f'{path}: empty link')
                parsed = urlsplit(target)
                if parsed.scheme or parsed.netloc or not parsed.path:
                    continue
                resolved = (path.parent / unquote(parsed.path)).resolve()
                check(resolved.exists(), f'{path}: missing local link {target}')
                links += 1
    for path in EVIDENCE.rglob('*'):
        if not path.is_file() or path.suffix == '.png':
            continue
        text = path.read_text()
        check('/Users/' not in text, f'{path}: private absolute path')
        check(not re.search(r'user:[a-f0-9-]{36}:', text), f'{path}: unredacted owner ref')
        check(not re.search(r'eyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.', text), f'{path}: possible JWT')
    print(f'PASS: {len(actual)} hashed evidence files, 4 report structure/arithmetic checks, '
          f'{len(docs)} Markdown files and {links} local links; observed failures preserved.')
    print('Groundedness issues, failed editing and pending human review remain; see EVALUATION.md.')


if __name__ == '__main__':
    main()
