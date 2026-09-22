@echo off
setlocal EnableExtensions
cd /d "%~dp0"
title Yingce Desktop Builder

rem ============================================================
rem  Build the standalone desktop exe (single file, no login):
rem    1. bun run build         -> web/dist
rem    2. robocopy dist         -> backend/internal/webui/dist
rem    3. go build -tags webui desktop -> YingceDesktop.exe
rem  Portable toolchains under .local\tools are used when present.
rem  Output: YingceDesktop.exe at the repo root.
rem  Double-click YingceDesktop.exe to run: embedded frontend on
rem  http://127.0.0.1:8321, auto-login as local admin, WebView2
rem  window (falls back to the default browser).
rem ============================================================

rem ---- locate go.exe ----
set "GO_EXE="
for /f "delims=" %%P in ('where go 2^>nul') do if not defined GO_EXE set "GO_EXE=%%P"
if not defined GO_EXE if exist "%~dp0.local\tools\go\bin\go.exe" set "GO_EXE=%~dp0.local\tools\go\bin\go.exe"
if not defined GO_EXE (
    echo [ERROR] Go not found. Install Go 1.25+ or run .local\tools setup first.
    pause
    exit /b 1
)

rem ---- locate gcc.exe (go-sqlite3 requires cgo) ----
set "GCC_EXE="
for /f "delims=" %%P in ('where gcc 2^>nul') do if not defined GCC_EXE set "GCC_EXE=%%P"
if not defined GCC_EXE if exist "%~dp0.local\tools\mingw64\bin\gcc.exe" set "GCC_EXE=%~dp0.local\tools\mingw64\bin\gcc.exe"
if not defined GCC_EXE (
    echo [ERROR] gcc not found. go-sqlite3 needs CGO; install MinGW-w64 under .local\tools\mingw64.
    pause
    exit /b 1
)

rem ---- locate bun.exe ----
set "BUN_EXE="
for /f "delims=" %%P in ('where bun.exe 2^>nul') do if not defined BUN_EXE set "BUN_EXE=%%P"
if not defined BUN_EXE if exist "%~dp0.local\tools\bun-windows-x64\bun.exe" set "BUN_EXE=%~dp0.local\tools\bun-windows-x64\bun.exe"
if not defined BUN_EXE (
    echo [ERROR] bun.exe not found. Install Bun or place it under .local\tools\bun-windows-x64.
    pause
    exit /b 1
)

set "CGO_ENABLED=1"
set "CC=%GCC_EXE%"
set "GOCACHE=%~dp0.local\cache\go-build"
set "GOMODCACHE=%~dp0.local\cache\go-mod"
if not defined GOPROXY set "GOPROXY=https://goproxy.cn,direct"

rem ---- frontend dependencies + production build ----
if not exist "%~dp0web\node_modules" (
    echo [1/3] Installing frontend dependencies ...
    pushd web
    "%BUN_EXE%" install --frozen-lockfile
    if errorlevel 1 (
        echo [ERROR] bun install failed.
        popd
        pause
        exit /b 1
    )
    popd
)
echo [1/3] Building frontend (bun run build) ...
pushd web
"%BUN_EXE%" run build
if errorlevel 1 (
    echo [ERROR] frontend build failed.
    popd
    pause
    exit /b 1
)
popd

rem ---- copy dist into the embed directory ----
echo [2/3] Embedding frontend dist ...
robocopy "%~dp0web\dist" "%~dp0backend\internal\webui\dist" /MIR /NFL /NDL /NJH /NJS >nul
if errorlevel 8 (
    echo [ERROR] robocopy failed.
    pause
    exit /b 1
)

rem ---- build the desktop exe ----
echo [3/3] Building YingceDesktop.exe ...
pushd backend
"%GO_EXE%" build -tags "webui desktop" -ldflags "-s -w" -o "%~dp0YingceDesktop.exe" ./cmd/server
if errorlevel 1 (
    echo [ERROR] go build failed.
    popd
    pause
    exit /b 1
)
popd

echo.
echo Done. Output: %~dp0YingceDesktop.exe
echo Double-click it to start (data stored in data\ next to the exe).
pause
exit /b 0
