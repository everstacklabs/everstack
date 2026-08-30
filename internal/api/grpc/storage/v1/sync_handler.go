package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	storagepkg "github.com/everstacklabs/everstack/internal/storage"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/google/uuid"
)

// SyncHandler returns an http.HandlerFunc that lists objects directly from the
// S3-compatible bucket and upserts any missing objects into the database.
// This allows objects that were placed into the bucket outside of the app
// (e.g. via the AWS CLI) to show up in the UI.
//
// POST /api/v1/storage/sync
// Optional JSON body: { "config_id": "..." }
//
// Returns JSON: { "synced": <count>, "total": <count> }
func (s *Server) SyncHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		tenantID, err := s.authorizeStorage(r.Context(), storageauth.ActionAdminSync)
		if err != nil {
			writeStorageAuthorizationError(w, err)
			return
		}

		if s.db == nil {
			http.Error(w, "database not available", http.StatusInternalServerError)
			return
		}
		if s.uploadLifecycle == nil {
			http.Error(w, "upload lifecycle not available", http.StatusServiceUnavailable)
			return
		}

		var body struct {
			ConfigID string `json:"config_id"`
		}
		if r.Body != nil && r.ContentLength > 0 {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}

		// If a specific config_id is provided, sync only that one.
		// Otherwise, sync ALL configs for this tenant.
		type syncTarget struct {
			configID string
		}

		var targets []syncTarget

		if body.ConfigID != "" {
			targets = append(targets, syncTarget{configID: body.ConfigID})
		} else {
			// Load all config IDs for this tenant
			cfgRows, cfgErr := s.db.QueryContext(r.Context(),
				`SELECT id FROM object_storage_configs WHERE tenant_id = $1 AND enabled = true`,
				tenantID)
			if cfgErr != nil {
				http.Error(w, "failed to list storage configs", http.StatusInternalServerError)
				return
			}
			defer cfgRows.Close()
			for cfgRows.Next() {
				var id string
				if scanErr := cfgRows.Scan(&id); scanErr == nil {
					targets = append(targets, syncTarget{configID: id})
				}
			}
			if len(targets) == 0 {
				http.Error(w, "no storage configs found for tenant", http.StatusNotFound)
				return
			}
		}

		totalSynced := 0
		totalObjects := 0

		for _, t := range targets {
			store, cfg, err := s.getStoreForConfig(r.Context(), t.configID, tenantID)
			if err != nil {
				slog.Error("storage sync: failed to get store for config", "configID", t.configID)
				continue
			}

			bucket := ""
			configID := ""
			provider := ""
			pathPrefix := ""
			if cfg != nil {
				bucket = cfg.Bucket
				configID = cfg.ID
				provider = cfg.Provider
				pathPrefix = cfg.PathPrefix
			}

			slog.Info("storage sync: listing bucket",
				"bucket", bucket,
				"provider", provider,
				"pathPrefix", pathPrefix,
				"configID", configID,
				"tenantID", tenantID,
			)

			// List all objects from the bucket
			bucketObjects, err := store.List(r.Context(), bucket, "")
			if err != nil {
				slog.Error("storage sync: failed to list bucket objects", "bucket", bucket, "configID", configID)
				continue
			}
			blobStore, err := storagepkg.RequireBlobPlane(store)
			if err != nil {
				slog.Error("storage sync: provider cannot verify imported objects", "configID", configID)
				continue
			}

			// Keep legacy/backfilled registry rows stable while the import
			// idempotency key protects concurrent and later sync scans.
			existingKeys := make(map[string]bool)
			rows, queryErr := s.db.QueryContext(r.Context(),
				`SELECT key FROM object_storage_objects WHERE tenant_id = $1 AND config_id = $2 AND deleted_at IS NULL`,
				tenantID, configID)
			if queryErr != nil {
				slog.Warn("storage sync: failed to load existing registry keys", "configID", configID)
				continue
			}
			for rows.Next() {
				var existingKey string
				if scanErr := rows.Scan(&existingKey); scanErr == nil {
					existingKeys[existingKey] = true
				}
			}
			if rowsErr := rows.Err(); rowsErr != nil {
				rows.Close()
				slog.Warn("storage sync: existing registry key scan failed", "configID", configID)
				continue
			}
			rows.Close()

			for _, obj := range bucketObjects {
				if obj.Key == "" || strings.HasSuffix(obj.Key, "/") || obj.SizeBytes < 0 {
					continue // skip directory markers and invalid provider listings
				}
				if existingKeys[obj.Key] {
					continue
				}

				objectID := uuid.New().String()
				filename := filepath.Base(obj.Key)
				contentType := mime.TypeByExtension(filepath.Ext(filename))
				if contentType == "" {
					contentType = "application/octet-stream"
				}

				// Derive purpose from key path if it follows tenants/<id>/<purpose>/... pattern
				purpose := derivePurpose(obj.Key)
				reader, readErr := blobStore.Get(r.Context(), bucket, obj.Key)
				if readErr != nil || reader == nil || reader.Body == nil {
					slog.Warn("storage sync: failed to read provider object for verification", "configID", configID)
					continue
				}
				hasher := sha256.New()
				readLimit := obj.SizeBytes + 1
				if obj.SizeBytes == int64(^uint64(0)>>1) {
					readLimit = obj.SizeBytes
				}
				actualSize, readErr := io.Copy(hasher, io.LimitReader(reader.Body, readLimit))
				closeErr := reader.Body.Close()
				if readErr != nil || closeErr != nil || actualSize != obj.SizeBytes || reader.SizeBytes != obj.SizeBytes {
					slog.Warn("storage sync: provider object failed size verification", "configID", configID)
					continue
				}
				checksum := hex.EncodeToString(hasher.Sum(nil))

				keyDigest := sha256.Sum256([]byte(fmt.Sprintf(
					"%s\x00%s\x00%d\x00%d",
					configID,
					obj.Key,
					obj.SizeBytes,
					obj.LastModified.UnixNano(),
				)))
				_, created, importErr := s.uploadLifecycle.ImportReady(r.Context(), storagepkg.ImportReadyUploadParams{
					ObjectID:       objectID,
					TenantID:       tenantID,
					ConfigID:       configID,
					Key:            obj.Key,
					Filename:       filename,
					ContentType:    contentType,
					SizeBytes:      obj.SizeBytes,
					ChecksumSHA256: checksum,
					Purpose:        purpose,
					Metadata:       json.RawMessage(`{}`),
					IdempotencyKey: fmt.Sprintf("import:%x", keyDigest[:]),
					ImportedAt:     obj.LastModified,
				})
				if importErr != nil {
					slog.Warn("storage sync: failed to import provider object", "configID", configID)
					continue // skip individual failures
				}
				if created {
					totalSynced++
				}
			}

			totalObjects += len(bucketObjects)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{
			"synced": totalSynced,
			"total":  totalObjects,
		})
	}
}

// derivePurpose tries to extract the purpose from a key like tenants/<id>/<purpose>/<rest>.
// Falls back to "upload" if the key doesn't match a known purpose.
func derivePurpose(key string) string {
	parts := strings.SplitN(key, "/", 4)
	if len(parts) >= 3 && parts[0] == "tenants" {
		switch parts[2] {
		case "dataset", "artifact", "upload", "eval_result":
			return parts[2]
		}
	}
	return "upload"
}
