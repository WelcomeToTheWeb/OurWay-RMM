package reports

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestStore creates an in-memory-ish store using a shared Postgres instance
// (via the test DSN env) or skips if unavailable.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("OURWAY_RMM_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("OURWAY_RMM_TEST_PG_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return NewStore(pool)
}

func TestSchedulerRunsDueSchedule(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create a schedule that is due (never run, 1h interval).
	sch := Schedule{
		Name:         "test-schedule",
		ReportType:   TypeFleetStatus,
		Schedule:     "1h",
		OutputFormat: "csv",
		Enabled:      true,
		CreatedBy:    "test",
	}
	sch, err := store.CreateSchedule(ctx, sch)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	t.Cleanup(func() { store.DeleteSchedule(ctx, sch.ID) })

	// Verify it's due.
	s := NewScheduler(store, time.Minute, log.New(os.Stderr, "test: ", 0))
	if !s.isDue(sch, time.Now()) {
		t.Fatal("schedule should be due (never run)")
	}

	// Run it.
	s.runSchedule(ctx, sch, time.Now())

	// Verify it was marked as run.
	fresh, err := store.GetSchedule(ctx, sch.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if fresh.LastRunAt == nil {
		t.Fatal("last_run_at should be set after run")
	}

	// Verify a run record was created.
	runs, err := store.ListRuns(ctx, 10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	found := false
	for _, r := range runs {
		if r.ScheduleID != nil && *r.ScheduleID == sch.ID {
			found = true
			if r.Status != "completed" {
				t.Fatalf("run status = %q, want completed", r.Status)
			}
			if r.TriggeredBy != "schedule" {
				t.Fatalf("triggered_by = %q, want schedule", r.TriggeredBy)
			}
		}
	}
	if !found {
		t.Fatal("no run record found for schedule")
	}
}

func TestSchedulerNotDueBeforeInterval(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sch := Schedule{
		Name:         "not-due-schedule",
		ReportType:   TypeFleetStatus,
		Schedule:     "24h",
		OutputFormat: "csv",
		Enabled:      true,
		CreatedBy:    "test",
	}
	sch, err := store.CreateSchedule(ctx, sch)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	t.Cleanup(func() { store.DeleteSchedule(ctx, sch.ID) })

	s := NewScheduler(store, time.Minute, log.New(os.Stderr, "test: ", 0))

	// Never run — should be due.
	if !s.isDue(sch, time.Now()) {
		t.Fatal("should be due (never run)")
	}

	// Mark as just run — should NOT be due (24h interval).
	now := time.Now()
	store.UpdateLastRunAt(ctx, sch.ID, now)
	sch.LastRunAt = &now
	if s.isDue(sch, now.Add(1*time.Hour)) {
		t.Fatal("should not be due after 1h (interval is 24h)")
	}

	// Should be due after 25h.
	if !s.isDue(sch, now.Add(25*time.Hour)) {
		t.Fatal("should be due after 25h (interval is 24h)")
	}
}
