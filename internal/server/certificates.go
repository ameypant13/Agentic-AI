package server

import (
	"errors"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/certs"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
)

func (s *Server) registerCertRoutes(rt *mux.Router) {
	rt.HandleFunc("/certificates", s.listCertificates).Methods(http.MethodGet)
	rt.HandleFunc("/certificates/scan", s.scanCertificates).Methods(http.MethodPost)
	rt.HandleFunc("/certificates/repair", s.repairCertificate).Methods(http.MethodPost)
}

// repairCertificate asks control-hub to restore the DNS records a certificate's
// renewal needs (admins). The target comes from the stored inventory item, so a
// caller can only repair what the scan found broken. control-hub does the
// write with the admin's own token; the console's service tokens stay read-only.
func (s *Server) repairCertificate(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	if !s.cfg.CertRepairEnabled {
		writeError(w, http.StatusForbidden, "certificate repair is turned off on this console (CERT_REPAIR_ENABLED)")
		return
	}
	if s.certs == nil {
		writeError(w, http.StatusServiceUnavailable, "certificate tracking is not configured")
		return
	}
	var in struct {
		Key    string `json:"key"`
		DryRun bool   `json:"dryRun"`
		Force  bool   `json:"force"` // wildcard-renew only: renew in ACM outside its 60-day window
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	it := s.certs.Item(in.Key)
	if it == nil || it.Repair == nil {
		writeError(w, http.StatusBadRequest, "this certificate has nothing control-hub can repair")
		return
	}
	sess := session(r)
	u, err := s.user(sess)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session expired; sign in again")
		return
	}
	path, target := "/api/certificates/repair", it.Repair.CertificateARN+it.Repair.Certificate
	body := map[string]any{"kind": it.Repair.Kind, "certificateArn": it.Repair.CertificateARN,
		"certificate": it.Repair.Certificate, "dryRun": in.DryRun}
	if it.Repair.Kind == "wildcard-renew" {
		path, target = "/api/certificates/wildcards/renew", it.Repair.SecretName
		body = map[string]any{"secretName": it.Repair.SecretName, "dryRun": in.DryRun, "force": in.Force}
	}
	raw, err := s.ch.Post(r.Context(), u.Token, path, body)
	action := "certificate_repair"
	if in.DryRun {
		action = "certificate_repair_plan"
	}
	if err != nil {
		s.st.Audit(sess.Email, action, target, "error", err.Error())
		var apiErr *controlhub.APIError
		if errors.As(err, &apiErr) {
			// control-hub's own answer (403 for non-admins, 502 with partial results).
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(apiErr.Status)
			_, _ = w.Write([]byte(apiErr.Body))
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.st.Audit(sess.Email, action, target, "ok", string(raw))
	if !in.DryRun {
		s.certs.Trigger() // pick up the new records on the next scan
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

// listCertificates returns the stored inventory, soonest expiry first.
func (s *Server) listCertificates(w http.ResponseWriter, r *http.Request) {
	if s.certs == nil {
		writeError(w, http.StatusServiceUnavailable, "certificate tracking is not configured")
		return
	}
	resp := map[string]any{"scannedAt": nil, "durationMs": 0, "items": []certs.Item{}, "errors": []string{},
		"running": s.certs.Running(), "intervalMinutes": int(s.certs.Interval().Minutes()), "repairEnabled": s.cfg.CertRepairEnabled}
	counts := map[string]int{certs.StatusExpired: 0, certs.StatusCritical: 0, certs.StatusWarning: 0, certs.StatusUnknown: 0, certs.StatusOK: 0}
	if inv := s.certs.Inventory(); inv != nil {
		kind := r.URL.Query().Get("kind")
		items := make([]certs.Item, 0, len(inv.Items))
		for _, it := range inv.Items {
			if kind != "" && it.Kind != kind {
				continue
			}
			items = append(items, it)
			counts[it.Status]++
		}
		resp["scannedAt"], resp["durationMs"], resp["items"], resp["errors"] = inv.ScannedAt, inv.DurationMs, items, inv.Errors
	}
	resp["counts"] = counts
	writeJSON(w, http.StatusOK, resp)
}

// scanCertificates queues a rescan (admins).
func (s *Server) scanCertificates(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	if s.certs == nil {
		writeError(w, http.StatusServiceUnavailable, "certificate tracking is not configured")
		return
	}
	s.certs.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}
