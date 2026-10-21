"""Native package tests (not plugin E2E); existing executor tests stay unchanged."""
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest

import yaml

PACKAGE = Path(__file__).resolve().parents[2] / 'workflows/superpowers-brief-showcase'
SPEC = importlib.util.spec_from_file_location('brief_showcase', PACKAGE / 'scripts/brief.py')
brief = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(brief)


class ShowcaseTests(unittest.TestCase):
    def test_normal(self):
        self.assertEqual(brief.check_showcase_brief({'brief': brief.BRIEF})['status'], 'PASS')

    def test_missing(self):
        with self.assertRaisesRegex(ValueError, '^BRIEF_MISSING$'):
            brief.check_showcase_brief({'brief': None})

    def test_empty(self):
        with self.assertRaisesRegex(ValueError, '^BRIEF_EMPTY$'):
            brief.check_showcase_brief({'brief': ''})

    def test_repaired_input_keeps_provenance(self):
        candidate = {'brief': brief.BRIEF, 'fault_injection': 'FAULT_BRIEF_MISSING'}
        self.assertEqual(brief.check_showcase_brief(candidate)['status'], 'PASS')

    def test_unchanged_retry_stays_blocked(self):
        candidate = {'brief': None, 'fault_injection': 'FAULT_BRIEF_MISSING'}
        for _ in range(2):
            with self.assertRaisesRegex(ValueError, 'BRIEF_MISSING'):
                brief.check_showcase_brief(candidate)

    def test_invalid_candidate(self):
        for value in ([], {'brief': 1}, {'brief': 'wrong task'}):
            with self.assertRaises(ValueError):
                brief.check_showcase_brief(value)

    def test_core_boundary_declared(self):
        config = yaml.safe_load((PACKAGE / 'workflow.yaml').read_text())
        state = yaml.safe_load((PACKAGE / 'scenario/state.yml').read_text())
        self.assertEqual(config['runtime']['post_step_checks'], [{
            'step_id': 'validate_brief', 'tool': 'check_showcase_brief', 'arguments': {'candidate': 'admission'}}])
        self.assertTrue(state['steps']['validate_brief']['tools_only'])
        self.assertEqual(state['transitions']['validate_brief'], [{'to': 'implement'}])
        self.assertNotIn('tools', state['steps']['implement'])
        self.assertEqual(state['steps']['implement']['mode'], 'human')


class RecordedShowcaseTests(unittest.TestCase):
    """Offline receipt checks only; these tests never claim to start a live Agent."""

    def setUp(self):
        self.root = Path(__file__).resolve().parents[2]
        self.evidence = Path(__file__).resolve().parent / 'evidence/showcase'
        self.receipt = json.loads((self.evidence / 'receipt.json').read_text())

    def test_published_sources_and_real_screenshots(self):
        for name, expected in self.receipt['package_source_sha256'].items():
            if name in self.receipt.get('format_only_yaml_semantics_sha256', {}):
                value = yaml.safe_load((self.root / name).read_text())
                actual = hashlib.sha256(json.dumps(value, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
                self.assertEqual(actual, self.receipt['format_only_yaml_semantics_sha256'][name])
            else:
                self.assertEqual(hashlib.sha256((self.root / name).read_bytes()).hexdigest(), expected)
        for name, expected in self.receipt['screenshots'].items():
            self.assertEqual(hashlib.sha256((self.evidence / name).read_bytes()).hexdigest(), expected)

    def test_recorded_admission_boundary(self):
        records = self.receipt['records']
        normal = records['normal-final-completed.json']['values']
        self.assertEqual(normal['/control/session/status'], 'completed')
        self.assertTrue(normal['/control/projection/completed'])
        failed = records['missing-final-blocked.json']['values']
        self.assertFalse(failed['/control/control/admission']['can_begin'])
        self.assertEqual(failed['/control/projection/nodes']['implement']['execution'], 'none')
        refusal = records['denied-implement-final.json']['values']['/details']
        self.assertEqual(refusal['status'], 409)
        self.assertEqual(refusal['response']['error']['code'], 'WORKFLOW_ADMISSION_DENIED')
        self.assertTrue(any(x['event'].get('message') == 'BRIEF_MISSING'
                            for x in records['missing-task.sse']['events']))
        self.assertTrue(records['normal-independent.json']['values']['/artifact_equals_file'])
        self.assertEqual(records['normal-independent.json']['values']['/returncode'], 0)

    def test_recovery_gap_is_not_reported_as_success(self):
        records = self.receipt['records']
        history = records['missing-projection.json']['values']['/attempt_history']
        self.assertEqual([a['status'] for a in history['validate_brief']], ['failed', 'failed'])
        after = records['missing-final-after-edit.json']['values']
        self.assertEqual(after['/control/control/active_executions'], 0)
        self.assertEqual(after['/control/projection/nodes']['validate_brief']['validity'], 'stale')
        self.assertIn('button (disabled) 重试', records['ui-recovery.json']['values']['/state'])
        self.assertEqual(self.receipt['outcomes']['edited_input_same_session_recovery'], 'BLOCKED')
        self.assertEqual(self.receipt['outcomes']['automatic_desktop_continuation'], 'BLOCKED')


if __name__ == '__main__':
    unittest.main()
