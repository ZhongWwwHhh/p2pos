package tasks

import (
	"context"
	"fmt"
	"time"

	"p2pos/internal/update"
)

type UpdateCheckTask struct {
	owner string
	repo  string
}

func NewUpdateCheckTask(owner, repo string) *UpdateCheckTask {
	return &UpdateCheckTask{
		owner: owner,
		repo:  repo,
	}
}

func (t *UpdateCheckTask) Name() string {
	return "update-check"
}

func (t *UpdateCheckTask) Interval() time.Duration {
	return 3 * time.Minute
}

func (t *UpdateCheckTask) RunOnStart() bool {
	return false
}

func (t *UpdateCheckTask) Run(_ context.Context) error {
	fmt.Println("[UPDATE] Checking for updates...")
	if err := update.CheckAndUpdate(t.owner, t.repo); err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	return nil
}
