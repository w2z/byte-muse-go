package httpapi

import (
	"bytemuse/backend/internal/application"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUploadRoutesRequireSession prevents anonymous directory and upload-task disclosure.
func TestUploadRoutesRequireSession(t *testing.T) {
	handler := New(Dependencies{})
	for _, route := range []string{"directories", "status", "directories/progress", "files"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/cloud-upload/"+route, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s: %d", route, response.Code)
		}
	}
}

// TestUploadPagingRejectsInvalidBounds keeps polling responses bounded for every caller.
func TestUploadPagingRejectsInvalidBounds(t *testing.T) {
	handler := uploadFilesProgress(application.NewUploadService(nil, nil, nil, nil))
	for _, query := range []string{"page=0", "page=1000001", "page=bad", "page_size=101", "page_size=-1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", query, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?page=1&page_size=15", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("empty page: %d", response.Code)
	}
}
