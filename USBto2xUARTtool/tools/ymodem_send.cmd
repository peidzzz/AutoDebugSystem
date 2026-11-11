@echo off
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0ymodem_send.ps1" %*
