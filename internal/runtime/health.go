package runtime

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

type HealthState struct {
	startedAtUnix int64
	lastPollUnix  int64
	restartCount  int64
	goalQueueDepth int64
	activeGoals    int64
	lastError     atomic.Value
}

type HealthSnapshot struct {
	Status       string `json:"status"`
	StartedAt    string `json:"started_at"`
	LastPollAt   string `json:"last_poll_at,omitempty"`
	RestartCount int64  `json:"restart_count"`
	GoalQueueDepth int64 `json:"goal_queue_depth"`
	ActiveGoals   int64  `json:"active_goals"`
	LastError    string `json:"last_error,omitempty"`
}

func NewHealthState() *HealthState {
	now := time.Now().UTC().UnixNano()
	h := &HealthState{
		startedAtUnix: now,
		lastPollUnix:  now,
	}
	h.lastError.Store("")
	return h
}

func (h *HealthState) RecordPollSuccess() {
	atomic.StoreInt64(&h.lastPollUnix, time.Now().UTC().UnixNano())
	h.lastError.Store("")
}

func (h *HealthState) RecordPollError(err error) {
	if err == nil {
		return
	}
	h.lastError.Store(err.Error())
}

func (h *HealthState) RecordRestart(err error) {
	atomic.AddInt64(&h.restartCount, 1)
	if err != nil {
		h.lastError.Store(err.Error())
	}
}

func (h *HealthState) RecordGoalEnqueued() {
	atomic.AddInt64(&h.goalQueueDepth, 1)
}

func (h *HealthState) RecordGoalStarted() {
	atomic.AddInt64(&h.activeGoals, 1)
	atomic.AddInt64(&h.goalQueueDepth, -1)
}

func (h *HealthState) RecordGoalFinished() {
	atomic.AddInt64(&h.activeGoals, -1)
}

func (h *HealthState) Snapshot() HealthSnapshot {
	started := time.Unix(0, atomic.LoadInt64(&h.startedAtUnix)).UTC()
	lastPoll := time.Unix(0, atomic.LoadInt64(&h.lastPollUnix)).UTC()

	lastErr := ""
	if v, ok := h.lastError.Load().(string); ok {
		lastErr = v
	}

	return HealthSnapshot{
		Status:       "ok",
		StartedAt:    started.Format(time.RFC3339),
		LastPollAt:   lastPoll.Format(time.RFC3339),
		RestartCount: atomic.LoadInt64(&h.restartCount),
		GoalQueueDepth: atomic.LoadInt64(&h.goalQueueDepth),
		ActiveGoals:  atomic.LoadInt64(&h.activeGoals),
		LastError:    lastErr,
	}
}

func HealthHandler(state *HealthState) http.Handler {
	if state == nil {
		state = NewHealthState()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state.Snapshot())
	})
}
