#!/usr/bin/env python3
"""Backport lazy RAG exports to the pinned LazyLLM wheel used by installers.

Reader implementations stay unchanged. Only the package initializer is made
lazy so importing OCR cannot initialize document stores or optional RAG deps.
"""

import ast
import importlib.util
from pathlib import Path


def patch_source(source, *, parsing_service=False):
    tree = ast.parse(source)
    if any(isinstance(node, ast.FunctionDef) and node.name == '__getattr__' for node in tree.body):
        return source
    exports = {}
    public = None
    for node in tree.body:
        if isinstance(node, ast.ImportFrom) and node.level == 1:
            for alias in node.names:
                exports[alias.asname or alias.name] = ('.' + node.module, alias.name)
        elif isinstance(node, ast.Assign):
            if any(isinstance(target, ast.Name) and target.id == '__all__' for target in node.targets):
                public = ast.literal_eval(node.value)
    special = 'add_post_action_for_default_reader'
    required = ({'_Processor', 'DocumentProcessor', 'DocumentProcessorWorker'} if parsing_service
                else {'Document', 'PDFReader', 'inject_reader_config'})
    if not public or not required <= exports.keys():
        raise RuntimeError('Unrecognized LazyLLM RAG initializer; review the reader compatibility patch')
    if set(public) - exports.keys() - {special}:
        raise RuntimeError('Unknown RAG exports; refusing to drop public APIs')
    lines = ['from importlib import import_module', '', '_EXPORTS = {']
    lines.extend(f'    {name!r}: {target!r},' for name, target in exports.items())
    lines.extend(['}', '', '', 'def __getattr__(name):'])
    if not parsing_service:
        lines.extend([f'    if name == {special!r}:',
                      "        return __getattr__('SimpleDirectoryReader').add_post_action_for_default_reader"])
    lines.extend([
        '    if name not in _EXPORTS:', '        raise AttributeError(name)',
        '    module_name, attribute = _EXPORTS[name]',
        '    value = getattr(import_module(module_name, __name__), attribute)',
        '    globals()[name] = value', '    return value', '', '',
        f'__all__ = {public!r}', '',
    ])
    return '\n'.join(lines)


if __name__ == '__main__':
    spec = importlib.util.find_spec('lazyllm')
    if spec is None or not spec.submodule_search_locations:
        raise RuntimeError('LazyLLM must be installed before patching its reader imports')
    root = Path(next(iter(spec.submodule_search_locations))) / 'tools/rag'
    for relative, parsing in [('__init__.py', False), ('parsing_service/__init__.py', True)]:
        target = root / relative
        original = target.read_text(encoding='utf-8')
        patched = patch_source(original, parsing_service=parsing)
        if patched != original:
            target.write_text(patched, encoding='utf-8')
    print('LazyLLM document readers are independent of RAG initialization')
