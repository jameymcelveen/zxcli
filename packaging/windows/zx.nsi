; Windows installer for zx. Built by "make windows-installer".
; Installs for the current user (no admin prompt) and adds zx to the user PATH.

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef BIN
  !error "pass -DBIN=<path to zx.exe>"
!endif
!ifndef OUT
  !define OUT "zx_setup.exe"
!endif

!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\zx"

Unicode true
Name "zx ${VERSION}"
OutFile "${OUT}"
InstallDir "$LOCALAPPDATA\Programs\zx"
RequestExecutionLevel user
SetCompressor /SOLID lzma
ShowInstDetails show

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Section "Install"
  SetOutPath "$INSTDIR"
  File "/oname=zx.exe" "${BIN}"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; Add the install folder to the user PATH if it is not there yet.
  nsExec::ExecToLog `powershell -NoProfile -ExecutionPolicy Bypass -Command "$$d = '$INSTDIR'; $$p = [Environment]::GetEnvironmentVariable('Path', 'User'); if (-not $$p) { $$p = '' }; if (($$p -split ';') -notcontains $$d) { [Environment]::SetEnvironmentVariable('Path', ($$p.TrimEnd(';') + ';' + $$d).TrimStart(';'), 'User') }"`
  Pop $0

  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "zx"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "Jamey McElveen"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "URLInfoAbout" "https://github.com/jameymcelveen/zxcli"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1

  DetailPrint "zx is installed. Open a new terminal and run: zx"
SectionEnd

Section "Uninstall"
  ; Take the install folder back out of the user PATH.
  nsExec::ExecToLog `powershell -NoProfile -ExecutionPolicy Bypass -Command "$$d = '$INSTDIR'; $$p = [Environment]::GetEnvironmentVariable('Path', 'User'); if ($$p) { [Environment]::SetEnvironmentVariable('Path', (($$p -split ';' | Where-Object { $$_ -and $$_ -ne $$d }) -join ';'), 'User') }"`
  Pop $0

  Delete "$INSTDIR\zx.exe"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
SectionEnd
