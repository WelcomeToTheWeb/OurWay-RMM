//go:build windows

// Windows process and service management.

package uplink

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	agentv1 "github.com/welcometotheweb/ourway-rmm/proto/gen/ourway-rmm/agent/v1"
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
			PID:       p.Id,
			Name:      p.Name,
			CPU:       fmt.Sprintf("%.1f", p.CPU),
			Mem:       fmt.Sprintf("%.1f", memPct),
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
	Id           int     `json:"Id"`
	Name         string  `json:"Name"`
	CPU          float64 `json:"CPU"`
	WorkingSet64 int64   `json:"WorkingSet64"`
	StartTime    string  `json:"StartTime"`
}

func totalMemoryBytes() uint64 {
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
	// Simplified: use tasklist to get session info
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return "", err
	}
	_ = out // For now, just return a placeholder
	return "SYSTEM", nil
}

func killProcessWindows(pid int, force bool) error {
	if force {
		return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
	}
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid)).Run()
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
			Active: serviceStatusString(status.State),
		})
	}

	return services, nil
}

func serviceStatusString(state svc.State) string {
	switch state {
	case svc.Running:
		return "running"
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	default:
		return "unknown"
	}
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
		err = s.Start()
	case "stop":
		_, err = s.Control(svc.Stop)
	case "restart":
		_, err = s.Control(svc.Stop)
		if err == nil {
			time.Sleep(500 * time.Millisecond)
			err = s.Start()
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

// Suppress unused variable warning for windows handle operations
var _ = windows.OpenProcess
