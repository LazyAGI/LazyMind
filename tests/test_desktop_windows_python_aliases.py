"""Native Windows integration test; never emulate junction semantics on macOS."""
import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

spec=importlib.util.spec_from_file_location('aliases',Path(__file__).parents[1]/'desktop/scripts/normalize-windows-python.py')
aliases=importlib.util.module_from_spec(spec);spec.loader.exec_module(aliases)

@unittest.skipUnless(os.name=='nt', 'Requires native Windows Python and junctions')
class AliasTest(unittest.TestCase):
    def test_real_interpreter_alias_normalization_and_repeat(self):
        with tempfile.TemporaryDirectory() as temp:
            runtime=Path(temp)/'中文 build runtime'
            real=runtime/'runtimes/python/cpython-real'
            shutil.copytree(sys.base_prefix,real,ignore=shutil.ignore_patterns('site-packages','__pycache__'))
            alias=real.with_name('cpython-alias')
            subprocess.run(['cmd','/c','mklink','/J',str(alias),str(real)],check=True,capture_output=True)
            for name in ('algorithm','auth-service','channel-gateway'):
                subprocess.run([str(alias/'python.exe'),'-m','venv','--without-pip',str(runtime/'deps/python'/name)],check=True)
            aliases.normalize(runtime)
            for name in ('algorithm', 'auth-service', 'channel-gateway'):
                config = runtime / 'deps/python' / name / 'pyvenv.cfg'
                self.assertIn(str(real.resolve()), config.read_text(encoding='utf-8'))
            self.assertFalse(alias.exists())
            self.assertTrue((real/'python.exe').exists())
            aliases.normalize(runtime)
            for name in ('algorithm','auth-service','channel-gateway'):
                subprocess.run([str(runtime/'deps/python'/name/'Scripts/python.exe'),'-I','-c','import ssl,sqlite3,json'],check=True)

if __name__=='__main__': unittest.main()
