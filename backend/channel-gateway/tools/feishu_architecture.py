#!/usr/bin/env python3
"""Generate a reproducible Feishu/CardKit architecture inventory.

The report intentionally uses only the Python standard library.  It inventories
every Python file and every class/function/method in the Feishu gateway slice,
then emits Mermaid views for file imports, symbol containment and statically
resolved calls.  The same command is used before and after each cleanup round.
"""

from __future__ import annotations

import argparse
import ast
import hashlib
import importlib.util
import json
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable, Sequence


_FEISHU_DIRECT_FILES = (
    "channel_gateway/bootstrap.py",
    "channel_gateway/common/application/actions.py",
    "channel_gateway/common/application/capabilities.py",
    "channel_gateway/common/application/conversations.py",
    "channel_gateway/common/domain/chat.py",
    "channel_gateway/common/domain/commands.py",
    "channel_gateway/common/infrastructure/lazymind.py",
    "channel_gateway/common/infrastructure/postgres.py",
    "channel_gateway/common/infrastructure/sqlite.py",
    "channel_gateway/common/ports/repository.py",
    "test_feishu_workspace.py",
    "tools/feishu_architecture.py",
)


@dataclass(frozen=True, order=True)
class Edge:
    source: str
    target: str


@dataclass(frozen=True)
class Symbol:
    id: str
    module: str
    file: str
    qualname: str
    kind: str
    line: int
    parent: str | None
    bases: tuple[str, ...] = ()
    calls: tuple[ast.Call, ...] = field(default=(), compare=False, repr=False)


@dataclass(frozen=True)
class SourceFile:
    path: Path
    relative: str
    module: str
    tree: ast.Module
    imports: tuple[str, ...]
    aliases: dict[str, str]
    symbols: tuple[Symbol, ...]


@dataclass(frozen=True)
class Diagram:
    group: str
    title: str
    code: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--gateway-root",
        type=Path,
        default=Path(__file__).resolve().parents[1],
        help="backend/channel-gateway directory",
    )
    parser.add_argument("--output", type=Path, required=True, help="Markdown report path")
    parser.add_argument(
        "--visualization",
        type=Path,
        help="Optional visualization HTML fragment path",
    )
    parser.add_argument("--stage", default="baseline", help="Stage label in the report")
    parser.add_argument(
        "--scope",
        choices=("feishu", "gateway"),
        default="feishu",
        help="Inventory the Feishu interaction slice or the broader gateway",
    )
    return parser.parse_args()


def discover_source_paths(gateway_root: Path, scope: str) -> list[Path]:
    package_root = gateway_root / "channel_gateway"
    if scope == "feishu":
        candidates = {
            *package_root.joinpath("feishu").rglob("*.py"),
            *(gateway_root / relative for relative in _FEISHU_DIRECT_FILES),
        }
        return sorted(path for path in candidates if path.is_file())
    candidates = {
        gateway_root / "main.py",
        gateway_root / "test_feishu_workspace.py",
        gateway_root / "tools" / "feishu_architecture.py",
        package_root / "__init__.py",
        package_root / "app.py",
        package_root / "bootstrap.py",
        *package_root.joinpath("common").rglob("*.py"),
        *package_root.joinpath("feishu").rglob("*.py"),
    }
    return sorted(path for path in candidates if path.is_file())


def module_name(gateway_root: Path, path: Path) -> str:
    relative = path.relative_to(gateway_root)
    parts = list(relative.with_suffix("").parts)
    if parts[-1] == "__init__":
        parts.pop()
    return ".".join(parts)


def resolve_relative_import(current_module: str, is_package: bool, node: ast.ImportFrom) -> str:
    if node.level == 0:
        return node.module or ""
    package = current_module if is_package else current_module.rpartition(".")[0]
    reference = "." * node.level + (node.module or "")
    try:
        return importlib.util.resolve_name(reference, package)
    except (ImportError, ValueError):
        return node.module or ""


