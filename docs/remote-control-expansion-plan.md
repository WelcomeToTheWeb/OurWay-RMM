# Remote Control Expansion Plan

## Overview
Expand RMMWay's remote management capabilities with six new feature modules. Each feature follows the existing architecture: gRPC protocol definition, server HTTP API, agent-side execution, and React frontend integration.

## Feature Modules

### 1. Process & Service Management
**Value:** High - Essential for operational troubleshooting

**Protocol:** New `process_service.proto`
- `ProcessListRequest/Response` - List running processes (PID, name, CPU%, memory, user)
- `ProcessKillRequest/Response` - Kill process by PID
- `ProcessStartRequest/Response` - Start process with args and working directory
- `ServiceListRequest/Response` - List services (name, status, startup type)
- `ServiceControlRequest/Response` - Start/stop/restart service

**Server API:**
- `GET /api/devices/{id}/processes` - List processes
- `POST /api/devices/{id}/processes/kill` - Kill process
- `POST /api/devices/{id}/processes/start` - Start process
- `GET /api/devices/{id}/services` - List services
- `POST /api/devices/{id}/services/control` - Control service

**Frontend:** Process/service tab on device detail page with sortable table and action buttons

**Agent Implementation:**
- Linux: `/proc` filesystem, `kill` syscall, `systemctl`
- Windows: `CreateToolhelp32Snapshot`, `Process32Next`, `OpenProcess`, `TerminateProcess`, service control manager APIs
- macOS: `sysctl`, `kill`, `launchctl`

### 2. Interactive Shell/Terminal
**Value:** Critical - Most flexible remote control method

**Protocol:** New `terminal.proto`
- `TerminalOpenRequest/Response` - Open shell session (shell type, size, env vars)
- `TerminalInput` - Send keystrokes/data
- `TerminalOutput` - Receive data (chunked)
- `TerminalResize` - Resize terminal
- `TerminalClose` - Close session

**Server API:**
- `POST /api/devices/{id}/terminal/open` - Open session, returns session ID
- `POST /api/devices/{id}/terminal/{session}/input` - Send input
- `GET /api/devices/{id}/terminal/{session}/stream` - SSE output stream
- `DELETE /api/devices/{id}/terminal/{session}` - Close session

**Frontend:** Full-screen terminal component using xterm.js with copy/paste support

**Agent Implementation:**
- Linux: `libtinfo`/`libtermcap` or pty forkexec
- Windows: ConPTY API (Windows 10+)
- macOS: `libutil` (openpty), fork/exec

### 3. File Browser/Explorer
**Value:** High - Intuitive file management

**Protocol:** New `filesystem.proto`
- `FsListRequest/Response` - List directory contents
- `FsStatRequest/Response` - Get file/directory info
- `FsUploadRequest/Response` - Upload file (chunked)
- `FsDownloadRequest/Response` - Download file (chunked)
- `FsDeleteRequest/Response` - Delete file/directory
- `FsMkdirRequest/Response` - Create directory
- `FsSearchRequest/Response` - Search by name/pattern

**Server API:**
- `GET /api/devices/{id}/fs/list?path=...` - List directory
- `GET /api/devices/{id}/fs/stat?path=...` - File info
- `POST /api/devices/{id}/fs/upload` - Upload file
- `GET /api/devices/{id}/fs/download?path=...` - Download file
- `DELETE /api/devices/{id}/fs/delete?path=...` - Delete
- `POST /api/devices/{id}/fs/mkdir?path=...` - Create directory
- `GET /api/devices/{id}/fs/search?query=...` - Search files

**Frontend:** File manager UI with breadcrumbs, upload/download buttons, search

### 4. Software Deployment
**Value:** High - Centralize software management

**Protocol:** New `software.proto`
- `SoftwareListRequest/Response` - List installed software
- `SoftwareInstallRequest/Response` - Install software (source, args, silent flag)
- `SoftwareUninstallRequest/Response` - Uninstall software
- `SoftwareUpdateRequest/Response` - Update software

**Server API:**
- `GET /api/devices/{id}/software` - List installed software
- `POST /api/devices/{id}/software/install` - Install software
- `POST /api/devices/{id}/software/uninstall` - Uninstall software

**Frontend:** Software tab with installed apps list and install/uninstall actions

**Agent Implementation:**
- Linux: dpkg/rpm query, apt/yum/dnf install
- Windows: WMI `Win32_Product`, registry `Uninstall` keys, MSIEXEC
- macOS: `pkgutil`, homebrew

### 5. User Session Management
**Value:** Medium - Operational convenience

**Protocol:** New `users.proto`
- `UserListRequest/Response` - List users/sessions
- `UserLogoutRequest/Response` - Log off user session
- `UserSwitchRequest/Response` - Switch active session

**Server API:**
- `GET /api/devices/{id}/users` - List users/sessions
- `POST /api/devices/{id}/users/logout` - Log off user
- `POST /api/devices/{id}/users/switch` - Switch session

**Agent Implementation:**
- Linux: `who`, `w`, `loginctl`, `pkill -u`
- Windows: WTS API, `taskkill /f /fi SESSIONNAME eq ...`
- macOS: `dscl`, `osascript`

### 6. Windows Registry Editor
**Value:** Medium - Windows-specific admin tasks

**Protocol:** New `registry.proto`
- `RegListKeysRequest/Response` - List subkeys
- `RegListValuesRequest/Response` - List values
- `RegGetValueRequest/Response` - Get value
- `RegSetValueRequest/Response` - Set value
- `RegDeleteValueRequest/Response` - Delete value
- `RegDeleteKeyRequest/Response` - Delete key
- `RegSearchRequest/Response` - Search values

**Server API:**
- `GET /api/devices/{id}/registry/keys?path=...` - List subkeys
- `GET /api/devices/{id}/registry/values?path=...` - List values
- `GET /api/devices/{id}/registry/value?path=...` - Get value
- `POST /api/devices/{id}/registry/set` - Set value
- `DELETE /api/devices/{id}/registry/delete` - Delete value/key
- `GET /api/devices/{id}/registry/search?query=...` - Search

**Frontend:** Registry explorer with tree view and value editor

**Agent Implementation:** Windows registry APIs (`RegOpenKeyEx`, `RegEnumKeyEx`, etc.)

## Implementation Order
1. Process & Service Management (simplest, high value)
2. Interactive Shell/Terminal (essential, high complexity)
3. File Browser/Explorer (intuitive, medium complexity)
4. Software Deployment (important, medium complexity)
5. User Session Management (convenience, low complexity)
6. Windows Registry Editor (niche, low complexity)

## Release Process (v1.2.0)
1. Update version constants to 1.2.0
2. Run `make build && make test`
3. Cross-compile: `VERSION=1.2.0 make agent`
4. Sign artifacts: `MINISIGN_PASS=... VERSION=1.2.0 make sign`
5. Build and push Docker images to GHCR
6. Sign images with cosign
7. Generate SBOMs: `VERSION=1.2.0 make sbom`
8. Update CHANGELOG.md
9. Create git tag: `git tag -a v1.2.0`
10. Publish GitHub release with all artifacts
