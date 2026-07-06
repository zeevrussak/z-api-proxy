@echo off
setlocal enabledelayedexpansion

:: ============================================================
::  z-api-proxy — Release Build Pipeline
::  Builds Go binaries (amd64 + arm64), MSIs, and NSIS installer,
::  placing all artifacts in releases/.
:: ============================================================

:: --- Kill any running instances so we can overwrite the binary ---
taskkill /f /im z-api-proxy.exe >nul 2>&1
if %ERRORLEVEL% equ 0 (
    echo Stopped running z-api-proxy.exe instance.
)

:: --- Read version from VERSION file ---
for /f "delims=" %%v in (VERSION) do set VERSION=%%v
set VERSION=%VERSION: =%
if "%VERSION%"=="" (
    echo ERROR: VERSION file is empty or missing.
    exit /b 1
)

:: --- Derive numeric MSI version (strip pre-release suffix) ---
for /f "tokens=1 delims=-" %%a in ("%VERSION%") do set MSIVER=%%a

echo ============================================================
echo   z-api-proxy release build
echo   Version: %VERSION%  (MSI numeric: %MSIVER%)
echo ============================================================

:: --- Prepare directories ---
if not exist build\amd64 mkdir build\amd64
if not exist build\arm64 mkdir build\arm64
if not exist releases mkdir releases

:: --- Generate Windows resource (icon) .syso file ---
echo.
echo [0/5] Generating Windows icon + manifest resource...
where rsrc >nul 2>&1
if %ERRORLEVEL% equ 0 (
    rsrc -manifest app.manifest -ico assets\icon.ico -o z-api-proxy.syso
    echo       Done.
) else (
    echo       rsrc not found, using existing z-api-proxy.syso
)

:: --- Build Go binaries ---
echo.
echo [1/5] Building amd64 binary...
set GOOS=windows
set GOARCH=amd64
go build -ldflags "-H windowsgui -X main.version=%VERSION%" -o build\amd64\z-api-proxy.exe .
if %ERRORLEVEL% neq 0 (
    echo FAILED: amd64 build
    exit /b 1
)
echo       Done.

echo [2/5] Building arm64 binary...
set GOARCH=arm64
go build -ldflags "-H windowsgui -X main.version=%VERSION%" -o build\arm64\z-api-proxy.exe .
if %ERRORLEVEL% neq 0 (
    echo FAILED: arm64 build
    exit /b 1
)
set GOOS=
set GOARCH=
echo       Done.

:: --- Build cursor-setup-helper ---
echo.
echo [2.5/6] Building cursor-setup-helper...
go build -ldflags "-X main.version=%VERSION%" -o releases\cursor-setup-helper.exe ./cmd/cursor-setup-helper/
if %ERRORLEVEL% neq 0 (
    echo FAILED: cursor-setup-helper build
    exit /b 1
)
echo       cursor-setup-helper.exe

:: --- Build MSIs ---
echo.
echo [3/5] Building MSI installers...
set WIX_VARS=-d MsiVersion=%MSIVER% -d DisplayVersion=%VERSION%

wix build installer.wxs -arch x64 %WIX_VARS% ^
    -d "BinPath=build\amd64\z-api-proxy.exe" ^
    -d UpgradeCode=18CAB0AD-AF9E-4C0B-AD01-99EF83004F7C ^
    -o "releases\z-api-proxy-win-%VERSION%-amd64.msi"
if %ERRORLEVEL% neq 0 (
    echo FAILED: amd64 MSI
    exit /b 1
)
echo       z-api-proxy-win-%VERSION%-amd64.msi

wix build installer.wxs -arch arm64 %WIX_VARS% ^
    -d "BinPath=build\arm64\z-api-proxy.exe" ^
    -d UpgradeCode=B7DE7313-6CBD-4BB4-8D65-91D23429F1DE ^
    -o "releases\z-api-proxy-win-%VERSION%-arm64.msi"
if %ERRORLEVEL% neq 0 (
    echo FAILED: arm64 MSI
    exit /b 1
)
echo       z-api-proxy-win-%VERSION%-arm64.msi

:: --- Build NSIS installer ---
echo.
echo [4/5] Building NSIS installer...
where makensis >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo WARNING: makensis not found on PATH, skipping NSIS installer.
    goto :summary
)
makensis /DAPPVERSION=%VERSION% installer.nsi
if %ERRORLEVEL% neq 0 (
    echo FAILED: NSIS build
    exit /b 1
)
move /Y z-api-proxy-win-setup.exe "releases\z-api-proxy-win-%VERSION%-setup.exe" >nul
echo       z-api-proxy-win-%VERSION%-setup.exe

:summary
echo.
echo ============================================================
echo   Release artifacts in releases\:
echo ============================================================
dir /b releases\
echo ============================================================
