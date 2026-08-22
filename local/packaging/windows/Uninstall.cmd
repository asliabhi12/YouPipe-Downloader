@echo off
setlocal

rem Removes YouPiper Helper for the current user.
rem
rem Your downloaded files are never touched - only the Helper itself, its
rem startup entry and its log file are removed.

set "TARGET=%LOCALAPPDATA%\Programs\YouPiper Helper"

echo Removing YouPiper Helper...
echo.

rem Remove the startup entry first, so nothing is left starting at login even
rem if a file happens to be locked below.
reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v "YouPiper Helper" /f >nul 2>&1

taskkill /f /im YouPiper-Helper.exe >nul 2>&1

rem Give the process a moment to release its files.
timeout /t 2 /nobreak >nul 2>&1

if exist "%LOCALAPPDATA%\YouPiper" rd /s /q "%LOCALAPPDATA%\YouPiper"

rem Deleting the folder this script is running from would fail, so hand the
rem last step to a detached command that outlives this window.
if /i "%~dp0"=="%TARGET%\" (
    start "" /min cmd /c "timeout /t 2 /nobreak >nul & rd /s /q ""%TARGET%"""
    echo Done. YouPiper Helper will no longer start automatically.
    echo Your downloads were not touched.
    echo.
    timeout /t 4 /nobreak >nul
    exit /b 0
)

if exist "%TARGET%" rd /s /q "%TARGET%"

echo Done. YouPiper Helper will no longer start automatically.
echo Your downloads were not touched.
echo.
pause