def collect_imports(module: str, path: Path, tree: ast.Module) -> tuple[tuple[str, ...], dict[str, str]]:
    imports: set[str] = set()
    aliases: dict[str, str] = {}
    is_package = path.name == "__init__.py"
    for node in tree.body:
        if isinstance(node, ast.Import):
            for item in node.names:
                imports.add(item.name)
                aliases[item.asname or item.name.split(".")[0]] = item.name
        elif isinstance(node, ast.ImportFrom):
            imported_module = resolve_relative_import(module, is_package, node)
            if imported_module:
                imports.add(imported_module)
            for item in node.names:
                if item.name == "*":
                    continue
                local_name = item.asname or item.name
                aliases[local_name] = f"{imported_module}.{item.name}" if imported_module else item.name
    return tuple(sorted(imports)), aliases


class DefinitionCollector:
    def __init__(self, module: str, relative: str) -> None:
        self.module = module
        self.relative = relative
        self.symbols: list[Symbol] = []

    def collect(self, tree: ast.Module) -> tuple[Symbol, ...]:
        pending: list[tuple[Sequence[ast.stmt], tuple[str, ...]]] = [
            (tree.body, ())
        ]
        while pending:
            body, parents = pending.pop()
            nested: list[
                tuple[Sequence[ast.stmt], tuple[str, ...]]
            ] = []
            for node in body:
                if isinstance(node, ast.ClassDef):
                    self._add_class(node, parents)
                    nested.append((node.body, (*parents, node.name)))
                elif isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    self._add_function(node, parents)
                    nested.append((node.body, (*parents, node.name)))
            pending.extend(reversed(nested))
        return tuple(sorted(self.symbols, key=lambda item: (item.line, item.qualname)))

    def _add_class(self, node: ast.ClassDef, parents: tuple[str, ...]) -> None:
        qualname = ".".join((*parents, node.name))
        parent = ".".join(parents) or None
        symbol_id = f"{self.module}:{qualname}"
        bases = tuple(ast.unparse(base) for base in node.bases)
        self.symbols.append(
            Symbol(symbol_id, self.module, self.relative, qualname, "class", node.lineno, parent, bases)
        )

    def _add_function(
        self,
        node: ast.FunctionDef | ast.AsyncFunctionDef,
        parents: tuple[str, ...],
    ) -> None:
        qualname = ".".join((*parents, node.name))
        parent = ".".join(parents) or None
        kind = "method" if parents and self._parent_is_class(parents) else "function"
        calls = tuple(CallCollector.collect(node))
        self.symbols.append(
            Symbol(
                f"{self.module}:{qualname}",
                self.module,
                self.relative,
                qualname,
                kind,
                node.lineno,
                parent,
                calls=calls,
            )
        )

    def _parent_is_class(self, parents: tuple[str, ...]) -> bool:
        parent_name = ".".join(parents)
        return any(item.qualname == parent_name and item.kind == "class" for item in self.symbols)


class CallCollector(ast.NodeVisitor):
    def __init__(self, root: ast.AST) -> None:
        self.root = root
        self.calls: list[ast.Call] = []

    @classmethod
    def collect(cls, node: ast.AST) -> list[ast.Call]:
        collector = cls(node)
        for child in ast.iter_child_nodes(node):
            collector.visit(child)
        return collector.calls

    def visit_Call(self, node: ast.Call) -> None:
        self.calls.append(node)
        self.generic_visit(node)

    def visit_FunctionDef(self, node: ast.FunctionDef) -> None:
        if node is self.root:
            self.generic_visit(node)

    def visit_AsyncFunctionDef(self, node: ast.AsyncFunctionDef) -> None:
        if node is self.root:
            self.generic_visit(node)

    def visit_ClassDef(self, node: ast.ClassDef) -> None:
        if node is self.root:
            self.generic_visit(node)


def parse_source_files(gateway_root: Path, paths: Sequence[Path]) -> list[SourceFile]:
    parsed: list[SourceFile] = []
    for path in paths:
        relative = path.relative_to(gateway_root).as_posix()
        module = module_name(gateway_root, path)
        tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
        imports, aliases = collect_imports(module, path, tree)
        symbols = DefinitionCollector(module, relative).collect(tree)
        parsed.append(SourceFile(path, relative, module, tree, imports, aliases, symbols))
    return parsed


