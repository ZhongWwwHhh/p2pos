package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrTaskCompleted = errors.New("task completed")

type Task interface {
	Name() string
	Interval() time.Duration
	RunOnStart() bool
	Run(ctx context.Context) error
}

type Scheduler struct {
	mu      sync.Mutex
	tasks   []Task
	started bool
	wg      sync.WaitGroup
}

func New() *Scheduler {
	return &Scheduler{}
}

func (s *Scheduler) Register(task Task) error {
	if task == nil {
		return fmt.Errorf("task cannot be nil")
	}
	if task.Interval() <= 0 {
		return fmt.Errorf("task %s has invalid interval %s", task.Name(), task.Interval())
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("cannot register task %s after scheduler start", task.Name())
	}

	s.tasks = append(s.tasks, task)
	return nil
}

func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	tasks := append([]Task(nil), s.tasks...)
	s.mu.Unlock()

	for _, task := range tasks {
		s.wg.Add(1)
		go s.runTaskLoop(ctx, task)
	}
}

func (s *Scheduler) Wait() {
	s.wg.Wait()
}

func (s *Scheduler) runTaskLoop(ctx context.Context, task Task) {
	defer s.wg.Done()

	run := func() bool {
		if err := task.Run(ctx); err != nil {
			if errors.Is(err, ErrTaskCompleted) {
				fmt.Printf("[SCHED] Task completed: %s\n", task.Name())
				return false
			}
			fmt.Printf("[SCHED] Task %s failed: %v\n", task.Name(), err)
		}
		return true
	}

	if task.RunOnStart() && !run() {
		return
	}

	ticker := time.NewTicker(task.Interval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !run() {
				return
			}
		}
	}
}
