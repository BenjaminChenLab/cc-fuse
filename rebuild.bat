@echo off
rem cc-fuse rebuild: GUI-subsystem exe (no console window), then restart the
rem tray app so the new build is live immediately. Double-click to use.
cd /d "%~dp0"
go build -ldflags "-H=windowsgui" -o cc-fuse.exe .\cmd\cc-fuse
if errorlevel 1 (
  echo BUILD FAILED
  exit /b 1
)
echo BUILD OK
taskkill /F /IM cc-fuse.exe >nul 2>&1
start "" "%~dp0cc-fuse.exe"
echo cc-fuse restarted in the system tray.
