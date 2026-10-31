package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writePortableFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyWindowsPortableVersionedLayout(t *testing.T) {
	verify := filepath.Join("..", "scripts", "verify-windows-portable.sh")
	good := t.TempDir()
	// versioned-v1 root entries
	writePortableFixture(t, good, "Pondera.exe", "launcher")
	writePortableFixture(t, good, "pondera-cli.exe", "cli-entry")
	ver := filepath.Join(good, "versions", "v1.20.0")
	if err := os.MkdirAll(ver, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pondera-desktop.exe", "pondera-cli.exe", "pondera-update-helper.exe"} {
		writePortableFixture(t, ver, name, name)
	}
	// The Electron bundle is the app/ tree member of the active version.
	appDir := filepath.Join(ver, "app")
	if err := os.MkdirAll(filepath.Join(appDir, "resources", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(appDir, "resources", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePortableFixture(t, appDir, "Pondera.exe", "shell")
	writePortableFixture(t, filepath.Join(appDir, "resources"), "app.asar", "asar")
	writePortableFixture(t, filepath.Join(appDir, "resources"), "build.json", "{}")
	writePortableFixture(t, filepath.Join(appDir, "resources", "app"), "index.html", "<html></html>")
	writePortableFixture(t, filepath.Join(appDir, "resources", "bin"), "pondera-cli-launcher.exe", "cli-entry")
	if err := os.WriteFile(filepath.Join(good, "current.json"), []byte(`{
  "schemaVersion": 1,
  "activeVersion": "v1.20.0",
  "activeDir": "versions/v1.20.0"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", verify, good).CombinedOutput(); err != nil {
		t.Fatalf("valid versioned portable failed: %v\n%s", err, out)
	}
	writePortableFixture(t, good, "pondera-launcher.exe", "launcher")
	if out, err := exec.Command("bash", verify, good).CombinedOutput(); err == nil {
		t.Fatalf("canonical package accepted legacy entry: %s", out)
	}
	if out, err := exec.Command("bash", verify, good, "legacy-dual").CombinedOutput(); err != nil {
		t.Fatalf("explicit legacy package rejected: %v\n%s", err, out)
	}
	writePortableFixture(t, good, "pondera-launcher.exe", "mismatch")
	if out, err := exec.Command("bash", verify, good, "legacy-dual").CombinedOutput(); err == nil {
		t.Fatalf("legacy package accepted mismatched entries: %s", out)
	}
	if err := os.Remove(filepath.Join(good, "pondera-launcher.exe")); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"legacy-dual", "auto"} {
		if out, err := exec.Command("bash", verify, good, mode).CombinedOutput(); err == nil {
			t.Fatalf("accepted mode %s: %s", mode, out)
		}
	}
	writePortableFixture(t, good, "unexpected.EXE", "extra")
	if out, err := exec.Command("bash", verify, good).CombinedOutput(); err == nil {
		t.Fatalf("accepted extra executable: %s", out)
	}
	if err := os.Remove(filepath.Join(good, "unexpected.EXE")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "current.json"), []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", verify, good).CombinedOutput(); err == nil {
		t.Fatalf("accepted damaged pointer: %s", out)
	}

	// Flat Guard layout must be rejected.
	flat := t.TempDir()
	for _, name := range []string{
		"pondera-desktop.exe",
		"pondera-guard.exe",
		"pondera-update-helper.exe",
		"pondera-launcher.exe",
		"Pondera.exe",
		"pondera-cli.exe",
	} {
		writePortableFixture(t, flat, name, name)
	}
	if out, err := exec.Command("bash", verify, flat).CombinedOutput(); err == nil {
		t.Fatalf("flat portable with guard should fail, output=%s", out)
	}
}

func TestDesktopPackagesPreserveNativePlatformLaunchers(t *testing.T) {
	buildData, err := os.ReadFile("../scripts/desktop-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	build := string(buildData)
	for _, want := range []string{
		`CLINAME="pondera"`,
		`WINDOWS_CLINAME="pondera-cli"`,
		`./cmd/pondera`,
		`./cmd/pondera-legacy-migrator`,
		`./cmd/pondera-launcher`,
		`cp "$cli_out" "$app/Contents/Resources/service/$CLINAME"`,
		`ln -s "../Resources/service/$BINNAME" "$app/Contents/MacOS/$BINNAME"`,
		`macOS bundle must not include $GUARDNAME`,
		`[ "$bundle_executable" = "$APPNAME" ]`,
		`Print :CFBundleIconFile`,
		`darwin_icon="$ROOT/desktop/build/darwin/icon.icns"`,
		`[ -s "$app/Contents/Resources/$bundle_icon" ]`,
		`macOS bundle icon is missing: $bundle_icon`,
		`-H windowsgui`,
		`stamp_windows_executable "$guard_out" "Pondera Legacy Migrator"`,
		`stamp_windows_executable "$launcher_out" "Pondera Launcher"`,
		`stamp_windows_executable "$installer_dir/$UPDATE_HELPER" "Pondera Update Helper"`,
		`payload_dir="$ROOT/desktop/build/windows/signing-payload"`,
		`for name in "$BINNAME.exe" "$GUARDNAME.exe" "$LAUNCHERNAME.exe" "$UPDATE_HELPER" "$WINDOWS_CLINAME.exe" "pondera-uninstall.exe"; do`,
		`cp "$installer_dir/$name" "$payload_dir/$name"`,
		`cp -R "$installer_dir/app" "$payload_dir/app"`,
		`node "$ROOT/desktop/packaging/signing-files.mjs" "$payload_dir"`,
		`"$ROOT/scripts/package-windows-desktop.sh" "$arch" "$payload_dir"`,
		`"$BINNAME" "$LAUNCHERNAME" "$GUARDNAME" "$CLINAME"`,
		`Exec=pondera-launcher`,
	} {
		if !strings.Contains(build, want) {
			t.Errorf("desktop-build.sh missing packaging contract %q", want)
		}
	}
	if strings.Contains(build, `Set :CFBundleExecutable $GUARDNAME`) {
		t.Fatal("macOS package must not replace the Electron bundle executable with Guard")
	}
	launcherStamp := strings.Index(build, `stamp_windows_executable "$launcher_out" "Pondera Launcher"`)
	payloadCopy := strings.Index(build, `cp "$installer_dir/$name" "$payload_dir/$name"`)
	if launcherStamp < 0 || payloadCopy < 0 || launcherStamp > payloadCopy {
		t.Fatalf("Windows payload must copy the already-stamped launcher (stamp=%d copy=%d)", launcherStamp, payloadCopy)
	}
	if strings.Contains(build, `"$staging/$CLINAME.exe"`) {
		t.Fatal("Windows package must not collide pondera.exe with the Pondera.exe launcher")
	}
	darwinIconCheck := strings.Index(build, `[ -s "$app/Contents/Resources/$bundle_icon" ]`)
	developerIDSign := strings.Index(build, `node "$ROOT/desktop/packaging/sign-macos.mjs" "$app" "$identity"`)
	if darwinIconCheck < 0 || developerIDSign < 0 || darwinIconCheck > developerIDSign {
		t.Fatalf("macOS bundle icon must be verified before signing (icon=%d sign=%d)", darwinIconCheck, developerIDSign)
	}
	for _, copyCommand := range []string{
		`cp "$service_out" "$app/Contents/Resources/service/$BINNAME"`,
		`ln -s "../Resources/service/$BINNAME" "$app/Contents/MacOS/$BINNAME"`,
		`cp "$cli_out" "$app/Contents/Resources/service/$CLINAME"`,
	} {
		if index := strings.Index(build, copyCommand); index < 0 || index > developerIDSign {
			t.Errorf("macOS sidecar must be installed before signing: %s", copyCommand)
		}
	}

	for _, want := range []string{
		`dpkg-deb --field "$deb_path" Package | grep -x 'pondera-desktop'`,
		`usr/lib/pondera/pondera-update-helper`,
		`usr/share/polkit-1/actions/io.pondera.desktop.update.policy`,
	} {
		if !strings.Contains(build, want) {
			t.Errorf("desktop-build.sh missing Linux deb helper contract %q", want)
		}
	}
	for _, unsafe := range []string{
		`dpkg-deb --field "$deb_path" Package | grep -qx`,
		`dpkg-deb --field "$deb_path" Version | grep -qx`,
		`dpkg-deb --field "$deb_path" Depends | grep -Fq`,
		`dpkg-deb --contents "$deb_path" | grep -Eq`,
	} {
		if strings.Contains(build, unsafe) {
			t.Errorf("desktop-build.sh uses early-exit grep under pipefail: %q", unsafe)
		}
	}

	desktopEntry, err := os.ReadFile("build/linux/pondera.desktop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(desktopEntry), "Exec=pondera-launcher") || strings.Contains(string(desktopEntry), "pondera-guard") {
		t.Fatal("Linux desktop entry must launch the permanent launcher without Guard")
	}
	nfpmData, err := os.ReadFile("build/linux/nfpm.yaml")
	if err != nil {
		t.Fatal(err)
	}
	nfpm := string(nfpmData)
	if !strings.Contains(nfpm, "dst: /usr/bin/pondera-launcher") || strings.Contains(nfpm, "dst: /usr/bin/pondera-guard") {
		t.Fatal("Linux deb must install the permanent launcher and must not persist Guard")
	}
	if !strings.Contains(nfpm, "postinstall: ./build/linux/postinstall.sh") {
		t.Fatal("Linux deb must refresh native desktop icon caches after install and upgrade")
	}
	if !strings.Contains(nfpm, "dst: /usr/share/applications/pondera.desktop") {
		t.Fatal("Linux deb must install the Pondera desktop entry")
	}
	postInstall, err := os.ReadFile("build/linux/postinstall.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gtk-update-icon-cache", "update-desktop-database"} {
		if !strings.Contains(string(postInstall), want) {
			t.Errorf("Linux post-install icon repair missing %q", want)
		}
	}

	windowsData, err := os.ReadFile("build/windows/installer/project.nsi")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(windowsData, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal("Windows installer script must have a UTF-8 BOM so native makensis accepts localized strings")
	}
	windows := string(windowsData)
	for _, want := range []string{
		`File "/oname=${PONDERA_CLI}" "${PONDERA_CLI}"`,
		`!define PONDERA_UNINST_FINALIZE 'cmd.exe /C copy /Y "%1" "pondera-uninstall.exe" >NUL'`,
		`!uninstfinalize '${PONDERA_UNINST_FINALIZE}'`,
		`File "/oname=uninstall.exe" "${ARG_PONDERA_SIGNED_UNINSTALLER}"`,
		`StrCpy $R9 "$INSTDIR\versions\.installer-${PONDERA_VERSION_TAG}-$R8"`,
		`File "/oname=${PONDERA_LAYOUT_INSTALLER}" "${PONDERA_GUARD}"`,
		`nsExec::ExecToLog /OEM`,
		`Pondera layout activator output:`,
		`--activate-staging "$R9" --no-relaunch`,
		`CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" "" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" 0`,
		`CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" "" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" 0`,
		`StrCmp $PonderaStageMode "1" pondera_stage_payload`,
		`File "/oname=${PONDERA_GUARD}" "${PONDERA_GUARD}"`,
	} {
		if !strings.Contains(windows, want) {
			t.Errorf("Windows installer missing versioned-layout contract %q", want)
		}
	}
	if strings.Contains(windows, `FileOpen $0 "$INSTDIR\current.json" w`) ||
		strings.Contains(windows, `SetOutPath "$INSTDIR\versions\v${INFO_PRODUCTVERSION}"`) {
		t.Fatal("normal Windows installer must not write the live version or current.json in place")
	}
	for _, leak := range []string{
		`$INSTDIR\versions\v${INFO_PRODUCTVERSION}`,
		`.installer-v${INFO_PRODUCTVERSION}`,
		`--version "v${INFO_PRODUCTVERSION}"`,
	} {
		if strings.Contains(windows, leak) {
			t.Errorf("numeric Windows resource version leaked into release identity: %q", leak)
		}
	}
	if strings.Contains(windows, `ExecWait '"$PLUGINSDIR\${PONDERA_LAYOUT_INSTALLER}"`) {
		t.Fatal("Windows installer must not discard layout activator stdout/stderr")
	}
	if strings.Contains(windows, `CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PONDERA_LAUNCHER}" "" "$INSTDIR\versions\v${INFO_PRODUCTVERSION}\${PRODUCT_EXECUTABLE}" 0`) ||
		strings.Contains(windows, `CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PONDERA_LAUNCHER}" "" "$INSTDIR\versions\v${INFO_PRODUCTVERSION}\${PRODUCT_EXECUTABLE}" 0`) {
		t.Fatal("Windows shortcut icon must not point into a version directory that retention removes")
	}
}
