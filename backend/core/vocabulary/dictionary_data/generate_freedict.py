#!/usr/bin/env python3
'''Build the bundled compact FreeDict index. Development-time tool only.'''

import argparse
import gzip
import json
import xml.etree.ElementTree as ET


NS = '{http://www.tei-c.org/ns/1.0}'


def text(node):
    return ' '.join(''.join(node.itertext()).split()) if node is not None else ''


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('source')
    parser.add_argument('output')
    args = parser.parse_args()
    rows = []
    for _, entry in ET.iterparse(args.source, events=('end',)):
        if entry.tag != NS + 'entry':
            continue
        term = text(entry.find('./' + NS + 'form/' + NS + 'orth'))
        if not term:
            entry.clear()
            continue
        prons = [text(item) for item in entry.findall('./' + NS + 'form/' + NS + 'pron') if text(item)]
        pos = text(entry.find('./' + NS + 'gramGrp/' + NS + 'pos'))
        translations = []
        definitions = []
        examples = []
        for sense in entry.findall('.//' + NS + 'sense'):
            definitions += [text(item) for item in sense.findall('./' + NS + 'def') if text(item)]
            for citation in sense.findall('./' + NS + 'cit'):
                quote = text(citation.find('./' + NS + 'quote'))
                kind = citation.attrib.get('type', '')
                if quote and kind == 'trans':
                    translations.append(quote)
                elif quote and kind in ('example', 'exampleTranslation'):
                    examples.append(quote)
        rows.append({
            'term': term,
            'phonetic': ' '.join(prons),
            'pos': pos,
            'translation': '；'.join(dict.fromkeys(translations)),
            'definition': '; '.join(dict.fromkeys(definitions)),
            'examples': list(dict.fromkeys(examples)),
        })
        entry.clear()
    rows.sort(key=lambda item: item['term'].casefold())
    with gzip.open(args.output, 'wt', encoding='utf-8', compresslevel=9) as output:
        json.dump(rows, output, ensure_ascii=False, separators=(',', ':'))


if __name__ == '__main__':
    main()
