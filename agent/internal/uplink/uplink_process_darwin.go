//go:build darwin

// Process and service management command handlers (macOS).

package uplink

import (
	"context"
	"encoding/json"

	agentv1 "github.com/welcometotheweb/rmmway/proto/gen/rmmway/agent/v1"
)

// listProcessesCommand returns a JSON list of running processes.
func (u *Uplink) listProcessesCommand(ctx context.Context, stream agentv1.AgentService_StreamClient, cmd *agentv1.Command) error {
	lp := cmd.GetListProcesses()
	if lp == nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, "nil list_processes")
	}
	u.cfg.Logger.Info("list_processes", "cmd", cmd.GetId(), "filter", lp.GetNameFilter())

	var processes []ProcessInfo
	var err error
	processes, err = listProcessesDarwin(lp.GetNameFilter())
	if err != nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, err.Error())
	}

	data, err := json.Marshal(processes)
	if err != nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, "json marshal: "+err.Error())
	}
	return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_SUCCEEDED, 0, data, nil, "")
}

// killProcessCommand terminates a process by PID.
func (u *Uplink) killProcessCommand(ctx context.Context, stream agentv1.AgentService_StreamClient, cmd *agentv1.Command) error {
	kill := cmd.GetKillProcess()
	if kill == nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, "nil kill_process")
	}
	u.cfg.Logger.Info("kill_process", "cmd", cmd.GetId(), "pid", kill.GetPid(), "force", kill.GetForce())

	err := killProcessUnix(int(kill.GetPid()), kill.GetForce())
	if err != nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, err.Error())
	}
	return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_SUCCEEDED, 0, nil, nil, "")
}

// listServicesCommand returns a JSON list of system services.
func (u *Uplink) listServicesCommand(ctx context.Context, stream agentv1.AgentService_StreamClient, cmd *agentv1.Command) error {
	ls := cmd.GetListServices()
	if ls == nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, "nil list_services")
	}
	u.cfg.Logger.Info("list_services", "cmd", cmd.GetId(), "filter", ls.GetNameFilter())

	var services []ServiceInfo
	var err error
	services, err = listServicesDarwin(ls.GetNameFilter())
	if err != nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, err.Error())
	}

	data, err := json.Marshal(services)
	if err != nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, "json marshal: "+err.Error())
	}
	return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_SUCCEEDED, 0, data, nil, "")
}

// serviceControlCommand manages a service (start/stop/restart).
func (u *Uplink) serviceControlCommand(ctx context.Context, stream agentv1.AgentService_StreamClient, cmd *agentv1.Command) error {
	sc := cmd.GetServiceControl()
	if sc == nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, "nil service_control")
	}
	action := sc.GetAction().String()
	u.cfg.Logger.Info("service_control", "cmd", cmd.GetId(), "service", sc.GetServiceName(), "action", action)

	err := serviceControlUnix(sc.GetServiceName(), serviceAction(sc.GetAction()))
	if err != nil {
		return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_FAILED, 0, nil, nil, err.Error())
	}
	return u.sendResult(stream, cmd.GetId(), agentv1.CommandResult_SUCCEEDED, 0, nil, nil, "")
}
