package runtime

import (
	"fmt"
	"strings"

	"openclaw-lite-go/internal/tools"
)

const repeatedToolErrorThreshold = 2

type OrchestratorState struct {
	Step                  int
	MaxSteps              int
	ParseFailures         int
	ConsecutiveToolErrors int
	LastToolFingerprint   string
}

type Orchestrator struct {
	state OrchestratorState
}

func NewOrchestrator(maxSteps int) *Orchestrator {
	if maxSteps <= 0 {
		maxSteps = 1
	}
	return &Orchestrator{
		state: OrchestratorState{
			MaxSteps: maxSteps,
		},
	}
}

func (o *Orchestrator) BeginStep() bool {
	if o.state.Step >= o.state.MaxSteps {
		return false
	}
	o.state.Step++
	return true
}

func (o *Orchestrator) State() OrchestratorState {
	return o.state
}

func (o *Orchestrator) RecordParseFailure() int {
	o.state.ParseFailures++
	return o.state.ParseFailures
}

func (o *Orchestrator) RecordToolResult(call tools.Call, err error) bool {
	if err == nil {
		o.state.ConsecutiveToolErrors = 0
		o.state.LastToolFingerprint = ""
		return false
	}

	fingerprint := toolCallFingerprint(call)
	if o.state.LastToolFingerprint == fingerprint {
		o.state.ConsecutiveToolErrors++
		return o.state.ConsecutiveToolErrors >= repeatedToolErrorThreshold
	}

	o.state.LastToolFingerprint = fingerprint
	o.state.ConsecutiveToolErrors = 1
	return false
}

func toolCallFingerprint(call tools.Call) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(call.Name)),
		strings.TrimSpace(call.Query),
		strings.TrimSpace(call.URL),
		strings.TrimSpace(call.Text),
		strings.TrimSpace(call.Skill),
		strings.TrimSpace(call.Script),
		strings.TrimSpace(call.Input),
		fmt.Sprintf("all=%t", call.All),
		fmt.Sprintf("max=%d", call.MaxBytes),
	}
	return strings.Join(parts, "|")
}
