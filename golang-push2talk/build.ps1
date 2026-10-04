$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
try {
    go build -ldflags='-H=windowsgui' -o push2talk.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    Write-Host "Built $PSScriptRoot\push2talk.exe"
} finally {
    Pop-Location
}
