from __future__ import annotations

import ast
import re
from dataclasses import asdict, dataclass
from pathlib import Path, PurePosixPath
from typing import Any

DOMAIN_ROOTS = ('algorithm/lazymind/chat', 'algorithm/lazymind/parsing')
BLOCKED_PARTS = {'__pycache__', '.git', 'artifacts', 'cache', 'generated'}
MAX_FILE_BYTES = 512 * 1024
MAX_INDEX_FILES = 120
MAX_INDEX_SYMBOLS = 240
MAX_ITEMS = 80
IDENT = re.compile(r'[A-Za-z_][A-Za-z0-9_]{2,}')


@dataclass(frozen=True)
class CodeSymbol:
    path: str
    domain: str
    kind: str
    qualname: str
    lineno: int
    end_lineno: int
    args: tuple[str, ...]
    calls: tuple[str, ...]
    exceptions: tuple[str, ...]
    identifiers: tuple[str, ...]
    literals: tuple[str, ...]
    node_kinds: tuple[str, ...]
    source_terms: tuple[str, ...]

    def as_dict(self) -> dict[str, Any]:
        return asdict(self)


@dataclass(frozen=True)
class CodeFile:
    path: str
    domain: str
    imports: tuple[str, ...]
    symbols: tuple[CodeSymbol, ...]
    skipped_reason: str = ''
    syntax_error: str = ''

    def as_dict(self) -> dict[str, Any]:
        data = asdict(self)
        data['symbols'] = [symbol.as_dict() for symbol in self.symbols]
        return data


@dataclass(frozen=True)
class CodeIndex:
    root: str
    files: tuple[CodeFile, ...]

    @property
    def symbols(self) -> tuple[CodeSymbol, ...]:
        return tuple(symbol for file in self.files for symbol in file.symbols)

    def as_dict(self) -> dict[str, Any]:
        symbol_budget = MAX_INDEX_SYMBOLS
        files = []
        for file in self.files[:MAX_INDEX_FILES]:
            data = asdict(file)
            data['symbols'] = [symbol.as_dict() for symbol in file.symbols[:symbol_budget]]
            symbol_budget = max(0, symbol_budget - len(data['symbols']))
            files.append(data)
        symbols = self.symbols
        return {
            'root': self.root,
            'domain_roots': list(DOMAIN_ROOTS),
            'file_count': len(self.files),
            'symbol_count': len(symbols),
            'truncated': len(self.files) > MAX_INDEX_FILES or len(symbols) > MAX_INDEX_SYMBOLS,
            'files': files,
        }


def build_code_index(root: Path, domain_roots: tuple[str, ...] = DOMAIN_ROOTS) -> CodeIndex:
    base = root.resolve()
    files = tuple(
        _index_file(base, path, domain_roots)
        for path in _python_files(base, domain_roots)
    )
    return CodeIndex(root=str(base), files=files)


def symbol_at(index: CodeIndex, path: str, qualname: str) -> CodeSymbol | None:
    normalized = _clean_relpath(path)
    for symbol in index.symbols:
        if symbol.path == normalized and symbol.qualname == qualname:
            return symbol
    return None


def _python_files(root: Path, domain_roots: tuple[str, ...]) -> tuple[Path, ...]:
    files: list[Path] = []
    for rel_root in domain_roots:
        directory = root / rel_root
        if directory.exists():
            files.extend(path for path in directory.rglob('*.py') if _editable(path))
    return tuple(sorted(files))


def _index_file(root: Path, path: Path, domain_roots: tuple[str, ...]) -> CodeFile:
    rel = path.relative_to(root).as_posix()
    domain = _domain(rel, domain_roots)
    if path.stat().st_size > MAX_FILE_BYTES:
        return CodeFile(rel, domain, (), (), skipped_reason='file_too_large')
    try:
        source = path.read_text(encoding='utf-8')
        tree = ast.parse(source, filename=rel)
    except SyntaxError as exc:
        return CodeFile(rel, domain, (), (), syntax_error=f'{exc.__class__.__name__}: {exc}')
    imports = tuple(sorted(_imports(tree)))
    symbols = tuple(_symbols(rel, domain, tree, source))
    return CodeFile(rel, domain, imports, symbols)


