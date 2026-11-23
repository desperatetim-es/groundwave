# Build the Windows release executable into dist\.
# Usage (PowerShell, from the repo folder):  .\build-windows.ps1 [-Version 0.1.0]
# Needs only Go (https://go.dev/dl/). -H=windowsgui gives the app its own window with no console.
param([string]$Version = "dev")
$ErrorActionPreference = "Stop"
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go) { $go = Join-Path $env:LOCALAPPDATA "Programs\go\bin\go.exe" }
New-Item -ItemType Directory -Force dist | Out-Null
$env:CGO_ENABLED = "0"; $env:GOOS = "windows"; $env:GOARCH = "amd64"
$out = "dist\groundwave-windows-amd64.exe"
& $go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$Version" -o $out ./cmd/groundwave
if ($LASTEXITCODE -ne 0) { throw "build failed" }
"{0}  {1:N1} MB  sha256 {2}" -f $out, ((Get-Item $out).Length / 1MB), (Get-FileHash $out).Hash