package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/services/firewall"
)

// handleFirewallStatus returns UFW status, default policies, and all rules.
// GET /api/firewall/status
func handleFirewallStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := firewall.GetStatus()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if status.Rules == nil {
			status.Rules = []firewall.Rule{}
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

// handleFirewallRules returns all UFW rules as a JSON array.
// GET /api/firewall/rules
func handleFirewallRules() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := firewall.GetStatus()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rules := status.Rules
		if rules == nil {
			rules = []firewall.Rule{}
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"rules": rules,
		})
	}
}

// handleFirewallAdd adds a new UFW rule.
// POST /api/firewall/rules
//
//	Body: {
//	  "action":    "allow" | "deny" | "reject" | "limit",
//	  "direction": "in" | "out",
//	  "protocol":  "tcp" | "udp" | "any",
//	  "port":      "22" | "80:443" | "ssh",
//	  "from":      "192.168.1.0/24" | "any",
//	  "to":        "any",
//	  "comment":   "SSH access"
//	}
func handleFirewallAdd() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req firewall.AddRuleRequest
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		req.Action = strings.TrimSpace(req.Action)
		if req.Action == "" {
			jsonError(w, http.StatusBadRequest, "action is required (allow|deny|reject|limit)")
			return
		}

		if err := firewall.AddRule(req); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}

		jsonResponse(w, http.StatusCreated, map[string]string{
			"message": "firewall rule added",
		})
	}
}

// handleFirewallDelete deletes a UFW rule by its number.
// DELETE /api/firewall/rules/{number}
func handleFirewallDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		numberStr := r.PathValue("number")
		if numberStr == "" {
			jsonError(w, http.StatusBadRequest, "rule number is required")
			return
		}

		number, err := strconv.Atoi(numberStr)
		if err != nil || number < 1 {
			jsonError(w, http.StatusBadRequest, "rule number must be a positive integer")
			return
		}

		if err := firewall.DeleteRule(number); err != nil {
			// SSH safety errors deserve a 403 Forbidden, others get 500.
			if strings.Contains(err.Error(), "safety:") {
				jsonError(w, http.StatusForbidden, err.Error())
				return
			}
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"message":    "firewall rule deleted",
			"ruleNumber": number,
		})
	}
}

// handleFirewallToggle enables UFW if inactive, disables it if active.
// POST /api/firewall/toggle
// Body: { "enable": true } — explicit intent required (no accidental toggle).
func handleFirewallToggle() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Enable *bool `json:"enable"` // pointer so we can detect missing field
		}
		if err := decodeStrictJSON(r, &req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if req.Enable == nil {
			jsonError(w, http.StatusBadRequest, `"enable" field is required (true or false)`)
			return
		}

		var opErr error
		if *req.Enable {
			opErr = firewall.Enable()
		} else {
			opErr = firewall.Disable()
		}

		if opErr != nil {
			jsonError(w, http.StatusInternalServerError, opErr.Error())
			return
		}

		action := "disabled"
		if *req.Enable {
			action = "enabled"
		}

		jsonResponse(w, http.StatusOK, map[string]string{
			"message": "UFW firewall " + action,
			"status":  action,
		})
	}
}
