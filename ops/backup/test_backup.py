"""Local-only smoke test: real pg_dump/restore, mocked off-host S3 transport.
Run with TEST_DATABASE_URL against a disposable localhost PostgreSQL cluster.
Never reads DATABASE_URL or .env. Requires pg_dump, pg_restore, psql, createdb.
"""
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import tarfile
import unittest
from urllib.parse import urlsplit, urlunsplit
import uuid


class BackupTest(unittest.TestCase):
    def test_dump_restore_and_failed_upload(self):
        url = os.environ.get('TEST_DATABASE_URL')
        if not url:
            self.skipTest('TEST_DATABASE_URL is not set')
        parts = urlsplit(url)
        self.assertIn(parts.hostname, ('127.0.0.1', 'localhost'), 'local test DB required')
        suffix = uuid.uuid4().hex
        schema, database = 'backup_' + suffix, 'restore_' + suffix
        restored = urlunsplit(parts._replace(path='/' + database))
        def sql(target, query):
            return subprocess.check_output(['psql', target, '-v', 'ON_ERROR_STOP=1', '-Atqc', query], text=True).strip()
        try:
            sql(url, f'CREATE SCHEMA {schema}; CREATE TABLE {schema}.sample(id integer); INSERT INTO {schema}.sample VALUES (42)')
            subprocess.run(['createdb', '--maintenance-db', url, database], check=True)
            with tempfile.TemporaryDirectory() as root:
                root = Path(root)
                (root / 'bin').mkdir()
                fake = root / 'bin' / 'aws'
                fake.write_text('#!/bin/sh\n[ "${FAIL_UPLOAD:-0}" != 1 ] || exit 1\n# Preserve exact bytes for restore testing, not an actual S3 request.\ncp "$3" "$FAKE_S3_DIR/$(basename "$3")"\n')
                fake.chmod(0o700)
                remote = root / 'remote'
                remote.mkdir()
                media = root / 'media'
                for skill in ['listening', 'writing', 'speaking']:
                    (media / skill).mkdir(parents=True)
                    (media / skill / 'snapshot.bin').write_bytes(b'private media fixture')
                env = dict(os.environ, PATH=str(root / 'bin') + ':' + os.environ['PATH'],
                           PGHOST=parts.hostname, PGPORT=str(parts.port or 5432), PGUSER=parts.username or 'postgres', PGPASSWORD=parts.password or 'test', PGDATABASE=parts.path.lstrip('/'),
                           BACKUP_S3_URI='s3://test-bucket/backup', AWS_ENDPOINT_URL='',
                           BACKUP_ONCE='true', BACKUP_DIR=str(root / 'backups'), BACKUP_STATUS_DIR=str(root / 'status'), FAKE_S3_DIR=str(remote),
                           BACKUP_MEDIA_ENABLED='true', BACKUP_MEDIA_DIR=str(media), BACKUP_MEDIA_S3_URI='s3://test-bucket/media', BACKUP_OFFHOST='false')
                script = Path(__file__).with_name('backup.sh')
                subprocess.run(['bash', str(script)], env=env, check=True)
                dump, = remote.glob('*.dump')
                checksum = Path(str(dump) + '.sha256').read_text().split()[0]
                self.assertEqual(checksum, hashlib.sha256(dump.read_bytes()).hexdigest())
                subprocess.run(['pg_restore', '--exit-on-error', '--no-owner', '--no-acl', '--dbname', restored, str(dump)], check=True)
                self.assertEqual(sql(restored, f'SELECT id FROM {schema}.sample'), '42')
                status = root / 'status' / 'backup.prom'
                previous = status.read_bytes()
                self.assertIn(b'iac_backup_offhost 0', previous)
                snapshot, = remote.glob('iac-media-*.tar.gz')
                self.assertEqual(Path(str(snapshot) + '.sha256').read_text().split()[0], hashlib.sha256(snapshot.read_bytes()).hexdigest())
                with tarfile.open(snapshot) as archive:
                    for skill in ['listening', 'writing', 'speaking']:
                        self.assertEqual(archive.extractfile(skill + '/snapshot.bin').read(), b'private media fixture')
                failed = subprocess.run(['bash', str(script)], env=dict(env, FAIL_UPLOAD='1'), check=False)
                self.assertNotEqual(failed.returncode, 0)
                self.assertEqual(status.read_bytes(), previous, 'failed upload advanced success heartbeat')
                self.assertEqual(len(list((root / 'backups').glob('*.dump'))), 1, 'failed dump published locally')
                self.assertFalse(list((root / 'backups').glob('.partial.*')), 'temporary artifacts leaked')
        finally:
            sql(url, f'DROP SCHEMA IF EXISTS {schema} CASCADE')
            subprocess.run(['dropdb', '--maintenance-db', url, '--if-exists', database], check=True)


if __name__ == '__main__':
    unittest.main()
