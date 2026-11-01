Unicode true

SetCompressor /SOLID /FINAL lzma
SetCompressorDictSize 32

####
## Pondera per-user NSIS installer (Electron shell).
##
## This file is COMMITTED and fully self-contained: the Electron packaging
## script (desktop/packaging/package.mjs) generates pondera_project.nsh with
## the INFO_* identity defines, and every macro the old Wails template provided
## is inlined below. The customizations vs. a stock NSIS template:
##
##   1. REQUEST_EXECUTION_LEVEL "user" + InstallDir under $LOCALAPPDATA - install
##      without administrator rights. This lets the auto-updater re-run a freshly
##      downloaded installer in a visible progress-only mode with no UAC prompt.
##   2. Uninstall registry under HKCU (not HKLM) - a non-admin install cannot
##      write HKLM, so the uninstaller macros below use HKCU.
##   3. InstallDir is remembered across updates via InstallDirRegKey +
##      InstallLocation (HKCU\...\Uninstall\InstallLocation). When upgrading from
##      a build that did not write InstallLocation yet, .onInit falls back to the
##      old DisplayIcon path before using the default. Without this, every release
##      forces the user back to %LOCALAPPDATA%\Programs\Pondera even if they had
##      moved the install to a different drive (e.g. D:\Tools\Pondera); the
##      auto-updater would overwrite the wrong dir, leaving the old install
##      orphaned.
##   4. The payload is the flat Go executables plus the Electron app/ tree,
##      installed recursively with `File /r` into the versioned staging
##      directory that the signed Go activator publishes as versions/v<ver>/.
####

## Install per-user (no admin).
!define REQUEST_EXECUTION_LEVEL "user"

####
## Product identity. PONDERA_VERSION_TAG is the release/install identity;
## INFO_PRODUCTVERSION is numeric metadata only.
####
!if /FileExists "pondera_project.nsh"
!include "pondera_project.nsh"
!else
!error "pondera_project.nsh is missing; run desktop/packaging/package.mjs first"
!endif
!include "x64.nsh"
!include "WinVer.nsh"
!include "FileFunc.nsh"
!include "LogicLib.nsh"

# The build script writes this host-specific include before invoking makensis.
# Keep a Windows fallback so opening this script directly still behaves like a
# native Windows build.
!if /FileExists "pondera_host.nsh"
!include "pondera_host.nsh"
!endif
!ifndef PONDERA_UNINST_FINALIZE
!define PONDERA_UNINST_FINALIZE 'cmd.exe /C copy /Y "%1" "pondera-uninstall.exe" >NUL'
!endif

# The service executable stays the active version entry the thin launcher
# starts; it bootstraps app\Pondera.exe (Electron) and exits.
!define PRODUCT_EXECUTABLE "${INFO_PROJECTNAME}.exe"
!define PONDERA_ELECTRON_EXECUTABLE "Pondera.exe"
!define UNINST_KEY_NAME "${INFO_COMPANYNAME}${INFO_PRODUCTNAME}"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${UNINST_KEY_NAME}"
RequestExecutionLevel "${REQUEST_EXECUTION_LEVEL}"

# Exactly one target architecture per installer, selected by the build script
# through the binary define it passes (values point at the staged service
# executable; only their presence selects the architecture).
!ifdef ARG_PONDERA_AMD64_BINARY
!define ARCH "amd64"
!endif
!ifdef ARG_PONDERA_ARM64_BINARY
!define ARCH "arm64"
!endif
!ifndef ARCH
!error "one of ARG_PONDERA_AMD64_BINARY or ARG_PONDERA_ARM64_BINARY is required; package-windows-desktop.sh passes it"
!endif

