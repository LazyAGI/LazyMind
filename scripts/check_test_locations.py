#!/usr/bin/env python3
"""Keep migrated algorithm tests out of their former directory."""

import subprocess
import sys
from pathlib import Path


def main():
    root = Path(__file__).resolve().parent.parent
    result = subprocess.run(
        ['git', 'ls-files', '-z', '--cached', '--others', '--exclude-standard',
         '--', 'algorithm/tests/'],
        cwd=root, check=True, capture_output=True, text=True,
    )
    paths = sorted({path for path in result.stdout.split('\0') if path})
    if paths:
        print('Algorithm tests belong in tests/algorithm/, not algorithm/tests/.', file=sys.stderr)
        for path in paths:
            print(f'  {path}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
