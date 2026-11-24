import os
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name('install-production-bundle.py')


class UploadContract(unittest.TestCase):
    def run_upload(self, home, stage):
        return subprocess.run(['python3', str(SCRIPT), str(stage)],
                              env={**os.environ, 'HOME': str(home)}, capture_output=True)

    def test_guard_precedes_write_and_preserves_mount_inode(self):
        with tempfile.TemporaryDirectory() as temp:
            home = pathlib.Path(temp)
            live = home / 'ielts-api'
            live.mkdir()
            target = live / 'config'
            target.write_text('old')
            inode = target.stat().st_ino
            stage = home / 'stage'
            stage.mkdir()
            (stage / 'config').write_text('raw')
            guard = home / 'drone-league/monitoring'
            guard.mkdir(parents=True)
            (guard / 'require-ielts-integration').touch()
            (guard / 'redeploy_guard.py').write_text(
                "import pathlib, sys; (pathlib.Path(sys.argv[3])/'config').write_text('patched')")
            result = self.run_upload(home, stage)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(target.read_text(), 'patched')
            self.assertEqual(target.stat().st_ino, inode)
            snapshots = list((home / 'ielts-deployment-backups').glob('*/config'))
            self.assertEqual(snapshots[0].read_text(), 'old')
            self.assertEqual(snapshots[0].stat().st_mode & 0o777, 0o600)

    def test_missing_guard_leaves_live_unchanged(self):
        with tempfile.TemporaryDirectory() as temp:
            home = pathlib.Path(temp)
            live = home / 'ielts-api'
            live.mkdir()
            (live / 'config').write_text('old')
            stage = home / 'stage'
            stage.mkdir()
            (stage / 'config').write_text('raw')
            guard = home / 'drone-league/monitoring'
            guard.mkdir(parents=True)
            (guard / 'require-ielts-integration').touch()
            self.assertNotEqual(self.run_upload(home, stage).returncode, 0)
            self.assertEqual((live / 'config').read_text(), 'old')

    def test_symlink_escape_is_rejected_before_copy(self):
        with tempfile.TemporaryDirectory() as temp:
            home = pathlib.Path(temp)
            stage = home / 'stage'
            stage.mkdir()
            (stage / 'config').write_text('raw')
            (stage / 'escape').symlink_to(home / 'outside')
            self.assertNotEqual(self.run_upload(home, stage).returncode, 0)
            self.assertFalse((home / 'ielts-api/config').exists())
