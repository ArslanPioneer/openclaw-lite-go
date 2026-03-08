package codexproxy

import "strings"

type RiskLevel string

const (
	RiskLevelInformational RiskLevel = "informational"
	RiskLevelMutating      RiskLevel = "mutating"
	RiskLevelHostCritical  RiskLevel = "host-critical"
)

type PolicyDecision struct {
	Risk                 RiskLevel
	Allowed              bool
	RequiresConfirmation bool
}

type Policy struct {
	DangerFullAccess  bool
	RequireConfirm    bool
}

func (p Policy) Evaluate(command string) PolicyDecision {
	risk := classifyRisk(command)
	allowed := true
	if risk == RiskLevelHostCritical && !p.DangerFullAccess {
		allowed = false
	}
	return PolicyDecision{
		Risk:                 risk,
		Allowed:              allowed,
		RequiresConfirmation: allowed && p.RequireConfirm && risk == RiskLevelHostCritical,
	}
}

func classifyRisk(command string) RiskLevel {
	text := strings.ToLower(strings.TrimSpace(command))
	if text == "" {
		return RiskLevelInformational
	}

	hostCriticalHints := []string{
		"rm -rf /",
		"reboot",
		"shutdown",
		"poweroff",
		"usermod",
		"userdel",
		"iptables -f",
		"mkfs",
		"fdisk",
		"dd if=",
	}
	for _, hint := range hostCriticalHints {
		if strings.Contains(text, hint) {
			return RiskLevelHostCritical
		}
	}

	mutatingHints := []string{
		"rm ",
		"mv ",
		"cp ",
		"sed -i",
		"tee ",
		"systemctl restart",
		"systemctl stop",
		"docker rm",
		"docker stop",
		"apt install",
		"apt remove",
	}
	for _, hint := range mutatingHints {
		if strings.Contains(text, hint) {
			return RiskLevelMutating
		}
	}

	return RiskLevelInformational
}
