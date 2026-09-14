// ProcessInfo and ServiceInfo are shared types used by all platform
// implementations of process and service management.

package uplink

// ProcessInfo is the JSON-serializable process record.
type ProcessInfo struct {
	PID       int    `json:"pid"`
	Name      string `json:"name"`
	CPU       string `json:"cpu_pct"`
	Mem       string `json:"mem_pct"`
	User      string `json:"user"`
	Status    string `json:"status"`
	StartTime string `json:"start_time"`
}

// ServiceInfo is the JSON-serializable service record.
type ServiceInfo struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Loaded string `json:"loaded"`
	Active string `json:"active"`
}
