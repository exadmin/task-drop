"""Transcribe ordered PCM WAV files with GigaAM, without FFmpeg or Hugging Face."""

from __future__ import annotations

import argparse
from array import array
from contextlib import redirect_stdout
from dataclasses import dataclass
import json
import logging
import math
from pathlib import Path
import sys
import tempfile
from types import MethodType
from typing import Iterator, Sequence
import wave

SAMPLE_RATE = 16_000
MAX_SAMPLES = 25 * SAMPLE_RATE
LOGGER = logging.getLogger(__name__)
ASR_MODELS = (
    "v1_ctc", "v1_rnnt", "v2_ctc", "v2_rnnt", "v3_ctc", "v3_rnnt",
    "v3_e2e_ctc", "v3_e2e_rnnt", "multilingual_ctc", "multilingual_large_ctc",
)


@dataclass(frozen=True)
class SplitOptions:
    max_seconds: float = 25.0
    silence_db: float = -45.0
    min_pause_ms: int = 300
    window_ms: int = 20

    def __post_init__(self) -> None:
        if not math.isfinite(self.max_seconds) or not 1 <= self.max_seconds <= 25:
            raise ValueError("max_seconds must be between 1 and 25")
        if not math.isfinite(self.silence_db) or not -100 <= self.silence_db < 0:
            raise ValueError("silence_db must be between -100 and 0 (exclusive)")
        if not 1 <= self.window_ms <= 100 or self.min_pause_ms < self.window_ms:
            raise ValueError("min_pause_ms must be at least window_ms; window_ms must be 1–100")


@dataclass(frozen=True)
class AudioChunk:
    source: Path
    index: int
    start_frame: int
    pcm: bytes
    forced_cut: bool

    @property
    def duration(self) -> float:
        return len(self.pcm) / (2 * SAMPLE_RATE)


@dataclass(frozen=True)
class Segment:
    source: str
    chunk: int
    start: float
    end: float
    text: str
    forced_cut: bool


@dataclass(frozen=True)
class Transcription:
    text: str
    segments: list[Segment]


def validate_wav(reader: wave.Wave_read, path: Path) -> None:
    if (reader.getnchannels(), reader.getsampwidth(), reader.getframerate(), reader.getcomptype()) != (
        1, 2, SAMPLE_RATE, "NONE"
    ):
        raise ValueError(f"{path}: expected mono PCM 16-bit WAV at 16000 Hz; use the updated Go recorder")


def pcm_samples(data: bytes) -> array:
    samples = array("h")
    samples.frombytes(data)
    if sys.byteorder != "little":
        samples.byteswap()
    return samples


def pause_cut(data: bytes, options: SplitOptions) -> int | None:
    """Find the latest sufficiently long quiet interval using short-window RMS."""
    samples = pcm_samples(data)
    step = options.window_ms * SAMPLE_RATE // 1000
    minimum = options.min_pause_ms * SAMPLE_RATE // 1000
    threshold_squared = (32768 * 10 ** (options.silence_db / 20)) ** 2
    quiet_start: int | None = None
    latest: int | None = None
    for start in range(0, len(samples), step):
        end = min(start + step, len(samples))
        quiet = sum(int(value) ** 2 for value in samples[start:end]) / (end - start) <= threshold_squared
        if quiet:
            if quiet_start is None:
                quiet_start = start
            if end - quiet_start >= minimum:
                candidate = (quiet_start + end) // 2
                # Avoid repeatedly cutting the same pause into tiny fragments.
                if candidate >= SAMPLE_RATE:
                    latest = candidate
        else:
            quiet_start = None
    return latest


