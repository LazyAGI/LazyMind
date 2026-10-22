#!/usr/bin/env python3
"""Create a new disposable CSV task for the documented developer walkthrough."""
import argparse
from pathlib import Path
import subprocess

from gates import git, save


PLAN = '''# CSV reader implementation plan

### Task 1: csv-records

Implement csv_records.read_records(text) -> list[dict[str, str]] using Python's csv module.
No network or external dependencies. Only modify csv_records.py. Do not edit the Plan or tests.

Requirements / coverage ids:
- quoted: accept quoted commas and embedded newlines in CSV fields.
- headers: trim header names and cell whitespace; reject empty or duplicate normalized headers.
- ragged: raise ValueError for rows with a different number of cells from the header.
- empty: empty or whitespace-only input returns an empty list; header-only input returns an empty list.

Run python3 -m unittest -v test_csv_records before and after implementation.
Write .eval/coverage.json with one DONE entry and nonempty test evidence per coverage id,
and .eval/implementer-report.md. Commit only the implementation; do not push.
'''
TESTS = '''import unittest
from csv_records import read_records

class CSVRecordsTests(unittest.TestCase):
    def test_quoted(self):
        self.assertEqual(read_records('name,note\\nAda,"hello, world"\\n'), [{'name':'Ada', 'note':'hello, world'}])
        self.assertEqual(read_records('name,note\\nAda,"first\\nsecond"\\n'), [{'name':'Ada', 'note':'first\\nsecond'}])
    def test_headers(self):
        self.assertEqual(read_records(' name , age \\n Ada , 42 \\n'), [{'name':'Ada','age':'42'}])
        for text in ['x,x\\n1,2\\n', ' x ,x\\n1,2\\n', 'x,\\n1,2\\n']:
            with self.subTest(text=text), self.assertRaises(ValueError): read_records(text)
    def test_ragged(self):
        for text in ['a,b\\n1\\n', 'a,b\\n1,2,3\\n']:
            with self.subTest(text=text), self.assertRaises(ValueError): read_records(text)
    def test_empty(self):
        for text in ['', ' \\n\\t', 'a,b\\n']:
            with self.subTest(text=text): self.assertEqual(read_records(text), [])
'''


def create(root):
    root = Path(root).resolve()
    root.mkdir(parents=True, exist_ok=False, mode=0o700)
    repo = root / 'repository'
    repo.mkdir()
    (repo / 'docs/plans').mkdir(parents=True)
    (repo / 'docs/plans/csv.md').write_text(PLAN)
    (repo / '.gitignore').write_text('.eval/\n__pycache__/\n')
    (repo / 'csv_records.py').write_text('def read_records(text):\n    raise NotImplementedError\n')
    (repo / 'test_csv_records.py').write_text(TESTS)
    for cmd in [['git','init','-q','-b','main'], ['git','config','user.name','Reliability Demo'],
                ['git','config','user.email','demo@example.invalid'], ['git','add','.'],
                ['git','-c','commit.gpgsign=false','commit','-qm','Declare CSV task and acceptance tests']]:
        subprocess.run(cmd, cwd=repo, capture_output=True, check=True)
    base = git(repo, 'rev-parse', 'HEAD')
    subprocess.run(['git','worktree','add','-q','-b','csv-task', str(root/'worktree'), base], cwd=repo, check=True)
    task = {'schema_version':1,'task_id':'csv-records','plan':'docs/plans/csv.md','plan_task':1,
            'expected_base':base,'allowed_files':['csv_records.py'], 'protected_files':['test_csv_records.py'],
            'coverage':[{'id':i,'description':i+' behavior in the tracked Plan', 'checks':['csv-tests']}
                        for i in ['quoted','headers','ragged','empty']],
            'checks':[{'id':'csv-tests','argv':['python3','-m','unittest','-v','test_csv_records']}]}
    save(root/'task.json', task)
    return task


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    create(parser.parse_args().directory)
    print('Created a fresh CSV repository, worktree and task.json; no existing directory was reused.')
