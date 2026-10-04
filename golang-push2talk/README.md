# Go push-to-talk

Hold **Ctrl+Alt+P** to record your microphone to a WAV file on Windows 10 or 11.
Release any key in the combination to stop. A small overlay shows the recording duration and microphone level.
It appears near the bottom center of the monitor containing the foreground window, above ordinary desktop windows.
It does not activate or capture mouse clicks. Secure desktops and exclusive fullscreen applications can obscure it.

At startup, the application launches one resident Python worker and loads GigaAM before accepting inference requests.
Completed WAV recordings queue for that worker, including recordings made before the model is ready.
The recognized text appears in a wider overlay for five seconds, without changing the foreground window.
Text wraps onto multiple lines; text beyond the configured maximum height is clipped.
The complete transcript is saved as a UTF-8 `.txt` file in the recording directory.
Set `path-to-store-text` to save an additional copy. An omitted or empty value disables the additional copy.
The WAV is deleted only after all configured copies are saved. Failed transcription or saving leaves the WAV available.
Empty recognition results display `No speech recognized.`
New recordings remain available during transcription. Transcriptions run sequentially.
Results wait while you record or another result is visible. A new recording clears the previous displayed result.
The overlay shows loading progress until the Python model is ready, then a ready notification for three seconds.
While a recording waits for transcription, the overlay shows a transcription notification until the result arrives.

## Build and run

Install Go 1.24 or later. No C compiler or third-party packages are required.
The module is named `push2talk`, so a plain `go build` creates `push2talk.exe` on Windows.

```powershell
cd golang-push2talk
Copy-Item properties.json.example properties.json
go build
.\push2talk.exe
```

Keep `properties.json` next to the executable. The application runs in the background with `st-icon.png` in System Tray.
The PNG is embedded during the build, so you do not need to distribute it next to the executable.
The icon may appear under the taskbar's hidden icons. Click it or right-click it to open the menu.
Choose **Open recordings folder** to view transcripts and pending WAV files or **Exit** to close the application.
An active recording is finalized before the application exits normally.

With `hide_console` enabled, a normal build detaches its console at startup.
Launching it from Explorer can briefly show a console before it detaches.
For a Windows GUI build that never creates a console window, use:

```powershell
.\build.ps1
```

The script builds `push2talk.exe` using `go build -ldflags='-H=windowsgui'`.
You can run that Go command directly if PowerShell blocks script execution.
The application's tray icon is restored after Explorer restarts.

Automatic transcription requires Python 3.10 or later and the sibling `python-tst` directory.
By default, the configured Python executable runs `transcribe.py` with packages installed in that interpreter.
The current workstation already has a working GigaAM installation in its system Python, so this avoids redundant installation.
Set `python_executable` to a prepared virtual environment's Python to use that environment instead.
For automatic isolated setup, set `launcher_path` to `../python-tst/launch.py`; this uses the same setup as `run.cmd`.
Upstream ONNX dependency pins require an older Python version or compatible package adjustments for fresh Python 3.14 installs.
Initial readiness can take longer while model weights download and load into memory.
The model remains loaded across recordings, so later requests avoid Python startup and model loading.
Python exits with the Go application. If the worker crashes, Go restarts it with a five-second backoff.
A failed or timed-out request leaves its WAV available; it is not automatically retried.
Inference itself still takes time, and the model occupies RAM or GPU memory while the application is idle.
Python runs without a console window. Errors appear in the tray and log; the recorded WAV remains available.

For development or a different configuration location:

```powershell
go run . -config .\properties.json
.\push2talk.exe -config C:\path\to\properties.json
```

## Configuration

All behavior settings come from `properties.json`. Copy `properties.json.example` when setting up a new checkout,
then edit the local configuration. Git ignores `properties.json`; the example contains portable defaults.
The example leaves `path-to-store-text` empty. Set it to `texts` or another directory to enable additional TXT copies.
Restart the application after changing settings.
Unknown fields and invalid settings are rejected.

| Property | Meaning |
| --- | --- |
| `hide_console` | Detach the console on Windows. Default: `true`. Set `false` with a normal build to keep it for development. |
| `log_file` | Log file path. Default: `push2talk.log`. Relative paths resolve against the configuration file directory. |
| `hot_key` | One letter or digit with Ctrl, Alt, Shift, or Win modifiers, separated by `+`. Default: `Ctrl+Alt+P`. |
| `output_directory` | Recording directory. Relative paths resolve against the configuration file directory. |
| `path-to-store-text` | Optional additional TXT directory, created automatically. Omitted or empty disables copying. Relative paths resolve against the configuration file directory. |
| `microphone_device_id` | WASAPI endpoint ID. Empty selects the default communications microphone at each recording. |
| `audio_poll_ms` | Audio buffer polling interval, from 1 to 100 ms. Default: 5. |
| `output_sample_rate` | Output WAV sample rate, from 8000 to 48000 Hz. Default: 16000. Keep 16000 for the Python transcriber. |
| `overlay.width` | Window width in logical pixels, from 160 to 1000. |
| `overlay.height` | Window height in logical pixels, from 50 to 300. |
| `overlay.bottom_margin` | Distance above the monitor work area bottom, from 0 to 1000 logical pixels. |
| `overlay.opacity` | Window opacity, from 1 to 255. The default, 102, gives 60% transparency. |

