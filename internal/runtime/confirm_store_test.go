package runtime

import (
	"errors"
	"testing"
	"time"
)

func TestConfirmStorePersistsAndClearsPendingConfirmation(t *testing.T) {
	store := NewConfirmStore(t.TempDir())
	want := PendingConfirmation{
		GoalID:     "goal-xyz",
		RawRequest: "[goal:goal-xyz] reboot the host",
		RiskLevel:  "host-critical",
		CreatedAt:  time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC),
	}

	if err := store.Save(51, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Load(51)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.GoalID != want.GoalID || got.RawRequest != want.RawRequest || got.RiskLevel != want.RiskLevel {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("CreatedAt = %s, want %s", got.CreatedAt, want.CreatedAt)
	}

	if err := store.Clear(51); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if _, err := store.Load(51); !errors.Is(err, ErrPendingConfirmationNotFound) {
		t.Fatalf("Load() after Clear error = %v, want ErrPendingConfirmationNotFound", err)
	}
}
