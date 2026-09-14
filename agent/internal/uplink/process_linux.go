//go:build linux

// Linux process and service management.

package uplink

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	agentv1 "github.com/welcometotheweb/rmmway/proto/gen/rmmway/agent/v1"
)

func listProcessesLinux(nameFilter string) ([]ProcessInfo, error) {
	var processes []ProcessInfo

	// Use /proc filesystem directly
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a PID
		}

		info, err := processInfoFromProc(pid)
		if err != nil {
			continue // process exited
		}

		if nameFilter != "" && !strings.Contains(strings.ToLower(info.Name), strings.ToLower(nameFilter)) {
			continue
		}

		processes = append(processes, info)
	}

	return processes, nil
}

func processInfoFromProc(pid int) (ProcessInfo, error) {
	info := ProcessInfo{PID: pid}

	// Get process name from /proc/<pid>/stat
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	statData, err := os.ReadFile(statPath)
	if err != nil {
		return info, err
	}

	// stat format: pid (comm) state ...
	// Find the closing parenthesis to extract comm
	openParen := strings.IndexByte(string(statData), '(')
	closeParen := strings.IndexByte(string(statData), ')')
	if openParen >= 0 && closeParen >= 0 {
		info.Name = string(statData)[openParen+1 : closeParen]
	} else {
		info.Name = "unknown"
	}

	// Get UID from /proc/<pid>/status
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	statusData, err := os.ReadFile(statusPath)
	if err == nil {
		for _, line := range strings.Split(string(statusData), "\n") {
			if strings.HasPrefix(line, "Uid:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					uid, err := strconv.Atoi(parts[1])
					if err == nil {
						userInfo, err := userForUID(uid)
						if err == nil {
							info.User = userInfo
						}
					}
				}
				break
			}
		}
	}

	return info, nil
}

func userForUID(uid int) (string, error) {
	out, err := exec.Command("id", "-un", strconv.Itoa(uid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func killProcessUnix(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(pid, sig)
}

func listServicesLinux(nameFilter string) ([]ServiceInfo, error) {
	var services []ServiceInfo

	out, err := exec.Command("systemctl", "list-units", "--type=service", "--all", "--no-pager", "--no-legend").Output()
	if err != nil {
		// Try older sysv style
		return listServicesSysv(nameFilter)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		name := strings.TrimSuffix(fields[0], ".service")
		loaded := fields[1]
		active := fields[2]
		status := fields[3]

		if nameFilter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(nameFilter)) {
			continue
		}

		services = append(services, ServiceInfo{
			Name:   name,
			Status: status,
			Loaded: loaded,
			Active: active,
		})
	}

	return services, nil
}

func listServicesSysv(nameFilter string) ([]ServiceInfo, error) {
	var services []ServiceInfo

	// Check /etc/init.d/ directory
	entries, err := os.ReadDir("/etc/init.d")
	if err != nil {
		return services, nil
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		if nameFilter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(nameFilter)) {
			continue
		}

		services = append(services, ServiceInfo{Name: name, Status: "unknown"})
	}

	return services, nil
}

func serviceControlUnix(serviceName string, action string) error {
	// Try systemctl first
	out, err := exec.Command("systemctl", action, serviceName+".service").CombinedOutput()
	if err == nil {
		return nil
	}

	// Fall back to service command
	out2, err2 := exec.Command("service", serviceName, action).CombinedOutput()
	if err2 == nil {
		return nil
	}

	return fmt.Errorf("systemctl: %v (%s); service: %v (%s)", err, string(out), err2, string(out2))
}

func serviceAction(a agentv1.ServiceControl_Action) string {
	switch a {
	case agentv1.ServiceControl_START:
		return "start"
	case agentv1.ServiceControl_STOP:
		return "stop"
	case agentv1.ServiceControl_RESTART:
		return "restart"
	default:
		return ""
	}
}

func runtimeError(msg string) error {
	return fmt.Errorf("%s", msg)
}
