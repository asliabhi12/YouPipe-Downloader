@echo off
setlocal

rem Installs YouPiper Helper for the current user only.
rem
rem No administrator rights are needed and nothing machine-wide is changed:
rem the files go into this user's own application folder and the login entry
rem goes into this user's own registry hive.

set "TARGET=%LOCALAPPDATA%\Programs\YouPiper Helper"
set "SOURCE=%~dp0"

echo Installing YouPiper Helper...
echo.

rem Copy to a stable location. Running it straight out of a Downloads folder
rem would break the moment that folder is cleaned up.
if /i "%SOURCE%"=="%TARGET%\" goto :launch

if not exist "%TARGET%" mkdir "%TARGET%" >nul 2>&1
robocopy "%SOURCE%." "%TARGET%" /E /NFL /NDL /NJH /NJS /NP >nul
if errorlevel 8 (
    echo Could not copy the files to:
    echo   %TARGET%
    echo.
    pause
    exit /b 1
)

:launch
rem The Helper registers itself to start at login the first time it runs.
start "" "%TARGET%\YouPiper-Helper.exe"

echo Installed to:
echo   %TARGET%
echo.
echo YouPiper Helper is running and will start automatically when you log in.
echo Open YouPiper in your browser and downloads will save straight to this PC.
echo.
echo Windows may show a "Windows protected your PC" warning the first time.
echo Choose "More info" then "Run anyway" if you trust this download.
echo.
pause
