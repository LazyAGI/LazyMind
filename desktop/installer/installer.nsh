!include "LogicLib.nsh"
!include "nsDialogs.nsh"
!include "WordFunc.nsh"
!include "FileFunc.nsh"
!insertmacro VersionCompare
!insertmacro GetParameters
!insertmacro GetOptions
!include "${BUILD_RESOURCES_DIR}\lazymind-installer-metadata.nsh"

!macro customHeader
  Var InstallerHelper
  !ifndef BUILD_UNINSTALLER
    Var InstallDataChoice
    Var InstallTypeChoice
    Var InstallSimpleRadio
    Var InstallFullRadio
    Var InstallKeepRadio
    Var InstallPurgeRadio
    Var InstalledVersion
    Var IsManualUpgrade
    Var LegacyUninstallString
    Var UpgradeUninstaller
  !else
    Var UninstallDataChoice
    Var UninstallKeepRadio
    Var UninstallPurgeRadio
  !endif

  LangString LMDataPageTitle ${LANG_ENGLISH} "Existing LazyMind data"
  LangString LMDataPageTitle ${LANG_SIMPCHINESE} "现有 LazyMind 数据"
  LangString LMDataPageText ${LANG_ENGLISH} "LazyMind found local application data at $LOCALAPPDATA\LazyMind. Choose how setup should handle it. Documents\LazyMind is never changed."
  LangString LMDataPageText ${LANG_SIMPCHINESE} "检测到 $LOCALAPPDATA\LazyMind。请选择安装程序如何处理这些本地数据。Documents\LazyMind 永远不会被更改。"
  LangString LMKeepData ${LANG_ENGLISH} "Keep existing data (recommended; schemas may be upgraded)"
  LangString LMKeepData ${LANG_SIMPCHINESE} "保留现有数据（推荐；数据库结构可能升级）"
  LangString LMPurgeData ${LANG_ENGLISH} "Delete all Local AppData, then install"
  LangString LMPurgeData ${LANG_SIMPCHINESE} "清除全部 Local AppData 后安装"
  LangString LMUninstallText ${LANG_ENGLISH} "Choose whether to remove only the program or also all data under $LOCALAPPDATA\LazyMind. Documents\LazyMind is never changed."
  LangString LMUninstallText ${LANG_SIMPCHINESE} "请选择只卸载程序，还是同时清除 $LOCALAPPDATA\LazyMind 下的全部数据。Documents\LazyMind 永远不会被更改。"
  LangString LMProgramOnly ${LANG_ENGLISH} "Uninstall the program only (recommended)"
  LangString LMProgramOnly ${LANG_SIMPCHINESE} "只卸载程序（推荐）"
  LangString LMProgramAndData ${LANG_ENGLISH} "Uninstall the program and delete Local AppData"
  LangString LMProgramAndData ${LANG_SIMPCHINESE} "卸载程序并清除 Local AppData"
  LangString LMProcessScanFailed ${LANG_ENGLISH} "LazyMind process detection failed. Setup cannot safely continue."
  LangString LMProcessScanFailed ${LANG_SIMPCHINESE} "LazyMind 进程检测失败，安装程序无法安全继续。"
  LangString LMForceStopFailed ${LANG_ENGLISH} "Some LazyMind processes could not be force-closed. Setup cannot safely continue."
  LangString LMForceStopFailed ${LANG_SIMPCHINESE} "部分 LazyMind 进程无法强制关闭，安装程序无法安全继续。"
  LangString LMUpgradeRepairFailed ${LANG_ENGLISH} "The previous LazyMind uninstaller could not be updated for a safe upgrade. Setup cannot continue."
  LangString LMUpgradeRepairFailed ${LANG_SIMPCHINESE} "无法更新旧版 LazyMind 卸载程序以安全升级，安装程序无法继续。"
  LangString LMPurgeFailed ${LANG_ENGLISH} "Local AppData could not be safely removed. No path outside LocalAppData\LazyMind was touched."
  LangString LMPurgeFailed ${LANG_SIMPCHINESE} "无法安全清除 Local AppData。未操作 LocalAppData\LazyMind 之外的任何路径。"
  LangString LMWarmupFailed ${LANG_ENGLISH} "LazyMind warmup failed or timed out. Installation will finish normally; the first launch may take longer."
  LangString LMWarmupFailed ${LANG_SIMPCHINESE} "LazyMind 预热失败或超时。安装将正常完成；首次启动可能需要更长时间。"
  LangString LMPreflightFailed ${LANG_ENGLISH} "LazyMind cannot be installed safely with the current temporary or installation path. Check that TEMP is on a writable local disk, at least ${LAZYMIND_MIN_FREE_SPACE_MB} MB is free, and the path is not too long."
  LangString LMPreflightFailed ${LANG_SIMPCHINESE} "当前临时目录或安装目录无法可靠安装 LazyMind。请确认 TEMP 位于可写的本地磁盘、至少有 ${LAZYMIND_MIN_FREE_SPACE_MB} MB 可用空间，并且路径不过长。"
  LangString LMDowngradeBlocked ${LANG_ENGLISH} "A newer LazyMind version is already installed. Downgrade is blocked to protect local data."
  LangString LMDowngradeBlocked ${LANG_SIMPCHINESE} "已安装更高版本的 LazyMind。为保护本地数据，禁止降级安装。"
  LangString LMUpgradePurgeBlocked ${LANG_ENGLISH} "Upgrades always keep Local AppData. Remove --purge-lazymind-local-data and retry."
  LangString LMUpgradePurgeBlocked ${LANG_SIMPCHINESE} "升级始终保留 Local AppData。请移除 --purge-lazymind-local-data 后重试。"
  LangString LMInstallTypePageTitle ${LANG_ENGLISH} "Installation type"
  LangString LMInstallTypePageTitle ${LANG_SIMPCHINESE} "安装方式"
  LangString LMInstallTypePageText ${LANG_ENGLISH} "Choose when LazyMind should prepare its bundled Python runtime."
  LangString LMInstallTypePageText ${LANG_SIMPCHINESE} "请选择 LazyMind 何时准备内置 Python 运行环境。"
  LangString LMSimpleInstall ${LANG_ENGLISH} "Simple installation (recommended)"
  LangString LMSimpleInstall ${LANG_SIMPCHINESE} "简易安装（推荐）"
  LangString LMSimpleInstallHelp ${LANG_ENGLISH} "Finish setup quickly without expanding Python files. Python is prepared automatically on first launch."
  LangString LMSimpleInstallHelp ${LANG_SIMPCHINESE} "安装时不展开 Python 文件，可避免防火墙扫描造成的长时间等待；首次启动时自动准备。"
  LangString LMFullInstall ${LANG_ENGLISH} "Full installation"
  LangString LMFullInstall ${LANG_SIMPCHINESE} "完整安装"
  LangString LMFullInstallHelp ${LANG_ENGLISH} "Expand Python files and warm up all local services before setup finishes, as in previous versions."
  LangString LMFullInstallHelp ${LANG_SIMPCHINESE} "安装时展开 Python 文件，并在安装结束前预热全部本地服务。"

  !ifndef BUILD_UNINSTALLER
  Function LMInstallTypePageCreate
    !insertmacro MUI_HEADER_TEXT "$(LMInstallTypePageTitle)" "$(LMInstallTypePageText)"
    nsDialogs::Create 1018
    Pop $0
    ${NSD_CreateLabel} 0 0 100% 18u "$(LMInstallTypePageText)"
    Pop $1
    ${NSD_CreateRadioButton} 8u 30u 92% 18u "$(LMSimpleInstall)"
    Pop $InstallSimpleRadio
    ${NSD_CreateLabel} 24u 50u 88% 30u "$(LMSimpleInstallHelp)"
    Pop $1
    ${NSD_CreateRadioButton} 8u 88u 92% 18u "$(LMFullInstall)"
    Pop $InstallFullRadio
    ${NSD_CreateLabel} 24u 108u 88% 30u "$(LMFullInstallHelp)"
    Pop $1
    ${If} $InstallTypeChoice == "full"
      ${NSD_Check} $InstallFullRadio
    ${Else}
      ${NSD_Check} $InstallSimpleRadio
    ${EndIf}
    nsDialogs::Show
  FunctionEnd

  Function LMInstallTypePageLeave
    ${NSD_GetState} $InstallFullRadio $0
    ${If} $0 == ${BST_CHECKED}
      StrCpy $InstallTypeChoice "full"
    ${Else}
      StrCpy $InstallTypeChoice "simple"
    ${EndIf}
  FunctionEnd

  Function LMInstallDataPageCreate
    ${If} $IsManualUpgrade == "1"
      Abort
    ${EndIf}
    ${IfNot} ${FileExists} "$LOCALAPPDATA\LazyMind\*.*"
      Abort
    ${EndIf}
    !insertmacro MUI_HEADER_TEXT "$(LMDataPageTitle)" "$(LMDataPageText)"
    nsDialogs::Create 1018
    Pop $0
    ${NSD_CreateLabel} 0 0 100% 42u "$(LMDataPageText)"
    Pop $1
    ${NSD_CreateRadioButton} 8u 52u 92% 18u "$(LMKeepData)"
    Pop $InstallKeepRadio
    ${NSD_CreateRadioButton} 8u 78u 92% 18u "$(LMPurgeData)"
    Pop $InstallPurgeRadio
    ${NSD_Check} $InstallKeepRadio
    nsDialogs::Show
  FunctionEnd

  Function LMInstallDataPageLeave
    ${NSD_GetState} $InstallPurgeRadio $0
    ${If} $0 == ${BST_CHECKED}
      StrCpy $InstallDataChoice "purge"
    ${Else}
      StrCpy $InstallDataChoice "keep"
    ${EndIf}
  FunctionEnd
  !else
  Function un.LMDataPageCreate
    ${If} ${isUpdated}
      Abort
    ${EndIf}
    !insertmacro MUI_HEADER_TEXT "$(LMDataPageTitle)" "$(LMUninstallText)"
    nsDialogs::Create 1018
    Pop $0
    ${NSD_CreateLabel} 0 0 100% 42u "$(LMUninstallText)"
    Pop $1
    ${NSD_CreateRadioButton} 8u 52u 92% 18u "$(LMProgramOnly)"
    Pop $UninstallKeepRadio
    ${NSD_CreateRadioButton} 8u 78u 92% 18u "$(LMProgramAndData)"
    Pop $UninstallPurgeRadio
    ${NSD_Check} $UninstallKeepRadio
    nsDialogs::Show
  FunctionEnd

  Function un.LMDataPageLeave
    ${NSD_GetState} $UninstallPurgeRadio $0
    ${If} $0 == ${BST_CHECKED}
      StrCpy $UninstallDataChoice "purge"
    ${Else}
      StrCpy $UninstallDataChoice "keep"
    ${EndIf}
  FunctionEnd
  !endif