def import_edges(files: Sequence[SourceFile]) -> set[Edge]:
    modules = {item.module for item in files}
    edges: set[Edge] = set()
    for source in files:
        for imported in source.imports:
            matches = [module for module in modules if imported == module or imported.startswith(f"{module}.")]
            if not matches:
                continue
            target = max(matches, key=len)
            if target != source.module:
                edges.add(Edge(source.module, target))
    return edges


def inheritance_edges(files: Sequence[SourceFile]) -> set[Edge]:
    symbols = {symbol.id: symbol for file in files for symbol in file.symbols}
    by_module_name = {(symbol.module, symbol.qualname.split(".")[-1]): symbol.id for symbol in symbols.values()}
    edges: set[Edge] = set()
    for file in files:
        for symbol in file.symbols:
            if symbol.kind != "class":
                continue
            for base in symbol.bases:
                target = by_module_name.get((symbol.module, base.split(".")[-1]))
                if target:
                    edges.add(Edge(symbol.id, target))
    return edges


def resolve_call(
    source: Symbol,
    call: ast.Call,
    source_file: SourceFile,
    symbol_ids: set[str],
    local_top_level: dict[str, str],
) -> str | None:
    function = call.func
    candidate: str | None = None
    if isinstance(function, ast.Name):
        candidate = local_top_level.get(function.id) or source_file.aliases.get(function.id)
    elif isinstance(function, ast.Attribute) and isinstance(function.value, ast.Name):
        owner = function.value.id
        if owner in {"self", "cls"}:
            class_parts = source.qualname.split(".")[:-1]
            while class_parts:
                candidate = f"{source.module}:{'.'.join(class_parts)}.{function.attr}"
                if candidate in symbol_ids:
                    break
                class_parts.pop()
        elif owner in source_file.aliases:
            candidate = f"{source_file.aliases[owner]}.{function.attr}"
        else:
            local_owner = local_top_level.get(owner)
            if local_owner:
                owner_qualname = local_owner.partition(":")[2]
                candidate = f"{source.module}:{owner_qualname}.{function.attr}"
    if not candidate:
        return None
    if candidate in symbol_ids:
        return candidate
    if ":" not in candidate:
        module_part, _, name = candidate.rpartition(".")
        candidate = f"{module_part}:{name}"
    return candidate if candidate in symbol_ids else None


def call_edges(files: Sequence[SourceFile]) -> set[Edge]:
    symbols = [symbol for file in files for symbol in file.symbols]
    symbol_ids = {symbol.id for symbol in symbols}
    file_by_module = {file.module: file for file in files}
    local_by_module: dict[str, dict[str, str]] = {}
    for symbol in symbols:
        if "." not in symbol.qualname:
            local_by_module.setdefault(symbol.module, {})[symbol.qualname] = symbol.id
    edges: set[Edge] = set()
    for source in symbols:
        source_file = file_by_module[source.module]
        for call in source.calls:
            target = resolve_call(
                source,
                call,
                source_file,
                symbol_ids,
                local_by_module.get(source.module, {}),
            )
            if target and target != source.id:
                edges.add(Edge(source.id, target))
    return edges


def strongly_connected_components(nodes: Iterable[str], edges: Iterable[Edge]) -> list[list[str]]:
    adjacency: dict[str, list[str]] = {node: [] for node in nodes}
    self_edges: set[str] = set()
    for edge in edges:
        adjacency.setdefault(edge.source, []).append(edge.target)
        adjacency.setdefault(edge.target, [])
        if edge.source == edge.target:
            self_edges.add(edge.source)

    index = 0
    stack: list[str] = []
    on_stack: set[str] = set()
    indices: dict[str, int] = {}
    lowlinks: dict[str, int] = {}
    components: list[list[str]] = []

    def visit(node: str) -> None:
        nonlocal index
        indices[node] = index
        lowlinks[node] = index
        index += 1
        stack.append(node)
        on_stack.add(node)
        for target in adjacency[node]:
            if target not in indices:
                visit(target)
                lowlinks[node] = min(lowlinks[node], lowlinks[target])
            elif target in on_stack:
                lowlinks[node] = min(lowlinks[node], indices[target])
        if lowlinks[node] != indices[node]:
            return
        component: list[str] = []
        while stack:
            member = stack.pop()
            on_stack.remove(member)
            component.append(member)
            if member == node:
                break
        if len(component) > 1 or component[0] in self_edges:
            components.append(sorted(component))

    for node in sorted(adjacency):
        if node not in indices:
            visit(node)
    return sorted(components)


