"""Atomic local macOS bundle replacement with a recoverable previous bundle.

The caller stops the application and validates the candidate before calling.
"""
from pathlib import Path
import os
import subprocess
import tempfile


def replace_application(source: Path, target: Path, backup: Path, verify):
    if backup.exists():
        raise FileExistsError(f"Backup already exists: {backup}")
    backup.parent.mkdir(parents=True, exist_ok=True)
    # Prepare all files before replacing the installed directory. Besides keeping
    # partial copies out of /Applications, this avoids macOS protecting a bundle
    # halfway through a recursive copy as soon as its metadata appears.
    with tempfile.TemporaryDirectory(prefix='replacement-', dir=backup.parent) as staging:
        candidate = Path(staging) / source.name
        subprocess.run(['/bin/cp', '-cR', str(source), str(candidate)], check=True)
        os.rename(target, backup)
        try:
            os.rename(candidate, target)
            verify(target)
        except BaseException:
            # Preserve failed bytes for diagnosis rather than overwriting backup.
            failed = backup.parent / (backup.name + '.failed')
            if target.exists():
                if failed.exists():
                    failed = Path(tempfile.mkdtemp(prefix='failed-', dir=backup.parent)) / target.name
                os.rename(target, failed)
            os.rename(backup, target)
            raise