def iter_chunks(path: str | Path, options: SplitOptions | None = None) -> Iterator[AudioChunk]:
    """Stream a WAV with bounded memory; preserve every sample and enforce the duration limit."""
    options = options or SplitOptions()
    source = Path(path).expanduser().resolve()
    limit = int(options.max_seconds * SAMPLE_RATE)
    with wave.open(str(source), "rb") as reader:
        validate_wav(reader, source)
        if reader.getnframes() == 0:
            raise ValueError(f"{source}: WAV contains no audio frames")
        remaining = reader.getnframes()
        buffer = b""
        position = 0
        index = 0
        while remaining or buffer:
            need = min(remaining, limit + 1 - len(buffer) // 2)
            if need:
                part = reader.readframes(need)
                if len(part) != need * 2:
                    raise ValueError(f"{source}: WAV data is truncated")
                buffer += part
                remaining -= need
            forced = False
            frames = len(buffer) // 2
            if frames > limit:
                cut = pause_cut(buffer[:limit * 2], options)
                forced = cut is None
                frames = cut if cut is not None else limit
            pcm, buffer = buffer[:frames * 2], buffer[frames * 2:]
            yield AudioChunk(source, index, position, pcm, forced)
            position += frames
            index += 1


def write_chunk(path: Path, pcm: bytes) -> None:
    with wave.open(str(path), "wb") as writer:
        writer.setnchannels(1)
        writer.setsampwidth(2)
        writer.setframerate(SAMPLE_RATE)
        writer.writeframes(pcm)


def prepare_pcm_wav(model: object, wav_file: str):
    """Instance-level replacement for GigaAM's FFmpeg-based prepare_wav method."""
    import torch

    with wave.open(wav_file, "rb") as reader:
        validate_wav(reader, Path(wav_file))
        frames = reader.getnframes()
        if not 0 < frames <= MAX_SAMPLES:
            raise ValueError("GigaAM chunks must contain 1–400000 samples")
        raw = reader.readframes(frames)
        if len(raw) != frames * 2:
            raise ValueError("WAV data is truncated")
    if sys.byteorder != "little":
        raw = pcm_samples(raw).tobytes()
    parameter = next(model.parameters())
    waveform = torch.frombuffer(bytearray(raw), dtype=torch.int16).float() / 32768.0
    waveform = waveform.to(device=parameter.device, dtype=parameter.dtype).unsqueeze(0)
    length = torch.tensor([frames], device=parameter.device, dtype=torch.long)
    return waveform, length


class Transcriber:
    """Own one lazily loaded model. Calls are sequential; create a separate instance per worker."""

    def __init__(
        self, model_name: str = "v3_e2e_rnnt", device: str = "auto",
        cache_directory: str | Path | None = None, options: SplitOptions | None = None,
    ) -> None:
        if model_name not in ASR_MODELS:
            raise ValueError(f"unsupported ASR model: {model_name}")
        if device not in ("auto", "cpu", "cuda"):
            raise ValueError("device must be auto, cpu, or cuda")
        self.model_name = model_name
        self.device = device
        self.cache_directory = str(Path(cache_directory).expanduser().resolve()) if cache_directory else None
        self.options = options or SplitOptions()
        self._model = None

    def _load_model(self):
        if self._model is None:
            try:
                import gigaam
                import torch
            except ImportError as exc:
                raise RuntimeError("Install the dependencies from requirements.txt before transcription") from exc
            device = ("cuda" if torch.cuda.is_available() else "cpu") if self.device == "auto" else self.device
            if device == "cuda" and not torch.cuda.is_available():
                raise RuntimeError("CUDA is unavailable; use --device cpu or install a compatible PyTorch CUDA build")
            LOGGER.info("Loading %s on %s", self.model_name, device)
            model = gigaam.load_model(
                self.model_name, device=device, fp16_encoder=device == "cuda", use_flash=False,
                download_root=self.cache_directory,
            )
            if not callable(getattr(model, "prepare_wav", None)) or not callable(getattr(model, "transcribe", None)):
                raise RuntimeError("This GigaAM version does not support the PCM adapter")
            # Only this owned instance changes; library globals and other models remain untouched.
            model.prepare_wav = MethodType(prepare_pcm_wav, model)
            self._model = model
        return self._model

    def transcribe_files(
        self, wav_files: Sequence[str | Path], chunks_directory: str | Path | None = None,
    ) -> Transcription:
        if isinstance(wav_files, (str, bytes, Path)) or not wav_files:
            raise ValueError("wav_files must be a nonempty ordered list of WAV paths")
        paths = [Path(path).expanduser().resolve() for path in wav_files]
        # Validate every header before model downloads or inference begins.
        for path in paths:
            with wave.open(str(path), "rb") as reader:
                validate_wav(reader, path)
                if not reader.getnframes():
                    raise ValueError(f"{path}: WAV contains no audio frames")
        segments: list[Segment] = []
        if chunks_directory is not None:
            Path(chunks_directory).mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix="gigaam-pcm-") as temporary:
            temp_path = Path(temporary) / "chunk.wav"
            for file_index, path in enumerate(paths):
                for chunk in iter_chunks(path, self.options):
                    if chunk.forced_cut:
                        LOGGER.warning("%s: no pause before %.2fs; cutting continuous speech", path.name,
                                       (chunk.start_frame + len(chunk.pcm) // 2) / SAMPLE_RATE)
                    chunk_path = temp_path
                    if chunks_directory is not None:
                        chunk_path = Path(chunks_directory) / f"{file_index + 1:04d}-{chunk.index + 1:06d}.wav"
                        if chunk_path.exists():
                            raise FileExistsError(f"chunk already exists: {chunk_path}; choose an empty directory")
                    write_chunk(chunk_path, chunk.pcm)
                    # Pure digital silence needs no model and produces no invented transcript.
                    text = ""
                    if any(chunk.pcm):
                        result = self._load_model().transcribe(str(chunk_path))
                        text = result.text.strip() if hasattr(result, "text") else str(result).strip()
                    segments.append(Segment(str(path), chunk.index, chunk.start_frame / SAMPLE_RATE,
                                            chunk.start_frame / SAMPLE_RATE + chunk.duration, text, chunk.forced_cut))
        return Transcription(" ".join(segment.text for segment in segments if segment.text), segments)


def transcribe_files(wav_files: Sequence[str | Path], **kwargs) -> str:
    """Convenience API. Use Transcriber to retain the model across multiple requests."""
    return Transcriber(**kwargs).transcribe_files(wav_files).text


def main(argv: Sequence[str] | None = None) -> int:
    arguments = list(sys.argv[1:] if argv is None else argv)
    if "--server" in arguments:
        from service import main as service_main
        arguments.remove("--server")
        return service_main(arguments)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("wav_files", nargs="+", type=Path, help="WAV files in transcription order")
    parser.add_argument("--model", choices=ASR_MODELS, default="v3_e2e_rnnt")
    parser.add_argument("--device", choices=("auto", "cpu", "cuda"), default="auto")
    parser.add_argument("--cache-directory", type=Path)
    parser.add_argument("--max-seconds", type=float, default=25)
    parser.add_argument("--silence-db", type=float, default=-45)
    parser.add_argument("--min-pause-ms", type=int, default=300)
    parser.add_argument("--chunks-directory", type=Path, help="Keep the generated WAV chunks")
    parser.add_argument("--output", type=Path, help="Write the combined text as UTF-8")
    parser.add_argument("--json-output", type=Path, help="Write text and per-source segment timestamps")
    args = parser.parse_args(argv)
    logging.basicConfig(level=logging.INFO, stream=sys.stderr, format="%(levelname)s: %(message)s")
    try:
        inputs = {path.expanduser().resolve() for path in args.wav_files}
        outputs = [path.expanduser().resolve() for path in (args.output, args.json_output) if path is not None]
        if any(path in inputs for path in outputs):
            raise ValueError("output paths must differ from the input WAV files")
        if len(set(outputs)) != len(outputs):
            raise ValueError("text and JSON output paths must differ")
        options = SplitOptions(args.max_seconds, args.silence_db, args.min_pause_ms)
        transcriber = Transcriber(args.model, args.device, args.cache_directory, options)
        # Some upstream decoders print progress; stdout stays usable as plain text.
        with redirect_stdout(sys.stderr):
            result = transcriber.transcribe_files(args.wav_files, args.chunks_directory)
        if args.output:
            args.output.write_text(result.text + "\n", encoding="utf-8")
        if args.json_output:
            from dataclasses import asdict
            args.json_output.write_text(json.dumps(asdict(result), ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        if hasattr(sys.stdout, "reconfigure"):
            sys.stdout.reconfigure(encoding="utf-8")
        print(result.text)
        return 0
    except (OSError, ValueError, RuntimeError, wave.Error, EOFError, ImportError) as exc:
        LOGGER.error("%s", exc)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
