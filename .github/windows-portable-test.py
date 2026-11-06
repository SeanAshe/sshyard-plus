#!/usr/bin/env python3
"""Focused checks for the portable ZIP and its release download link."""

import importlib.util
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import zipfile


HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("portable", HERE / "windows-portable.py")
portable = importlib.util.module_from_spec(spec)
spec.loader.exec_module(portable)


class WindowsPortableTest(unittest.TestCase):
    def test_archive_contains_runtime_and_resources_only(self):
        with tempfile.TemporaryDirectory() as work:
            stage = Path(work)
            files = {
                "SSHYard.exe": b"executable fixture",
                "runtime.dll": b"runtime fixture",
                "icon.png": b"icon fixture",
                "licenses/NOTICE": b"license fixture",
                "resources/.hidden": b"hidden resource fixture",
                "resources/install.ps1": b"nested resource fixture",
                "empty/": None,
            }
            for name, data in files.items():
                path = stage / name
                path.parent.mkdir(parents=True, exist_ok=True)
                if data is None:
                    path.mkdir()
                else:
                    path.write_bytes(data)
            for name in (
                "sshyard-setup-0.0.4-amd64.exe",
                "sshyard-0.0.4-windows-amd64.tar.gz",
                "sshyard-0.0.3-to-0.0.4-windows-amd64.delta",
                "sshyard-0.0.4-windows-amd64-portable.zip",
                "update-windows-amd64.json",
                "install.ps1",
            ):
                (stage / name).write_bytes(b"excluded build artifact")
            output = portable.package(stage, "0.0.4", "amd64")
            self.assertEqual(output.name, "sshyard-0.0.4-windows-amd64-portable.zip")
            with zipfile.ZipFile(output) as archive:
                self.assertIsNone(archive.testzip())
                self.assertEqual(
                    {name for name in archive.namelist() if not name.endswith("/")},
                    {name for name, data in files.items() if data is not None},
                )
                self.assertIn("empty/", archive.namelist())
                for name, data in files.items():
                    if data is not None:
                        self.assertEqual(archive.read(name), data)

    def test_unsigned_beta_and_missing_executable(self):
        with tempfile.TemporaryDirectory() as work:
            stage = Path(work)
            with self.assertRaises(FileNotFoundError):
                portable.package(stage, "0.0.4", "amd64")
            (stage / "SSHYard.exe").write_bytes(b"executable fixture")
            output = portable.package(stage, "0.0.4-beta-20261104120000", "amd64")
            self.assertIn("0.0.4-beta-20261104120000", output.name)
            with zipfile.ZipFile(output) as archive:
                self.assertEqual(archive.namelist(), ["SSHYard.exe"])

    def test_arm64_archive_skips_its_own_artifacts(self):
        with tempfile.TemporaryDirectory() as work:
            stage = Path(work)
            (stage / "SSHYard.exe").write_bytes(b"executable fixture")
            for name in (
                "sshyard-setup-0.0.4-arm64.exe",
                "sshyard-0.0.4-windows-arm64.tar.gz",
                "sshyard-0.0.3-to-0.0.4-windows-arm64.delta",
                "update-windows-arm64.json",
            ):
                (stage / name).write_bytes(b"excluded build artifact")
            output = portable.package(stage, "0.0.4", "arm64")
            self.assertEqual(output.name, "sshyard-0.0.4-windows-arm64-portable.zip")
            with zipfile.ZipFile(output) as archive:
                self.assertEqual(archive.namelist(), ["SSHYard.exe"])

    def test_download_link_requires_portable_asset(self):
        # Resolve Git Bash from PATH instead of Windows' System32 WSL launcher.
        bash = shutil.which("bash")
        self.assertIsNotNone(bash, "Bash executable not found in PATH")
        with tempfile.TemporaryDirectory() as work:
            dist = Path(work)
            for name in (
                "sshyard-0.0.4-arm64.dmg", "sshyard-0.0.4-amd64.dmg",
                "sshyard-setup-0.0.4-amd64.exe", "sshyard_0.0.4_amd64.deb",
                "sshyard-0.0.4-linux-amd64.tar.gz", "sshyard-setup-0.0.4-arm64.exe",
                "sshyard-0.0.4-windows-arm64-portable.zip", "sshyard_0.0.4_arm64.deb",
                "sshyard-0.0.4-linux-arm64.tar.gz", "install.sh", "SHA256SUMS",
            ):
                (dist / name).touch()
            command = [bash, (HERE / "release-download-notes.sh").as_posix(),
                       dist.as_posix(), "v0.0.4", "Licoy/sshyard"]
            result = subprocess.run(command, capture_output=True, encoding="utf-8")
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("expected one asset matching", result.stderr, repr(result))
            name = "sshyard-0.0.4-windows-amd64-portable.zip"
            (dist / name).touch()
            result = subprocess.run(command, capture_output=True, encoding="utf-8", check=True)
            self.assertIn(
                f"[Portable ZIP](https://github.com/Licoy/sshyard/releases/download/v0.0.4/{name})",
                result.stdout,
            )
            self.assertIn(
                "| Arm64 | [Installer](https://github.com/Licoy/sshyard/releases/download/v0.0.4/sshyard-setup-0.0.4-arm64.exe)"
                " · [Portable ZIP](https://github.com/Licoy/sshyard/releases/download/v0.0.4/sshyard-0.0.4-windows-arm64-portable.zip) |",
                result.stdout,
            )
            self.assertNotIn("macOS", result.stdout)
            self.assertNotIn("Linux", result.stdout)
            (dist / "sshyard-0.0.5-windows-amd64-portable.zip").touch()
            result = subprocess.run(command, capture_output=True, encoding="utf-8")
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("found 2", result.stderr)


if __name__ == "__main__":
    unittest.main()
