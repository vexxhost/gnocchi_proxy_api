package gnocchi

import (
	"encoding/json"
	"errors"
	"net/http"
)

type ErrorResponse struct {
	Description string `json:"description"`
	Title       string `json:"title"`
	Code        int    `json:"code"`
}

func WriteError(w http.ResponseWriter, status int, title, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Description: description,
		Title:       title,
		Code:        status,
	})
}

func Unsupported(w http.ResponseWriter, detail string) {
	WriteError(w, http.StatusNotImplemented, "Not Implemented", detail)
}

var ErrNotFound = errors.New("not found")