!macro pondera.checkArchitecture
    ${If} ${AtLeastWin10}
        !if "${ARCH}" == "amd64"
            ${if} ${IsNativeAMD64}
                Goto pondera_arch_ok
            ${EndIf}
        !else
            ${if} ${IsNativeARM64}
                Goto pondera_arch_ok
            ${EndIf}
        !endif

        IfSilent pondera_arch_silent pondera_arch_interactive
        pondera_arch_silent:
            SetErrorLevel 65
            Abort
        pondera_arch_interactive:
            MessageBox MB_OK "This product can't be installed on the current Windows architecture. Supports: ${ARCH}"
            Quit
    ${else}
        IfSilent pondera_win_silent pondera_win_interactive
        pondera_win_silent:
            SetErrorLevel 64
            Abort
        pondera_win_interactive:
            MessageBox MB_OK "This product is only supported on Windows 10 (Server 2016) and later."
            Quit
    ${EndIf}

    pondera_arch_ok:
!macroend

!macro pondera.setShellContext
    ${If} ${REQUEST_EXECUTION_LEVEL} == "admin"
        SetShellVarContext all
    ${else}
        SetShellVarContext current
    ${EndIf}
!macroend

# The release unit: the Go service executable plus the Electron app/ tree.
# package-windows-desktop.sh stages both next to this script before makensis.
!macro pondera.files
    File "/oname=${PRODUCT_EXECUTABLE}" "${PRODUCT_EXECUTABLE}"
    !if /FileExists "app\${PONDERA_ELECTRON_EXECUTABLE}"
    File /r "app"
    !else
    !error "the Electron app tree is missing; run desktop/packaging/package.mjs first"
    !endif
!macroend

# Pondera registers no file associations or custom protocols; keep the hooks
# as no-ops so the install/uninstall flow keeps its shape.
!macro pondera.associateFiles
!macroend

!macro pondera.unassociateFiles
!macroend

!macro pondera.associateCustomProtocols
!macroend

!macro pondera.unassociateCustomProtocols
!macroend

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${PONDERA_DISPLAY_VERSION}"
VIAddVersionKey "FileVersion"     "${PONDERA_DISPLAY_VERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
# !define MUI_WELCOMEFINISHPAGE_BITMAP "resources\leftimage.bmp" #Include this to add a bitmap on the left side of the Welcome Page. Must be a size of 164x314
!define MUI_FINISHPAGE_NOAUTOCLOSE # Wait on the INSTFILES page so the user can take a look into the details of the installation steps
!define MUI_ABORTWARNING # This will warn the user if they exit from the installer.

!define MUI_PAGE_CUSTOMFUNCTION_PRE pondera.skipSetupPageForUpdate
!insertmacro MUI_PAGE_WELCOME # Welcome to the installer page.
# !insertmacro MUI_PAGE_LICENSE "resources\eula.txt" # Adds a EULA page to the installer
!define MUI_PAGE_CUSTOMFUNCTION_PRE pondera.skipSetupPageForUpdate
!insertmacro MUI_PAGE_DIRECTORY # In which folder install page.
!define MUI_PAGE_CUSTOMFUNCTION_SHOW pondera.showUpdateProgress
!insertmacro MUI_PAGE_INSTFILES # Installing page.
!define MUI_PAGE_CUSTOMFUNCTION_PRE pondera.skipFinishPageForUpdate
!insertmacro MUI_PAGE_FINISH # Finished installation page.

!insertmacro MUI_UNPAGE_INSTFILES # Uinstalling page

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "TradChinese"

