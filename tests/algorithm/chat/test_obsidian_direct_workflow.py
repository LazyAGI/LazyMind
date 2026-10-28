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

    def test_first_publication_uses_the_only_configured_vault(self):
        converted = resources.convert_document('# New note\n\nBody.\n', 'obsidian')
        result = resources.write_document(converted, title='New note')
        self.assertTrue(result['provider_synced'])
        self.assertEqual((self.note.parent / 'New note.md').read_text(), '# New note\n\nBody.\n')
        self.assertTrue(result['target_document']['meta']['publication_revision'])

    def test_multiple_vaults_require_an_explicit_choice(self):
        (self.root / 'Other Vault' / '.obsidian').mkdir(parents=True)
        from lazyllm.tools.fs.supplier.obsidian import _VAULT_DISCOVERY_CACHE
        _VAULT_DISCOVERY_CACHE.clear()
        converted = resources.convert_document('# New\n', 'obsidian')
        with self.assertRaisesRegex(ValueError, 'OBSIDIAN_VAULT_REQUIRED'):
            resources.write_document(converted, title='New')
        self.assertFalse(list(self.root.rglob('New.md')))

    def test_first_publication_does_not_rename_or_replace_existing_note(self):
        converted = resources.convert_document('# Replacement\n', 'obsidian')
        with self.assertRaisesRegex(ValueError, 'OBSIDIAN_TARGET_EXISTS'):
            resources.write_document(converted, title='Note')
        self.assertIn('Original text.', self.note.read_text())
        self.assertEqual(list(self.note.parent.glob('*.md')), [self.note])

    def test_external_edit_is_not_overwritten(self):
        loaded = json.loads(resources.WriterResourceCapabilities().load_document(self.request))
        target = loaded['target_document']
        self.note.write_text('# External edit\n')
        converted = resources.convert_document('# Replacement\n', 'obsidian', target_document=target)
        with self.assertRaisesRegex(ValueError, 'OBSIDIAN_TARGET_CHANGED'):
            resources.write_document(converted, target)
        self.assertEqual(self.note.read_text(), '# External edit\n')

    def test_concurrent_updates_do_not_overwrite_each_other(self):
        from concurrent.futures import ThreadPoolExecutor
        from lazymind.document_tools.obsidian import ObsidianTargetError
        target = json.loads(resources.WriterResourceCapabilities().load_document(self.request))['target_document']

        def update(title):
            try:
                resources.write_document(resources.convert_document(f'# {title}\n', 'obsidian'), target)
                return title
            except ObsidianTargetError as exc:
                self.assertEqual(exc.error_code, 'OBSIDIAN_TARGET_CHANGED')
                return None

        with ThreadPoolExecutor(max_workers=2) as executor:
            results = list(executor.map(update, ['First', 'Second']))
        winners = [result for result in results if result]
        self.assertEqual(len(winners), 1)
        self.assertEqual(self.note.read_text(), f'# {winners[0]}\n')

    def test_new_note_respects_explicit_subdirectory(self):
        folder = self.note.parent / 'Articles'
        folder.mkdir()
        vault = self.fs.discover_vaults()[0]
        result = resources.write_document(
            resources.convert_document('# Exact\n', 'obsidian'), title='Exact',
            parent_uri=f'obsidian://{vault.vault_id}/Articles/',
        )
        self.assertTrue(result['provider_synced'])
        self.assertEqual((folder / 'Exact.md').read_text(), '# Exact\n')

    def test_new_note_rejects_symlink_escape(self):
        outside = self.root / 'outside'
        outside.mkdir()
        (self.note.parent / 'Escape').symlink_to(outside, target_is_directory=True)
        vault = self.fs.discover_vaults()[0]
        with self.assertRaisesRegex(ValueError, 'OBSIDIAN_PATH_INVALID'):
            resources.write_document(
                resources.convert_document('# Exact\n', 'obsidian'), title='Exact',
                parent_uri=f'obsidian://{vault.vault_id}/Escape/',
            )
        self.assertFalse(list(outside.iterdir()))

    def test_preparation_does_not_create_an_empty_note(self):
        from lazymind.document_tools.actions import invoke_document_action
        result = invoke_document_action('builtin:document.publication_target.v1', 'preview',
                                        {'provider': 'obsidian', 'title': 'Prepared'})
        self.assertEqual(result['state'], 'ready')
        self.assertTrue(result['target_document']['meta']['create_pending'])
        self.assertFalse((self.note.parent / 'Prepared.md').exists())

    def test_prepared_target_resumes_without_selecting_another_vault(self):
        from lazymind.document_tools.obsidian import LazyMindObsidianProvider
        provider = LazyMindObsidianProvider()
        target = provider.prepare_target('Prepared')
        (self.root / 'Another' / '.obsidian').mkdir(parents=True)
        self.assertEqual(len(provider.status()['vaults']), 2)
        provider.prepare_target(target=target)
        resources.write_document(resources.convert_document('# Prepared\n', 'obsidian'), target.model_dump())
        self.assertTrue((self.note.parent / 'Prepared.md').exists())
        self.assertFalse((self.root / 'Another' / 'Prepared.md').exists())

    def test_attachments_are_copied_and_missing_attachment_blocks_confirmation(self):
        from lazymind.document_tools.actions import invoke_document_action
        source = self.root / 'fixture.png'
        source.write_bytes(b'fixture-image-bytes')
        assets = {'library_id': 'fixture-library', 'assets': {'image': {
            'media_asset_id': 'image', 'asset_type': 'image', 'source_type': 'input_resource',
            'uri': 'https://example.test/image.png', 'local_path': str(source),
        }}}
        markdown = '# Illustrated\n\n![Fixture](https://example.test/image.png)\n'
        converted = resources.convert_document(markdown, 'obsidian', media_assets=assets)
        result = resources.write_document(converted, media_assets=assets, title='Illustrated')
        copies = list((self.note.parent / 'assets/lazymind').glob('*.png'))
        self.assertEqual(len(copies), 1)
        self.assertEqual(copies[0].read_bytes(), source.read_bytes())
        self.assertIn('![[assets/lazymind/', (self.note.parent / 'Illustrated.md').read_text())
        copies[0].unlink()
        checked = invoke_document_action('builtin:document.reconcile.v1', 'preview', {
            'converted_document': converted, 'target_document': result['target_document'], 'media_assets': assets,
        })
        self.assertEqual(checked['state'], 'unknown')
        self.assertFalse(copies[0].exists())

    def test_unavailable_local_image_does_not_write_a_note(self):
        converted = resources.convert_document('# Broken\n\n![Missing](missing.png)\n', 'obsidian')
        with self.assertRaisesRegex(ValueError, 'OBSIDIAN_ATTACHMENT_UNAVAILABLE'):
            resources.write_document(converted, title='Broken')
        self.assertFalse((self.note.parent / 'Broken.md').exists())

    def test_chat_read_and_implicit_create_declare_different_permissions(self):
        from lazymind.document_tools.host_files import resolve_writer_files
        reading = resolve_writer_files({'user_input': self.request}, reading=True)
        self.assertIn((str(self.note.resolve()), 'read'), {(item.path, item.operation) for item in reading.files})
        self.assertFalse(any(item.operation == 'write' for item in reading.files))
        converted = resources.convert_document('# New note\n', 'obsidian')
        writing = resolve_writer_files({'converted_document_json': json.dumps(converted)})
        self.assertIn((str((self.note.parent / 'New note.md').resolve()), 'write'),
                      {(item.path, item.operation) for item in writing.files})

    def test_chat_revision_preserves_target_metadata_and_response_contract(self):
        from lazymind.document_tools.artifacts import markdown_to_writer_document
        loaded = json.loads(resources.WriterResourceCapabilities().load_document(self.request))
        source = markdown_to_writer_document(loaded['source_document'], document_id='fixture-note')
        source.metadata['source'] = loaded['target_document']
        source.provider_binding = {'provider': 'obsidian', 'uri': loaded['target_document']['uri']}
        result = json.loads(resources.WriterResourceCapabilities().publish_revision(
            source.model_dump_json(), json.dumps({'patch_id': 'revision-fixture',
                                                  'target_doc_id': source.document_id, 'new_title': 'Revised'})))
        self.assertEqual(result['provider'], 'obsidian')
        self.assertEqual(result['representation'], 'ir')
        self.assertTrue(result['published_link'].startswith('obsidian://open?'))
        self.assertTrue(result['draft_document']['metadata']['source']['meta']['publication_revision'])
        self.assertEqual(list(self.note.parent.glob('*.md')), [self.note])

    def test_reconcile_lost_response_reads_without_writing(self):
        from lazymind.document_tools.actions import invoke_document_action
        prepared = invoke_document_action('builtin:document.publication_target.v1', 'preview',
                                          {'provider': 'obsidian', 'title': 'Prepared'})
        target = prepared['target_document']
        converted = resources.convert_document('# Prepared\n', 'obsidian')
        resources.write_document(converted, target)
        note = self.note.parent / 'Prepared.md'
        modified = note.stat().st_mtime_ns
        checked = invoke_document_action('builtin:document.reconcile.v1', 'preview',
                                         {'converted_document': converted, 'target_document': target})
        self.assertEqual(checked['state'], 'confirmed')
        self.assertEqual(note.stat().st_mtime_ns, modified)
        note.write_text('# External edit\n')
        checked = invoke_document_action('builtin:document.reconcile.v1', 'preview',
                                         {'converted_document': converted, 'target_document': target})
        self.assertEqual(checked['state'], 'unknown')
        self.assertEqual(note.read_text(), '# External edit\n')

    def test_preserves_frontmatter_and_updates_baseline(self):
        original = '---\ntags: [fixture]\n---\n# Fixture\n\nOriginal text.\n'
        self.note.write_text(original)
        loaded = json.loads(resources.WriterResourceCapabilities().load_document(self.request))
        target = loaded['target_document']
        result = resources.write_document(resources.convert_document('# Changed\n', 'obsidian'), target)
        self.assertEqual(self.note.read_text(), '---\ntags: [fixture]\n---\n# Changed\n')
        result = resources.write_document(resources.convert_document('# Twice\n', 'obsidian'), result['target_document'])
        self.assertTrue(result['provider_synced'])
        self.assertEqual(self.note.read_text(), '---\ntags: [fixture]\n---\n# Twice\n')

    def test_read_only_vault_is_not_ready(self):
        from lazymind.document_tools.obsidian import LazyMindObsidianProvider
        vault = self.note.parent
        mode = vault.stat().st_mode
        self.addCleanup(vault.chmod, mode)
        vault.chmod(0o555)
        self.assertEqual(LazyMindObsidianProvider().status()['state'], 'permission_required')
        with self.assertRaisesRegex(ValueError, 'OBSIDIAN_PERMISSION_REQUIRED'):
            resources.write_document(resources.convert_document('# New\n', 'obsidian'), title='New')

    def test_chat_declares_note_and_attachment_writes_for_approval(self):
        from lazymind.document_tools.host_files import resolve_writer_files
        from lazymind.document_tools.obsidian import LazyMindObsidianProvider
        target = LazyMindObsidianProvider().prepare_target('Approved')
        resolution = resolve_writer_files({'target_document_json': json.dumps(target.model_dump())})
        intents = {(item.path, item.operation) for item in resolution.files}
        self.assertIn((str((self.note.parent / 'Approved.md').resolve()), 'write'), intents)

    def test_action_reports_conflict_as_no_write(self):
        from lazymind.document_tools.actions import invoke_document_action
        loaded = json.loads(resources.WriterResourceCapabilities().load_document(self.request))
        self.note.write_text('# External\n')
        result = invoke_document_action('builtin:document.write_document.v1', 'execute', {
            'converted_document': resources.convert_document('# Replacement\n', 'obsidian'),
            'target_document': loaded['target_document'],
        })
        self.assertFalse(result['success'])
        self.assertEqual(result['patch_result']['meta'], {'code': 'OBSIDIAN_TARGET_CHANGED', 'no_write': True})
        self.assertEqual(self.note.read_text(), '# External\n')

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
