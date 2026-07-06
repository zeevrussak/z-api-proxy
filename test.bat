@echo off
echo Running z-api-proxy test suite...
echo.

echo [1/2] Unit tests...
go test ./internal/...
if %ERRORLEVEL% neq 0 (
    echo FAILED: unit tests
    exit /b 1
)
echo       Passed.

echo.
echo [2/2] UI integration test ^(builds exe + opens settings window^)...
go test -run TestUISettingsWindow -count=1 . 
if %ERRORLEVEL% neq 0 (
    echo FAILED: UI integration test
    exit /b 1
)
echo       Passed.

echo.
echo ==============================
echo   All tests passed!
echo ==============================
