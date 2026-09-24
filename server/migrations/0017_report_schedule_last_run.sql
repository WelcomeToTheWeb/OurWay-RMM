-- 0017_report_schedule_last_run.sql — add last_run_at to report_schedules
-- so the scheduler can determine which schedules are due.

ALTER TABLE report_schedules
  ADD COLUMN IF NOT EXISTS last_run_at timestamptz;

COMMENT ON COLUMN report_schedules.last_run_at IS
    'last time this schedule ran (NULL if never)';
