"""Real filesystem/Git tests with explicitly injected launch callbacks, not Agent E2E."""
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

from fixtures import create
from gates import Guard, GateError, Trace, check_isolation, coverage_check, digest, verify_completion


class Gates(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        fixture = create(self.root / "fixture")
        self.repo = Path(fixture["repo"])
        self.trace = Trace(self.root / "trace.jsonl")
        self.guard = Guard(self.repo, self.root / "state", fixture["base"], self.trace)
        self.guard.brief.write_text("Implement the approved fixture.")
        self.guard.set_todo("in_progress")
        self.calls = 0

    def tearDown(self):
        self.tmp.cleanup()

    def launch(self, brief):
        self.calls += 1
        (self.root / "launched").write_text(brief)
        return {"returncode": 0, "turn_completed": True, "kind": "test-callback"}

    def blocked(self, code):
        with self.assertRaises(GateError) as caught:
            self.guard.dispatch(self.launch)
        self.assertEqual(caught.exception.code, code)
        self.assertEqual(self.calls, 0)
        self.assertFalse((self.root / "launched").exists())

    def test_normal_dispatch(self):
        self.guard.validate()
        self.guard.dispatch(self.launch)
        self.assertEqual(self.calls, 1)

    def test_missing_brief_blocks_actual_callback(self):
        self.guard.validate()
        self.guard.brief.unlink()
        self.blocked("BRIEF_MISSING")

    def test_empty_brief_blocks_actual_callback(self):
        self.guard.validate()
        self.guard.brief.write_text(" \n\t")
        self.blocked("BRIEF_EMPTY")

    def test_changed_brief_invalidates_prior_admission(self):
        self.guard.validate()
        self.guard.brief.write_text("different task")
        self.blocked("ADMISSION_CHANGED")

    def test_symlink_brief_blocked(self):
        self.guard.brief.unlink()
        self.guard.brief.symlink_to(self.repo / "slug.py")
        self.blocked("BRIEF_MISSING")

    def test_missing_todo(self):
        self.guard.todo.unlink()
        self.blocked("TODO_MISSING")

    def test_stale_todo(self):
        self.guard.set_todo("completed")
        self.blocked("TODO_NOT_IN_PROGRESS")

    def test_unobserved_todo(self):
        todo = json.loads(self.guard.todo.read_text())
        todo["at"] = "not an event"
        self.guard.todo.write_text(json.dumps(todo))
        self.blocked("TODO_UNOBSERVED")

    def test_wrong_base(self):
        self.guard.expected_base = "0" * 40
        self.blocked("BASE_MISMATCH")

    def test_actual_head_change_blocks_launch(self):
        self.guard.validate()
        subprocess.run(["git", "-C", str(self.repo), "commit", "--allow-empty",
                        "-qm", "injected wrong base"], check=True)
        self.blocked("BASE_MISMATCH")

    def test_dirty_base(self):
        (self.repo / "slug.py").write_text("changed")
        self.blocked("BASE_DIRTY")

    def test_validation_cannot_be_skipped(self):
        self.blocked("ADMISSION_CHANGED")

    def test_dispatch_failure_is_not_success(self):
        self.guard.validate()
        with self.assertRaisesRegex(GateError, "DISPATCH_FAILED"):
            self.guard.dispatch(lambda _: {"returncode": 9, "turn_completed": False})

    def test_missing_terminal_event_is_not_success(self):
        self.guard.validate()
        with self.assertRaisesRegex(GateError, "DISPATCH_FAILED"):
            self.guard.dispatch(lambda _: {"returncode": 0})

    def test_lock_prevents_duplicate_launch(self):
        self.guard.validate()
        with self.guard.lock():
            self.blocked("DISPATCH_BUSY")

    def test_distinct_worktrees_have_distinct_indexes(self):
        other = self.root / "second"
        subprocess.run(["git", "-C", str(self.repo), "worktree", "add", "--detach",
                        str(other), self.guard.expected_base], check=True, capture_output=True)
        observations = check_isolation([self.repo, other])
        self.assertNotEqual(observations[0]["index"], observations[1]["index"])
        self.assertEqual(observations[0]["common_dir"], observations[1]["common_dir"])

    def test_same_worktree_rejected(self):
        with self.assertRaisesRegex(GateError, "ISOLATION_COLLISION"):
            check_isolation([self.repo, self.repo])

    def test_coverage_missing_duplicate_extra_and_skipped(self):
        required = ("spaces", "punctuation", "empty")
        good = [{"id": i, "status": "DONE", "evidence": "test_" + i} for i in required]
        self.assertEqual(coverage_check(good, required), good)
        for rows in [good[:-1], good + [good[0]], good + [{"id": "extra"}],
                     [{**r, "status": "SKIPPED", "reason": "injected"} for r in good]]:
            with self.subTest(rows=rows), self.assertRaises(GateError):
                coverage_check(rows, required)

    def test_coverage_requires_nonempty_text_evidence(self):
        for invalid in (None, [], {}, 42, False, "", " \n"):
            with self.subTest(evidence=invalid), self.assertRaisesRegex(GateError, "COVERAGE_INCOMPLETE"):
                coverage_check([{"id": "spaces", "status": "DONE", "evidence": invalid}], ("spaces",))

    def test_checklist_cannot_hide_failing_code(self):
        (self.repo / ".eval").mkdir()
        (self.repo / ".eval/coverage.json").write_text(json.dumps([
            {"id": i, "status": "DONE", "evidence": "claim only"}
            for i in ("spaces", "punctuation", "empty")]))
        with self.assertRaisesRegex(GateError, "TEST_FAILURE"):
            verify_completion(self.repo, self.guard.expected_base,
                              digest((self.repo / "test_slug.py").read_bytes()), self.trace)

    def test_modified_tests_rejected(self):
        (self.repo / ".eval").mkdir()
        (self.repo / ".eval/coverage.json").write_text(json.dumps([
            {"id": i, "status": "DONE", "evidence": "claimed"}
            for i in ("spaces", "punctuation", "empty")]))
        original = digest((self.repo / "test_slug.py").read_bytes())
        (self.repo / "test_slug.py").write_text("pass\n")
        with self.assertRaisesRegex(GateError, "TESTS_CHANGED"):
            verify_completion(self.repo, self.guard.expected_base, original, self.trace)


if __name__ == "__main__":
    unittest.main()
