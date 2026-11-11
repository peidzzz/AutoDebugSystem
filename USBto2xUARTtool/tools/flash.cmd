@echo off
rem Wrapper so the script runs even when the PowerShell execution policy is
rem "Restricted":  flash.cmd combined          -> drag-and-drop, bootloader + app
rem                  flash.cmd bootloader -Via openocd
rem                  flash.cmd -Erase -Via openocd
setlocal
set "ARGS=%*"
set "A1=%~1"
if defined A1 if not "%A1:~0,1%"=="-" set "ARGS=-Image %*"
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0flash.ps1" %ARGS%
exit /b %ERRORLEVEL%