LangString ponderaUpdateTitle ${LANG_ENGLISH} "Updating Pondera"
LangString ponderaUpdateTitle ${LANG_SIMPCHINESE} "正在更新 Pondera"
LangString ponderaUpdateTitle ${LANG_TRADCHINESE} "正在更新 Pondera"
LangString ponderaUpdateSubtitle ${LANG_ENGLISH} "Installing the verified update. Pondera will restart automatically."
LangString ponderaUpdateSubtitle ${LANG_SIMPCHINESE} "正在安装已验证的更新，完成后 Pondera 将自动重启。"
LangString ponderaUpdateSubtitle ${LANG_TRADCHINESE} "正在安裝已驗證的更新，完成後 Pondera 將自動重新啟動。"
LangString ponderaActivateBusy ${LANG_ENGLISH} "Pondera is still running or another installation is in progress. Close it and click Retry. Details: %APPDATA%\pondera\desktop-shell\logs\recovery.log"
LangString ponderaActivateBusy ${LANG_SIMPCHINESE} "Pondera 仍在运行，或另一个安装正在进行。请关闭后点击“重试”。详情见 %APPDATA%\pondera\desktop-shell\logs\recovery.log"
LangString ponderaActivateBusy ${LANG_TRADCHINESE} "Pondera 仍在執行，或另一個安裝正在進行。請關閉後點擊「重試」。詳情見 %APPDATA%\pondera\desktop-shell\logs\recovery.log"
LangString ponderaActivateLocked ${LANG_ENGLISH} "Pondera could not activate the release, often because a file was temporarily locked by antivirus or sync software. Wait a moment and click Retry. Details: %APPDATA%\pondera\desktop-shell\logs\recovery.log"
LangString ponderaActivateLocked ${LANG_SIMPCHINESE} "Pondera 无法启用新版本，通常是文件被杀毒或同步软件临时锁定。请稍候再点击“重试”。详情见 %APPDATA%\pondera\desktop-shell\logs\recovery.log"
LangString ponderaActivateLocked ${LANG_TRADCHINESE} "Pondera 無法啟用新版本，通常是檔案被防毒或同步軟體暫時鎖定。請稍候再點擊「重試」。詳情見 %APPDATA%\pondera\desktop-shell\logs\recovery.log"

## Preserve the first-pass generated uninstaller so the release workflow can
## Authenticode-sign it together with the other installed payload files.
## The second pass provides ARG_PONDERA_SIGNED_UNINSTALLER and embeds that
## signed binary instead of generating another unsigned uninstaller.
!ifndef ARG_PONDERA_SIGNED_UNINSTALLER
!uninstfinalize '${PONDERA_UNINST_FINALIZE}'
!endif
#!finalize 'signtool --file "%1"'

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe" # Name of the installer's file.
!define PONDERA_DEFAULT_INSTALLDIR "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!define PONDERA_UPDATE_HELPER "pondera-update-helper.exe"
!define PONDERA_GUARD "pondera-guard.exe"
!define PONDERA_LAUNCHER "pondera-launcher.exe"
!define PONDERA_CLI "pondera-cli.exe"
!define PONDERA_PORTABLE_ENTRY "Pondera.exe"
!define PONDERA_LAYOUT_INSTALLER "pondera-layout-installer.exe"
!define PONDERA_PAYLOAD_MANIFEST "pondera-payload.json"
!define PONDERA_PAYLOAD_SIGNATURE "pondera-payload.json.minisig"
!define PONDERA_LEGACY_UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Pondera"
!define PONDERA_LEGACY_PRODUCT_KEY "Software\pondera\Pondera"
Var PonderaUpdateMode
Var PonderaStageMode
InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation" # Reuse the previous install path on update; .onInit falls back to the default on first install.
InstallDir "${PONDERA_DEFAULT_INSTALLDIR}" # Per-user install location (no admin rights required).
ShowInstDetails show # This will always show the installation details.

