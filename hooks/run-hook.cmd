@echo off
where /q bash 2>nul
if %errorlevel% == 0 (
    bash %*
    exit /b %errorlevel%
)
if exist "C:\Program Files\Git\bin\bash.exe" (
    "C:\Program Files\Git\bin\bash.exe" %*
    exit /b %errorlevel%
)
echo ERROR: bash not found. Install Git for Windows.
exit /b 1
