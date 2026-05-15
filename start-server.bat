@echo off
title Eris Server
echo ============================================
echo  Eris - Peer-to-Peer Communication Platform
echo ============================================
echo.

where node >nul 2>nul
if %errorlevel% neq 0 (
    echo ERROR: Node.js not found.
    echo Please install Node.js from https://nodejs.org/
    echo.
    pause
    exit /b 1
)

echo Starting server...
echo To stop: press Ctrl+C
echo.
node "%~dp0server\index.js" %*
set EXIT_CODE=%errorlevel%

echo.
if %EXIT_CODE% neq 0 (
    echo Server exited with code %EXIT_CODE%.
) else (
    echo Server stopped.
)

echo.
pause
