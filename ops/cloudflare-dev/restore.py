#!/usr/bin/env python3
"""Restore the captured snapshot into the new dev stack exactly once."""
import os
import pathlib
import subprocess
import time

root = pathlib.Path('/home/almat/ielts-guest-dev')
os.chdir(root)
if (root / 'restore-complete').exists():
    raise SystemExit('Dev restore is already complete; refusing to replace its data')
for _ in range(20):
    if subprocess.run(['docker', 'exec', 'ielts-guest-dev-postgres-1',
                       'pg_isready', '-U', 'ielts_dev', '-d', 'ielts_dev'],
                      stdout=subprocess.DEVNULL).returncode == 0:
        break
    time.sleep(2)
else:
    raise SystemExit('Dev PostgreSQL not ready')
with (root / 'snapshot/postgres.dump').open('rb') as source:
    subprocess.run(['docker', 'exec', '-i', 'ielts-guest-dev-postgres-1',
                    'pg_restore', '-U', 'ielts_dev', '-d', 'ielts_dev',
                    '--no-owner', '--no-acl', '--exit-on-error'], stdin=source, check=True)
app = dict(line.split('=', 1) for line in (root / 'app.env').read_text().splitlines() if '=' in line)
subprocess.run(['docker', 'run', '--rm', '--network', 'ielts-guest-dev_default',
                '-v', str(root / 'migrations') + ':/migrations:ro',
                'migrate/migrate:v4.18.3', '-path=/migrations',
                '-database=' + app['DATABASE_URL'], 'up'], check=True)
with (root / 'sanitize.sql').open('rb') as source:
    subprocess.run(['docker', 'exec', '-i', 'ielts-guest-dev-postgres-1',
                    'psql', '-v', 'ON_ERROR_STOP=1', '-U', 'ielts_dev', '-d', 'ielts_dev'],
                   stdin=source, check=True)
infra = dict(line.split('=', 1) for line in (root / 'dev.env').read_text().splitlines() if '=' in line)
env = dict(os.environ)
env['MC_HOST_iacdev'] = f"http://{infra['MINIO_ROOT_USER']}:{infra['MINIO_ROOT_PASSWORD']}@127.0.0.1:19010"
subprocess.run(['mc', 'mb', '--ignore-existing', 'iacdev/ielts-dev-media'], env=env, check=True)
for media in ['listening-media', 'writing-media']:
    subprocess.run(['mc', 'mirror', '--quiet', str(root / 'snapshot' / media) + '/',
                    'iacdev/ielts-dev-media/'], env=env, check=True)
# Recordings and student answers were cleared; do not publish their copies.
(root / 'restore-complete').write_text('2026-10-07\n')
print('Dev snapshot restored, student data anonymized, media seeded into private MinIO')
