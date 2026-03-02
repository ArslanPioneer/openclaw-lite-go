package runtime

import (
	"errors"
	"testing"

	"openclaw-lite-go/internal/tools"
)

func TestOrchestratorBeginStepRespectsMaxSteps(t *testing.T) {
	o := NewOrchestrator(2)

	if !o.BeginStep() {
		t.Fatal("expected first step to start")
	}
	if !o.BeginStep() {
		t.Fatal("expected second step to start")
	}
	if o.BeginStep() {
		t.Fatal("did not expect third step to start")
	}
}

func TestOrchestratorTracksRepeatedToolError(t *testing.T) {
	o := NewOrchestrator(4)
	call := tools.Call{Name: "stock_price", Query: "???"}

	first := o.RecordToolResult(call, errors.New("invalid stock ticker"))
	if first {
		t.Fatal("first error should not be marked repeated")
	}

	second := o.RecordToolResult(call, errors.New("invalid stock ticker"))
	if !second {
		t.Fatal("second identical error should be marked repeated")
	}
}

func TestOrchestratorClearsErrorStreakOnSuccess(t *testing.T) {
	o := NewOrchestrator(4)
	call := tools.Call{Name: "web_search", Query: "NVDA"}

	_ = o.RecordToolResult(call, errors.New("temporary failure"))
	o.RecordToolResult(call, nil)

	state := o.State()
	if state.ConsecutiveToolErrors != 0 {
		t.Fatalf("expected streak reset, got %d", state.ConsecutiveToolErrors)
	}
}
