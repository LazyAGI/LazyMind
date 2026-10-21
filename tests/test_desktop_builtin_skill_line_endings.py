"""Keep locked builtin Skill package bytes stable across Git platforms."""
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class BuiltinSkillLineEndingTests(unittest.TestCase):
    def test_windows_git_checkout_preserves_locked_builtin_skill_bytes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(['git', 'init', '-q', str(root)], check=True)
            (root / '.gitattributes').write_bytes((ROOT / '.gitattributes').read_bytes())
            fixtures = {
                'skills/search/personal-document-search/SKILL.md': b'# Skill\n\nBody\n',
                'skills/search/personal-document-search/references/source-routing.md': b'# Routing\n\nBody\n',
            }
            for name, data in fixtures.items():
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(data)
            subprocess.run(['git', '-C', str(root), '-c', 'core.autocrlf=false', 'add', '.'], check=True)
            for name in fixtures:
                (root / name).unlink()
            subprocess.run(['git', '-C', str(root), '-c', 'core.autocrlf=true',
                            'checkout-index', '--all'], check=True)
            for name, data in fixtures.items():
                with self.subTest(skill_file=name):
                    self.assertEqual((root / name).read_bytes(), data)


if __name__ == '__main__':
    unittest.main()
