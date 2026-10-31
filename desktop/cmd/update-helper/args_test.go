package main

import (
	"strings"
	"testing"
)

func TestInstallerCommandLineUsesVisibleUpdateModeAndLeavesDFlagLast(t *testing.T) {
	got := installerCommandLine(`C:\Temp\Pondera Installer.exe`, `D:\Tools\Pondera App`)
	want := `"C:\Temp\Pondera Installer.exe" /REASONIXUPDATE=1 /REASONIXSTAGE=1 /D=D:\Tools\Pondera App`
	if got != want {
		t.Fatalf("installerCommandLine = %q, want %q", got, want)
	}
	if strings.Contains(got, " /S") {
		t.Fatalf("auto-update must expose progress instead of using silent mode, got %q", got)
	}
	if !strings.HasSuffix(got, `/D=D:\Tools\Pondera App`) {
		t.Fatalf("/D= must be the final unquoted NSIS token, got %q", got)
	}
	if !strings.Contains(got, " /REASONIXSTAGE=1") {
		t.Fatalf("auto-update must extract away from the live install, got %q", got)
	}
}
