<#
PowerShell build helper
Usage:
    .\scripts\build.ps1              # build local binary (default)
    .\scripts\build.ps1 -Target linux-amd64
    .\scripts\build.ps1 -Target macos-arm64
    .\scripts\build.ps1 -Target windows-amd64

Examples (cross-build):
    pwsh -File .\scripts\build.ps1 -Target linux-amd64 -Out censys_go_bin
    pwsh -File .\scripts\build.ps1 -Target macos-arm64 -Out censys_go_bin
    pwsh -File .\scripts\build.ps1 -Target windows-amd64 -Out censys_go.exe

This script is a lightweight helper; the repository also includes a Makefile
and a POSIX `scripts/build.sh` wrapper for non-Windows platforms.
#>

param(
    [string]$Target = "local",
    [string]$Out = "censys_go.exe"
)

function Do-Build($goos, $goarch, $outfile) {
    Write-Host "Building for $goos/$goarch -> $outfile"
    $env:GOOS = $goos
    $env:GOARCH = $goarch
    $cmd = "go build -o $outfile ."
    & cmd /c $cmd
    if ($LASTEXITCODE -ne 0) {
        throw "go build failed"
    }
}

try {
    switch ($Target) {
        'local' {
            Write-Host "Building local binary: $Out"
            go build -o $Out .
        }
        'linux-amd64' {
            Do-Build 'linux' 'amd64' "$Out-linux-amd64"
        }
        'linux-arm64' {
            Do-Build 'linux' 'arm64' "$Out-linux-arm64"
        }
        'macos-amd64' {
            Do-Build 'darwin' 'amd64' "$Out-darwin-amd64"
        }
        'macos-arm64' {
            Do-Build 'darwin' 'arm64' "$Out-darwin-arm64"
        }
        'windows-amd64' {
            Do-Build 'windows' 'amd64' "$Out-windows-amd64.exe"
        }
        default {
            throw "Unknown target: $Target"
        }
    }
} catch {
    Write-Error $_.Exception.Message
    exit 1
}
