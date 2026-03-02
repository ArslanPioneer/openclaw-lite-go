package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandlerReturnsSnapshot(t *testing.T) {
	state := NewHealthState()
	state.RecordPollSuccess()
	state.RecordRestart(assertErr("panic happened"))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	HealthHandler(state).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d", rr.Code)
	}

	var body HealthSnapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.RestartCount != 1 {
		t.Fatalf("expected restart_count=1, got %d", body.RestartCount)
	}
	if body.LastError == "" {
		t.Fatal("expected last_error to be present")
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

func assertErr(msg string) error { return testErr(msg) }