####
## Per-user uninstaller registry (HKCU). HKLM writes would fail without admin
## rights, so the uninstaller registration lives entirely under HKCU.
####
!macro pondera.writeUninstaller
    !ifdef ARG_PONDERA_SIGNED_UNINSTALLER
    File "/oname=uninstall.exe" "${ARG_PONDERA_SIGNED_UNINSTALLER}"
    !else
    WriteUninstaller "$INSTDIR\uninstall.exe"
    !endif

    WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${PONDERA_DISPLAY_VERSION}"
    !if /FileExists "${PONDERA_LAUNCHER}"
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}"
    !else
    WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    !endif
    WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
    WriteRegStr HKCU "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
    # Persist the resolved install path so a subsequent update picks it up
    # via InstallDirRegKey above. Without this, every release would force the
    # user back to %LOCALAPPDATA%\Programs\Pondera even if they had moved
    # the install to a different drive (e.g. D:\Tools\Pondera). The auto-
    # updater trusts this persisted path, so it has to be present before the
    # visible progress-only re-install.
    WriteRegStr HKCU "${UNINST_KEY}" "InstallLocation" "$INSTDIR"

    ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
    IntFmt $0 "0x%08X" $0
    WriteRegDWORD HKCU "${UNINST_KEY}" "EstimatedSize" "$0"
!macroend

; Tauri 0.53 separately persisted $INSTDIR under its manufacturer/product key
; and restores that value before every later install. Clear only a same-root
; value so re-running 0.53 cannot overwrite the current uninstaller; preserve a
; genuinely separate legacy installation. If this cleanup fails, retain the old
; uninstall alias so a later update can retry the migration.
!macro pondera.deleteLegacyInstallerStateIfOwned
    StrCpy $1 "1"
    ClearErrors
    ReadRegStr $0 HKCU "${PONDERA_LEGACY_PRODUCT_KEY}" ""
    ${If} $0 == "$INSTDIR"
        ClearErrors
        DeleteRegValue HKCU "${PONDERA_LEGACY_PRODUCT_KEY}" ""
        ${If} ${Errors}
            StrCpy $1 "0"
        ${EndIf}
    ${ElseIf} $0 == "$\"$INSTDIR$\""
        ClearErrors
        DeleteRegValue HKCU "${PONDERA_LEGACY_PRODUCT_KEY}" ""
        ${If} ${Errors}
            StrCpy $1 "0"
        ${EndIf}
    ${EndIf}

    ${If} $1 == "1"
        ClearErrors
        ReadRegStr $0 HKCU "${PONDERA_LEGACY_UNINST_KEY}" "InstallLocation"
        ${If} $0 == "$INSTDIR"
            DeleteRegKey HKCU "${PONDERA_LEGACY_UNINST_KEY}"
        ${ElseIf} $0 == "$\"$INSTDIR$\""
            DeleteRegKey HKCU "${PONDERA_LEGACY_UNINST_KEY}"
        ${Else}
            ClearErrors
            ReadRegStr $0 HKCU "${PONDERA_LEGACY_UNINST_KEY}" "UninstallString"
            ${If} $0 == "$INSTDIR\uninstall.exe"
                DeleteRegKey HKCU "${PONDERA_LEGACY_UNINST_KEY}"
            ${ElseIf} $0 == "$\"$INSTDIR\uninstall.exe$\""
                DeleteRegKey HKCU "${PONDERA_LEGACY_UNINST_KEY}"
            ${EndIf}
        ${EndIf}
    ${EndIf}
!macroend

!macro pondera.deleteUninstaller
    Delete "$INSTDIR\uninstall.exe"
    DeleteRegKey HKCU "${UNINST_KEY}"
!macroend

Function .onInit
   !ifdef ARG_PONDERA_UNINSTALLER_ONLY
   ; This compiler artifact exists only to extract the shared uninstaller.
   ; It is never an installable or publishable product.
   Quit
   !endif
   !insertmacro pondera.checkArchitecture

   ; The helper passes /PONDERAUPDATE=1 and a final /D=<current directory>.
   ; This mode remains visible but skips every page that could change the
   ; destination, then closes automatically after the file copy so the helper
   ; can relaunch Pondera. A normal manual installer keeps the full wizard.
   StrCpy $PonderaUpdateMode "0"
   StrCpy $PonderaStageMode "0"
   ${GetParameters} $R0
   ClearErrors
   ${GetOptions} $R0 "/PONDERAUPDATE=" $R1
   IfErrors pondera_update_mode_done
   StrCmp $R1 "1" 0 pondera_update_mode_done
   StrCpy $PonderaUpdateMode "1"

