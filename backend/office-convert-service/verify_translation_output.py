"""Read-only completeness check: python verify_translation_output.py JOB_PREFIX."""
import json
import re
import sys
import unicodedata
import fitz


def normalize(value):
    value = unicodedata.normalize('NFKC', value)
    return re.sub(r'\s+', '', ''.join(c for c in value if not unicodedata.category(c).startswith('C')))


def verify(prefix):
    with open(prefix + '.input-layout.translations.json') as source:
        translations = json.load(source)
    with fitz.open(prefix + '.translated.pdf') as document:
        reflowed = '重排译文' in document[0].get_text()
        content = normalize(''.join(
            page.get_text(clip=fitz.Rect(40, 50, 560, 805)) if reflowed else page.get_text()
            for page in document
        ))
        missing = [key for key, text in translations.items() if normalize(text) not in content]
        return {'pages': len(document), 'reflowed': reflowed, 'blocks': len(translations), 'missing_blocks': missing}


if __name__ == '__main__':
    result = verify(sys.argv[1])
    print(json.dumps(result, ensure_ascii=False))
    sys.exit(bool(result['missing_blocks']))
