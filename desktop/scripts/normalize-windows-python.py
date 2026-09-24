#!/usr/bin/env python3
"""Normalize uv interpreter aliases without copying their targets (Windows only)."""
import argparse
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess


def is_link(path):
    info = path.lstat()
    return stat.S_ISLNK(info.st_mode) or bool(getattr(info, 'st_file_attributes', 0) & stat.FILE_ATTRIBUTE_REPARSE_POINT)


def normalize(runtime):
    if os.name != 'nt':
        raise RuntimeError('Windows Python normalization requires native Windows')
    runtime = runtime.resolve(strict=True)
    root = runtime / 'runtimes/python'
    aliases = {}
    for path in root.iterdir():
        if is_link(path):
            target = path.resolve(strict=True)
            if target.parent != root or is_link(target) or not (target / 'python.exe').is_file():
                raise RuntimeError(f'Unsafe interpreter alias: {path} -> {target}')
            aliases[path] = target
    homes = {}
    for name in ('algorithm', 'auth-service', 'channel-gateway'):
        venv = runtime / 'deps/python' / name
        cfg = venv / 'pyvenv.cfg'
        lines = cfg.read_text(encoding='utf-8').splitlines()
        fields = dict(line.split('=', 1) for line in lines if '=' in line)
        fields = {k.strip(): v.strip() for k, v in fields.items()}
        home = Path(fields['home']).resolve(strict=True)
        if home.parent != root or not (home / 'python.exe').is_file():
            raise RuntimeError(f'Venv home escapes bundled interpreters: {home}')
        replacements = {'home': str(home), 'executable': str(home / 'python.exe'),
                        'base-executable': str(home / 'python.exe'), 'base-prefix': str(home),
                        'base-exec-prefix': str(home)}
        rewritten = []
        for line in lines:
            key = line.partition('=')[0].strip()
            rewritten.append(f'{key} = {replacements[key]}' if key in replacements else line)
        tmp = cfg.with_suffix('.tmp')
        tmp.write_text('\n'.join(rewritten) + '\n', encoding='utf-8')
        os.replace(tmp, cfg)
        # Use the same relocatable executable/DLL layout as runtime-manager.
        for source in home.iterdir():
            if source.is_file() and (source.name in ('python.exe', 'pythonw.exe') or source.suffix.lower() == '.dll'):
                shutil.copy2(source, venv / 'Scripts' / source.name)
        probe = 'import sys,json; print(json.dumps([sys.prefix,sys.base_prefix]))'
        result = subprocess.check_output([str(venv / 'Scripts/python.exe'), '-I', '-B', '-c', probe], text=True)
        prefix, base = map(Path, json.loads(result))
        if prefix.resolve() != venv.resolve() or base.resolve() != home:
            raise RuntimeError(f'Venv launch did not use bundled interpreter: {name}')
        homes[name] = str(home.relative_to(runtime))
    # Only delete links after all three interpreters successfully start.
    for alias in aliases:
        if alias.is_symlink():
            alias.unlink()
        else:
            os.rmdir(alias)  # junction itself, never its contents
    report = runtime / 'config/python-aliases.json'
    report.parent.mkdir(parents=True, exist_ok=True)
    previous = json.loads(report.read_text()) if report.exists() else {}
    report.write_text(json.dumps({'homes': homes, 'removedAliases': sorted(set(previous.get('removedAliases', [])) |
        {str(p.relative_to(runtime)) for p in aliases})}, indent=2) + '\n')
    print(report.read_text())


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('runtime', type=Path)
    normalize(parser.parse_args().runtime)