pondera_update_mode_done:
   ClearErrors
   ${GetOptions} $R0 "/PONDERASTAGE=" $R2
   IfErrors pondera_stage_mode_done
   StrCmp $R2 "1" 0 pondera_stage_mode_done
   StrCpy $PonderaStageMode "1"

pondera_stage_mode_done:

   ; InstallDirRegKey leaves $INSTDIR empty when the InstallLocation value is
   ; missing. Older installers still wrote DisplayIcon, so use its parent folder
   ; as a compatibility bridge before falling back to the per-user default.
   StrCmp $INSTDIR "" 0 done
   ClearErrors
   ReadRegStr $0 HKCU "${UNINST_KEY}" "DisplayIcon"
   IfErrors legacy_location
   StrCmp $0 "" legacy_location
   ${GetParent} "$0" $INSTDIR
   StrCmp $INSTDIR "" legacy_location done

legacy_location:
   ; Tauri 0.53 used a different uninstall key and may have stored the selected
   ; directory with surrounding quotes (for example "D:\Pondera"). Reuse it
   ; only while its uninstaller still exists so a stale registry value cannot
   ; redirect the repair installer into an unrelated directory.
   ClearErrors
   ReadRegStr $0 HKCU "${PONDERA_LEGACY_UNINST_KEY}" "InstallLocation"
   IfErrors legacy_uninstaller
   StrCmp $0 "" legacy_uninstaller
   StrCpy $1 $0 1
   StrCmp $1 "$\"" 0 legacy_location_ready
   StrCpy $1 $0 1 -1
   StrCmp $1 "$\"" 0 legacy_location_ready
   StrCpy $0 $0 -1 1

legacy_location_ready:
   IfFileExists "$0\uninstall.exe" 0 legacy_uninstaller
   StrCpy $INSTDIR $0
   Goto done

legacy_uninstaller:
   ClearErrors
   ReadRegStr $0 HKCU "${PONDERA_LEGACY_UNINST_KEY}" "UninstallString"
   IfErrors fallback
   StrCmp $0 "" fallback
   StrCpy $1 $0 1
   StrCmp $1 "$\"" 0 legacy_uninstaller_ready
   StrCpy $1 $0 1 -1
   StrCmp $1 "$\"" 0 legacy_uninstaller_ready
   StrCpy $0 $0 -1 1

legacy_uninstaller_ready:
   IfFileExists "$0" 0 fallback
   ${GetParent} "$0" $INSTDIR
   StrCmp $INSTDIR "" fallback done

fallback:
   StrCpy $INSTDIR "${PONDERA_DEFAULT_INSTALLDIR}"
done:
FunctionEnd

Function pondera.skipSetupPageForUpdate
   StrCmp $PonderaUpdateMode "1" 0 pondera_show_setup_page
   Abort

pondera_show_setup_page:
FunctionEnd

Function pondera.showUpdateProgress
   StrCmp $PonderaUpdateMode "1" 0 pondera_update_progress_done
   !insertmacro MUI_HEADER_TEXT "$(ponderaUpdateTitle)" "$(ponderaUpdateSubtitle)"
   SetDetailsView hide
   SetAutoClose true
   BringToFront

pondera_update_progress_done:
FunctionEnd

Function pondera.skipFinishPageForUpdate
   StrCmp $PonderaUpdateMode "1" 0 pondera_show_finish_page
   Abort

pondera_show_finish_page:
FunctionEnd

# Check every stable entry point before extracting a replacement.  A running
# shell may have already exited its Go service while still holding one of
# these files open; treating that as an installable state recreates the
# "installed but does not open" failure.  Silent installs fail closed.
Function pondera.waitForExecutableUnlock
   StrCpy $3 40
pondera_unlock_check:
   StrCpy $2 0
   IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 pondera_unlock_versioned
   ClearErrors
   FileOpen $1 "$INSTDIR\${PRODUCT_EXECUTABLE}" a
   IfErrors pondera_unlock_stable_locked
   FileClose $1
   Goto pondera_unlock_versioned
