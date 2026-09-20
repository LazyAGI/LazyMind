"""Real local-file coverage for Obsidian links pasted after a Writer mention."""
import importlib.util
import json
import sys
import tempfile
import unittest
from contextlib import ExitStack
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from lazyllm.tools.fs.supplier.obsidian import ObsidianFS
from lazyllm.tools.writer.provider.obsidian import ObsidianWriterProvider
from lazymind.document_tools import resources


class ObsidianDirectWorkflowTest(unittest.TestCase):
    def setUp(self):
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.root = Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        vault = self.root / 'Fixture Vault'
        (vault / '.obsidian').mkdir(parents=True)
        self.note = vault / 'Note.md'
        self.note.write_text('# Fixture\n\nOriginal text.\n', encoding='utf-8')
        self.fs = ObsidianFS(token=str(self.root), skip_instance_cache=True)
        self.stack.enter_context(patch.object(ObsidianWriterProvider, '_fs', return_value=self.fs))
        self.stack.enter_context(patch.object(resources, '_temp_root', return_value=self.root / 'artifacts'))
        self.locator = 'obsidian://open?vault=Fixture%20Vault&file=Note'
        self.request = 'AI Writer' + self.locator + '，润色这个文档'

    def test_existing_session_keeps_the_full_locator(self):
        for prefix in ('AI Writer', 'AI Writer ', ''):
            with self.subTest(prefix=prefix):
                self.assertEqual(resources.provider_reference(prefix + self.locator), self.locator)

    def test_legacy_link_loads_and_writes_the_same_file(self):
        loaded = json.loads(resources.WriterResourceCapabilities().load_document(self.request))
        self.assertEqual(loaded['representation'], 'markdown')
        self.assertIn('Original text.', loaded['source_document'])
        target = loaded['target_document']
        revised = '# Fixture\n\nRevised text.\n'
        converted = resources.convert_document(revised, 'obsidian', target_document=target)
        result = resources.write_document(converted, target)
        self.assertTrue(result['provider_synced'])
        self.assertEqual(self.note.read_text(encoding='utf-8'), revised)
        self.assertEqual(list(self.note.parent.glob('*.md')), [self.note])

    def test_missing_note_does_not_become_a_new_document(self):
        self.note.unlink()
        with self.assertRaises(FileNotFoundError):
            resources.WriterResourceCapabilities().load_document(self.request)
        self.assertFalse(list(self.note.parent.glob('*.md')))

    def test_prepare_produces_source_and_original_target(self):
        project = Path(__file__).resolve().parents[3]
        script = project / 'workflows/writer-workflow/scripts/tools.py'
        spec = importlib.util.spec_from_file_location('obsidian_direct_workflow_test_tools', script)
        tools = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = tools
        self.addCleanup(sys.modules.pop, spec.name, None)
        spec.loader.exec_module(tools)
        context = SimpleNamespace(workspace_path=str(self.root / 'workspace'),
                                  params={'user_input': self.request, 'step_id': 'prepare'})
        self.stack.enter_context(patch.object(tools, 'require_context', return_value=context))
        # Language-model planning and Session persistence are outside this local-file test.
        for name, value in {
            'writer_classify_structure': 'sectioned',
            'writer_build_writing_task': '',
            'writer_collect_available_media': {'media_assets': '', 'profile_input_resources': ''},
            'writer_profile_resources': '',
            'writer_create_writing_context': '',
            '_save_draft_workspace_artifacts': [],
        }.items():
            self.stack.enter_context(patch.object(tools, name, return_value=value))
        result = tools.writer_prepare_workspace(operation='revise_document')
        self.assertIn('source_document', result)
        self.assertIn('Original text.', Path(result['source_document']).read_text())
        target = json.loads(Path(result['target_document']).read_text())
        self.assertIn(self.fs.resolve_locator(self.locator).vault.vault_id, json.dumps(target))
        command = tools._load_writer_command(result['writer_command'])
        self.assertEqual(command.action, 'revise')
        self.assertEqual(command.source_ref, self.locator)
        self.assertEqual(result['next_step'], 'write_document')


if __name__ == '__main__':
    unittest.main()
