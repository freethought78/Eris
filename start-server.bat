@echo off
title Mesh Server
echo ============================================
echo  Mesh - Peer-to-Peer Communication Platform
echo ============================================
echo.
echo Starting server...
echo.
node "%~dp0server\index.js" %*
echo.
if errorlevel 1 (
    echo Server exited with an error (code %errorlevel%).
    pause
)
