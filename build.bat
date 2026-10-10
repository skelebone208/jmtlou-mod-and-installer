@echo off
rem Builds "TLOU Mod Installer.exe" on Windows. Needs Go 1.24 or newer: https://go.dev/dl/
setlocal
cd /d "%~dp0"
echo [1/3] Downloading dependencies...
go mod download || goto :fail
echo [2/3] Embedding icon and manifest...
if not exist icon.ico certutil -f -decode icon.ico.b64 icon.ico >nul || goto :fail
go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -ico icon.ico -arch amd64 -o rsrc_windows_amd64.syso || goto :fail
echo [3/3] Building...
set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
go build -trimpath -ldflags "-H windowsgui -s -w" -o "TLOU Mod Installer.exe" . || goto :fail
echo.
echo Done: "TLOU Mod Installer.exe"
certutil -hashfile "TLOU Mod Installer.exe" SHA256
exit /b 0
:fail
echo Build failed.
exit /b 1
