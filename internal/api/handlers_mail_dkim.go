package api

import (
	"encoding/json"
	"net/http"

	"github.com/IamYGT/heyserver/internal/config"
	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

// handleMailDKIMList handles GET /api/mail/dkim/{domain}
// Returns all DKIM signature configurations known to Stalwart for that domain.
func handleMailDKIMList(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain path parameter is required")
			return
		}

		svc := newMailService(cfg)
		keys, err := svc.ListDKIMKeys(domain)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, keys)
	}
}

// handleMailDKIMGenerate handles POST /api/mail/dkim/{domain}
//
// Request body (JSON):
//
//	{
//	  "selector":  "mail-20260407",   // optional — defaults to "mail-YYYYMMDD"
//	  "algorithm": "Ed25519"          // optional — defaults to "Ed25519"; "Rsa" also accepted
//	}
//
// On success returns 201 Created with the generated DKIMKeyInfo.
func handleMailDKIMGenerate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain path parameter is required")
			return
		}

		var body struct {
			Selector  string                `json:"selector"`
			Algorithm mailsvc.DKIMAlgorithm `json:"algorithm"`
		}
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
		}

		svc := newMailService(cfg)
		info, err := svc.GenerateDKIMKey(domain, body.Selector, body.Algorithm)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, info)
	}
}

// handleMailDKIMDNS handles GET /api/mail/dkim/{domain}/{selector}/dns
// Returns the DNS TXT record value for the specified DKIM key.
func handleMailDKIMDNS(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		selector := r.PathValue("selector")
		if domain == "" || selector == "" {
			jsonError(w, http.StatusBadRequest, "domain and selector path parameters are required")
			return
		}

		svc := newMailService(cfg)
		rec, err := svc.GetDKIMPublicKey(domain, selector)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, rec)
	}
}

// handleMailDKIMRotate handles POST /api/mail/dkim/{domain}/rotate
// Generates a new key (new date-based selector), keeping the current key
// active in Stalwart as a backup until manually removed.
//
// Request body (JSON, optional):
//
//	{ "algorithm": "Ed25519" }
func handleMailDKIMRotate(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain path parameter is required")
			return
		}

		var body struct {
			Algorithm mailsvc.DKIMAlgorithm `json:"algorithm"`
		}
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
				return
			}
		}

		svc := newMailService(cfg)
		info, err := svc.RotateDKIMKey(domain, body.Algorithm)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, info)
	}
}

// handleMailDKIMConfig handles GET /api/mail/dkim/{domain}/config
// Returns raw Stalwart signature settings for the domain.
func handleMailDKIMConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.PathValue("domain")
		if domain == "" {
			jsonError(w, http.StatusBadRequest, "domain path parameter is required")
			return
		}

		svc := newMailService(cfg)
		cfg2, err := svc.GetDKIMConfig(domain)
		if err != nil {
			jsonError(w, mailErrorStatus(err, http.StatusBadGateway), err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, cfg2)
	}
}
