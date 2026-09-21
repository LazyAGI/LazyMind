"""Export/check real Action wire contracts without loading the model runtime.

The revision generator is replaced; unused carrier file loading is forbidden.
Registry input validation,
preview/execute handlers, manifest checks and result serialization are real.
Run with the project's Pydantic dependency; no pytest or model is required.
"""
from __future__ import annotations

import argparse
import importlib.util
import json
from pathlib import Path
import sys
import tempfile
from types import ModuleType, SimpleNamespace

ROOT = Path(__file__).resolve().parents[3]
OUTPUT = ROOT / 'backend/core/testdata/document-rewrite/contract.json'


def contracts():
    package = ModuleType('_document_contract')
    package.__path__ = [str(ROOT / 'algorithm/lazymind/document_tools')]
    sys.modules[package.__name__] = package
    spec = importlib.util.spec_from_file_location(
        '_document_contract.actions', Path(package.__path__[0]) / 'actions.py')
    actions = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = actions
    spec.loader.exec_module(actions)
    artifacts = ModuleType('_document_contract.artifacts')
    def forbidden_file_read(*args, **kwargs):
        raise AssertionError('data envelopes must not load carrier files')
    artifacts._read_artifact_data = forbidden_file_read
    sys.modules[artifacts.__name__] = artifacts
    revision = ModuleType('_document_contract.revision')
    sys.modules[revision.__name__] = revision
    cases = []
    for representation in ('markdown', 'ir'):
        source = 'Original.'
        candidate = 'Rewritten.'
        selection = {'type': 'markdown', 'selected_text': 'Original.'}
        target = {'type': 'block', 'block_type': 'paragraph', 'target_start': 0, 'target_end': 9}
        patch_type = 'string_replace_set'
        if representation == 'ir':
            source = {'document_id': 'rewrite-doc', 'blocks': [{'node_id': 'p', 'type': 'paragraph', 'content': 'Original.'}]}
            candidate = {'document_id': 'rewrite-doc', 'blocks': [{'node_id': 'p', 'type': 'paragraph', 'content': 'Rewritten.'}]}
            selection = {'type': 'ir', 'node_id': 'p'}
            target = {'type': 'block', 'block_type': 'paragraph', 'node_id': 'p'}
            patch_type = 'writer_ir_patch'
        arguments = {'type': representation, 'instruction': 'Make it clearer',
                     'selection_ranges': [{k: v for k, v in selection.items() if k != 'type'}]}
        calls = []

        def preview(document, instruction, ranges, context, *, artifact_store):
            assert document == source and instruction == arguments['instruction']
            assert ranges == arguments['selection_ranges']
            calls.append(True)
            result = {'representation': representation, 'results': [{
                'target': target,
                'preview': {'old_text': 'Original.', 'new_text': 'Rewritten.'},
                'patch': {'type': patch_type, 'payload': {}},
            }]}
            if representation == 'ir':
                result['revised_document'] = SimpleNamespace(model_dump=lambda **kwargs: candidate)
            else:
                path = Path(artifact_store) / 'candidate.md'
                path.write_text(candidate, encoding='utf-8')
                result['revised_document_md'] = str(path)
            return result

        revision.preview_selection_rewrite = preview
        with tempfile.TemporaryDirectory(prefix='document-contract-') as directory:
            reference = 'builtin:document.rewrite_selection.v1'
            try:
                actions.invoke_document_action(reference, 'preview',
                    {'instruction': arguments['instruction'], 'selection': selection},
                    artifact={'data': source}, artifact_store=directory)
            except actions.DocumentActionError as error:
                assert error.status_code == 422 and error.error_code == 'WORKFLOW_ACTION_INVALID'
            else:
                raise AssertionError('old Go request unexpectedly accepted')
            assert calls == []
            result = actions.invoke_document_action(reference, 'preview', arguments,
                artifact={'data': source}, artifact_store=directory)
            execute = actions.invoke_document_action(reference, 'execute',
                {'commit_token': result['commit']['token']},
                artifact={'data': source}, artifact_store=directory)
            assert len(calls) == 1 and execute['artifact']['value'] == candidate
            result['commit']['token'] = '00000000000000000000000000000001'
            cases.append({'representation': representation, 'source': source,
                          'candidate': candidate, 'public_selection': selection,
                          'algorithm_arguments': arguments, 'preview_result': result,
                          'execute_result': execute})
    return json.dumps(cases, ensure_ascii=False, indent=2, sort_keys=True) + '\n'


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    actual = contracts()
    if args.check:
        if OUTPUT.read_text(encoding='utf-8') != actual:
            raise SystemExit('Algorithm rewrite contract fixture is stale')
        print('Actual Algorithm models/handlers match the Go contract fixture')
    else:
        OUTPUT.write_text(actual, encoding='utf-8')
        print(OUTPUT.relative_to(ROOT))
