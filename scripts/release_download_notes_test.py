"""Offline regression coverage for fork release download tables."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("release-download-notes.py")
SPEC = importlib.util.spec_from_file_location("release_download_notes", SCRIPT)
notes = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(notes)


class ReleaseDownloadNotesTest(unittest.TestCase):
    def test_fork_table_uses_installer_assets_and_its_own_docker_repository(self):
        tag = "v1.0.0-rc.3-fork.1"
        names = [
            "axonhub-desktop-1.0.0-rc.3-fork.1-arm64.dmg",
            "axonhub-desktop-setup-1.0.0-rc.3-fork.1-amd64.exe",
            "axonhub-desktop_1.0.0-rc.3-fork.1_arm64.deb",
            "axonhub_1.0.0-rc.3-fork.1_linux_amd64.zip",
            "axonhub-desktop-1.0.0-rc.3-fork.1-arm64.tar.gz",
            "axonhub-desktop-1.0.0-rc.3-fork.1-arm64.delta",
            "checksums.txt",
        ]
        section = notes.download_section("shay-wong/axonhub", tag, names, "shayos/axonhub")

        for name in names[:4]:
            self.assertIn(f"/releases/download/{tag}/{name}", section)
        for name in names[4:]:
            self.assertNotIn(f"/releases/download/{tag}/{name}", section)
        self.assertIn("https://hub.docker.com/r/shayos/axonhub", section)
        self.assertNotIn("looplj/", section)
        self.assertNotIn("Homebrew", section)
        self.assertNotIn("ghcr.io", section)

    def test_docker_repository_matches_publishing_defaults_and_validation(self):
        self.assertEqual(notes.dockerhub_repository("example/fork"), "example/axonhub")
        self.assertEqual(notes.dockerhub_repository("example/fork", username="publisher"), "publisher/axonhub")
        self.assertEqual(notes.dockerhub_repository("example/fork", "docker.io/Publisher/Custom/"), "publisher/custom")
        self.assertEqual(notes.dockerhub_repository("example/fork", "Publisher"), "publisher/axonhub")
        self.assertEqual(notes.dockerhub_repository("example/fork", "docker.io/"), "example/axonhub")
        for value in ("publisher/image:latest", "publisher/image with spaces", "https://hub.docker.com/r/publisher/image"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                notes.dockerhub_repository("example/fork", value)

    def test_upstream_keeps_its_homebrew_link_and_replacement_preserves_notes(self):
        section = notes.download_section("looplj/axonhub", "v1.0.0", [])
        self.assertIn("https://github.com/looplj/homebrew-axonhub", section)
        original = f"Original release notes\n\n{notes.START}\nold table\n{notes.END}\n\nMigration notes\n"
        rendered = notes.splice(original, section)
        self.assertTrue(rendered.startswith("Original release notes\n\n"))
        self.assertTrue(rendered.endswith("Migration notes\n"))
        self.assertNotIn("old table", rendered)
        self.assertEqual(rendered.count(notes.START), 1)
        self.assertEqual(notes.splice(rendered, section), rendered)

    def test_dry_run_reads_real_asset_json_without_calling_github(self):
        with tempfile.TemporaryDirectory() as directory:
            fixture = Path(directory) / "release.json"
            fixture.write_text(json.dumps({
                "body": "Release notes stay here.",
                "assets": [{"name": "axonhub_1.0.0-rc.3-fork.1_linux_amd64.zip"}],
            }))
            env = dict(os.environ, DOCKERHUB_IMAGE="docker.io/Actual/Image/", DOCKERHUB_USERNAME="unused")
            completed = subprocess.run(
                [sys.executable, str(SCRIPT), "--repo", "example/fork", "--tag", "v1.0.0-rc.3-fork.1", "--assets-file", str(fixture)],
                env=env,
                capture_output=True,
                text=True,
                check=True,
            )
        self.assertIn("https://hub.docker.com/r/actual/image", completed.stdout)
        self.assertIn("Release notes stay here.", completed.stdout)
        self.assertIn("axonhub_1.0.0-rc.3-fork.1_linux_amd64.zip", completed.stdout)
        self.assertNotIn("homebrew-axonhub", completed.stdout)


if __name__ == "__main__":
    unittest.main()
