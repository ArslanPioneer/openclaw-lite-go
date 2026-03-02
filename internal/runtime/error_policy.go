package runtime

import "strings"

type ErrorKind string

const (
	ErrorRateLimit ErrorKind = "rate_limit"
	ErrorTimeout   ErrorKind = "timeout"
	ErrorAuth      ErrorKind = "auth"
	ErrorBilling   ErrorKind = "billing"
	ErrorFormat    ErrorKind = "format"
	ErrorUnknown   ErrorKind = "unknown"
)

func ClassifyExecutionError(err error) ErrorKind {
	if err == nil {
		return ErrorUnknown
	}

	raw := strings.ToLower(strings.TrimSpace(err.Error()))
	if raw == "" {
		return ErrorUnknown
	}

	if containsAny(raw, "429", "rate limit", "too many requests", "quota", "throttl", "resource has been exhausted") {
		return ErrorRateLimit
	}
	if containsAny(raw, "context deadline exceeded", "deadline exceeded", "timed out", "timeout", "i/o timeout") {
		return ErrorTimeout
	}
	if containsAny(raw, "402", "payment required", "insufficient credits", "insufficient balance", "billing", "credit balance") {
		return ErrorBilling
	}
	if containsAny(raw, "401", "unauthorized", "invalid api key", "authentication", "api key revoked", "forbidden") {
		return ErrorAuth
	}
	if containsAny(raw, "parse tool call", "tool call parse", "invalid character", "invalid request format", "malformed") {
		return ErrorFormat
	}

	return ErrorUnknown
}

func FormatUserFacingExecutionError(kind ErrorKind) string {
	switch kind {
	case ErrorRateLimit:
		return "The model provider is rate-limited right now. Please retry in a minute."
	case ErrorTimeout:
		return "The model request timed out. Please retry in a moment."
	case ErrorAuth:
		return "Agent authentication failed. Please check API key and provider configuration."
	case ErrorBilling:
		return "Agent billing or credit limit was reached. Please top up credits or switch API key."
	case ErrorFormat:
		return "The model returned an invalid response format. Please retry."
	default:
		return "This request could not be completed right now. Please retry shortly."
	}
}

func containsAny(raw string, patterns ...string) bool {
	for _, p := range patterns {
		if strings.Contains(raw, p) {
			return true
		}
	}
	return false
}
