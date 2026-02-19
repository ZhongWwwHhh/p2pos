package tasks

import (
	"context"
	"time"
)

type UpdateRunner interface {
	RunOnce(ctx context.Context) error
}

type UpdateCheckTask struct {
	runner   UpdateRunner
	interval time.Duration
}

func NewUpdateCheckTask(runner UpdateRunner, interval time.Duration) *UpdateCheckTask {
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	return &UpdateCheckTask{
		runner:   runner,
		interval: interval,
	}
}

func (t *UpdateCheckTask) Name() string {
	return "update-check"
}

func (t *UpdateCheckTask) Interval() time.Duration {
	return t.interval
}

func (t *UpdateCheckTask) RunOnStart() bool {
	return false
}

func (t *UpdateCheckTask) Run(ctx context.Context) error {
	return t.runner.RunOnce(ctx)
}
