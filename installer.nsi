; Z-API Proxy NSIS Installer (dual-arch: amd64 + arm64)
!include "MUI2.nsh"
!include "LogicLib.nsh"

Name "Z-API Proxy ${APPVERSION}"
OutFile "z-api-proxy-setup.exe"
InstallDir "$PROGRAMFILES64\Z-API-Proxy"
RequestExecutionLevel admin
Unicode True
ShowInstDetails show

!define APPNAME "Z-API Proxy"
!ifndef APPVERSION
  !define APPVERSION "1.0.0"
!endif

; Use the app icon for the installer executable and shortcuts
Icon "assets\icon.ico"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Install"
  SetOutPath "$INSTDIR"

  ; NSIS is a 32-bit process. On ARM64 Windows PROCESSOR_ARCHITEW6432=ARM64,
  ; on x64 Windows PROCESSOR_ARCHITEW6432=AMD64.
  ReadEnvStr $0 "PROCESSOR_ARCHITEW6432"
  ${If} $0 == "ARM64"
    File /oname=z-api-proxy.exe "build\arm64\z-api-proxy.exe"
  ${Else}
    File /oname=z-api-proxy.exe "build\amd64\z-api-proxy.exe"
  ${EndIf}

  ; Start Menu shortcut
  CreateShortcut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\z-api-proxy.exe" "" "$INSTDIR\z-api-proxy.exe" 0

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
