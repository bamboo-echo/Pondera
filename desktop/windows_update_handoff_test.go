package main

import (
	"os"
	"strings"
	"testing"
)

func TestInstallerCommandLineUsesVisibleUpdateModeAndKeepsDFlagLast(t *testing.T) {
	got := installerCommandLine(`C:\Temp\Pondera Installer.exe`, `D:\Tools\Pondera App`)
	want := `"C:\Temp\Pondera Installer.exe" /PONDERAUPDATE=1 /PONDERASTAGE=1 /D=D:\Tools\Pondera App`
	if got != want {
		t.Fatalf("installerCommandLine = %q, want %q", got, want)
	}
	if strings.Contains(got, " /S") {
		t.Fatalf("auto-update must expose progress instead of using silent mode, got %q", got)
	}
	if !strings.HasSuffix(got, `/D=D:\Tools\Pondera App`) {
		t.Fatalf("/D= must be the final unquoted NSIS token, got %q", got)
	}
}

func TestWindowsUpdateHandoffArgsCarryParentInstallAndRelaunch(t *testing.T) {
	got := windowsUpdateHandoffArgs(
		4242,
		`C:\Users\Jane Doe\AppData\Local\Pondera\updates\Pondera-windows-amd64-installer.exe`,
		strings.Repeat("a", 64),
		`D:\Tools\Pondera App`,
		`D:\Tools\Pondera App\pondera-desktop.exe`,
		"v1.6.0",
		"2026-07-29T00:00:00Z",
		"transaction-1",
	)
	want := []string{
		"--parent-pid", "4242",
		"--installer", `C:\Users\Jane Doe\AppData\Local\Pondera\updates\Pondera-windows-amd64-installer.exe`,
		"--installer-sha256", strings.Repeat("a", 64),
		"--to-version", "v1.6.0",
		"--created-at", "2026-07-29T00:00:00Z",
		"--transaction-id", "transaction-1",
		"--install-dir", `D:\Tools\Pondera App`,
		"--relaunch", `D:\Tools\Pondera App\pondera-desktop.exe`,
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestWindowsVersionedUpdateHandoffArgsDoNotRequireLegacyPendingIdentity(t *testing.T) {
	got := windowsVersionedUpdateHandoffArgs(
		4242,
		`C:\Temp\Pondera-installer.exe`,
		strings.Repeat("b", 64),
		`D:\Tools\Pondera`,
		`D:\Tools\Pondera\pondera-launcher.exe`,
		"v1.20.0",
	)
	want := []string{
		"--parent-pid", "4242",
		"--installer", `C:\Temp\Pondera-installer.exe`,
		"--installer-sha256", strings.Repeat("b", 64),
		"--to-version", "v1.20.0",
		"--install-layout", "versioned-v1",
		"--install-dir", `D:\Tools\Pondera`,
		"--relaunch", `D:\Tools\Pondera\pondera-launcher.exe`,
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	for _, legacy := range []string{"--created-at", "--transaction-id"} {
		if strings.Contains(strings.Join(got, " "), legacy) {
			t.Fatalf("versioned handoff must not carry legacy field %s", legacy)
		}
	}
}

func TestWindowsInstallerScriptWaitsBeforeCopyingExecutable(t *testing.T) {
	data, err := os.ReadFile("build/windows/installer/project.nsi")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, want := range []string{
		`!define PONDERA_LEGACY_UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Pondera"`,
		`!define PONDERA_LEGACY_PRODUCT_KEY "Software\pondera\Pondera"`,
		`!define PONDERA_UPDATE_HELPER "pondera-update-helper.exe"`,
		`!define PONDERA_GUARD "pondera-guard.exe"`,
		`!define PONDERA_LAUNCHER "pondera-launcher.exe"`,
		`!define PONDERA_CLI "pondera-cli.exe"`,
		`!define PONDERA_PORTABLE_ENTRY "Pondera.exe"`,
		`!define PONDERA_LAYOUT_INSTALLER "pondera-layout-installer.exe"`,
		`!define PONDERA_PAYLOAD_MANIFEST "pondera-payload.json"`,
		`!define PONDERA_PAYLOAD_SIGNATURE "pondera-payload.json.minisig"`,
		"Var PonderaUpdateMode",
		"Var PonderaStageMode",
		`${GetOptions} $R0 "/PONDERAUPDATE=" $R1`,
		`${GetOptions} $R0 "/PONDERASTAGE=" $R2`,
		"Function pondera.skipSetupPageForUpdate",
		"Function pondera.showUpdateProgress",
		`!define MUI_PAGE_CUSTOMFUNCTION_PRE pondera.skipFinishPageForUpdate`,
		"Function pondera.skipFinishPageForUpdate",
		`StrCmp $PonderaUpdateMode "1" 0 pondera_show_finish_page`,
		"SetAutoClose true",
		"BringToFront",
		`LangString ponderaUpdateTitle ${LANG_ENGLISH} "Updating Pondera"`,
		`LangString ponderaUpdateTitle ${LANG_SIMPCHINESE} "正在更新 Pondera"`,
		`LangString ponderaUpdateTitle ${LANG_TRADCHINESE} "正在更新 Pondera"`,
		`LangString ponderaUpdateSubtitle ${LANG_ENGLISH} "Installing the verified update. Pondera will restart automatically."`,
		`LangString ponderaUpdateSubtitle ${LANG_SIMPCHINESE} "正在安装已验证的更新，完成后 Pondera 将自动重启。"`,
		`LangString ponderaUpdateSubtitle ${LANG_TRADCHINESE} "正在安裝已驗證的更新，完成後 Pondera 將自動重新啟動。"`,
		"Function pondera.waitForExecutableUnlock",
		`FileOpen $1 "$INSTDIR\${PRODUCT_EXECUTABLE}" a`,
		`FileOpen $1 "$INSTDIR\versions\${PONDERA_VERSION_TAG}\${PRODUCT_EXECUTABLE}" a`,
		`FileOpen $1 "$INSTDIR\${PONDERA_GUARD}" a`,
		`FileOpen $1 "$INSTDIR\${PONDERA_LAUNCHER}" a`,
		`FileOpen $1 "$INSTDIR\${PONDERA_CLI}" a`,
		`FileOpen $1 "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" a`,
		"SetErrorLevel 1618",
		"Call pondera.waitForExecutableUnlock",
		`File "/oname=${PONDERA_UPDATE_HELPER}" "${PONDERA_UPDATE_HELPER}"`,
		`File "/oname=${PONDERA_CLI}" "${PONDERA_CLI}"`,
		`File "/oname=${PONDERA_LAYOUT_INSTALLER}" "${PONDERA_GUARD}"`,
		`nsExec::ExecToLog /OEM`,
		`Pondera layout activator output:`,
		`--activate-staging "$R9" --no-relaunch`,
		`LangString ponderaActivateBusy ${LANG_ENGLISH}`,
		`LangString ponderaActivateBusy ${LANG_SIMPCHINESE}`,
		`LangString ponderaActivateBusy ${LANG_TRADCHINESE}`,
		`LangString ponderaActivateLocked ${LANG_ENGLISH}`,
		`LangString ponderaActivateLocked ${LANG_SIMPCHINESE}`,
		`LangString ponderaActivateLocked ${LANG_TRADCHINESE}`,
		`MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(ponderaActivateBusy)" IDRETRY pondera_layout_activate`,
		`MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(ponderaActivateLocked)" IDRETRY pondera_layout_activate`,
		`File "/oname=${PONDERA_PAYLOAD_MANIFEST}" "${PONDERA_PAYLOAD_MANIFEST}"`,
		`File "/oname=${PONDERA_PAYLOAD_SIGNATURE}" "${PONDERA_PAYLOAD_SIGNATURE}"`,
		`Delete "$INSTDIR\${PONDERA_UPDATE_HELPER}"`,
		`Delete "$INSTDIR\${PONDERA_CLI}"`,
		`DeleteRegValue HKCU "${PONDERA_LEGACY_PRODUCT_KEY}" ""`,
		`!insertmacro pondera.deleteLegacyInstallerStateIfOwned`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("project.nsi missing %q", want)
		}
	}
	finishPageHook := strings.Index(script, "!define MUI_PAGE_CUSTOMFUNCTION_PRE pondera.skipFinishPageForUpdate")
	finishPage := strings.Index(script, "!insertmacro MUI_PAGE_FINISH")
	if finishPageHook < 0 || finishPage < 0 || finishPageHook > finishPage {
		t.Fatalf("update-only finish page hook must be attached to MUI_PAGE_FINISH (hook=%d page=%d)", finishPageHook, finishPage)
	}
	activation := script[strings.Index(script, "Pondera layout activator output:"):]
	retryPrompt := strings.Index(activation, `IDRETRY pondera_layout_activate`)
	discardStaging := strings.Index(activation, `RMDir /r "$R9"`)
	silentAbort := strings.Index(activation, "IfSilent pondera_activation_failed 0")
	if retryPrompt < 0 || discardStaging < 0 || retryPrompt > discardStaging || silentAbort < 0 || silentAbort > retryPrompt {
		t.Fatalf("activation failure must offer Retry before discarding the staged files, and silent installs must skip the prompt (retry=%d discard=%d silent=%d)", retryPrompt, discardStaging, silentAbort)
	}
	if levelBeforePrompt := strings.Index(activation, "SetErrorLevel"); levelBeforePrompt < retryPrompt {
		t.Fatalf("SetErrorLevel must follow the Retry prompt so a successful retry exits 0 (level=%d retry=%d)", levelBeforePrompt, retryPrompt)
	}
	wait := strings.Index(script, "Call pondera.waitForExecutableUnlock")
	copyFiles := strings.Index(script, "pondera_normal_install:")
	if wait < 0 || copyFiles < 0 || wait > copyFiles {
		t.Fatalf("installer must wait for the running exe to unlock before the normal-install payload extraction (wait=%d copy=%d)", wait, copyFiles)
	}
	stageBranch := strings.Index(script, "StrCmp $PonderaStageMode \"1\" pondera_stage_payload")
	if stageBranch < 0 || stageBranch > copyFiles {
		t.Fatalf("staging mode must bypass live executable unlock before payload extraction (branch=%d copy=%d)", stageBranch, copyFiles)
	}
	if !strings.Contains(script, "Goto pondera_section_done") {
		t.Fatal("staging mode must skip registry, shortcuts, associations, and uninstaller")
	}
	if strings.Contains(script, `FileOpen $0 "$INSTDIR\current.json" w`) {
		t.Fatal("normal installer must delegate the current.json commit to the atomic Go activator")
	}
	writeCurrent := strings.Index(script, `!insertmacro pondera.writeUninstaller`)
	deleteLegacy := strings.Index(script, `!insertmacro pondera.deleteLegacyInstallerStateIfOwned`)
	if writeCurrent < 0 || deleteLegacy < 0 || writeCurrent > deleteLegacy {
		t.Fatalf("installer must write the current uninstall entry before reconciling owned legacy state (write=%d delete=%d)", writeCurrent, deleteLegacy)
	}
	legacyMacro := script[strings.Index(script, `!macro pondera.deleteLegacyInstallerStateIfOwned`):strings.Index(script, `!macro pondera.deleteUninstaller`)]
	deleteLegacyLocation := strings.Index(legacyMacro, `DeleteRegValue HKCU "${PONDERA_LEGACY_PRODUCT_KEY}" ""`)
	deleteLegacyAlias := strings.Index(legacyMacro, `DeleteRegKey HKCU "${PONDERA_LEGACY_UNINST_KEY}"`)
	if deleteLegacyLocation < 0 || deleteLegacyAlias < 0 || deleteLegacyLocation > deleteLegacyAlias {
		t.Fatalf("installer must clear the same-root Tauri install-location breadcrumb before deleting its uninstall alias (location=%d alias=%d)", deleteLegacyLocation, deleteLegacyAlias)
	}
	metadataBranch := strings.Index(script, `pondera_stage_payload:`)
	metadataFile := strings.Index(script, `File "/oname=${PONDERA_PAYLOAD_MANIFEST}"`)
	normalInstall := strings.Index(script, `pondera_normal_install:`)
	if metadataBranch < 0 || metadataFile < 0 || normalInstall < 0 || metadataBranch > metadataFile || metadataFile > normalInstall {
		t.Fatalf("payload manifest must be extracted only in staging mode (branch=%d file=%d)", metadataBranch, metadataFile)
	}
}

func TestWindowsInstallerUsesPreviousDirectoryAsManualInstallDefault(t *testing.T) {
	data, err := os.ReadFile("build/windows/installer/project.nsi")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, want := range []string{
		`InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation"`,
		`InstallDir "${PONDERA_DEFAULT_INSTALLDIR}"`,
		`!insertmacro MUI_PAGE_DIRECTORY`,
		`!define MUI_PAGE_CUSTOMFUNCTION_PRE pondera.skipSetupPageForUpdate`,
		`StrCmp $PonderaUpdateMode "1" 0 pondera_show_setup_page`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("project.nsi missing manual-install path contract %q", want)
		}
	}
	page := strings.Index(script, "!insertmacro MUI_PAGE_DIRECTORY")
	pageHook := strings.Index(script, "!define MUI_PAGE_CUSTOMFUNCTION_PRE pondera.skipSetupPageForUpdate\n!insertmacro MUI_PAGE_DIRECTORY")
	if page < 0 || pageHook < 0 || pageHook > page {
		t.Fatal("directory selection page must remain available for manual installs")
	}
	if strings.Contains(script, `StrCpy $PonderaUpdateMode "1"
	Goto pondera_show_setup_page`) {
		t.Fatal("automatic updates must not reopen the manual directory selection page")
	}
}

func TestDesktopBuildScriptCompilesAndPackagesWindowsUpdateHelper(t *testing.T) {
	data, err := os.ReadFile("../scripts/desktop-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, want := range []string{
		`UPDATE_HELPER="pondera-update-helper.exe"`,
		`go build -trimpath -o "$windows_resource_tool" ./cmd/windows-resource`,
		`GOOS=windows GOARCH="$arch" go build`,
		`./cmd/update-helper`,
		`"$installer_dir/$UPDATE_HELPER"`,
		`stamp_windows_executable "$installer_dir/$UPDATE_HELPER" "Pondera Update Helper"`,
		`for name in "$BINNAME.exe" "$GUARDNAME.exe" "$LAUNCHERNAME.exe" "$UPDATE_HELPER" "$WINDOWS_CLINAME.exe" "pondera-uninstall.exe"; do`,
		`cp "$installer_dir/$name" "$payload_dir/$name"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("desktop-build.sh missing %q", want)
		}
	}

	packageData, err := os.ReadFile("../scripts/package-windows-desktop.sh")
	if err != nil {
		t.Fatal(err)
	}
	packager := string(packageData)
	for _, want := range []string{
		`cp "$PAYLOAD/$UPDATE_HELPER" "$INSTALLER_DIR/$UPDATE_HELPER"`,
		`cp "$PAYLOAD/$UPDATE_HELPER" "$portable_staging/versions/$version_label/$UPDATE_HELPER"`,
		`"$ROOT/scripts/verify-windows-portable.sh" "$portable_staging"`,
	} {
		if !strings.Contains(packager, want) {
			t.Fatalf("package-windows-desktop.sh missing %q", want)
		}
	}
}

func TestWindowsUpdateRequiresObservedHelperHandoff(t *testing.T) {
	data, err := os.ReadFile("updater_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if !strings.Contains(source, "return startWindowsUpdateHelper(") {
		t.Fatal("Windows update handoff does not require the observed helper path")
	}
	if strings.Contains(source, "return installerCommand(installerPath, installDir).Start()") {
		t.Fatal("Windows update silently falls back to an unobserved installer")
	}
	if !strings.Contains(source, "cmd := proc.Command(helperPath") || strings.Contains(source, "proc.VisibleCommand(helperPath") {
		t.Fatal("Windows handoff helper should stay hidden while NSIS shows update progress")
	}
	helperData, err := os.ReadFile("cmd/update-helper/main_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	helperSource := string(helperData)
	if !strings.Contains(helperSource, "reconcileWindowsUninstallRegistrationFn(installDir, toVersion)") {
		t.Fatal("versioned Windows activation must refresh its managed uninstall registration")
	}
	if strings.Contains(helperSource, "installerCommandLine(installer, installDir), HideWindow: true") {
		t.Fatal("update helper still hides the NSIS progress window")
	}
}
