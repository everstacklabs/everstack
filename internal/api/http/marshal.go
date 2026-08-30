package http

import (
	"encoding/json"
	"net/http"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

func MarshalJSON(w http.ResponseWriter, v interface{}, err error, statusCode int) {
	if err != nil {
		http.Error(w, err.Error(), statusCode)
		return
	}

	jsonb, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(jsonb)
	logger.WithFields("logID", "HTTP-mslJson").OnError(err).Error("Error writing JSON response")
}
