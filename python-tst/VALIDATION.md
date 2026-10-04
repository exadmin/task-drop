# Validation

The module was tested with Python 3.14.2, PyTorch 2.11.0+cpu, Torchaudio 2.11.0+cpu,
and the preinstalled GigaAM checkout at commit `5b5b4add01f8122e103b046f0ec82bd64369d4bc`.
The pinned dependency targets the current upstream commit; a fresh installation of that dependency was not performed.

The CPU model `v3_e2e_rnnt` successfully transcribed two copies of the
[official example WAV](https://cdn.chatwm.opensmodel.sberdevices.ru/GigaAM/example.wav) in the supplied order.
It also transcribed a 36.87-second test recording built from three copies of that example with pauses.
The splitter produced chunks of 24.08 and 12.79 seconds, both cut at pauses, and retained every sample.
The long recording and a following short file produced one combined transcript.
FFmpeg was not installed in PATH during these runs. No Hugging Face token was used.

The standard-library tests cover sample preservation, pause boundaries, the exact 25-second limit,
one sample beyond the limit, list ordering, digital silence, invalid formats, truncated files, and output path protection.
Go tests cover packet continuity, stereo downmix, invalid floating-point samples, silent packets,
fractional-rate output lengths, filter flushing, pass-band amplitude, and suppression of a 12 kHz tone during 48-to-16 kHz conversion.
`go vet` passes, and the Windows GUI executable was rebuilt.

The microphone hardware path, CUDA inference, and fresh dependency installation remain unverified.

A dependency-resolution check in the fresh Python 3.14 environment failed because upstream pins
`onnxruntime==1.23.*`, while compatible Python 3.14 wheels start with newer releases.
The Go configuration therefore uses the already working system Python and `transcribe.py` by default.
Automatic isolated setup through `launch.py` remains available for a compatible Python environment.

The Go-to-Python bridge was also verified against the installed CPU model, including a WAV path with spaces.
Subprocess tests cover Unicode results, metacharacters in filenames, failure diagnostics, and timeout cancellation.
The native Windows test shows a recognized-text overlay, verifies that it remains visible for five seconds,
and checks automatic hiding and preservation of the foreground window.

Resident-worker tests verify one model load across repeated JSON requests, Unicode responses,
request errors that preserve the worker, and process restart after termination.
Two real GigaAM recognitions on CPU were run through the same Python PID.