pondera_unlock_stable_locked:
   StrCpy $2 1
pondera_unlock_versioned:
   IfFileExists "$INSTDIR\versions\${PONDERA_VERSION_TAG}\${PRODUCT_EXECUTABLE}" 0 pondera_unlock_guard
   ClearErrors
   FileOpen $1 "$INSTDIR\versions\${PONDERA_VERSION_TAG}\${PRODUCT_EXECUTABLE}" a
   IfErrors pondera_unlock_versioned_locked
   FileClose $1
   Goto pondera_unlock_guard
pondera_unlock_versioned_locked:
   StrCpy $2 1
pondera_unlock_guard:
   IfFileExists "$INSTDIR\${PONDERA_GUARD}" 0 pondera_unlock_launcher
   ClearErrors
   FileOpen $1 "$INSTDIR\${PONDERA_GUARD}" a
   IfErrors pondera_unlock_guard_locked
   FileClose $1
   Goto pondera_unlock_launcher
pondera_unlock_guard_locked:
   StrCpy $2 1
pondera_unlock_launcher:
   IfFileExists "$INSTDIR\${PONDERA_LAUNCHER}" 0 pondera_unlock_cli
   ClearErrors
   FileOpen $1 "$INSTDIR\${PONDERA_LAUNCHER}" a
   IfErrors pondera_unlock_launcher_locked
   FileClose $1
   Goto pondera_unlock_cli
pondera_unlock_launcher_locked:
   StrCpy $2 1
pondera_unlock_cli:
   IfFileExists "$INSTDIR\${PONDERA_CLI}" 0 pondera_unlock_portable
   ClearErrors
   FileOpen $1 "$INSTDIR\${PONDERA_CLI}" a
   IfErrors pondera_unlock_cli_locked
   FileClose $1
   Goto pondera_unlock_portable
pondera_unlock_cli_locked:
   StrCpy $2 1
pondera_unlock_portable:
   IfFileExists "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" 0 pondera_unlock_result
   ClearErrors
   FileOpen $1 "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" a
   IfErrors pondera_unlock_portable_locked
   FileClose $1
   Goto pondera_unlock_result
pondera_unlock_portable_locked:
   StrCpy $2 1
pondera_unlock_result:
   StrCmp $2 0 pondera_unlock_ok
   IntOp $3 $3 - 1
   IntCmp $3 0 pondera_unlock_failed pondera_unlock_retry pondera_unlock_retry
pondera_unlock_retry:
   Sleep 500
   Goto pondera_unlock_check
pondera_unlock_failed:
   SetErrorLevel 1618
   IfSilent pondera_unlock_abort pondera_unlock_prompt
pondera_unlock_prompt:
   MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "Pondera is still running. Close it and click Retry, or cancel this installation." IDRETRY pondera_unlock_check
pondera_unlock_abort:
   Abort
pondera_unlock_ok:
FunctionEnd


!ifdef ARG_PONDERA_UNINSTALLER_ONLY
Section
    WriteUninstaller "$INSTDIR\uninstall.exe"
