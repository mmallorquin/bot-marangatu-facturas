import hashlib
import io
import tarfile
import sqlite3
import tempfile
import subprocess
import sys
import unittest
from pathlib import Path
from contextlib import closing

import oracle_deploy as deploy


def package(files, links=None):
    data = io.BytesIO()
    with tarfile.open(fileobj=data, mode="w:") as archive:
        for name, content in files.items():
            member = tarfile.TarInfo(name)
            member.size = len(content)
            archive.addfile(member, io.BytesIO(content))
        for name, target in (links or {}).items():
            member = tarfile.TarInfo(name)
            member.type = tarfile.SYMTYPE
            member.linkname = target
            archive.addfile(member)
    return data.getvalue()


def release_files():
    elf = b"\x7fELF\x02\x01" + bytes(12) + b"\x3e\x00" + b"test binary"
    files = {"bot": elf, "metricas": elf, "REVISION": b"a" * 40 + b"\n"}
    files["SHA256SUMS"] = "".join(
        hashlib.sha256(content).hexdigest() + "  " + name + "\n"
        for name, content in files.items()
    ).encode()
    return files


class PackageTests(unittest.TestCase):
    def test_cli_apply_checks_package_without_undefined_functions(self):
        script = Path(deploy.__file__).resolve()
        probe = "import os,runpy,sys; os.geteuid=lambda:0; sys.argv=[sys.argv[1],'--apply']; runpy.run_path(sys.argv[0],run_name='__main__')"
        result = subprocess.run([sys.executable, "-c", probe, str(script)], input=b"not a tar", capture_output=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn(b"paquete tar", result.stderr)
        self.assertNotIn(b"not defined", result.stderr)

    def test_valid_release_returns_checked_binaries_and_revision(self):
        files = release_files()
        self.assertEqual(deploy.validate_package(package(files)), files)

    def test_rejects_paths_outside_the_release(self):
        files = release_files()
        files["../../etc/sudoers"] = b"unauthorized"
        with self.assertRaises(ValueError):
            deploy.validate_package(package(files))

    def test_rejects_symlinks_even_with_allowed_name(self):
        files = release_files()
        del files["bot"]
        with self.assertRaises(ValueError):
            deploy.validate_package(package(files, {"bot": "/etc/shadow"}))

    def test_rejects_binary_modified_after_manifest(self):
        files = release_files()
        files["bot"] += b"changed"
        with self.assertRaises(ValueError):
            deploy.validate_package(package(files))

    def test_rejects_invalid_revision_even_with_matching_checksum(self):
        files = release_files()
        files["REVISION"] = b"../../untrusted\n"
        files["SHA256SUMS"] = "".join(
            hashlib.sha256(files[name]).hexdigest() + "  " + name + "\n"
            for name in ("bot", "metricas", "REVISION")
        ).encode()
        with self.assertRaises(ValueError):
            deploy.validate_package(package(files))


class LocalService:
    def __init__(self, target, reject_new=False):
        self.target = target
        self.reject_new = reject_new
        self.active = True

    def stop(self):
        self.active = False

    def start(self):
        self.active = True

    def health(self, expected_hash):
        content = (self.target / "bot").read_bytes()
        if not self.active or (self.reject_new and content != b"old bot"):
            raise RuntimeError("service failed to initialize")
        if hashlib.sha256(content).hexdigest() != expected_hash:
            raise RuntimeError("wrong executable")


class DeployTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        base = Path(self.directory.name)
        self.paths = deploy.Paths(base / "bin", base / "facturas.db", base / "backups", base / "deploy.lock")
        self.paths.target.mkdir()
        self.old = {"bot": b"old bot", "metricas": b"old metrics", "REVISION": b"b" * 40 + b"\n"}
        for name, content in self.old.items():
            (self.paths.target / name).write_bytes(content)
        with closing(sqlite3.connect(self.paths.database)) as connection:
            connection.execute("CREATE TABLE invoices (amount INTEGER)")
            connection.execute("INSERT INTO invoices VALUES (123)")
            connection.commit()

    def test_deploy_keeps_database_and_creates_readable_consistent_backup(self):
        service = LocalService(self.paths.target)
        files = release_files()
        deploy.deploy_release(package(files), self.paths, service)
        self.assertEqual((self.paths.target / "REVISION").read_bytes(), b"a" * 40 + b"\n")
        self.assertEqual((self.paths.target / "bot").read_bytes(), files["bot"])
        self.assertTrue(service.active)
        backups = list(self.paths.backups.glob("*/facturas.db"))
        self.assertEqual(len(backups), 1)
        for database in (backups[0], self.paths.database):
            with closing(sqlite3.connect(database)) as connection:
                self.assertEqual(connection.execute("SELECT amount FROM invoices").fetchall(), [(123,)])
                self.assertEqual(connection.execute("PRAGMA quick_check").fetchone(), ("ok",))
        self.assertEqual((backups[0].parent / "bot").read_bytes(), b"old bot")

    def test_failed_start_restores_all_previous_executables_and_revision(self):
        service = LocalService(self.paths.target, reject_new=True)
        with self.assertRaises(RuntimeError):
            deploy.deploy_release(package(release_files()), self.paths, service)
        for name, content in self.old.items():
            self.assertEqual((self.paths.target / name).read_bytes(), content)
        self.assertTrue(service.active)

    def test_invalid_package_leaves_running_service_and_files_untouched(self):
        service = LocalService(self.paths.target)
        files = release_files()
        files["bot"] += b"corrupt"
        with self.assertRaises(ValueError):
            deploy.deploy_release(package(files), self.paths, service)
        self.assertTrue(service.active)
        self.assertEqual((self.paths.target / "bot").read_bytes(), b"old bot")
        self.assertFalse(self.paths.backups.exists())

    def test_stop_failure_after_shutdown_restarts_previous_bot(self):
        class InterruptedStop(LocalService):
            interrupted = False

            def stop(self):
                super().stop()
                if not self.interrupted:
                    self.interrupted = True
                    raise RuntimeError("stop interrupted after shutdown")

        service = InterruptedStop(self.paths.target)
        with self.assertRaises(RuntimeError):
            deploy.deploy_release(package(release_files()), self.paths, service)
        self.assertTrue(service.active)
        for name, content in self.old.items():
            self.assertEqual((self.paths.target / name).read_bytes(), content)


if __name__ == "__main__":
    unittest.main()
