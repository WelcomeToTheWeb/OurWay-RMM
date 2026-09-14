//go:build darwin

// macOS process and service management.

package uplink

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	agentv1 "github.com/welcometotheweb/rmmway/proto/gen/rmmway/agent/v1"
)

func listProcessesDarwin(nameFilter string) ([]ProcessInfo, error) {
	var processes []ProcessInfo

	// Use /proc-like approach via ps command
	out, err := exec.Command("ps", "-axo", "pid,comm,%cpu,%mem,user,start").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for i, line := range lines {
		if i == 0 {
			continue // header
		}

		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		if nameFilter != "" && !strings.Contains(strings.ToLower(fields[1]), strings.ToLower(nameFilter)) {
			continue
		}

		processes = append(processes, ProcessInfo{
			PID:       pid,
			Name:      fields[1],
			CPU:       fields[2],
			Mem:       fields[3],
			User:      fields[4],
			StartTime: strings.Join(fields[5:7], " "),
		})
	}

	return processes, nil
}

func killProcessUnix(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return syscall.Kill(pid, sig)
}

func listServicesDarwin(nameFilter string) ([]ServiceInfo, error) {
	var services []ServiceInfo

	// Use launchctl to list services
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("launchctl list: %w", err)
	}

	// launchctl list output is JSON array
	var svcList []map[string]interface{}
	if err := json.Unmarshal(out, &svcList); err != nil {
		// Parse as tab-separated
		return listServicesDarwinFallback(nameFilter)
	}

	for _, svc := range svcList {
		name, _ := svc["label"].(string)
		if name == "" {
			continue
		}

		if nameFilter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(nameFilter)) {
			continue
		}

		status, _ := svc["status"].(float64)
		runStatus := "stopped"
		if status == 0 {
			runStatus = "running"
		}

		services = append(services, ServiceInfo{
			Name:   name,
			Status: runStatus,
			Loaded: "yes",
			Active: runStatus,
		})
	}

	return services, nil
}

func listServicesDarwinFallback(nameFilter string) ([]ServiceInfo, error) {
	var services []ServiceInfo

	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return services, nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		name := fields[2]
		if nameFilter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(nameFilter)) {
			continue
		}

		services = append(services, ServiceInfo{
			Name:   name,
			Status: "unknown",
			Loaded: "yes",
		})
	}

	return services, nil
}

func serviceControlUnix(serviceName string, action string) error {
	return exec.Command("launchctl", action, serviceName).Run()
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