!macroend

!macro customInstallMode
  StrCpy $isForceCurrentInstall "1"
!macroend

!macro customInit
  InitPluginsDir
  StrCpy $InstallDataChoice "keep"
  StrCpy $InstallTypeChoice "simple"
  StrCpy $IsManualUpgrade "0"
  StrCpy $InstallerHelper "$PLUGINSDIR\lazymind-installer-maintenance.exe"
  File /oname=$PLUGINSDIR\lazymind-installer-maintenance.exe "${BUILD_RESOURCES_DIR}\lazymind-installer-maintenance.exe"
  StrCpy $UpgradeUninstaller "$PLUGINSDIR\lazymind-upgrade-uninstaller.exe"
  File /oname=$PLUGINSDIR\lazymind-upgrade-uninstaller.exe "${UNINSTALLER_OUT_FILE}"

  nsExec::ExecToStack '"$InstallerHelper" preflight --install-dir "$INSTDIR" --temp-dir "$TEMP" --minimum-free-space-mb ${LAZYMIND_MIN_FREE_SPACE_MB} --maximum-relative-path-length ${LAZYMIND_MAX_RELATIVE_PATH_LENGTH}'
  Pop $0
  Pop $1
  ${If} $0 != 0
    DetailPrint "LazyMind installer preflight failed: $1"
    MessageBox MB_OK|MB_ICONSTOP "$(LMPreflightFailed)$\r$\n$1" /SD IDOK
    SetErrorLevel 9
    Quit
  ${EndIf}

  ReadRegStr $InstalledVersion HKCU "${UNINSTALL_REGISTRY_KEY}" "DisplayVersion"
  ReadRegStr $LegacyUninstallString HKCU "${UNINSTALL_REGISTRY_KEY}" "UninstallString"
  ${If} $InstalledVersion != ""
    ${VersionCompare} $InstalledVersion "${VERSION}" $0
    ${If} $0 == "1"
      MessageBox MB_OK|MB_ICONSTOP "$(LMDowngradeBlocked)" /SD IDOK
      SetErrorLevel 2
      Quit
    ${ElseIf} $0 == "2"
      StrCpy $IsManualUpgrade "1"
    ${EndIf}
  ${EndIf}

  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "--full-install" $1
  ${IfNot} ${Errors}
    StrCpy $InstallTypeChoice "full"
  ${EndIf}
  ClearErrors
  ${GetOptions} $0 "--simple-install" $1
  ${IfNot} ${Errors}
    StrCpy $InstallTypeChoice "simple"
  ${EndIf}
  ClearErrors
  ${GetOptions} $0 "--purge-lazymind-local-data" $1
  ${IfNot} ${Errors}
    ${If} $IsManualUpgrade == "1"
      MessageBox MB_OK|MB_ICONSTOP "$(LMUpgradePurgeBlocked)" /SD IDOK
      SetErrorLevel 2
      Quit
    ${EndIf}
    StrCpy $InstallDataChoice "purge"
  ${EndIf}
