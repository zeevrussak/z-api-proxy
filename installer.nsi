; Z-API Proxy NSIS Installer
!include "MUI2.nsh"

Name "Z-API Proxy"
OutFile "z-api-proxy-setup.exe"
InstallDir "$PROGRAMFILES64\Z-API-Proxy"
RequestExecutionLevel admin
Unicode True
ShowInstDetails show

!define APPNAME "Z-API Proxy"
!define APPVERSION "1.0.0"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Install"
  SetOutPath "$INSTDIR"
  File "z-api-proxy.exe"

  ; Start Menu shortcut
  CreateShortcut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\z-api-proxy.exe"

  ; Registry entry for Add/Remove Programs
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "DisplayName" "${APPNAME}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "DisplayVersion" "${APPVERSION}"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "Publisher" "Z-API-Proxy"

  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; Launch after install
  Exec "$INSTDIR\z-api-proxy.exe"
SectionEnd

Section "Uninstall"
  ; Kill running instance
  ExecWait "taskkill /im z-api-proxy.exe /f"

  Delete "$INSTDIR\z-api-proxy.exe"
  Delete "$INSTDIR\uninstall.exe"
  Delete "$SMPROGRAMS\${APPNAME}.lnk"
  RMDir "$INSTDIR"

  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}"
SectionEnd
