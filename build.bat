@echo off
echo Building z-api-proxy...
go build -ldflags "-H windowsgui" -o z-api-proxy.exe .
if %ERRORLEVEL% neq 0 (
    echo Build failed.
    exit /b 1
)
echo Done. Output: z-api-proxy.exe
