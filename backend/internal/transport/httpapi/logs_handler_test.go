package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestLogTimeRangeAcceptsRFC3339AndRejectsReversedRange(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/logs?start_time=2026-09-24T09:00:00%2B08:00&end_time=2026-09-24T10:00:00%2B08:00", nil)
	response := httptest.NewRecorder()
	start, end, ok := logTimeRange(response, request)
	if !ok || start == nil || end == nil || !start.Before(*end) {
		t.Fatalf("valid time range = start=%v end=%v ok=%v", start, end, ok)
	}
	if response.Code != 200 {
		t.Fatalf("valid time range status = %d", response.Code)
	}

	reversed := httptest.NewRequest("GET", "/api/v1/logs?start_time=2026-09-24T10:00:00Z&end_time=2026-09-24T09:00:00Z", nil)
	reversedResponse := httptest.NewRecorder()
	_, _, ok = logTimeRange(reversedResponse, reversed)
	if ok || reversedResponse.Code != 400 {
		t.Fatalf("reversed time range ok=%v status=%d", ok, reversedResponse.Code)
	}
}

func TestLogPaginationDefaultsToOneHundredAndCapsAtFiveHundred(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/logs", nil)
	response := httptest.NewRecorder()
	page, pageSize, ok := paginationWithLimit(response, request, 100, 500)
	if !ok || page != 1 || pageSize != 100 {
		t.Fatalf("default log pagination = page %d size %d ok=%v", page, pageSize, ok)
	}

	tooLarge := httptest.NewRequest("GET", "/api/v1/logs?page_size=501", nil)
	tooLargeResponse := httptest.NewRecorder()
	_, _, ok = paginationWithLimit(tooLargeResponse, tooLarge, 100, 500)
	if ok || tooLargeResponse.Code != 400 {
		t.Fatalf("too-large log pagination ok=%v status=%d", ok, tooLargeResponse.Code)
	}
}
