package codexproxy

import "testing"

func TestPolicyFlagsDangerousCommandsButAllowsConfiguredExecution(t *testing.T) {
	policy := Policy{
		DangerFullAccess: true,
	}

	tests := []struct {
		command string
		want    RiskLevel
	}{
		{command: "rm -rf /", want: RiskLevelHostCritical},
		{command: "reboot", want: RiskLevelHostCritical},
		{command: "usermod -aG sudo deploy", want: RiskLevelHostCritical},
		{command: "iptables -F", want: RiskLevelHostCritical},
		{command: "ls -la", want: RiskLevelInformational},
	}

	for _, tc := range tests {
		decision := policy.Evaluate(tc.command)
		if decision.Risk != tc.want {
			t.Fatalf("Evaluate(%q).Risk = %q, want %q", tc.command, decision.Risk, tc.want)
		}
		if tc.want == RiskLevelHostCritical && !decision.Allowed {
			t.Fatalf("expected %q to remain allowed in configured full-access mode", tc.command)
		}
	}
}
