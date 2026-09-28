package application

import (
	"bytemuse/backend/internal/ports"
	"context"
	"testing"
)

func TestQueueRejectsUnavailableStorage(t *testing.T) {
	s := NewQueuedCollectionService(nil, nil, nil)
	if _, e := s.Enqueue(context.Background(), ports.CollectionRequest{}); e == nil {
		t.Fatal("unavailable queue accepted request")
	}
	if _, e := s.RunStatus(context.Background(), "missing"); e == nil {
		t.Fatal("unavailable queue returned status")
	}
}
