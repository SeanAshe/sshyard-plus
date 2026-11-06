#!/usr/bin/env python3
"""fork-version.sh appends commits ahead of upstream and leaves mygo.json alone."""

from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parent / "fork-version.sh"
CONFIG = '{\n  "name": "Release test",\n  "version": "1.2.3"\n}\n'


class ForkVersionTest(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        self.root = Path(work.name)
        self.upstream = self.root / "upstream"
        self.repo = self.root / "repo"
        self.upstream.mkdir()
        self.git(self.upstream, "init", "-b", "main")
        self.configure(self.upstream)
        (self.upstream / "README").write_text("upstream\n", encoding="utf-8")
        self.git(self.upstream, "add", ".")
        self.git(self.upstream, "commit", "-m", "upstream")
        self.git(self.repo.parent, "clone", str(self.upstream), self.repo.name)
        self.configure(self.repo)
        script = self.repo / ".github" / "fork-version.sh"
        script.parent.mkdir()
        script.write_bytes(SCRIPT.read_bytes())
        (self.repo / "mygo.json").write_text(CONFIG, encoding="utf-8")
        self.git(self.repo, "add", "mygo.json")
        self.git(self.repo, "commit", "-m", "fork config")
        (self.repo / "change.txt").write_text("ours\n", encoding="utf-8")
        self.git(self.repo, "add", "change.txt")
        self.git(self.repo, "commit", "-m", "fork change")

    def configure(self, repo):
        for key, value in {
            "user.name": "Release test",
            "user.email": "release@example.invalid",
            "commit.gpgsign": "false",
            "tag.gpgsign": "false",
            "core.hooksPath": str(self.root / "no-hooks"),
        }.items():
            self.git(repo, "config", key, value)

    def git(self, repo, *args):
        return subprocess.check_output(["git", *args], cwd=repo, text=True, encoding="utf-8", errors="replace").strip()

    def version(self):
        result = subprocess.run(
            ["bash", ".github/fork-version.sh", self.upstream.as_posix()],
            cwd=self.repo,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.strip()

    def test_ahead_count_leaves_version_file(self):
        self.assertEqual(self.version(), "1.2.3_2")
        self.assertEqual((self.repo / "mygo.json").read_text(encoding="utf-8"), CONFIG)
        (self.upstream / "later.txt").write_text("later\n", encoding="utf-8")
        self.git(self.upstream, "add", "later.txt")
        self.git(self.upstream, "commit", "-m", "upstream later")
        self.assertEqual(self.version(), "1.2.3_2")


if __name__ == "__main__":
    unittest.main()
