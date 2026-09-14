import { useCallback, useEffect, useState } from "react";
import { api } from "../../api.js";
import { EmptyState, Banner } from "../../ui/index.js";

function ProcessServiceManagement({ token, device, onUnauthorized, onSaved }) {
  const [processes, setProcesses] = useState(null);
  const [services, setServices] = useState(null);
  const [loading, setLoading] = useState(false);
  const [processFilter, setProcessFilter] = useState("");
  const [serviceFilter, setServiceFilter] = useState("");
  const [error, setError] = useState(null);

  const loadProcesses = useCallback(async () => {
    if (loading) return;
    setLoading(true);
    setError(null);
    try {
      const res = await api.dispatchCommand(token, device.id, {
        action: "list_processes",
        path: processFilter,
      });
      // Command dispatched; results come back through commands audit view
      onSaved(`Process list requested (${res.command_id})`);
    } catch (e) {
      if (e.unauthorized) onUnauthorized();
      else {
        setError(e.message);
        onSaved("Error listing processes");
      }
    } finally {
      setLoading(false);
    }
  }, [token, device.id, loading, processFilter, onUnauthorized, onSaved]);

  const loadServices = useCallback(async () => {
    if (loading) return;
    setLoading(true);
    setError(null);
    try {
      const res = await api.dispatchCommand(token, device.id, {
        action: "list_services",
        path: serviceFilter,
      });
      onSaved(`Service list requested (${res.command_id})`);
    } catch (e) {
      if (e.unauthorized) onUnauthorized();
      else {
        setError(e.message);
        onSaved("Error listing services");
      }
    } finally {
      setLoading(false);
    }
  }, [token, device.id, loading, serviceFilter, onUnauthorized, onSaved]);

  return (
    <div className="process-service-management">
      {error && <Banner variant="error">{error}</Banner>}

      <h3>Processes</h3>
      <p className="muted">
        List running processes, filter by name, and kill processes by PID.
      </p>

      <div className="form-row">
        <label className="muted">Name filter</label>
        <input
          className="text"
          type="text"
          placeholder="e.g., ssh, chrome"
          value={processFilter}
          onChange={(e) => setProcessFilter(e.target.value)}
        />
        <button
          className="btn btn-primary"
          onClick={loadProcesses}
          disabled={loading}
        >
          {loading ? "Loading..." : "List Processes"}
        </button>
      </div>

      <p className="muted">
        To kill a process, use the Commands tab with action "kill_process" and
        provide the PID.
      </p>

      <h3>Services</h3>
      <p className="muted">
        List system services and control them (start/stop/restart).
      </p>

      <div className="form-row">
        <label className="muted">Name filter</label>
        <input
          className="text"
          type="text"
          placeholder="e.g., sshd, nginx"
          value={serviceFilter}
          onChange={(e) => setServiceFilter(e.target.value)}
        />
        <button
          className="btn btn-primary"
          onClick={loadServices}
          disabled={loading}
        >
          {loading ? "Loading..." : "List Services"}
        </button>
      </div>

      <p className="muted">
        Service control commands: start, stop, restart. Use the Commands tab
        with action "service_control".
      </p>
    </div>
  );
}

export default ProcessServiceManagement;
