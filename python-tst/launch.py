"""Launch the module through its local virtual environment, without shell quoting."""

from pathlib import Path
import subprocess
import sys


def main() -> int:
    root = Path(__file__).resolve().parent
    python = root / ".venv" / ("Scripts/python.exe" if sys.platform == "win32" else "bin/python")
    flags = subprocess.CREATE_NO_WINDOW if sys.platform == "win32" else 0
    try:
        if not python.is_file():
            print("Creating Python environment...", file=sys.stderr)
            subprocess.run([sys.executable, "-m", "venv", str(root / ".venv")], check=True, creationflags=flags,
                           stdin=subprocess.DEVNULL, stdout=sys.stderr, stderr=sys.stderr)
        return subprocess.run([str(python), str(root / "bootstrap.py"), *sys.argv[1:]], creationflags=flags,
                              stdin=subprocess.DEVNULL, stdout=sys.stdout, stderr=sys.stderr).returncode
    except (OSError, subprocess.CalledProcessError) as exc:
        print(f"Python launch failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