def _symbols(path: str, domain: str, tree: ast.AST, source: str) -> list[CodeSymbol]:
    symbols: list[CodeSymbol] = []

    class Visitor(ast.NodeVisitor):
        def __init__(self) -> None:
            self.stack: list[str] = []

        def visit_ClassDef(self, node: ast.ClassDef) -> None:
            qualname = '.'.join([*self.stack, node.name])
            symbols.append(_symbol(path, domain, 'class', qualname, node, (), source))
            self.stack.append(node.name)
            self.generic_visit(node)
            self.stack.pop()

        def visit_FunctionDef(self, node: ast.FunctionDef) -> None:
            self._visit_function(node)

        def visit_AsyncFunctionDef(self, node: ast.AsyncFunctionDef) -> None:
            self._visit_function(node)

        def _visit_function(self, node: ast.FunctionDef | ast.AsyncFunctionDef) -> None:
            qualname = '.'.join([*self.stack, node.name])
            args = tuple(arg.arg for arg in (*node.args.posonlyargs, *node.args.args, *node.args.kwonlyargs))
            symbols.append(_symbol(path, domain, 'function', qualname, node, args, source))
            self.stack.append(node.name)
            self.generic_visit(node)
            self.stack.pop()

    Visitor().visit(tree)
    return symbols


def _symbol(path: str, domain: str, kind: str, qualname: str, node: ast.AST,
            args: tuple[str, ...], source: str) -> CodeSymbol:
    calls, exceptions, identifiers, literals, node_kinds = set(), set(), set(), set(), set()
    for child in ast.walk(node):
        node_kinds.add(child.__class__.__name__)
        if isinstance(child, ast.Call):
            if name := _call_name(child.func):
                calls.add(name)
        elif isinstance(child, ast.ExceptHandler):
            if child.type and (name := _call_name(child.type)):
                exceptions.add(name)
        elif isinstance(child, ast.Name):
            identifiers.add(child.id)
        elif isinstance(child, ast.Attribute):
            identifiers.add(child.attr)
        elif isinstance(child, ast.Constant) and isinstance(child.value, str):
            text = ' '.join(child.value.split())
            if text:
                literals.add(text[:160])
    return CodeSymbol(
        path=path,
        domain=domain,
        kind=kind,
        qualname=qualname,
        lineno=int(getattr(node, 'lineno', 1) or 1),
        end_lineno=int(getattr(node, 'end_lineno', getattr(node, 'lineno', 1)) or 1),
        args=args,
        calls=_limited(calls),
        exceptions=_limited(exceptions),
        identifiers=_limited(identifiers),
        literals=_limited(literals),
        node_kinds=_limited(node_kinds),
        source_terms=_limited(set(IDENT.findall(ast.get_source_segment(source, node) or ''))),
    )


def _imports(tree: ast.AST) -> set[str]:
    names: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            names.update(alias.name for alias in node.names)
        elif isinstance(node, ast.ImportFrom):
            module = '.' * node.level + (node.module or '')
            names.add(module)
    return names


def _call_name(node: ast.AST) -> str:
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        prefix = _call_name(node.value)
        return f'{prefix}.{node.attr}' if prefix else node.attr
    return ''


def _domain(path: str, domain_roots: tuple[str, ...]) -> str:
    for root in domain_roots:
        if path == root or path.startswith(f'{root}/'):
            return root.rsplit('/', 1)[-1]
    return ''


def _editable(path: Path) -> bool:
    return not any(part in BLOCKED_PARTS for part in path.parts)


def _clean_relpath(value: str) -> str:
    text = str(value or '').strip().strip('/')
    return PurePosixPath(text).as_posix() if text else ''


def _limited(values: set[str]) -> tuple[str, ...]:
    return tuple(sorted(values)[:MAX_ITEMS])
