"""Synthetic observation tests for false-positive prevention; not Agent E2E."""
from copy import deepcopy
import unittest

from gates import GateError
from parallel import validate_observations


class ParallelEvidence(unittest.TestCase):
    def setUp(self):
        self.before = [{"worktree": "repo" + str(i), "git_dir": "git" + str(i),
                        "index": "index" + str(i), "branch": "branch" + str(i),
                        "head": "base", "slug_sha256": "stub", "tracked_status": ""}
                       for i in range(2)]
        self.after = [{**x, "head": "commit" + str(i), "slug_sha256": "implemented"}
                      for i, x in enumerate(self.before)]
        self.parent = {"head": "base", "index_sha256": "unchanged"}
        self.runs = [{"returncode": 0,
                      "result": {"result": "PASS", "session_id": "session" + str(i),
                                 "control": {"projection": {"completed": True}}},
                      "child": {"started_at": "2026-01-01T00:00:00+00:00",
                                "completed_at": "2026-01-01T00:00:10+00:00"}}
                     for i in range(2)]

    def validate(self):
        return validate_observations(self.before, self.after, self.parent, self.parent, self.runs)

    def test_accepts_distinct_overlapping_workers(self):
        self.assertEqual(self.validate(), 10)

    def test_serial_success_is_not_parallel_success(self):
        self.runs[1]["child"].update(started_at="2026-01-01T00:00:11+00:00",
                                     completed_at="2026-01-01T00:00:20+00:00")
        with self.assertRaisesRegex(GateError, "NO_OBSERVED_CONCURRENCY"):
            self.validate()

    def test_parent_repository_must_remain_unchanged(self):
        with self.assertRaisesRegex(GateError, "PARENT_REPOSITORY_CHANGED"):
            validate_observations(self.before, self.after, self.parent,
                                  {**self.parent, "index_sha256": "changed"}, self.runs)

    def test_shared_index_and_shared_session_fail(self):
        before = deepcopy(self.before)
        self.before[1]["index"] = self.before[0]["index"]
        with self.assertRaisesRegex(GateError, "ISOLATION_COLLISION"):
            self.validate()
        self.before = before
        self.runs[1]["result"]["session_id"] = self.runs[0]["result"]["session_id"]
        with self.assertRaisesRegex(GateError, "SESSION_COLLISION"):
            self.validate()

    def test_incomplete_results_cannot_pass(self):
        self.runs.pop()
        with self.assertRaisesRegex(GateError, "INCOMPLETE_PARALLEL_OBSERVATION"):
            self.validate()


if __name__ == "__main__":
    unittest.main()