def node_id(value: str) -> str:
    return "n" + hashlib.sha1(value.encode("utf-8")).hexdigest()[:12]


def escape_label(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', "'").replace("<", "&lt;").replace(">", "&gt;")


def file_group(relative: str) -> str:
    if relative in {"main.py", "channel_gateway/app.py", "channel_gateway/bootstrap.py", "channel_gateway/__init__.py"}:
        return "Entry and composition"
    if "/common/domain/" in f"/{relative}":
        return "Common domain"
    if "/common/ports/" in f"/{relative}":
        return "Common ports"
    if "/common/application/" in f"/{relative}":
        return "Common application"
    if "/common/infrastructure/" in f"/{relative}":
        return "Common infrastructure"
    if "/feishu/" in f"/{relative}":
        return "Feishu adapter"
    return "Other"


def group_files(files: Sequence[SourceFile]) -> dict[str, list[SourceFile]]:
    grouped: dict[str, list[SourceFile]] = {}
    for file in files:
        grouped.setdefault(file_group(file.relative), []).append(file)
    return {group: sorted(members, key=lambda item: item.relative) for group, members in sorted(grouped.items())}


def render_layer_diagram(files: Sequence[SourceFile], edges: set[Edge]) -> Diagram:
    file_by_module = {file.module: file for file in files}
    groups = group_files(files)
    group_edges = {
        Edge(file_group(file_by_module[edge.source].relative), file_group(file_by_module[edge.target].relative))
        for edge in edges
        if edge.source in file_by_module
        and edge.target in file_by_module
        and file_group(file_by_module[edge.source].relative) != file_group(file_by_module[edge.target].relative)
    }
    lines = ["flowchart LR"]
    for group, members in groups.items():
        lines.append(f'  {node_id(group)}["{escape_label(group)}<br/>{len(members)} files"]')
    for edge in sorted(group_edges):
        lines.append(f"  {node_id(edge.source)} --> {node_id(edge.target)}")
    return Diagram("Dependencies", "Layer overview", "\n".join(lines))


def render_file_dependency_diagrams(files: Sequence[SourceFile], edges: set[Edge]) -> list[Diagram]:
    file_by_module = {file.module: file for file in files}
    diagrams: list[Diagram] = []
    for group, members in group_files(files).items():
        member_modules = {file.module for file in members}
        relevant = [edge for edge in sorted(edges) if edge.source in member_modules]
        external_groups = sorted(
            {
                file_group(file_by_module[edge.target].relative)
                for edge in relevant
                if edge.target in file_by_module and edge.target not in member_modules
            }
        )
        lines = ["flowchart LR", f'  subgraph source["{escape_label(group)}"]']
        for file in members:
            lines.append(f'    {node_id(file.module)}["{escape_label(file.relative)}"]')
        lines.append("  end")
        for external_group in external_groups:
            anchor = f"external:{group}:{external_group}"
            lines.append(f'  {node_id(anchor)}["{escape_label(external_group)}<br/>dependency"]')
        emitted_edges: set[Edge] = set()
        for edge in relevant:
            if edge.target not in file_by_module:
                continue
            if edge.target in member_modules:
                rendered = edge
            else:
                target_group = file_group(file_by_module[edge.target].relative)
                rendered = Edge(edge.source, f"external:{group}:{target_group}")
            if rendered in emitted_edges:
                continue
            emitted_edges.add(rendered)
            lines.append(f"  {node_id(rendered.source)} --> {node_id(rendered.target)}")
        diagrams.append(Diagram("Dependencies", f"Files / {group}", "\n".join(lines)))
    return diagrams


def render_symbol_inventory(files: Sequence[SourceFile], chunk_size: int = 4) -> list[Diagram]:
    diagrams: list[Diagram] = []
    for file in files:
        symbols = list(file.symbols)
        if not symbols:
            code = f'flowchart LR\n  {node_id(file.module)}["{escape_label(file.relative)} / no symbols"]'
            diagrams.append(Diagram("All symbols", file.relative, code))
            continue
        chunks = [symbols[index : index + chunk_size] for index in range(0, len(symbols), chunk_size)]
        class_ids = {symbol.qualname: symbol.id for symbol in symbols if symbol.kind == "class"}
        for chunk_index, chunk in enumerate(chunks, start=1):
            lines = ["flowchart LR", f'  {node_id(file.module)}["{escape_label(file.relative)}"]']
            emitted: set[str] = set()
            for symbol in chunk:
                if symbol.parent and symbol.parent in class_ids and class_ids[symbol.parent] not in emitted:
                    parent_id = class_ids[symbol.parent]
                    lines.append(f'  {node_id(parent_id)}["class {escape_label(symbol.parent)}"]')
                    emitted.add(parent_id)
                label = f"{symbol.kind} {symbol.qualname} / L{symbol.line}"
                lines.append(f'  {node_id(symbol.id)}["{escape_label(label)}"]')
                emitted.add(symbol.id)
            for symbol in chunk:
                if symbol.parent and symbol.parent in class_ids:
                    lines.append(f"  {node_id(class_ids[symbol.parent])} --> {node_id(symbol.id)}")
                else:
                    lines.append(f"  {node_id(file.module)} --> {node_id(symbol.id)}")
            suffix = f" / {chunk_index}/{len(chunks)}" if len(chunks) > 1 else ""
            diagrams.append(Diagram("All symbols", f"{file.relative}{suffix}", "\n".join(lines)))
    return diagrams


def render_inheritance_diagrams(files: Sequence[SourceFile], edges: set[Edge]) -> list[Diagram]:
    symbols = {symbol.id: symbol for file in files for symbol in file.symbols}
    diagrams: list[Diagram] = []
    for group, members in group_files(files).items():
        member_files = {file.relative for file in members}
        relevant = [edge for edge in sorted(edges) if symbols[edge.source].file in member_files]
        if not relevant:
            continue
        involved = sorted({edge.source for edge in relevant} | {edge.target for edge in relevant})
        lines = ["flowchart LR"]
        for symbol_id in involved:
            symbol = symbols[symbol_id]
            label = (
                f"{escape_label(symbol.file)}<br/>"
                f"{escape_label(symbol.qualname)}"
            )
            lines.append(f'  {node_id(symbol_id)}["{label}"]')
        for edge in relevant:
            lines.append(f"  {node_id(edge.source)} --> {node_id(edge.target)}")
        diagrams.append(Diagram("Dependencies", f"Inheritance / {group}", "\n".join(lines)))
    return diagrams


def render_call_diagrams(files: Sequence[SourceFile], edges: set[Edge], chunk_size: int = 12) -> list[Diagram]:
    symbols = {symbol.id: symbol for file in files for symbol in file.symbols}
    edges_by_file: dict[str, list[Edge]] = {file.relative: [] for file in files}
    for edge in sorted(edges):
        source = symbols.get(edge.source)
        if source:
            edges_by_file[source.file].append(edge)
    diagrams: list[Diagram] = []
    for file in files:
        file_edges = edges_by_file[file.relative]
        if not file_edges:
            continue
        chunks = [file_edges[index : index + chunk_size] for index in range(0, len(file_edges), chunk_size)]
        for chunk_index, chunk in enumerate(chunks, start=1):
            involved = sorted({edge.source for edge in chunk} | {edge.target for edge in chunk})
            lines = ["flowchart LR"]
            for symbol_id in involved:
                symbol = symbols[symbol_id]
                label = (
                    f"{escape_label(symbol.module)}<br/>"
                    f"{escape_label(symbol.qualname)}"
                )
                lines.append(f'  {node_id(symbol_id)}["{label}"]')
            for edge in chunk:
                lines.append(f"  {node_id(edge.source)} --> {node_id(edge.target)}")
            suffix = f" / {chunk_index}/{len(chunks)}" if len(chunks) > 1 else ""
            diagrams.append(Diagram("Resolved calls", f"{file.relative}{suffix}", "\n".join(lines)))
    return diagrams


def render_cycle_section(title: str, cycles: Sequence[Sequence[str]]) -> list[str]:
    lines = [f"### {title}", ""]
    if not cycles:
        lines.extend(["No cycles detected.", ""])
        return lines
    for cycle in cycles:
        lines.append(f"- `{'` → `'.join(cycle)}`")
    lines.append("")
    return lines


def write_markdown(
    output: Path,
    stage: str,
    files: Sequence[SourceFile],
    diagrams: Sequence[Diagram],
    import_cycles: Sequence[Sequence[str]],
    call_cycles: Sequence[Sequence[str]],
    inheritance_cycles: Sequence[Sequence[str]],
    import_edge_count: int,
    call_edge_count: int,
    inheritance_edge_count: int,
) -> None:
    classes = sum(symbol.kind == "class" for file in files for symbol in file.symbols)
    callables = sum(symbol.kind in {"function", "method"} for file in files for symbol in file.symbols)
    lines = [
        "# Feishu CardKit architecture inventory",
        "",
        f"Stage: **{stage}**  ",
        f"Generated: {datetime.now(timezone.utc).isoformat(timespec='seconds')}  ",
        f"Coverage: **{len(files)} files, {classes} classes, {callables} callables**  ",
        f"Edges: **{import_edge_count} imports, {inheritance_edge_count} inheritance, {call_edge_count} resolved calls**",
        "",
        "The symbol inventory is exhaustive for the selected Feishu gateway slice. Resolved-call views are conservative: dynamic dispatch through instance attributes remains represented by the owning symbol, not guessed as a false dependency.",
        "",
    ]
    lines.extend(render_cycle_section("File import cycles", import_cycles))
    lines.extend(render_cycle_section("Class inheritance cycles", inheritance_cycles))
    lines.extend(render_cycle_section("Resolved internal call cycles", call_cycles))
    for diagram in diagrams:
        lines.extend([f"## {diagram.group} / {diagram.title}", "", "```mermaid", diagram.code, "```", ""])
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text("\n".join(lines), encoding="utf-8")


def write_visualization(
    output: Path,
    stage: str,
    files: Sequence[SourceFile],
    diagrams: Sequence[Diagram],
    import_cycles: Sequence[Sequence[str]],
    call_cycles: Sequence[Sequence[str]],
) -> None:
    classes = sum(symbol.kind == "class" for file in files for symbol in file.symbols)
    callables = sum(symbol.kind in {"function", "method"} for file in files for symbol in file.symbols)
    payload = json.dumps(
        [{"group": item.group, "title": item.title, "code": item.code} for item in diagrams],
        ensure_ascii=False,
        separators=(",", ":"),
    )
    root_id = "feishu-cardkit-architecture-view"
    html = f'''<section id="{root_id}" aria-label="Feishu CardKit architecture inventory">
  <style>
    #{root_id} {{
      width: 100%;
      min-width: 0;
      color: var(--foreground);
    }}
    #{root_id} * {{ box-sizing: border-box; }}
    #{root_id} .fa-shell {{ width: 100%; min-width: 0; }}
    #{root_id} .fa-head {{ display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; padding: 8px 0; border-bottom: 1px solid var(--border); }}
    #{root_id} .fa-title {{ margin: 0; font-weight: 500; }}
    #{root_id} .fa-stage {{ margin-top: 2px; color: var(--muted-foreground); font-size: 11px; }}
    #{root_id} .fa-metrics {{ display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 6px; }}
    #{root_id} .fa-metric {{ color: var(--muted-foreground); font-size: 11px; }}
    #{root_id} .fa-controls {{ display: grid; grid-template-columns: 170px minmax(0,1fr); gap: 8px; padding: 10px 0; border-bottom: 1px solid var(--border); }}
    #{root_id} select {{ width: 100%; min-width: 0; }}
    #{root_id} .fa-status {{ display: flex; align-items: center; justify-content: space-between; gap: 10px; padding: 7px 0; border-bottom: 1px solid var(--border); color: var(--muted-foreground); font-size: 11px; }}
    #{root_id} .fa-cycle {{ color: var(--{'destructive' if import_cycles or call_cycles else 'green'}); font-weight: 500; }}
    #{root_id} .fa-diagram {{ min-width: 0; padding: 12px 0; }}
    #{root_id} .fa-diagram svg {{ display: block; width: 100%; min-width: 0; max-width: 100%; height: auto; margin: 0 auto; }}
    #{root_id} .fa-error {{ color: var(--destructive); white-space: pre-wrap; }}
    @media (max-width: 560px) {{
      #{root_id} .fa-head {{ flex-direction: column; }}
      #{root_id} .fa-metrics {{ justify-content: flex-start; }}
      #{root_id} .fa-controls {{ grid-template-columns: 1fr; }}
    }}
  </style>
  <div class="fa-shell">
    <header class="fa-head">
      <div><h2 class="fa-title">Feishu CardKit architecture</h2><div class="fa-stage">{escape_label(stage)} / exhaustive source inventory</div></div>
      <div class="fa-metrics"><span class="fa-metric">{len(files)} files</span><span class="fa-metric">{classes} classes</span><span class="fa-metric">{callables} callables</span><span class="fa-metric">{len(diagrams)} Mermaid views</span></div>
    </header>
    <div class="fa-controls"><select class="form-select" id="fa-group" aria-label="Diagram group"></select><select class="form-select" id="fa-view" aria-label="Diagram view"></select></div>
    <div class="fa-status"><span id="fa-current"></span><span class="fa-cycle">{len(import_cycles)} import cycles / {len(call_cycles)} call cycles</span></div>
    <div class="fa-diagram" id="fa-diagram" aria-live="polite"></div>
  </div>
  <script src="https://cdn.jsdelivr.net/npm/mermaid@11.4.1/dist/mermaid.min.js"></script>
  <script>
    (() => {{
      const root = document.getElementById("{root_id}");
      if (!root) return;
      const diagrams = {payload};
      const groupSelect = root.querySelector("#fa-group");
      const viewSelect = root.querySelector("#fa-view");
      const current = root.querySelector("#fa-current");
      const target = root.querySelector("#fa-diagram");
      const groups = [...new Set(diagrams.map((item) => item.group))];
      let renderVersion = 0;
      groups.forEach((group) => {{ const option = document.createElement("option"); option.value = group; option.textContent = group; groupSelect.appendChild(option); }});
      function fillViews() {{
        const views = diagrams.filter((item) => item.group === groupSelect.value);
        viewSelect.innerHTML = "";
        views.forEach((item, index) => {{ const option = document.createElement("option"); option.value = String(index); option.textContent = item.title; viewSelect.appendChild(option); }});
      }}
      async function render() {{
        const version = ++renderVersion;
        const views = diagrams.filter((item) => item.group === groupSelect.value);
        const item = views[Number(viewSelect.value || 0)];
        if (!item) return;
        current.textContent = `${{item.group}} / ${{item.title}}`;
        target.replaceChildren();
        target.setAttribute("aria-busy", "true");
        try {{
          const id = `fa-mermaid-${{Date.now()}}-${{version}}`;
          const result = await mermaid.render(id, item.code);
          if (version !== renderVersion) return;
          target.innerHTML = result.svg;
        }} catch (error) {{
          if (version !== renderVersion) return;
          target.innerHTML = "";
          const message = document.createElement("pre");
          message.className = "fa-error";
          message.textContent = String(error);
          target.appendChild(message);
        }} finally {{
          if (version === renderVersion) {{
            target.setAttribute("aria-busy", "false");
          }}
        }}
      }}
      const style = getComputedStyle(root);
      const normalizedColor = (value) => {{
        const canvas = document.createElement("canvas");
        canvas.width = 1;
        canvas.height = 1;
        const context = canvas.getContext("2d");
        context.clearRect(0, 0, 1, 1);
        context.fillStyle = value;
        context.fillRect(0, 0, 1, 1);
        const [red, green, blue, alpha] = context.getImageData(0, 0, 1, 1).data;
        return `rgba(${{red}}, ${{green}}, ${{blue}}, ${{alpha / 255}})`;
      }};
      const color = (name) => {{
        const token = style.getPropertyValue(name).trim();
        if (!token) return "";
        if (token.startsWith("light-dark(") && token.endsWith(")")) {{
          const choices = token.slice(11, -1);
          let depth = 0;
          for (let index = 0; index < choices.length; index += 1) {{
            if (choices[index] === "(") depth += 1;
            if (choices[index] === ")") depth -= 1;
            if (choices[index] === "," && depth === 0) {{
              return normalizedColor(choices.slice(
                window.matchMedia("(prefers-color-scheme: dark)").matches
                  ? index + 1
                  : 0,
                window.matchMedia("(prefers-color-scheme: dark)").matches
                  ? undefined
                  : index,
              ).trim());
            }}
          }}
        }}
        const probe = document.createElement("span");
        probe.style.cssText = `position:absolute;visibility:hidden;color:var(${{name}})`;
        root.appendChild(probe);
        const resolved = getComputedStyle(probe).color;
        probe.remove();
        return normalizedColor(resolved);
      }};
      const themeVariables = Object.fromEntries(
        [
          ["background", "--background"],
          ["primaryColor", "--muted"],
          ["primaryTextColor", "--foreground"],
          ["primaryBorderColor", "--border"],
          ["lineColor", "--muted-foreground"],
          ["secondaryColor", "--secondary"],
          ["tertiaryColor", "--accent"],
        ]
          .map(([key, token]) => [key, color(token)])
          .filter(([, value]) => value)
      );
      mermaid.initialize({{
        startOnLoad: false,
        theme: "base",
        securityLevel: "strict",
        ...(Object.keys(themeVariables).length ? {{ themeVariables }} : {{}}),
        flowchart: {{
          htmlLabels: true,
          curve: "basis",
          nodeSpacing: 28,
          rankSpacing: 42,
        }},
      }});
      groupSelect.addEventListener("change", () => {{ fillViews(); render(); }});
      viewSelect.addEventListener("change", render);
      fillViews();
      render();
    }})();
  </script>
</section>
'''
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(html, encoding="utf-8")


def main() -> int:
    args = parse_args()
    gateway_root = args.gateway_root.resolve()
    files = parse_source_files(
        gateway_root,
        discover_source_paths(gateway_root, args.scope),
    )
    imports = import_edges(files)
    inheritance = inheritance_edges(files)
    calls = call_edges(files)
    symbols = [symbol for file in files for symbol in file.symbols]
    import_cycles = strongly_connected_components((file.module for file in files), imports)
    inheritance_cycles = strongly_connected_components((symbol.id for symbol in symbols if symbol.kind == "class"), inheritance)
    call_cycles = strongly_connected_components((symbol.id for symbol in symbols), calls)
    diagrams = [
        render_layer_diagram(files, imports),
        *render_file_dependency_diagrams(files, imports),
        *render_inheritance_diagrams(files, inheritance),
        *render_symbol_inventory(files),
        *render_call_diagrams(files, calls),
    ]
    write_markdown(
        args.output.resolve(),
        args.stage,
        files,
        diagrams,
        import_cycles,
        call_cycles,
        inheritance_cycles,
        len(imports),
        len(calls),
        len(inheritance),
    )
    if args.visualization:
        write_visualization(
            args.visualization.resolve(),
            args.stage,
            files,
            diagrams,
            import_cycles,
            call_cycles,
        )
    summary = {
        "stage": args.stage,
        "files": len(files),
        "classes": sum(symbol.kind == "class" for symbol in symbols),
        "callables": sum(symbol.kind in {"function", "method"} for symbol in symbols),
        "import_edges": len(imports),
        "inheritance_edges": len(inheritance),
        "resolved_call_edges": len(calls),
        "import_cycles": import_cycles,
        "inheritance_cycles": inheritance_cycles,
        "call_cycles": call_cycles,
        "diagrams": len(diagrams),
    }
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
