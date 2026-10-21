#!/usr/bin/env python3
"""Create a build-local component lock with the selected LazyLLM version."""

import argparse
from pathlib import Path
import re


LAZYLLM_PIN = re.compile(r'^lazyllm==[^\s]+$', re.IGNORECASE)


def materialize(source: Path, output: Path, version: str) -> None:
    version = version.strip()
    if not version or re.search(r'[\s/\\]', version):
        raise ValueError('LazyLLM version must be a non-empty package version')
    lines = source.read_text(encoding='utf-8').splitlines()
    matches = [index for index, line in enumerate(lines) if LAZYLLM_PIN.fullmatch(line.strip())]
    if len(matches) != 1:
        raise RuntimeError(f'Expected exactly one lazyllm pin in {source}, found {len(matches)}')
    lines[matches[0]] = f'lazyllm=={version}'
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text('\n'.join(lines) + '\n', encoding='utf-8')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('version')
    args = parser.parse_args()
    materialize(args.source, args.output, args.version)
