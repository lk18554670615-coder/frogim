package deployment

import (
	"encoding/json"
	"io"
	"net/http"
)

type BackupControlRequest struct {
	Nonce            string          `json:"nonce"`
	Operation        BackupOperation `json:"operation"`
	ExpectedRevision int64           `json:"expectedRevision"`
}
type BackupControlReport struct {
	Nonce   string         `json:"nonce"`
	Status  ExecutorStatus `json:"status"`
	Receipt BackupReceipt  `json:"receipt"`
}

// Agent.Handler applies the platform mTLS identity check to all these routes.
// Request fields contain no path, key, shell, Docker options or destination URL.
func (a *Agent) backupRoutes(mux *http.ServeMux) {
	for _, action := range []string{"status", "submit", "retry", "cancel"} {
		mux.HandleFunc("POST /internal/agent/backup/"+action, func(w http.ResponseWriter, r *http.Request) {
			var in BackupControlRequest
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if r.Header.Get("Content-Type") != "application/json" || d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || !noncePattern.MatchString(in.Nonce) || in.ExpectedRevision < 0 {
				w.WriteHeader(400)
				return
			}
			var err error
			if action == "submit" {
				_, err = a.executor.SubmitBackup(in.Operation)
			}
			if action == "retry" || action == "cancel" {
				_, prior, e := a.executor.BackupStatus(in.Operation.ID)
				if e != nil || prior.Operation != in.Operation {
					w.WriteHeader(409)
					return
				}
				if action == "retry" {
					_, err = a.executor.RetryBackup(in.Operation.ID, in.ExpectedRevision)
				} else {
					_, err = a.executor.CancelBackup(in.Operation.ID, in.ExpectedRevision)
				}
			}
			if err != nil {
				w.WriteHeader(409)
				return
			}
			status, receipt, err := a.executor.BackupStatus(in.Operation.ID)
			if err != nil || (in.Operation.ID != "" && receipt.Operation != in.Operation) {
				w.WriteHeader(409)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(BackupControlReport{Nonce: in.Nonce, Status: status, Receipt: receipt})
		})
	}
}
