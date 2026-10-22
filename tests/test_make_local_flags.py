"""Check exported startup flags without building or starting local services."""
import os
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[1]


class LocalFlagsTest(unittest.TestCase):
    def exported_flags(self, *args):
        env = {key: value for key, value in os.environ.items()
               if key not in ('MAKEFLAGS', 'MFLAGS', 'MAKELEVEL', 'ENABLE_RAG', 'DYNAMIC_PROMPT',
                              'LAZYMIND_RAG_ENABLED', 'LAZYMIND_DYNAMIC_PROMPT_MODULES')}
        result = subprocess.run(
            ['make', '--no-print-directory', '-s', '-f', 'Makefile', '-f', '-',
             'test-local-flags', *args],
            input='test-local-flags:\n\t@echo "$$LAZYMIND_RAG_ENABLED $$LAZYMIND_DYNAMIC_PROMPT_MODULES"\n',
            cwd=ROOT, env=env, text=True, capture_output=True, check=True,
        )
        return result.stdout.strip()

    def test_full_names(self):
        self.assertEqual(self.exported_flags(
            'LAZYMIND_RAG_ENABLED=false', 'LAZYMIND_DYNAMIC_PROMPT_MODULES=true'), 'false true')

    def test_defaults(self):
        self.assertEqual(self.exported_flags(), 'true true')

    def test_deployment_disabled(self):
        self.assertEqual(self.exported_flags(
            'ENABLE_RAG=false', 'DYNAMIC_PROMPT=false'), 'false false')

    def test_short_flags_override_full_names(self):
        self.assertEqual(self.exported_flags(
            'LAZYMIND_RAG_ENABLED=true', 'LAZYMIND_DYNAMIC_PROMPT_MODULES=false',
            'ENABLE_RAG=false', 'DYNAMIC_PROMPT=true'), 'false true')

    def test_reverse_flags(self):
        self.assertEqual(self.exported_flags(
            'ENABLE_RAG=true', 'DYNAMIC_PROMPT=false'), 'true false')


if __name__ == '__main__':
    unittest.main()