The additional TXT path accepts `/` and `\` separators. In JSON, escape each backslash as `\\`:
`"C:/Users/name/texts"` and `"C:\\Users\\name\\texts"` identify the same Windows directory.

The `transcription` object contains these settings:

| Property | Default | Meaning |
| --- | --- | --- |
| `enabled` | `true` | Automatically transcribe completed recordings. |
| `python_executable` | `python` | Python executable name on PATH or an executable path. |
| `launcher_path` | `../python-tst/transcribe.py` | Python entry point. Relative paths resolve against `properties.json`. |
| `model` | `v3_e2e_rnnt` | GigaAM ASR model. |
| `device` | `auto` | `auto`, `cpu`, or `cuda`. |
| `timeout_seconds` | `1200` | Maximum time per transcription, including setup. |
| `display_seconds` | `5` | Visible time for each result, from 1 to 60 seconds. |
| `width` | `640` | Result overlay width in logical pixels. |
| `max_height` | `320` | Maximum result overlay height in logical pixels. |

When transcription is enabled, `output_sample_rate` must be 16000.
Set `transcription.enabled` to `false` to use the recorder without Python.
Exit cancels active Python processing and discards queued jobs; it still finalizes an active WAV recording.

The microphone opens only when the hotkey is pressed, using **WASAPI shared mode**.
The application never requests exclusive access and releases the microphone after recording.
Opening the device can delay the start of a recording; very short taps may produce no file.
Keyboard release detection runs approximately every 10 ms, subject to Windows scheduling.
No audio is captured while idle.

WAV files use mono PCM 16-bit audio at `output_sample_rate`, which defaults to 16000 Hz for GigaAM.
Capture uses the microphone's native shared-mode format. The recorder mixes channels to mono and converts PCM or float samples.
When sample rates differ, a streaming low-pass resampler converts them before WAV data is written.
No external encoder or FFmpeg process is required. Files use timestamped names under `output_directory`.
Resampling retains a small filter history; the tail is flushed when recording stops.
This change applies to new recordings; existing WAV files keep their original format.
Temporary files are finalized and renamed after recording. Recording errors appear as tray notifications and in the log file.
Windows notification settings can suppress tray notifications. Startup errors appear in a message box.
Logs append to `log_file` on every run; remove or archive the file when needed.
An interrupted process or power loss can leave an unfinished `.recording-*.tmp` file.
The WAV size limit is approximately 4 GiB per recording.

Windows must allow desktop applications to access the microphone.
Other applications can record concurrently in shared mode; exclusive access requested by another application can conflict.
A hotkey already registered by another application causes startup to fail with an error.
Win combinations can conflict with Windows shortcuts.

## Verify

```powershell
go test ./...
go vet ./...
$env:P2T_UI_TEST = '1'
go test -run TestWindowsOverlayLifecycle -timeout 15s
Remove-Item Env:P2T_UI_TEST
```

The optional Windows test shows the overlay briefly and checks that it does not change the foreground window.
It also checks that the tray icon is registered and that the tray Exit command closes the application.
It checks that a transcript remains visible for five seconds and disappears without changing focus.
It does not open the microphone.
For a hardware check, hold the configured hotkey while speaking, release it, and play the saved WAV file.
Repeat while another application uses the microphone to verify shared access with your device and driver.

## Platform boundaries

`config.go`, `pcm.go`, and `wav.go` contain portable configuration, conversion, and file logic.
`audio_windows.go` implements WASAPI capture. `ui_windows.go` implements the hotkey and native overlay.
`tray_windows.go` manages the tray icon, menu, notifications, and Explorer restart recovery.
`service.go` supervises the resident Python worker and exchanges newline-delimited UTF-8 JSON over stdin/stdout.
No HTTP server, listening port, or command shell is involved. Python diagnostics use stderr.
Windows amd64 and arm64 builds are supported. Linux and macOS have an explicit unsupported-platform entry point.
Their native capture, hotkey, and overlay implementations must be added before those builds can record audio.

Implementation references: [WASAPI](https://learn.microsoft.com/en-us/windows/win32/coreaudio/wasapi),
[RegisterHotKey](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-registerhotkey), and
[SetWindowPos](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setwindowpos).
