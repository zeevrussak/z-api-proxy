@echo off
echo Building z-api-proxy (amd64 + arm64)...

set LDFLAGS=-ldflags "-H windowsgui"

if not exist build mkdir build

echo   amd64...
set GOOS=windows
set GOARCH=amd64
go build %LDFLAGS% -o build\amd64\z-api-proxy.exe .
if %ERRORLEVEL% neq 0 (
    echo Build failed: amd64
    exit /b 1
)

echo   arm64...
set GOARCH=arm64
go build %LDFLAGS% -o build\arm64\z-api-proxy.exe .
if %ERRORLEVEL% neq 0 (
    echo Build failed: arm64
    exit /b 1
)

set GOOS=
set GOARCH=

echo Done.
echo   build\amd64\z-api-proxy.exe
echo   build\arm64\z-api-proxy.exe
