"""Serve newline-delimited JSON requests with one resident GigaAM model."""

import argparse
from contextlib import redirect_stdout
import json
import logging
from pathlib import Path
import sys

from transcribe import ASR_MODELS, Transcriber


def serve(transcriber, source, output):
    def send(message):
        output.write(json.dumps(message, ensure_ascii=False) + "\n")
        output.flush()

    with redirect_stdout(sys.stderr):
        transcriber._load_model()
    send({"type": "ready"})
    for line in source:
        identifier = None
        try:
            request = json.loads(line)
            if not isinstance(request, dict):
                raise ValueError("request must be a JSON object")
            identifier = request.get("id")
            if not isinstance(identifier, str):
                raise ValueError("id must be a string")
            paths = request.get("wav_files")
            if not isinstance(paths, list) or not paths or not all(isinstance(path, str) for path in paths):
                raise ValueError("wav_files must be a nonempty list of paths")
            with redirect_stdout(sys.stderr):
                result = transcriber.transcribe_files(paths)
            send({"type": "result", "id": identifier, "text": result.text})
        except Exception as exc:
            logging.exception("Transcription request failed")
            send({"type": "error", "id": identifier, "error": str(exc)})


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", choices=ASR_MODELS, default="v3_e2e_rnnt")
    parser.add_argument("--device", choices=("auto", "cpu", "cuda"), default="auto")
    parser.add_argument("--cache-directory", type=Path)
    args = parser.parse_args(argv)
    logging.basicConfig(level=logging.INFO, stream=sys.stderr, format="%(levelname)s: %(message)s")
    if hasattr(sys.stdin, "reconfigure"):
        sys.stdin.reconfigure(encoding="utf-8")
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    try:
        serve(Transcriber(args.model, args.device, args.cache_directory), sys.stdin, sys.stdout)
        return 0
    except Exception as exc:
        logging.exception("Service startup failed: %s", exc)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
