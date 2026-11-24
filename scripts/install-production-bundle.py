#!/usr/bin/env python3
"""Prepare a trusted CI upload before updating live files, preserving bind inodes."""
import pathlib
import shutil
import subprocess
import sys
from datetime import datetime, timezone

stage = pathlib.Path(sys.argv[1]).resolve()
live = pathlib.Path.home() / 'ielts-api'
monitoring = pathlib.Path.home() / 'drone-league/monitoring'
marker = monitoring / 'require-ielts-integration'
if stage == live.resolve() or not stage.is_dir():
    raise SystemExit('Expected an isolated upload directory')
if marker.exists():
    subprocess.run(['python3', str(monitoring / 'redeploy_guard.py'), 'prepare',
                    '--ielts-root', str(stage)], check=True)
files = sorted(p for p in stage.rglob('*') if not p.is_dir())
# Validate all paths before writing anything; reject symlinks in either tree.
for source in stage.rglob('*'):
    target = live / source.relative_to(stage)
    if source.is_symlink() or target.is_symlink():
        raise SystemExit('Symlinks are not supported in a production upload')
    if not target.resolve().is_relative_to(live.resolve()):
        raise SystemExit('Upload destination escapes production directory')
backup = pathlib.Path.home() / 'ielts-deployment-backups' / datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
backup.mkdir(parents=True, mode=0o700)
for source in files:
    relative = source.relative_to(stage)
    target = live / relative
    if target.exists():
        saved = backup / relative
        saved.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        shutil.copyfile(target, saved)
        saved.chmod(0o600)
for source in files:
    target = live / source.relative_to(stage)
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    # copyfile opens the existing file: Docker bind mounts keep the same inode.
    shutil.copyfile(source, target)
    target.chmod(0o600 if target.suffix == '.env' else 0o644)
print('Deployment bundle installed; private rollback snapshot:', backup)
