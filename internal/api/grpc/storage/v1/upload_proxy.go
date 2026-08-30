package v1

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/google/uuid"
)

const maxUploadSize = 100 << 20 // 100 MB

// UploadProxyHandler returns an http.HandlerFunc that accepts multipart file
// uploads and streams them directly to the configured object store (R2/S3/MinIO),
// bypassing presigned URLs entirely. This avoids browser CORS issues.
//
// POST /api/v1/storage/upload
// Form fields: purpose, reference_type, reference_id (all optional strings)
// File field:  file
//
// Returns JSON: { "objectId": "...", "key": "..." }
func (s *Server) UploadProxyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		tenantID, err := s.authorizeStorage(r.Context(), storageauth.ActionUploadProxy)
		if err != nil {
			writeStorageAuthorizationError(w, err)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
		if err := r.ParseMultipartForm(maxUploadSize); err != nil {
			http.Error(w, "file too large or bad request", http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file field", http.StatusBadRequest)
			return
		}
		defer file.Close()

		purpose := r.FormValue("purpose")
		if purpose == "" {
			purpose = "upload"
		}
		referenceType := r.FormValue("reference_type")
		referenceId := r.FormValue("reference_id")

		filename := header.Filename
		contentType := header.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if idempotencyKey == "" {
			idempotencyKey = strings.TrimSpace(r.FormValue("idempotency_key"))
		}
		if idempotencyKey == "" {
			idempotencyKey = "proxy:" + uuid.New().String()
		}

		upload, etag, err := s.uploadDirect(r.Context(), tenantID, directUploadParams{
			Purpose:        purpose,
			Filename:       filename,
			ContentType:    contentType,
			SizeBytes:      header.Size,
			ReferenceType:  referenceType,
			ReferenceID:    referenceId,
			IdempotencyKey: idempotencyKey,
			Body:           file,
		})
		if err != nil {
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"objectId":       upload.ObjectID,
			"key":            upload.Key,
			"etag":           etag,
			"idempotencyKey": upload.IdempotencyKey,
		})
	}
}
