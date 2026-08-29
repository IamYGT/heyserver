package api

import (
	"encoding/json"
	"net/http"

	"github.com/IamYGT/heyserver/internal/services/ssl"
)

// handleSSLStatus returns certbot and challenge-plugin readiness without
// exposing configured credential paths or values.
func handleSSLStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, ssl.GetStatus())
	}
}

// handleSSLList returns all Let's Encrypt certificates with health status.
func handleSSLList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		certs, err := ssl.List()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to list certificates: "+err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, certs)
	}
}

// handleSSLGet returns details for a single certificate by domain name.
func handleSSLGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain is required")
			return
		}

		cert, err := ssl.Get(domain)
		if err != nil {
			jsonError(w, http.StatusNotFound, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, cert)
	}
}

// handleSSLRenew triggers certbot renewal for a specific certificate.
func handleSSLRenew() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain is required")
			return
		}

		result, err := ssl.Renew(domain)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		status := http.StatusOK
		if !result.Success {
			status = http.StatusInternalServerError
		}
		jsonResponse(w, status, map[string]any{
			"ok":      result.Success,
			"message": result.Output,
			"domain":  result.Domain,
		})
	}
}

// handleSSLIssue issues a new certificate via certbot.
// Body: { "domain": "example.com", "challengeType": "http-01"|"dns-01", "email": "admin@example.com" }
func handleSSLIssue() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Domain        string `json:"domain"`
			ChallengeType string `json:"challengeType"`
			Email         string `json:"email"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}

		if body.Domain == "" {
			jsonError(w, http.StatusBadRequest, "domain is required")
			return
		}
		if body.ChallengeType != "http-01" && body.ChallengeType != "dns-01" {
			jsonError(w, http.StatusBadRequest, "challengeType must be http-01 or dns-01")
			return
		}

		// Map frontend challenge type to certbot method
		method := "nginx"
		if body.ChallengeType == "dns-01" {
			method = "dns-cloudflare"
		}

		req := &ssl.IssueRequest{
			Domain: body.Domain,
			Email:  body.Email,
			Method: method,
		}

		result, err := ssl.Issue(req)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}

		status := http.StatusCreated
		if !result.Success {
			status = http.StatusInternalServerError
		}
		jsonResponse(w, status, map[string]any{
			"ok":      result.Success,
			"message": result.Output,
			"domain":  result.Domain,
		})
	}
}
