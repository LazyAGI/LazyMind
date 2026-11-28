"""Offline supplement tests; --source-root supplies verified upstream public files."""
import argparse
from copy import deepcopy
import json
from pathlib import Path
import tempfile
import unittest
from zipfile import ZipFile

import package_skill as package
import check_recovery as recovery

SOURCE_ROOT = None


class Packaging(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if SOURCE_ROOT is None:
            raise unittest.SkipTest('Provide --source-root for the pinned package integration tests')
        cls.inputs = package.inputs_from(SOURCE_ROOT)

    def test_original_standalone_directory_reproduces_missing_dependency(self):
        with self.assertRaisesRegex(ValueError, 'Unresolved package dependency'):
            package.check_links({'SKILL.md': self.inputs['primary']})

    def test_packaged_reference_resolves_without_plugin_root(self):
        files = package.assemble(self.inputs)
        package.check_links(files)
        self.assertEqual(files['references/CONNECTORS.md'], self.inputs['dependency'])
        self.assertEqual(files['LICENSE'], self.inputs['license'])
        origin = json.loads(files['ORIGIN.json'])
        self.assertEqual(origin['primary']['path'], 'marketing/skills/competitive-brief/SKILL.md')
        self.assertNotIn('.mcp.json', files)

    def test_only_declared_packaging_changes(self):
        skill = package.assemble(self.inputs)['SKILL.md'].decode()
        reverted = skill.replace('\n' + package.CHANGE_NOTE, '', 1)
        reverted = reverted.replace('name: ' + package.ALIAS, 'name: competitive-brief', 1)
        reverted = reverted.replace('`/' + package.ALIAS + '`', '`/competitive-brief`')
        reverted = reverted.replace(package.NEW_LINK, package.OLD_LINK, 1)
        self.assertEqual(reverted.encode(), self.inputs['primary'])

    def test_product_management_cannot_be_substituted_for_marketing(self):
        data = dict(self.inputs)
        data['primary'] = (SOURCE_ROOT / package.LOCK['distinct_same_name']['path']).read_bytes()
        with self.assertRaisesRegex(ValueError, 'Pinned source mismatch'):
            package.assemble(data)

    def test_changed_connector_is_rejected(self):
        data = dict(self.inputs, dependency=self.inputs['dependency'] + b'\n')
        with self.assertRaisesRegex(ValueError, 'Pinned source mismatch'):
            package.assemble(data)

    def test_escape_and_missing_reference_are_rejected(self):
        for link in ('../outside.md', '%2e%2e/outside.md', '/outside.md', 'missing.md'):
            with self.subTest(link=link), self.assertRaises(ValueError):
                package.check_links({'SKILL.md': f'[reference]({link})'.encode()})

    def test_zip_is_reproducible_and_self_contained(self):
        with tempfile.TemporaryDirectory() as temp:
            first, second = Path(temp) / 'one', Path(temp) / 'two'
            a, b = package.build(first, self.inputs), package.build(second, self.inputs)
            self.assertEqual(a['archive_sha256'], b['archive_sha256'])
            with ZipFile(first / a['archive']) as z:
                extracted = {n: z.read(n) for n in z.namelist()}
                package.check_links(extracted)
                self.assertEqual(set(extracted), set(a['files']))
                self.assertEqual(extracted['LICENSE'], self.inputs['license'])
            with self.assertRaises(FileExistsError):
                package.build(first, self.inputs)


class RecordedRecovery(unittest.TestCase):
    def setUp(self):
        self.records = recovery.load_records()

    def final(self):
        return recovery.payload(self.records['http/projection-completed.json'])

    def test_real_recordings_preserve_assisted_not_autonomous_outcome(self):
        result = recovery.check(self.records)
        self.assertEqual(result['autonomous_external_task'], 'failed')
        self.assertEqual(result['assisted_session'], 'completed')
        self.assertEqual(result['new_live_runs'], 0)

    def test_external_success_cannot_be_inferred_from_session_success(self):
        recovery.payload(self.records['http/external-after-recovery.json'])['status'] = 'succeeded'
        with self.assertRaisesRegex(ValueError, 'failure must survive'):
            recovery.check(self.records)

    def test_graph_change_cannot_be_hidden(self):
        self.final()['graph_hash'] = 'different-graph'
        with self.assertRaisesRegex(ValueError, 'published graph'):
            recovery.check(self.records)

    def test_failed_attempt_cannot_be_erased(self):
        self.final()['attempt_history']['analyze_positioning'].pop(0)
        with self.assertRaisesRegex(ValueError, 'Failed attempt'):
            recovery.check(self.records)

    def test_research_rerun_cannot_be_called_reuse(self):
        attempts = self.final()['attempt_history']['research_competitors']
        attempts.append(deepcopy(attempts[0]))
        with self.assertRaisesRegex(ValueError, 'Upstream work reran'):
            recovery.check(self.records)

    def test_different_input_revision_cannot_be_called_reuse(self):
        final = self.final()
        retry = final['attempt_history']['analyze_positioning'][1]['attempt_id']
        final['input_witnesses'][retry][0]['revision_id'] = 'different-revision'
        with self.assertRaisesRegex(ValueError, 'input revisions'):
            recovery.check(self.records)

    def test_accepted_retry_from_another_session_is_rejected(self):
        recovery.payload(self.records['http/retry-command.json'])['SessionID'] = 'other-session'
        with self.assertRaisesRegex(ValueError, 'different session'):
            recovery.check(self.records)

    def test_edit_failure_requires_unchanged_report(self):
        slots = recovery.payload(self.records['http/slots-final.json'])['slots']
        next(s for s in slots if s['slot_id'] == 'competitive_brief')['revision'] = 2
        with self.assertRaisesRegex(ValueError, 'Failed edits changed'):
            recovery.check(self.records)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root', required=True, type=Path)
    args, remaining = parser.parse_known_args()
    SOURCE_ROOT = args.source_root
    unittest.main(argv=['test_supplement.py'] + remaining)
