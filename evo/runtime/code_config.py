from __future__ import annotations
import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from algorithm.config import config


@dataclass(frozen=True)
class StepSource:
    file: str = ''
    line: int = 0
    symbol: str = ''
    init_args: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True)
class SubjectIndex:
    subject_entry: Path | None = None
    step_to_source: dict[str, StepSource] = field(default_factory=dict)
    symbol_hints: dict[str, str] = field(default_factory=dict)


_DEFAULT_EXCLUDES = (
    '**/__pycache__/**',
    '**/.git/**',
    '**/.venv/**',
    '**/node_modules/**',
    '**/build/**',
    '**/dist/**',
)


@dataclass(frozen=True)
class ReadScope:
    project_roots: tuple[Path, ...] = ()
    third_party_packages: tuple[str, ...] = ()
    extra_roots: tuple[Path, ...] = ()
    exclude_globs: tuple[str, ...] = _DEFAULT_EXCLUDES


@dataclass(frozen=True)
class CodeAccessConfig:
    code_map: dict[str, str] = field(default_factory=dict)
    new_file_roots: tuple[str, ...] = ()
    read_scope: ReadScope = field(default_factory=ReadScope)
    subject_index: SubjectIndex = field(default_factory=SubjectIndex)


def code_context_dict(ca: 'CodeAccessConfig') -> dict[str, Any]:
    si = ca.subject_index
    return {
        'code_map_files': list(ca.code_map.keys()),
        'subject_entry': str(si.subject_entry) if si.subject_entry else None,
        'step_to_source': {
            k: {'file': v.file, 'line': v.line, 'symbol': v.symbol, 'init_args': v.init_args}
            for (k, v) in si.step_to_source.items()
        },
        'symbol_hints': dict(si.symbol_hints),
    }


def _resolve(p: str | Path, base: Path) -> Path:
    raw = Path(p).expanduser()
    resolved = raw.resolve() if raw.is_absolute() else (base / raw).resolve()
    return _container_path(resolved)


def _container_path(path: Path) -> Path:
    if path.exists():
        return path
    chat_source = config['evo_chat_source']
    if not chat_source:
        return path
    parts = path.parts
    marker = ('algorithm', 'chat')
    for i in range(len(parts) - 1):
        if parts[i: i + 2] == marker:
            mapped = Path(chat_source).joinpath(*parts[i + 2:])
            return mapped.resolve()
    return Path(chat_source).resolve() if path.is_absolute() else path


def load_code_access(path: Path | None) -> CodeAccessConfig:
    if path is None or not Path(path).is_file():
        return CodeAccessConfig()
    raw = json.loads(Path(path).read_text(encoding='utf-8'))
    if not isinstance(raw, dict):
        raise ValueError(f'code_map.json root must be an object, got {type(raw).__name__}')
    base = Path(path).resolve().parent
    cm = {str(_container_path(Path(k).expanduser())): str(v) for (k, v) in (raw.get('code_map') or {}).items()}
    rs_raw = raw.get('read_scope') or {}
    project_roots = tuple((_resolve(p, base) for p in rs_raw.get('project_roots') or [base]))
    third_party = tuple((str(p) for p in rs_raw.get('third_party_packages') or []))
    extra_roots = tuple((_resolve(p, base) for p in rs_raw.get('extra_roots') or []))
    exclude_globs = tuple((str(p) for p in rs_raw.get('exclude_globs') or _DEFAULT_EXCLUDES))
    rs = ReadScope(project_roots, third_party, extra_roots, exclude_globs)
    si_raw = raw.get('subject_index') or {}
    subject_entry = _resolve(si_raw['subject_entry'], base) if si_raw.get('subject_entry') else None
    step_to_source = {
        str(k): StepSource(
            file=str(v.get('file', '')),
            line=int(v.get('line', 0) or 0),
            symbol=str(v.get('symbol', '')),
            init_args=dict(v.get('init_args') or {}),
        )
        for (k, v) in (si_raw.get('step_to_source') or {}).items()
        if isinstance(v, dict)
    }
    symbol_hints = {str(k): str(v) for (k, v) in (si_raw.get('symbol_hints') or {}).items()}
    si = SubjectIndex(subject_entry, step_to_source, symbol_hints)
    nfr_raw: list[str] = []
    for x in raw.get('new_file_roots') or []:
        s = str(x).strip()
        if s:
            nfr_raw.append(s)
    for k, _v in cm.items():
        if not k or not k.rstrip().endswith('/'):
            continue
        nfr_raw.append(str(k).rstrip().rstrip('/'))
    nfr: tuple[str, ...] = tuple(dict.fromkeys(nfr_raw))
    return CodeAccessConfig(code_map=cm, new_file_roots=nfr, read_scope=rs, subject_index=si)
