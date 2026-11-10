package scheduler

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/patent-dev/bulk-file-loader/internal/database"
)

// Scheduler runs each scheduled product's sync on its cron schedule.
type Scheduler struct {
	db       *database.DB
	sync     func(productID string)
	cron     *cron.Cron
	entryIDs map[string]cron.EntryID
	specs    map[string]string // the schedule each entry was added with
	mu       sync.Mutex
	stopped  atomic.Bool
}

// New loads the stored schedules and starts running them; sync is called
// with the product ID whenever a schedule fires.
func New(db *database.DB, sync func(productID string)) *Scheduler {
	s := &Scheduler{
		db:       db,
		sync:     sync,
		cron:     cron.New(),
		entryIDs: make(map[string]cron.EntryID),
		specs:    make(map[string]string),
	}
	s.loadSchedules()
	s.cron.Start()
	return s
}

// NextRun validates a schedule (a standard five-field cron expression or a
// descriptor such as @daily) and returns its next run after from.
func NextRun(spec string, from time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q: %w", spec, err)
	}
	return sched.Next(from), nil
}

// Stop stops the schedules and waits for running jobs to finish.
func (s *Scheduler) Stop() {
	s.stopped.Store(true)
	<-s.cron.Stop().Done()
}

// Running reports whether the scheduler has not been stopped.
func (s *Scheduler) Running() bool { return !s.stopped.Load() }

func (s *Scheduler) ScheduleProduct(product *database.Product) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scheduleLocked(product)
}

func (s *Scheduler) scheduleLocked(product *database.Product) error {

	if entryID, ok := s.entryIDs[product.ID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryIDs, product.ID)
		delete(s.specs, product.ID)
	}

	if product.CheckWindowStart == "" {
		return nil
	}

	productID := product.ID
	entryID, err := s.cron.AddFunc(product.CheckWindowStart, func() {
		s.sync(productID)
	})
	if err != nil {
		return err
	}

	s.entryIDs[product.ID] = entryID
	s.specs[product.ID] = product.CheckWindowStart
	slog.Info("Scheduled product", "productID", product.ID, "schedule", product.CheckWindowStart)
	return nil
}

func (s *Scheduler) UnscheduleProduct(productID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unscheduleLocked(productID)
}

func (s *Scheduler) unscheduleLocked(productID string) {

	if entryID, ok := s.entryIDs[productID]; ok {
		s.cron.Remove(entryID)
		delete(s.entryIDs, productID)
		delete(s.specs, productID)
	}
}

// Reload brings the running schedules in line with the database, so changes
// made by another process (the CLI) take effect without a restart. It holds
// the lock while reading and applying, so a concurrent ScheduleProduct is
// applied either before the read or after the reload, never undone.
func (s *Scheduler) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	var products []database.Product
	if err := s.db.Where("auto_download = ? AND check_window_start != ?", true, "").Find(&products).Error; err != nil {
		slog.Error("Failed to reload schedules", "error", err)
		return
	}
	want := make(map[string]bool, len(products))
	for i := range products {
		p := &products[i]
		want[p.ID] = true
		if current, ok := s.specs[p.ID]; ok && current == p.CheckWindowStart {
			continue
		}
		if err := s.scheduleLocked(p); err != nil {
			slog.Error("Failed to schedule product", "productID", p.ID, "error", err)
		}
	}
	for id := range s.entryIDs {
		if !want[id] {
			s.unscheduleLocked(id)
		}
	}
}

func (s *Scheduler) loadSchedules() {
	var products []database.Product
	if err := s.db.Where("auto_download = ? AND check_window_start != ?", true, "").Find(&products).Error; err != nil {
		slog.Error("Failed to load scheduled products", "error", err)
		return
	}

	for i := range products {
		if err := s.ScheduleProduct(&products[i]); err != nil {
			slog.Error("Failed to schedule product", "productID", products[i].ID, "error", err)
		}
	}
	slog.Info("Loaded product schedules", "count", len(products))
}

func (s *Scheduler) GetNextRun(productID string) *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	entryID, ok := s.entryIDs[productID]
	if !ok {
		return nil
	}
	next := s.cron.Entry(entryID).Next
	return &next
}
