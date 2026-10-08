@echo off
rem Start the web UI (opens the browser; press Ctrl+C in this window to stop)
chcp 65001 >nul
cd /d "%~dp0"
llmtest.exe -web
pause