SectionEnd
!else
Section
    !insertmacro pondera.setShellContext

    ; /PONDERASTAGE=1: flat executables plus the Electron app/ tree for
    ; 1.18–1.19.1 helpers (and the new helper's staging extract). Do not write
    ; shortcuts/uninstaller.
    ; Normal install: versioned-v1 layout under versions/${PONDERA_VERSION_TAG}/
    ; with a permanent thin launcher at InstallRoot. Guard is only present in
    ; STAGE payloads (as the one-shot legacy migrator) and is not persisted on
    ; a normal install.
    StrCmp $PonderaStageMode "1" pondera_stage_payload
    ; The signed activator coordinates all installed versions before committing.
    Call pondera.waitForExecutableUnlock
    Goto pondera_normal_install

pondera_stage_payload:
    SetOutPath $INSTDIR
    !if /FileExists "${PONDERA_PAYLOAD_MANIFEST}"
    File "/oname=${PONDERA_PAYLOAD_MANIFEST}" "${PONDERA_PAYLOAD_MANIFEST}"
    !endif
    !if /FileExists "${PONDERA_PAYLOAD_SIGNATURE}"
    File "/oname=${PONDERA_PAYLOAD_SIGNATURE}" "${PONDERA_PAYLOAD_SIGNATURE}"
    !endif
    !insertmacro pondera.files
    !if /FileExists "${PONDERA_UPDATE_HELPER}"
    File "/oname=${PONDERA_UPDATE_HELPER}" "${PONDERA_UPDATE_HELPER}"
    !endif
    !if /FileExists "${PONDERA_GUARD}"
    File "/oname=${PONDERA_GUARD}" "${PONDERA_GUARD}"
    !endif
    !if /FileExists "${PONDERA_LAUNCHER}"
    File "/oname=${PONDERA_LAUNCHER}" "${PONDERA_LAUNCHER}"
    !endif
    !if /FileExists "${PONDERA_CLI}"
    File "/oname=${PONDERA_CLI}" "${PONDERA_CLI}"
    !endif
    Goto pondera_section_done

pondera_normal_install:
    ; Extract into an install-local temporary directory, then let the signed Go
    ; activator validate the complete release unit, transactionally publish the
    ; version/root entries, and strictly atomically replace current.json last.
    ; The normal/recovery installer therefore shares the same commit protocol as
    ; automatic updates instead of writing live files or current.json in place.
    System::Call 'kernel32::GetCurrentProcessId() i .R8'
    CreateDirectory "$INSTDIR\versions"
    StrCpy $R9 "$INSTDIR\versions\.installer-${PONDERA_VERSION_TAG}-$R8"
    RMDir /r "$R9"
    CreateDirectory "$R9"
    SetOutPath "$R9"
    !insertmacro pondera.files
    !if /FileExists "${PONDERA_UPDATE_HELPER}"
    File "/oname=${PONDERA_UPDATE_HELPER}" "${PONDERA_UPDATE_HELPER}"
    !else
    !warning "${PONDERA_UPDATE_HELPER} was not found; Windows auto-update will fail safely until the helper is installed."
    !endif
    !if /FileExists "${PONDERA_CLI}"
    File "/oname=${PONDERA_CLI}" "${PONDERA_CLI}"
    !else
    !warning "${PONDERA_CLI} was not found; remote upload installation will be unavailable."
    !endif
    !if /FileExists "${PONDERA_LAUNCHER}"
    File "/oname=${PONDERA_LAUNCHER}" "${PONDERA_LAUNCHER}"
    !endif

    SetOutPath "$PLUGINSDIR"
    !if /FileExists "${PONDERA_GUARD}"
    File "/oname=${PONDERA_LAYOUT_INSTALLER}" "${PONDERA_GUARD}"
    !else
    !error "${PONDERA_GUARD} was not found; normal installs require the signed layout activator."
    !endif
    DetailPrint "Pondera layout activator output:"
    StrCpy $R7 ""
    IfSilent +2 0
    StrCpy $R7 "--interactive-recovery"
pondera_layout_activate:
    nsExec::ExecToLog /OEM '"$PLUGINSDIR\${PONDERA_LAYOUT_INSTALLER}" --install-root "$INSTDIR" --version "${PONDERA_VERSION_TAG}" --activate-staging "$R9" --no-relaunch $R7'
    Pop $0
    StrCmp $0 "0" pondera_layout_activated
    DetailPrint "Pondera layout activation failed with exit code $0; the previous version remains active."
    ; 1602 is the user's own cancel in the recovery dialog. Every other failure
    ; keeps $R9 so Retry re-runs the activator against the same verified files;
    ; the exit code is set only once the attempt is truly abandoned.
    StrCmp $0 "1602" pondera_activation_cancelled
    IfSilent pondera_activation_failed 0
    StrCmp $0 "1618" pondera_activation_busy_prompt pondera_activation_locked_prompt
pondera_activation_busy_prompt:
    MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(ponderaActivateBusy)" IDRETRY pondera_layout_activate
    Goto pondera_activation_failed
pondera_activation_locked_prompt:
    MessageBox MB_ICONEXCLAMATION|MB_RETRYCANCEL "$(ponderaActivateLocked)" IDRETRY pondera_layout_activate
pondera_activation_failed:
    RMDir /r "$R9"
    StrCmp $0 "1618" 0 +3
    SetErrorLevel 1618
    Goto pondera_activation_abort
    SetErrorLevel 1
    Goto pondera_activation_abort
pondera_activation_cancelled:
    RMDir /r "$R9"
    SetErrorLevel 1602
pondera_activation_abort:
    Abort "Pondera could not activate the verified release. The previous version was left unchanged."

pondera_layout_activated:
    RMDir /r "$R9"
    SetOutPath "$INSTDIR"

    ; Remove flat leftovers from prior 1.18–1.19 installs when overwriting.
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Delete "$INSTDIR\${PONDERA_GUARD}"
    Delete "$INSTDIR\${PONDERA_UPDATE_HELPER}"

    !if /FileExists "${PONDERA_LAUNCHER}"
    ; Keep both target and icon on the stable launcher. Pointing IconLocation at
    ; versions\vX\pondera-desktop.exe leaves a blank shortcut as soon as version
    ; retention removes that directory after a later update.
    ; Preserve user arguments, icons and working directories on existing links;
    ; the owned-link repair below migrates their targets without replacing them.
    IfFileExists "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" +2 0
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" "" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" 0
    IfFileExists "$DESKTOP\${INFO_PRODUCTNAME}.lnk" +2 0
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" "" "$INSTDIR\${PONDERA_PORTABLE_ENTRY}" 0
    ; Stamp the exact paths created in this shell context before the user can pin them.
    nsExec::ExecToLog /OEM '"$INSTDIR\${PONDERA_PORTABLE_ENTRY}" --repair-shortcuts "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$DESKTOP\${INFO_PRODUCTNAME}.lnk"'
    Pop $0
    ${If} $0 != "0"
        DetailPrint "Warning: shortcut identity repair failed ($0); the next normal launch will retry."
    ${EndIf}
    !else
    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\versions\${PONDERA_VERSION_TAG}\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\versions\${PONDERA_VERSION_TAG}\${PRODUCT_EXECUTABLE}"
    !endif

    !insertmacro pondera.associateFiles
    !insertmacro pondera.associateCustomProtocols
    !insertmacro pondera.writeUninstaller
    !insertmacro pondera.deleteLegacyInstallerStateIfOwned

pondera_section_done:
SectionEnd
!endif

Section "uninstall"
    !insertmacro pondera.setShellContext

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the legacy webview data directory

    ; Precision uninstall: flat leftovers, thin entry points, and version trees.
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Delete "$INSTDIR\${PONDERA_UPDATE_HELPER}"
    Delete "$INSTDIR\${PONDERA_GUARD}"
    Delete "$INSTDIR\${PONDERA_LAUNCHER}"
    Delete "$INSTDIR\${PONDERA_CLI}"
    Delete "$INSTDIR\${PONDERA_PORTABLE_ENTRY}"
    Delete "$INSTDIR\current.json"
    RMDir /r "$INSTDIR\versions"

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro pondera.unassociateFiles
    !insertmacro pondera.unassociateCustomProtocols

    !insertmacro pondera.deleteUninstaller
    !insertmacro pondera.deleteLegacyInstallerStateIfOwned

    ; Only remove the installation directory if it is empty to prevent data loss
    RMDir $INSTDIR
SectionEnd
