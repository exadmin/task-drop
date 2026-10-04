"""Set up the local environment once, then run transcription with the same interpreter."""

from __future__ import annotations

import hashlib
from importlib import metadata, util
import json
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent
REQUIREMENTS = ROOT / "requirements.txt"
MARKER = Path(sys.prefix) / ".push2talk-setup.json"
REQUIRED_PACKAGES = ("gigaam", "torch", "torchaudio")


def fingerprint() -> dict[str, str]:
    return {
        "requirements_sha256": hashlib.sha256(REQUIREMENTS.read_bytes()).hexdigest(),
        "python_version": sys.version,
    }


def packages_present() -> bool:
    for package in REQUIRED_PACKAGES:
        try:
            metadata.version(package)
            if util.find_spec(package) is None:
                return False
        except (metadata.PackageNotFoundError, ImportError, ValueError):
            return False
    return True


def ensure_dependencies() -> None:
    if sys.prefix == sys.base_prefix:
        raise RuntimeError("Run run.cmd so dependencies are installed in .venv")
    expected = fingerprint()
    try:
        previous = json.loads(MARKER.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        previous = None
    if previous == expected and packages_present():
        return

    # Invalidate the marker before installing so a failed installation is retried.
    MARKER.unlink(missing_ok=True)
    print("Installing dependencies for this environment...", file=sys.stderr)
    child_options = dict(stdout=sys.stderr, stderr=sys.stderr, stdin=subprocess.DEVNULL,
                         creationflags=subprocess.CREATE_NO_WINDOW if sys.platform == "win32" else 0)
    subprocess.run(
        [sys.executable, "-m", "pip", "install", "-r", str(REQUIREMENTS)],
        **child_options, check=True,
    )
    subprocess.run([sys.executable, "-m", "pip", "check"], **child_options, check=True)
    if not packages_present():
        raise RuntimeError("Installation finished, but GigaAM, PyTorch, or Torchaudio is missing")
    # Verify native libraries before marking this environment as ready.
    subprocess.run(
        [sys.executable, "-c", "import gigaam, torch, torchaudio"],
        **child_options, check=True,
    )
    temporary = MARKER.with_suffix(".tmp")
    temporary.write_text(json.dumps(expected, indent=2) + "\n", encoding="utf-8")
    temporary.replace(MARKER)


def main() -> int:
    if sys.version_info < (3, 10):
        print("Python 3.10 or later is required.", file=sys.stderr)
        return 1
    try:
        # Help and missing arguments need no model dependencies or installation.
        if sys.argv[1:] and not any(arg in ("--help", "-h") for arg in sys.argv[1:]):
            ensure_dependencies()
        from transcribe import main as transcribe_main
        return transcribe_main(sys.argv[1:])
    except (OSError, RuntimeError, subprocess.CalledProcessError) as exc:
        print(f"Setup failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
