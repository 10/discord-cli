package api

import "fmt"

type Error struct {
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	Status     int     `json:"status,omitempty"`
	RetryAfter float64 `json:"retry_after,omitempty"`
	Details    any     `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) ExitCode() int {
	switch e.Code {
	case "invalid_arguments", "bad_request":
		return 2
	case "authentication_failed", "ambiguous_account", "permission_denied", "user_action_required", "desktop_missing", "desktop_locked", "desktop_unsupported", "desktop_unavailable":
		return 3
	case "not_found", "attachment_unavailable":
		return 4
	case "rate_limited":
		return 5
	default:
		return 1
	}
}
func Invalid(message string) error { return &Error{Code: "invalid_arguments", Message: message} }
func Upstream() error {
	return &Error{Code: "invalid_upstream_data", Message: "Discord returned an invalid or unexpected response"}
}
func ID(s string) bool {
	if len(s) == 0 || len(s) > 20 || s[0] == '0' {
		return false
	}
	var n uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
		d := uint64(c - '0')
		if n > (^uint64(0)-d)/10 {
			return false
		}
		n = n*10 + d
	}
	return true
}
func RequireIDs(ids ...string) error {
	for _, id := range ids {
		if !ID(id) {
			return Invalid("IDs must be positive decimal Discord IDs (passed as strings)")
		}
	}
	return nil
}
func Confirmation(operation, channel, message string) any {
	return map[string]any{"ok": true, "operation": operation, "channel_id": channel, "message_id": message}
}
func HTTPFailure(status int) *Error {
	e := &Error{Code: "upstream_error", Message: fmt.Sprintf("Discord returned HTTP %d", status), Status: status}
	switch status {
	case 400, 405, 413:
		e.Code = "bad_request"
		e.Message = "Discord rejected the request; check content, file sizes, and arguments"
	case 401:
		e.Code = "authentication_failed"
		e.Message = "The selected credential is invalid or expired; sign in to Discord or replace the explicit override"
	case 403:
		e.Code = "permission_denied"
		e.Message = "Discord denied this action for the selected account"
	case 404:
		e.Code = "not_found"
		e.Message = "The requested Discord resource is unavailable"
	case 429:
		e.Code = "rate_limited"
		e.Message = "Discord's rate-limit wait exceeds the command budget; retry later"
	case 202:
		e.Code = "indexing_delay"
		e.Message = "Discord is still indexing this scope; retry this search later"
	}
	return e
}
