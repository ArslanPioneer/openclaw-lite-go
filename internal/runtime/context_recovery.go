package runtime

import (
	"fmt"
	"strings"
)

type ContextRecoveryState struct {
	overflowAttempts int
}

func (s *ContextRecoveryState) RecordOverflow(maxAttempts int) bool {
	s.overflowAttempts++
	return s.overflowAttempts > maxAttempts
}

func TruncateToolOutputForContext(raw string, maxChars int) string {
	if maxChars <= 0 || len(raw) <= maxChars {
		return raw
	}

	head := maxChars / 2
	tail := maxChars - head
	if tail <= 0 {
		tail = 1
	}
	if head <= 0 {
		head = 1
	}
	if head+tail > len(raw) {
		return raw
	}

	marker := fmt.Sprintf("\n...[truncated tool output, original=%d chars]...\n", len(raw))
	return strings.TrimSpace(raw[:head]) + marker + strings.TrimSpace(raw[len(raw)-tail:])
}
