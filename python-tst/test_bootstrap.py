from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import bootstrap


class SetupTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        root = Path(directory.name)
        self.requirements = root / "requirements.txt"
        self.requirements.write_text("example==1\n", encoding="utf-8")
        self.marker = root / "setup.json"
        for mock in (
            patch.object(bootstrap, "REQUIREMENTS", self.requirements),
            patch.object(bootstrap, "MARKER", self.marker),
            patch.object(bootstrap.sys, "prefix", str(root)),
        ):
            mock.start()
            self.addCleanup(mock.stop)

    def test_first_setup_and_repeat_without_install(self):
        with patch.object(bootstrap, "packages_present", return_value=True), \
                patch.object(bootstrap.subprocess, "run") as run:
            bootstrap.ensure_dependencies()
            self.assertEqual(run.call_count, 3)
            self.assertTrue(self.marker.exists())
            run.reset_mock()
            bootstrap.ensure_dependencies()
            run.assert_not_called()

    def test_requirements_change_installs_again(self):
        with patch.object(bootstrap, "packages_present", return_value=True), \
                patch.object(bootstrap.subprocess, "run") as run:
            bootstrap.ensure_dependencies()
            self.requirements.write_text("example==2\n", encoding="utf-8")
            run.reset_mock()
            bootstrap.ensure_dependencies()
            self.assertEqual(run.call_count, 3)

    def test_failed_install_has_no_success_marker(self):
        with patch.object(bootstrap, "packages_present", return_value=True), \
                patch.object(bootstrap.subprocess, "run"):
            bootstrap.ensure_dependencies()
        self.requirements.write_text("changed\n", encoding="utf-8")
        with patch.object(bootstrap.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "pip")):
            with self.assertRaises(subprocess.CalledProcessError):
                bootstrap.ensure_dependencies()
        self.assertFalse(self.marker.exists())

    def test_missing_package_rechecks_installation(self):
        with patch.object(bootstrap, "packages_present", return_value=True), \
                patch.object(bootstrap.subprocess, "run"):
            bootstrap.ensure_dependencies()
        with patch.object(bootstrap, "packages_present", side_effect=[False, True]), \
                patch.object(bootstrap.subprocess, "run") as run:
            bootstrap.ensure_dependencies()
            self.assertEqual(run.call_count, 3)


if __name__ == "__main__":
    unittest.main()
