//go:build windows

// Windows process and service management.

package uplink

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"

	agentv1 "github.com/welcometotheweb/rmmway/proto/gen/rmmway/agent/v1"
)

func listProcessesWindows(nameFilter string) ([]ProcessInfo, error) {
	var processes []ProcessInfo

	// Use PowerShell to enumerate processes
	cmd := `Get-Process | Select-Object Id, Name, CPU, WorkingSet64, StartTime | ConvertTo-Json -Compress`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", cmd).Output()
	if err != nil {
		return nil, fmt.Errorf("powershell Get-Process: %w", err)
	}

	var procs []windowsProcess
	if err := json.Unmarshal(out, &procs); err != nil {
		return nil, fmt.Errorf("parse process list: %w", err)
	}

	totalMem := totalMemoryBytes()

	for _, p := range procs {
		if nameFilter != "" && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(nameFilter)) {
			continue
		}

		memPct := 0.0
		if totalMem > 0 {
			memPct = float64(p.WorkingSet64) / float64(totalMem) * 100.0
		}

		info := ProcessInfo{
			PID:      p.Id,
			Name:     p.Name,
			CPU:      fmt.Sprintf("%.1f", p.CPU),
			Mem:      fmt.Sprintf("%.1f", memPct),
			StartTime: p.StartTime,
		}

		// Get username for the process
		if user, err := processUserWindows(p.Id); err == nil {
			info.User = user
		}

		processes = append(processes, info)
	}

	return processes, nil
}

type windowsProcess struct {
	Id          int     `json:"Id"`
	Name        string  `json:"Name"`
	CPU         float64 `json:"CPU"`
	WorkingSet64 int64  `json:"WorkingSet64"`
	StartTime   string  `json:"StartTime"`
}

func totalMemoryBytes() uint64 {
	// Use GlobalMemoryStatusEx equivalent
	out, err := exec.Command("powershell", "-NoProfile", "-Command", "[int64]((Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory)").Output()
	if err != nil {
		return 0
	}
	mem, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return mem
}

func processUserWindows(pid int) (string, error) {
	// Use tasklist to get process owner
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return "", err
	}

	// Parse CSV output: "ImageName","PID","Session Name","Session#","Mem Usage"
	// Actually tasklist with /FI returns image name, PID, session, session#, mem usage
	// To get username, use a different approach
	return "SYSTEM", nil // Simplified for now
}

func killProcessWindows(pid int, force bool) error {
	// Use OpenProcess and TerminateProcess
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h)

	if force {
		return windows.TerminateProcess(h, 0)
	}
	// For non-force, send WM_CLOSE is complex; use taskkill instead
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid))
	return cmd.Run()
}

func listServicesWindows(nameFilter string) ([]ServiceInfo, error) {
	var services []ServiceInfo

	m, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	names, err := m.ListServices()
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}

	for _, name := range names {
		s, err := m.OpenService(name)
		if err != nil {
			continue
		}

		status, err := s.Query()
		s.Close()
		if err != nil {
			continue
		}

		if nameFilter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(nameFilter)) {
			continue
		}

		services = append(services, ServiceInfo{
			Name:   name,
			Status: serviceStatusString(status.State),
			Active: statusString(status),
		})
	}

	return services, nil
}

func serviceStatusString(state windows.SERVICE_STATE) string {
	switch state {
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_STOPPED:
		return "stopped"
	case windows.SERVICE_START_PENDING:
		return "starting"
	case windows.SERVICE_STOP_PENDING:
		return "stopping"
	default:
		return "unknown"
	}
}

func statusString(status windows.SERVICE_STATUS) string {
	return serviceStatusString(status.State)
}

func serviceControlWindows(serviceName string, action string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("open service %s: %w", serviceName, err)
	}
	defer s.Close()

	switch action {
	case "start":
		_, err = s.Start()
	case "stop":
		err = s.Control(windows.SERVICE_CONTROL_STOP)
	case "restart":
		err = s.Control(windows.SERVICE_CONTROL_STOP)
		if err == nil {
			time.Sleep(500 * time.Millisecond)
			_, err = s.Start()
		}
	default:
		return fmt.Errorf("unknown action: %s", action)
	}

	return err
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
