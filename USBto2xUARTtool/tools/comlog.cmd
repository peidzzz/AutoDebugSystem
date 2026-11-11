@echo off
rem Capture the debug console.  Examples:
rem   comlog.cmd                 30 s on COM9 -> logs\com-<stamp>.log
rem   comlog.cmd -Seconds 120
rem   comlog.cmd -Port COM5 -Baud 115200 -Out logs\app.log
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0comlog.ps1" %*
exit /b %ERRORLEVEL%
