"""Read-only pre-submit checks for the five compatibility patch evidence sets."""
import hashlib
import json
import re
from pathlib import Path


def main():
    root = Path(__file__).resolve().parent
    errors = []
    count = 0
    patterns = [
        r"/(?:Users|home)/[A-Za-z0-9_.-]+/",
        r"[A-Za-z]:\\Users\\",
        r"(?i)(?:authorization|cookie|access_token|refresh_token|api_key)\s*[=:]\s*[\"']?[A-Za-z0-9_./+-]{24,}",
        r"(?i)[?&](?:key|token|api_key)=[A-Za-z0-9_./+-]{16,}",
        r"-----BEGIN (?:RSA |OPENSSH |EC )?PRIVATE KEY-----",
    ]
    for folder in root.glob('*/lazymind-compat-v1'):
        for path in folder.rglob('*'):
            if not path.is_file() or '__pycache__' in path.parts:
                continue
            text = path.read_text()
            if any(re.search(p, text) for p in patterns):
                errors.append(str(path.relative_to(root)))
        index = folder / 'examples/run-index.json'
        if not index.exists():
            continue
        for case in json.loads(index.read_text())['cases']:
            assert (index.parent / case['html']).is_file(), case['html']
            for request in case['requests']:
                path = (index.parent / request['response_file']).resolve()
                assert path.is_relative_to(index.parent.resolve())
                assert hashlib.sha256(path.read_bytes()).hexdigest() == request['response_sha256'], path
                json.loads(path.read_text())
                count += 1
    assert not errors, 'Review sensitive-data patterns in: ' + ', '.join(errors)
    print(f'PASS: privacy pattern scan; {count} response snapshots verified')


if __name__ == '__main__':
    main()
