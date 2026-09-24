// scheduler.go — runs report schedules in the background.
// Periodically checks for enabled schedules that are due (based on their
// ISO 8601 duration) and generates the report.
package reports

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"
)

// Scheduler runs report schedules in the background.
type Scheduler struct {
	store    *Store
	interval time.Duration
	logger   *log.Logger
}

// NewScheduler creates a new report scheduler.
func NewScheduler(store *Store, interval time.Duration, logger *log.Logger) *Scheduler {
	if interval <= 0 {
		interval = time.Minute
	}
	return &Scheduler{
		store:    store,
		interval: interval,
		logger:   logger,
	}
}

// Start begins the scheduler loop. It blocks until the context is
// cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	s.logger.Printf("reports scheduler: started (check interval %s)", s.interval)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Printf("reports scheduler: stopped")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick checks for due schedules and runs them.
func (s *Scheduler) tick(ctx context.Context) {
	schedules, err := s.store.ListSchedules(ctx)
	if err != nil {
		s.logger.Printf("reports scheduler: list schedules: %v", err)
		return
	}
	now := time.Now()
	for _, sch := range schedules {
		if !sch.Enabled {
			continue
		}
		if !s.isDue(sch, now) {
			continue
		}
		s.runSchedule(ctx, sch, now)
	}
}

// isDue checks if a schedule is due to run.
func (s *Scheduler) isDue(sch Schedule, now time.Time) bool {
	dur, err := time.ParseDuration(sch.Schedule)
	if err != nil {
		s.logger.Printf("reports scheduler: schedule %q (%d): bad duration %q: %v",
			sch.Name, sch.ID, sch.Schedule, err)
		return false
	}
	if sch.LastRunAt == nil {
		// Never run — due immediately.
		return true
	}
	return now.Sub(*sch.LastRunAt) >= dur
}

// runSchedule generates a report for a due schedule.
func (s *Scheduler) runSchedule(ctx context.Context, sch Schedule, now time.Time) {
	s.logger.Printf("reports scheduler: running schedule %q (%s) [%s]",
		sch.Name, sch.ReportType, sch.OutputFormat)

	run := Run{
		ScheduleID:   &sch.ID,
		ReportType:   sch.ReportType,
		ClientID:     sch.ClientID,
		OutputFormat: sch.OutputFormat,
		TriggeredBy:  "schedule",
		Status:       "running",
	}
	run, err := s.store.CreateRun(ctx, run)
	if err != nil {
		s.logger.Printf("reports scheduler: schedule %q (%d): create run: %v",
			sch.Name, sch.ID, err)
		return
	}

	var content io.Reader
	switch sch.ReportType {
	case TypeFleetStatus:
		content, err = s.store.GenerateFleetStatusCSV(ctx)
	case TypePatchCompliance:
		content, err = s.store.GeneratePatchComplianceCSV(ctx)
	case TypeLicenseCompliance:
		content, err = s.store.GenerateLicenseComplianceCSV(ctx)
	case TypeUptimeSLA:
		content, err = s.store.GenerateUptimeSLACSV(ctx)
	default:
		err = fmt.Errorf("unsupported report type %q", sch.ReportType)
	}
	if err != nil {
		s.store.FailRun(ctx, run.ID, err.Error())
		s.logger.Printf("reports scheduler: schedule %q (%d): generate: %v",
			sch.Name, sch.ID, err)
		return
	}
	// Drain content (MinIO integration would store the blob).
	_, _ = io.ReadAll(content)

	objKey := fmt.Sprintf("report-%d.%s", run.ID, sch.OutputFormat)
	if err := s.store.CompleteRun(ctx, run.ID, objKey); err != nil {
		s.logger.Printf("reports scheduler: schedule %q (%d): complete run: %v",
			sch.Name, sch.ID, err)
		return
	}
	if err := s.store.UpdateLastRunAt(ctx, sch.ID, now); err != nil {
		s.logger.Printf("reports scheduler: schedule %q (%d): update last_run_at: %v",
			sch.Name, sch.ID, err)
		return
	}
	s.logger.Printf("reports scheduler: schedule %q (%d): completed -> %s",
		sch.Name, sch.ID, objKey)
}
