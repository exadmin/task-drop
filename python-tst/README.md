# GigaAM WAV transcription

Transcribe an ordered list of WAV recordings into one text with GigaAM.
Audio processing and inference run locally. FFmpeg, pyannote, and a Hugging Face token are not required.
The default Russian model is `v3_e2e_rnnt`, which supports punctuation and text normalization.
Model weights and the tokenizer download on first use and are cached by GigaAM.

## Input format

Each input must be an uncompressed **PCM 16-bit, mono, 16000 Hz WAV**.
The updated Go recorder produces this format with `output_sample_rate` set to `16000`.
Older recordings in float, stereo, or another sample rate are rejected with an explanatory error.

Files are processed in the supplied order, without sorting.
Long recordings are streamed using a buffer of approximately 25 seconds.
The splitter finds the latest pause of at least 300 ms below -45 dBFS before the duration limit.
It cuts near the pause center; when no suitable pause exists, it cuts at the limit and logs a warning.
Every sample is retained, and every generated WAV contains no more than 400000 samples, or 25 seconds.
These are level-based pause estimates: background noise and quiet speech can affect boundary selection.
There is no overlapping audio, speaker diarization, or word-boundary guarantee at forced cuts.
Pure digital silence produces empty text without loading the model.

## Install

On Windows, `run.cmd` creates `.venv`, installs dependencies when needed, and starts transcription:

```powershell
cd python-tst
.\run.cmd first.wav second.wav --output transcript.txt
```

Subsequent runs skip installation when `requirements.txt` and the Python version match the successful setup marker
and GigaAM, PyTorch, and Torchaudio are still installed.
Failed setups leave no marker, so the next run retries installation.
The marker lives at `.venv/.push2talk-setup.json`; remove that file to request another dependency setup.
The launcher preserves your current directory, so relative input and output paths resolve where you invoke it.
`launch.py` exposes the same setup for the Go application's direct Python invocation, without a command shell.
All arguments pass through to `transcribe.py`. `run.cmd --help` shows options without installing dependencies.
To set up the environment manually, use the commands below.

GigaAM requires Python 3.10 or later. Python 3.12 or 3.13 is a practical choice for a fresh environment
because the upstream package includes several binary dependencies.
The dependency is pinned to an upstream Git commit; installation requires Git and internet access.

```powershell
cd python-tst
python -m venv .venv
.\.venv\Scripts\python.exe -m pip install -r requirements.txt
```

The `torch` extra installs PyTorch and Torchaudio. For a specific CUDA build, install the matching versions using
the [PyTorch installation instructions](https://pytorch.org/get-started/locally/) before installing this module's requirements.
CPU inference is supported. `--device auto` selects CUDA when available and otherwise uses CPU.
Flash Attention is disabled, so that optional package is not required.
On Linux or macOS, use `.venv/bin/python` instead of `.venv\Scripts\python.exe`.
The module exposes CPU and CUDA modes; Apple MPS acceleration is not implemented.

## Command line

```powershell
.\.venv\Scripts\python.exe transcribe.py first.wav second.wav --output transcript.txt
```

The combined text is printed to stdout. Progress and warnings go to stderr.
You can preserve the generated WAV files and write segment metadata:

```powershell
.\.venv\Scripts\python.exe transcribe.py first.wav second.wav --device cpu --chunks-directory chunks --output transcript.txt --json-output segments.json
```

Use an empty chunk directory to avoid overwriting previous output.
Output files use UTF-8. Segment timestamps are relative to each original file, rather than a merged audio timeline.
The text is joined with spaces in source and chunk order; punctuation comes from the selected model.

| Option | Default | Meaning |
| --- | --- | --- |
| `--model` | `v3_e2e_rnnt` | GigaAM ASR model. |
| `--device` | `auto` | `auto`, `cpu`, or `cuda`. |
| `--cache-directory` | GigaAM default | Model download and cache directory. |
| `--max-seconds` | `25` | Chunk duration limit, from 1 to 25 seconds. |
| `--silence-db` | `-45` | RMS threshold in dBFS for pause detection. |
| `--min-pause-ms` | `300` | Minimum pause duration in milliseconds. |
| `--chunks-directory` | Temporary | Keep generated WAV chunks in this directory. |
| `--output` | None | Save combined text. |
| `--json-output` | None | Save combined text and segment metadata. |

## Python API

The Go application uses resident-worker mode:

```powershell
python -X utf8 -u transcribe.py --server --device cpu
```

The worker loads the model once and writes `{"type":"ready"}` to stdout when it is ready.
Send one JSON object per line on stdin:

```json
{"id":"42","wav_files":["C:\\recordings\\first.wav","C:\\recordings\\second.wav"]}
```

Responses use the same ID and contain either text or an error:

```json
{"type":"result","id":"42","text":"Recognized text"}
```

Requests execute sequentially. A bad WAV returns an error without unloading the model.
Progress and diagnostics go to stderr. Closing stdin terminates the worker after the current request.
Go saves the full result beside the recording and manages its five-second preview.
Standalone file-list CLI usage remains available.

Run from this directory or add it to your Python import path:

```python
from transcribe import Transcriber, transcribe_files

text = transcribe_files(["first.wav", "second.wav"], device="cpu")
print(text)

# Retain one model across multiple requests.
transcriber = Transcriber(device="auto")
result = transcriber.transcribe_files(["first.wav", "second.wav"])
print(result.text)
for segment in result.segments:
    print(segment.source, segment.start, segment.end, segment.text)
```

`Transcriber` owns a model loaded on demand. Its calls are sequential; use a separate instance per worker.
To avoid the upstream FFmpeg loader, the adapter replaces `prepare_wav` on that owned model instance.
It reads PCM samples with Python's `wave` module and prepares the tensor expected by GigaAM's normal `transcribe()` method.
It does not patch library globals. Future GigaAM changes to this interface may require updating the adapter.

## Verify

The splitting and orchestration tests use the standard library and do not download a model:

```powershell
python -m unittest -v
```

For real inference, provide a speech recording in the required format and run the command above.
No audio is uploaded to Hugging Face or another transcription service by this module.

References: [GigaAM repository](https://github.com/salute-developers/GigaAM),
[model inference](https://github.com/salute-developers/GigaAM/blob/main/gigaam/model.py), and
[upstream audio preparation](https://github.com/salute-developers/GigaAM/blob/main/gigaam/preprocess.py).