!macroend

!macro customPageAfterChangeDir
  PageEx custom
    PageCallbacks LMInstallTypePageCreate LMInstallTypePageLeave
  PageExEnd
  PageEx custom
    PageCallbacks LMInstallDataPageCreate LMInstallDataPageLeave
  PageExEnd
!macroend

!macro customCheckAppRunning
    ; Silent upgrade uninstallers call this macro before customUnInit. Always
    ; initialize the helper here so the check does not depend on hook order.
    InitPluginsDir
    StrCpy $InstallerHelper "$PLUGINSDIR\lazymind-installer-maintenance.exe"
    File /oname=$PLUGINSDIR\lazymind-installer-maintenance.exe "${BUILD_RESOURCES_DIR}\lazymind-installer-maintenance.exe"

    StrCpy $2 0
  LMCheckStopped:
    nsExec::ExecToStack '"$InstallerHelper" check-stopped --install-dir "$INSTDIR"'
    Pop $0
    Pop $1
    ${If} $0 == 10
      DetailPrint "Running LazyMind processes: $1"
      IntOp $2 $2 + 1
      ${If} $2 > 3
        DetailPrint "LazyMind processes restarted repeatedly after force-stop: $1"
        MessageBox MB_OK|MB_ICONSTOP "$(LMForceStopFailed)$\r$\n$1" /SD IDOK
        SetErrorLevel 7
        Quit
      ${EndIf}
      Goto LMForceStop
    ${ElseIf} $0 != 0
      DetailPrint "LazyMind process detection failed: $1"
      MessageBox MB_OK|MB_ICONSTOP "$(LMProcessScanFailed)$\r$\n$1" /SD IDOK
      SetErrorLevel 6
      Quit
    ${EndIf}
    Goto LMProcessCheckDone

  LMForceStop:
    DetailPrint "Force-closing running LazyMind processes..."
    nsExec::ExecToStack '"$InstallerHelper" force-stop --install-dir "$INSTDIR"'
    Pop $0
    Pop $1
    ${If} $0 != 0
      DetailPrint "LazyMind force-stop failed: $1"
      MessageBox MB_OK|MB_ICONSTOP "$(LMForceStopFailed)$\r$\n$1" /SD IDOK
      SetErrorLevel 7
      Quit
    ${EndIf}
    DetailPrint "LazyMind processes were force-closed: $1"
    Goto LMCheckStopped

  LMProcessCheckDone:
    ; electron-builder invokes an existing uninstaller silently during an
    ; upgrade. Older LazyMind uninstallers initialized their helper too late
    ; and always failed that path. Replace only the old program uninstaller
    ; with this build's fixed one before electron-builder invokes it.
    !ifndef BUILD_UNINSTALLER
      ${If} $LegacyUninstallString != ""
      ${OrIf} $InstalledVersion != ""
        DetailPrint "Updating the previous LazyMind uninstaller for upgrade compatibility..."
        CreateDirectory "$INSTDIR"
        ClearErrors
        CopyFiles /SILENT "$UpgradeUninstaller" "$INSTDIR\${UNINSTALL_FILENAME}"
        ${If} ${Errors}
          Goto LMUpgradeRepairFailed
        ${EndIf}
        ; Repair stale registrations too: the previous install may have left
        ; its registry entry after the uninstaller file was removed.
        ClearErrors
        WriteRegStr HKCU "${UNINSTALL_REGISTRY_KEY}" "UninstallString" '"$INSTDIR\${UNINSTALL_FILENAME}"'
        ${If} ${Errors}
          Goto LMUpgradeRepairFailed
        ${EndIf}
        ClearErrors
        ReadRegStr $0 HKCU "${UNINSTALL_REGISTRY_KEY}" "UninstallString"
        ${If} ${Errors}
          Goto LMUpgradeRepairFailed
        ${ElseIf} $0 != '"$INSTDIR\${UNINSTALL_FILENAME}"'
          Goto LMUpgradeRepairFailed
        ${EndIf}
        Goto LMUpgradeRepairDone

      LMUpgradeRepairFailed:
        DetailPrint "Could not update the previous LazyMind uninstaller at $INSTDIR\${UNINSTALL_FILENAME}."
        MessageBox MB_OK|MB_ICONSTOP "$(LMUpgradeRepairFailed)$\r$\n$INSTDIR\${UNINSTALL_FILENAME}" /SD IDOK
        SetErrorLevel 8
        Quit

      LMUpgradeRepairDone:
      ${EndIf}
    !endif
