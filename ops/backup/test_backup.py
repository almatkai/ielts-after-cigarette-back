"""Local-only smoke test: real pg_dump/restore, mocked off-host S3 and Google Drive transport.
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


class BackupConfigTest(unittest.TestCase):
    def test_missing_destinations_fails(self):
        script = Path(__file__).with_name('backup.sh')
        env = dict(os.environ, PGHOST='localhost', PGUSER='postgres', PGPASSWORD='x', PGDATABASE='db',
                   BACKUP_S3_URI='', BACKUP_GDRIVE_ENABLED='false', BACKUP_GDRIVE_REMOTE='')
        res = subprocess.run(['bash', str(script)], env=env, capture_output=True, text=True)
        self.assertNotEqual(res.returncode, 0)
        self.assertIn('At least one backup destination', res.stderr)

    def test_invalid_s3_uri_fails(self):
        script = Path(__file__).with_name('backup.sh')
        env = dict(os.environ, PGHOST='localhost', PGUSER='postgres', PGPASSWORD='x', PGDATABASE='db',
                   BACKUP_S3_URI='http://invalid', BACKUP_GDRIVE_ENABLED='false')
        res = subprocess.run(['bash', str(script)], env=env, capture_output=True, text=True)
        self.assertNotEqual(res.returncode, 0)
        self.assertIn('BACKUP_S3_URI must start with s3://', res.stderr)


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
                fake_aws = root / 'bin' / 'aws'
                fake_aws.write_text('#!/bin/sh\n[ "${FAIL_UPLOAD:-0}" != 1 ] || exit 1\n# Preserve exact bytes for restore testing, not an actual S3 request.\ncp "$3" "$FAKE_S3_DIR/$(basename "$3")"\n')
                fake_aws.chmod(0o700)

                fake_rclone = root / 'bin' / 'rclone'
                fake_rclone.write_text('#!/bin/sh\n[ "${FAIL_GDRIVE_UPLOAD:-0}" != 1 ] || exit 1\nif [ "$1" = "copy" ]; then\n  cp "$2" "$FAKE_GDRIVE_DIR/$(basename "$2")"\nelif [ "$1" = "delete" ]; then\n  exit 0\nfi\n')
                fake_rclone.chmod(0o700)

                remote_s3 = root / 'remote_s3'
                remote_s3.mkdir()
                remote_gdrive = root / 'remote_gdrive'
                remote_gdrive.mkdir()

                media = root / 'media'
                for skill in ['listening', 'writing', 'speaking']:
                    (media / skill).mkdir(parents=True)
                    (media / skill / 'snapshot.bin').write_bytes(b'private media fixture')

                env = dict(os.environ, PATH=str(root / 'bin') + ':' + os.environ['PATH'],
                           PGHOST=parts.hostname, PGPORT=str(parts.port or 5432), PGUSER=parts.username or 'postgres', PGPASSWORD=parts.password or 'test', PGDATABASE=parts.path.lstrip('/'),
                           BACKUP_S3_URI='s3://test-bucket/backup', AWS_ENDPOINT_URL='',
                           BACKUP_ONCE='true', BACKUP_DIR=str(root / 'backups'), BACKUP_STATUS_DIR=str(root / 'status'), FAKE_S3_DIR=str(remote_s3),
                           BACKUP_MEDIA_ENABLED='true', BACKUP_MEDIA_DIR=str(media), BACKUP_MEDIA_S3_URI='s3://test-bucket/media', BACKUP_OFFHOST='false',
                           BACKUP_GDRIVE_ENABLED='true', BACKUP_GDRIVE_REMOTE='gdrive:iac-backups/postgres', BACKUP_MEDIA_GDRIVE_REMOTE='gdrive:iac-backups/media',
                           FAKE_GDRIVE_DIR=str(remote_gdrive))
                script = Path(__file__).with_name('backup.sh')
                subprocess.run(['bash', str(script)], env=env, check=True)

                # Verify S3 dump & media
                dump_s3, = remote_s3.glob('*.dump')
                checksum = Path(str(dump_s3) + '.sha256').read_text().split()[0]
                self.assertEqual(checksum, hashlib.sha256(dump_s3.read_bytes()).hexdigest())

                # Verify GDrive dump & media
                dump_gdrive, = remote_gdrive.glob('*.dump')
                checksum_gdrive = Path(str(dump_gdrive) + '.sha256').read_text().split()[0]
                self.assertEqual(checksum_gdrive, hashlib.sha256(dump_gdrive.read_bytes()).hexdigest())

                subprocess.run(['pg_restore', '--exit-on-error', '--no-owner', '--no-acl', '--dbname', restored, str(dump_s3)], check=True)
                self.assertEqual(sql(restored, f'SELECT id FROM {schema}.sample'), '42')

                status = root / 'status' / 'backup.prom'
                previous = status.read_bytes()
                # Because Google Drive was enabled, offhost is 1 even if BACKUP_OFFHOST was false
                self.assertIn(b'iac_backup_offhost 1', previous)

                snapshot_s3, = remote_s3.glob('iac-media-*.tar.gz')
                snapshot_gdrive, = remote_gdrive.glob('iac-media-*.tar.gz')
                self.assertEqual(snapshot_s3.read_bytes(), snapshot_gdrive.read_bytes())
                with tarfile.open(snapshot_s3) as archive:
                    for skill in ['listening', 'writing', 'speaking']:
                        self.assertEqual(archive.extractfile(skill + '/snapshot.bin').read(), b'private media fixture')

                # Test failed S3 upload
                failed = subprocess.run(['bash', str(script)], env=dict(env, FAIL_UPLOAD='1'), check=False)
                self.assertNotEqual(failed.returncode, 0)
                self.assertEqual(status.read_bytes(), previous, 'failed upload advanced success heartbeat')

                # Test failed GDrive upload
                failed_gdrive = subprocess.run(['bash', str(script)], env=dict(env, FAIL_GDRIVE_UPLOAD='1'), check=False)
                self.assertNotEqual(failed_gdrive.returncode, 0)
                self.assertEqual(status.read_bytes(), previous, 'failed gdrive upload advanced success heartbeat')

                self.assertFalse(list((root / 'backups').glob('.partial.*')), 'temporary artifacts leaked')
        finally:
            sql(url, f'DROP SCHEMA IF EXISTS {schema} CASCADE')
            subprocess.run(['dropdb', '--maintenance-db', url, '--if-exists', database], check=True)


if __name__ == '__main__':
    unittest.main()
