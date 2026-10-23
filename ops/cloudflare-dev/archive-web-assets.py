#!/usr/bin/env python3
"""Retain public assets from dev web images before replacing the web service."""

from pathlib import Path
import fcntl
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import uuid

BASE = Path('/home/almat/ielts-guest-dev')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def retain_image(image, output, manifests):
    container = 'iac-asset-export-' + uuid.uuid4().hex[:12]
    files = []
    added = 0
    try:
        subprocess.run(
            ['docker', 'create', '--name', container, image],
            check=True, stdout=subprocess.DEVNULL,
        )
        with tempfile.TemporaryDirectory(prefix='iac-assets-') as folder:
            subprocess.run(
                ['docker', 'cp', container + ':/app/.output/public/assets/.', folder],
                check=True, stdout=subprocess.DEVNULL,
            )
            for source in sorted(Path(folder).rglob('*')):
                if source.is_symlink():
                    raise RuntimeError('Symlink rejected: ' + str(source))
                if not source.is_file():
                    continue
                relative = source.relative_to(folder)
                if any(part.startswith('.') for part in relative.parts):
                    continue
                target = output / relative
                sha256 = digest(source)
                files.append({
                    'path': str(relative), 'sha256': sha256,
                    'bytes': source.stat().st_size,
                })
                if target.exists():
                    # Compression metadata can differ for identical raw assets.
                    if target.suffix not in ('.gz', '.br', '.zst') and digest(target) != sha256:
                        raise RuntimeError('Immutable asset filename collision: ' + str(relative))
                    continue
                target.parent.mkdir(parents=True, exist_ok=True)
                descriptor, temporary = tempfile.mkstemp(prefix='.publish-', dir=target.parent)
                os.close(descriptor)
                try:
                    shutil.copyfile(source, temporary)
                    os.chmod(temporary, 0o644)
                    os.replace(temporary, target)
                finally:
                    if os.path.exists(temporary):
                        os.unlink(temporary)
                added += 1
        manifest = manifests / (image.replace(':', '_') + '.json')
        manifest.write_text(json.dumps({'image': image, 'files': files}, indent=2) + '\n')
        print(f'{image}: {len(files)} files, {added} retained', flush=True)
    finally:
        subprocess.run(
            ['docker', 'rm', '-f', container],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )


def main():
    images = sys.argv[1:]
    if images:
        if any(not image.startswith('iac-web:guest-dev-') for image in images):
            raise SystemExit('Only isolated iac-web:guest-dev-* images are allowed')
    else:
        images = subprocess.check_output(
            ['docker', 'image', 'ls', 'iac-web', '--format', '{{.Repository}}:{{.Tag}}'],
            text=True,
        ).splitlines()
        images = [image for image in images if image.startswith('iac-web:guest-dev-')]
    if not images:
        raise SystemExit('No dev web images supplied or found')
    with (BASE / 'asset-archive.lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        output = BASE / 'static-assets'
        manifests = BASE / 'asset-manifests'
        output.mkdir(mode=0o755, exist_ok=True)
        manifests.mkdir(exist_ok=True)
        for image in images:
            retain_image(image, output, manifests)
        print('Retained asset directory: ' + str(output), flush=True)


if __name__ == '__main__':
    main()
