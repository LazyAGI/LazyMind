"""Expand file-backed relay arguments after Windows creates the process."""

from __future__ import annotations

import importlib.util
import runpy
import sys
from pathlib import Path


def _expand_payload_args(argv: list[str]) -> list[str]:
    expanded: list[str] = []
    for arg in argv:
        matched = False
        for option in ('function', 'before_function', 'after_function', 'defined_pos'):
            prefix = f'--{option}-file='
            if arg.startswith(prefix):
                payload = Path(arg[len(prefix):]).read_text(encoding='ascii')
                expanded.append(f'--{option}={payload}')
                matched = True
                break
        if not matched:
            expanded.append(arg)
    return expanded


def main() -> None:
    spec = importlib.util.find_spec('lazyllm.components.deploy.relay.server')
    if spec is None or spec.origin is None:
        raise RuntimeError('cannot locate LazyLLM relay server')
    sys.argv = [spec.origin, *_expand_payload_args(sys.argv[1:])]
    runpy.run_path(spec.origin, run_name='__main__')


if __name__ == '__main__':
    main()
