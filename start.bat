@echo off
setlocal EnableExtensions
cd /d "%~dp0"
title Yingce Launcher

rem ============================================================
rem  Yingce one-click launcher (backend 8080 + frontend 3000).
rem  Double-click to run. Pure ASCII on purpose: cmd.exe batch
rem  files break when lines contain multibyte characters.
rem ============================================================

rem ---- locate node.exe: PATH first, then known install spots ----
set "NODE_EXE="
for /f "delims=" %%P in ('where node 2^>nul') do if not defined NODE_EXE set "NODE_EXE=%%P"
if not defined NODE_EXE if exist "%ProgramFiles%\nodejs\node.exe" set "NODE_EXE=%ProgramFiles%\nodejs\node.exe"
if not defined NODE_EXE if exist "%LocalAppData%\Programs\nodejs\node.exe" set "NODE_EXE=%LocalAppData%\Programs\nodejs\node.exe"
if not defined NODE_EXE if exist "C:\Users\Administrator\.workbuddy\binaries\node\versions\22.22.2-3\node.exe" set "NODE_EXE=C:\Users\Administrator\.workbuddy\binaries\node\versions\22.22.2-3\node.exe"
if not defined NODE_EXE (
    echo [ERROR] Node.js not found. Please install Node.js 20+ first.
    pause
    exit /b 1
)

rem ---- backend: skip when port 8080 is already listening ----
netstat -ano | findstr /r /c:":8080 .*LISTENING" >nul
if errorlevel 1 (
    echo [1/2] Starting backend window ...
    start "Yingce Backend" cmd /k ""%NODE_EXE%" "%~dp0.local\run-backend.mjs""
) else (
    echo [1/2] Port 8080 already in use - backend assumed running, skip.
)

rem ---- locate bun.exe BEFORE the if-block: %VAR% inside parentheses
rem ---- expands at parse time, so setting it inside the block is too late ----
set "BUN_EXE="
for /f "delims=" %%P in ('where bun.exe 2^>nul') do if not defined BUN_EXE set "BUN_EXE=%%P"
if not defined BUN_EXE if exist "%~dp0.local\tools\bun-windows-x64\bun.exe" set "BUN_EXE=%~dp0.local\tools\bun-windows-x64\bun.exe"
if not defined BUN_EXE if exist "C:\Users\Administrator\.workbuddy\binaries\node\versions\22.22.2-3\node_modules\bun\bin\bun.exe" set "BUN_EXE=C:\Users\Administrator\.workbuddy\binaries\node\versions\22.22.2-3\node_modules\bun\bin\bun.exe"

rem ---- frontend: skip when port 3000 already listening ----
netstat -ano | findstr /r /c:":3000 .*LISTENING" >nul
if errorlevel 1 (
    if not defined BUN_EXE (
        echo [2/2] [ERROR] bun.exe not found and port 3000 is free. Frontend cannot start.
        pause
        exit /b 1
    )
    if not exist "%~dp0web\node_modules" (
        echo [2/2] Installing frontend dependencies with bun ...
        pushd web
        "%BUN_EXE%" install --frozen-lockfile
        if errorlevel 1 (
            echo [2/2] [ERROR] bun install failed. Frontend cannot start.
            popd
            pause
            exit /b 1
        )
        popd
    )
    echo [2/2] Starting frontend window ...
    pushd web
    start "Yingce Frontend" cmd /k ""%BUN_EXE%" run dev"
    popd
) else (
    echo [2/2] Port 3000 already in use - frontend assumed running, skip.
)

rem ---- wait until the frontend answers, then open the browser ----
echo Waiting for http://localhost:3000 ...
set /a TRIES=0
:waitloop
set /a TRIES+=1
if %TRIES% GTR 90 (
    echo [WARN] Frontend did not respond within 3 minutes.
    echo        Check the "Yingce Backend" / "Yingce Frontend" windows for errors.
    pause
    exit /b 0
)
curl.exe -s -o NUL -m 2 http://127.0.0.1:3000 >nul 2>nul
if not errorlevel 1 goto opened
timeout /t 2 /nobreak >nul
goto waitloop

:opened
echo Ready. Opening browser ...
start "" "http://localhost:3000"
timeout /t 5 /nobreak >nul
exit /b 0