!macroend

!macro customInstall
  ${If} $InstallDataChoice == "purge"
    nsExec::ExecToStack '"$InstallerHelper" purge-local-data'
    Pop $0
    Pop $1
    ${If} $0 != 0
      MessageBox MB_OK|MB_ICONSTOP "$(LMPurgeFailed)$\r$\n$1" /SD IDOK
      SetErrorLevel 3
      Abort
    ${EndIf}
  ${EndIf}

  ${If} $InstallTypeChoice == "full"
    CreateDirectory "$LOCALAPPDATA\LazyMind\logs"
    StrCpy $5 "$LOCALAPPDATA\LazyMind\logs\installer-nsis.log"
    FileOpen $6 "$5" a
    FileWrite $6 "Starting Electron installer warmup (timeout=360s).$\r$\n"
    FileClose $6
    DetailPrint "Starting Electron installer warmup; log: $LOCALAPPDATA\LazyMind\logs\installer-warmup.log"
    ExecWait '"$INSTDIR\${APP_EXECUTABLE_FILENAME}" --installer-warmup --timeout-seconds 360' $3
    FileOpen $6 "$5" a
    FileWrite $6 "Electron installer warmup returned exit code $3.$\r$\n"
    FileClose $6
    DetailPrint "Electron installer warmup returned exit code $3."
    StrCpy $2 0
    StrCpy $4 0

  LMWarmupCheckStopped:
    nsExec::ExecToStack '"$InstallerHelper" check-stopped --install-dir "$INSTDIR"'
    Pop $0
    Pop $1
    FileOpen $6 "$5" a
    FileWrite $6 "Warmup process check returned exit code $0: $1$\r$\n"
    FileClose $6
    ${If} $0 == 10
      StrCpy $4 1
      IntOp $2 $2 + 1
      DetailPrint "Warmup left running LazyMind processes: $1"
      ${If} $2 > 3
        DetailPrint "Warmup process cleanup did not converge; installation will continue."
        StrCpy $3 4
        Goto LMWarmupFinished
      ${EndIf}
      DetailPrint "Force-closing processes left by installer warmup..."
      nsExec::ExecToStack '"$InstallerHelper" force-stop --install-dir "$INSTDIR"'
      Pop $0
      Pop $1
      ${If} $0 != 0
        DetailPrint "Warmup process cleanup failed; installation will continue: $1"
        StrCpy $3 4
        Goto LMWarmupFinished
      ${EndIf}
      Goto LMWarmupCheckStopped
    ${ElseIf} $0 != 0
      DetailPrint "Warmup process detection failed; installation will continue: $1"
      StrCpy $3 4
    ${EndIf}

    ; Returning success while leaving processes behind is itself a warmup
    ; failure, even though the processes were force-closed above.
    ${If} $4 == 1
      StrCpy $3 4
    ${EndIf}
    ${If} $3 != 0
      DetailPrint "$(LMWarmupFailed)"
      ${IfNot} ${Silent}
        MessageBox MB_OK|MB_ICONEXCLAMATION "$(LMWarmupFailed)" /SD IDOK
      ${EndIf}
    ${EndIf}
  LMWarmupFinished:
  ${Else}
    DetailPrint "Simple installation selected; bundled Python will be prepared on first launch."
  ${EndIf}
!macroend

!macro customUnInit
  InitPluginsDir
  StrCpy $UninstallDataChoice "keep"
  StrCpy $InstallerHelper "$PLUGINSDIR\lazymind-installer-maintenance.exe"
  File /oname=$PLUGINSDIR\lazymind-installer-maintenance.exe "${BUILD_RESOURCES_DIR}\lazymind-installer-maintenance.exe"
  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "--purge-lazymind-local-data" $1
  ${IfNot} ${Errors}
    ${IfNot} ${isUpdated}
      StrCpy $UninstallDataChoice "purge"
    ${EndIf}
  ${EndIf}
!macroend

!macro customUnWelcomePage
  !insertmacro MUI_UNPAGE_WELCOME
  UninstPage custom un.LMDataPageCreate un.LMDataPageLeave
!macroend

!macro customUnInstall
  ${If} $UninstallDataChoice == "purge"
    nsExec::ExecToStack '"$InstallerHelper" purge-local-data'
    Pop $0
    Pop $1
    ${If} $0 != 0
      MessageBox MB_OK|MB_ICONSTOP "$(LMPurgeFailed)$\r$\n$1" /SD IDOK
      SetErrorLevel 3
    ${EndIf}
  ${EndIf}
!macroend
