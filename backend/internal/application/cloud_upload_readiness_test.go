package application

import (
	"testing"
	"time"
)

func TestUploadReadinessDoesNotDelayConfirmedIdle(t *testing.T) {
	observations := map[string]uploadObservation{}
	file := uploadLocalFile{Absolute: "ready", Size: 12, Modified: 100}
	if reason := observeUploadSource(observations, file, time.Now(), "idle"); reason != "" {
		t.Fatal(reason)
	}
}

func TestUploadReadinessUnknownRequiresStableObservations(t *testing.T) {
	observations := map[string]uploadObservation{}
	file := uploadLocalFile{Absolute: "copy", Size: 12, Modified: 100}
	now := time.Now()
	if observeUploadSource(observations, file, now, "unknown") == "" {
		t.Fatal("first sample is not proof of stability")
	}
	file.Size = 20
	if observeUploadSource(observations, file, now.Add(3*time.Second), "unknown") == "" {
		t.Fatal("size change must restart observation")
	}
	if observeUploadSource(observations, file, now.Add(4*time.Second), "unknown") == "" {
		t.Fatal("not enough observation time")
	}
	if reason := observeUploadSource(observations, file, now.Add(5*time.Second), "unknown"); reason != "" {
		t.Fatal(reason)
	}
	if observeUploadSource(observations, file, now.Add(6*time.Second), "busy") != "该文件正在被占用" {
		t.Fatal("writer ignored")
	}
	if observeUploadSource(observations, file, now.Add(7*time.Second), "unknown") == "" {
		t.Fatal("writer must invalidate stability evidence")
	}
}
