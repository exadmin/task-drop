@echo off
setlocal DisableDelayedExpansion
if "%~1"=="" (
    echo Usage: run.cmd first.wav [second.wav ...] [--output transcript.txt]
    echo Use run.cmd --help for all options.
    exit /b 2
)
set "P2T_VENV_PYTHON=%~dp0.venv\Scripts\python.exe"
if not exist "%P2T_VENV_PYTHON%" (
    echo Creating Python environment... 1>&2
    python "%~dp0launch.py" %*
    if errorlevel 1 (
        echo Python setup or transcription failed. See the error above. 1>&2
        exit /b 1
    )
    exit /b 0
)
"%P2T_VENV_PYTHON%" "%~dp0bootstrap.py" %*
exit /b %errorlevel%
