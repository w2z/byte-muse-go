package application

import (
	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
	"context"
	"errors"
	"github.com/fsnotify/fsnotify"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Run owns all scheduling and controls. Recovery does not depend on a surviving source or mapping.
// Disabled monitoring stops discovery/new transfers; already-started replacements still reconcile.
func (s *UploadService) Run(ctx context.Context) {
	durable := s.store
	queue, loadErr := newUploadQueueStore(ctx, durable)
	if loadErr != nil {
		s.update(func(status *UploadStatus) { status.State, status.Error = "failed", loadErr.Error() })
		return
	}
	s.store = queue
	defer func() { s.store = durable }()
	s.mu.Lock()
	s.commands = make(chan uploadCommand)
	s.wake = make(chan struct{}, 1)
	s.mode = "running"
	s.active = make(map[string]bool)
	s.mu.Unlock()
	control, err := s.store.Get(ctx, uploadControlKey)
	if err != nil {
		s.update(func(st *UploadStatus) { st.Error = err.Error(); st.State = "failed" })
		return
	}
	if control != nil {
		s.mu.Lock()
		s.mode = control.Intent
		s.mu.Unlock()
		if control.Target != "" {
			if err = s.applyUploadControl(ctx, control.Target); err != nil {
				s.update(func(st *UploadStatus) { st.Error = err.Error() })
				return
			}
		}
	}
	_ = s.refreshUploadView(ctx)
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.ready = false; s.mu.Unlock() }()
	watch, watchErr := fsnotify.NewWatcher()
	var events <-chan fsnotify.Event
	var watchErrors <-chan error
	if watchErr == nil {
		events = watch.Events
		watchErrors = watch.Errors
		defer watch.Close()
	}
	knownDirs := map[string]bool{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	type completion struct {
		key string
		err error
	}
	results := make(chan completion, 32)
	workers := map[string]context.CancelFunc{}
	working := map[string]domain.UploadRecord{}
	providerRetry := map[string]time.Time{}
	finish := func(result completion) {
		var deferred *pan115.UploadRetryError
		if errors.As(result.err, &deferred) {
			kind := working[result.key].Kind
			if deferred.Until.After(providerRetry[kind]) {
				providerRetry[kind] = deferred.Until
			}
		}
		delete(workers, result.key)
		delete(working, result.key)
		s.update(func(st *UploadStatus) {
			delete(s.active, result.key)
			st.Current = ""
			var first domain.UploadRecord
			for _, record := range working {
				if st.Current == "" || uploadQueueBefore(record, first) {
					first = record
					st.Current = record.Key
				}
			}
			if result.err != nil {
				st.Error = result.err.Error()
			} else {
				st.Error = ""
			}
		})
		if record, readErr := queue.Get(ctx, result.key); readErr == nil && record != nil {
			s.mergeUploadView([]domain.UploadRecord{*record}, false)
		}
	}
	stopWorkers := func(matches func(string) bool) {
		waiting := map[string]bool{}
		for key, cancel := range workers {
			if matches(key) {
				cancel()
				waiting[key] = true
			}
		}
		for len(waiting) > 0 {
			result := <-results
			delete(waiting, result.key)
			finish(result)
		}
	}
	stopAll := func() { stopWorkers(func(string) bool { return true }) }
	defer stopAll()
	signature := ""
	nextScan := time.Time{}
	dirty := true
	enabled := false
	policy := "skip"
	uploadMode := "copy"
	concurrency := 5
	var mappings []UploadMapping
	var discovery <-chan uploadDiscovery
	var cancelDiscovery context.CancelFunc
	var scanManual bool
	var scanPolicy, scanMode string
	stopDiscovery := func() {
		if cancelDiscovery != nil {
			cancelDiscovery()
		}
		for discovery != nil {
			if _, ok := <-discovery; !ok {
				discovery = nil
			}
		}
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
	}
	defer stopDiscovery()
	observations := map[string]uploadObservation{}
	readinessOffset := 0
	for {
		// A failed bulk write must replay before discovery or another file can start.
		persisted, controlErr := s.store.Get(ctx, uploadControlKey)
		if controlErr == nil && persisted != nil && persisted.Target != "" {
			controlErr = s.applyUploadControl(ctx, persisted.Target)
		}
		if controlErr != nil {
			s.update(func(st *UploadStatus) { st.Error = controlErr.Error(); st.State = "failed" })
			select {
			case <-ctx.Done():
				return
			case command := <-s.commands:
				command.reply <- controlErr
			case <-ticker.C:
			}
			continue
		}
		values, e := s.settings(ctx)
		if e != nil {
			s.update(func(st *UploadStatus) { st.Error = e.Error() })
		} else {
			concurrency = uploadConcurrency(values["CLOUD_UPLOAD_CONCURRENCY"])
			uploadMode = "copy"
			if values["CLOUD_UPLOAD_MODE"] == "move" {
				uploadMode = "move"
			}
			next := values["CLOUD_UPLOAD_ENABLE"] + values["CLOUD_UPLOAD_PATHS"] + values["CLOUD_UPLOAD_CONFLICT"]
			if signature != next {
				stopDiscovery()
				stopAll()
				signature = next
				dirty = true
				mappings, e = parseUploadMappings(values["CLOUD_UPLOAD_PATHS"])
				enabled = e == nil && values["CLOUD_UPLOAD_ENABLE"] == "true"
				policy = values["CLOUD_UPLOAD_CONFLICT"]
				if policy == "" {
					policy = "skip"
				}
				s.mu.Lock()
				s.mappings = mappings
				s.status.Enabled = enabled
				if e != nil {
					s.status.Error = e.Error()
				}
				s.mu.Unlock()
				if viewErr := s.refreshUploadView(ctx); viewErr != nil {
					s.update(func(status *UploadStatus) { status.Error = viewErr.Error() })
				}
			}
		}
		now := time.Now()
		// A scan request forces one discovery pass. With monitoring off, manualUpload finishes that batch.
		s.mu.Lock()
		scanNow := s.scanRequested
		if scanNow {
			s.scanRequested = false
			if !enabled {
				s.manualUpload = true
			}
		}
		s.mu.Unlock()
		if discovery == nil && (scanNow || (enabled && s.mode == "running" && (dirty || !now.Before(nextScan)))) {
			dirty = false
			nextScan = now.Add(30 * time.Second)
			scanManual = scanNow
			scanPolicy, scanMode = policy, uploadMode
			scanContext, cancel := context.WithCancel(ctx)
			cancelDiscovery = cancel
			stream := make(chan uploadDiscovery, 1)
			discovery = stream
			s.mu.Lock()
			s.scanning = true
			s.mu.Unlock()
			go discoverUploadFiles(scanContext, append([]UploadMapping(nil), mappings...), stream)
		}
		s.mu.RLock()
		manual := s.manualUpload
		s.mu.RUnlock()
		if enabled && manual {
			s.mu.Lock()
			s.manualUpload = false
			s.mu.Unlock()
			manual = false
		}
		if len(workers) <= concurrency {
			records, listErr := s.store.PendingCommits(ctx)
			if listErr != nil {
				s.update(func(st *UploadStatus) { st.Error = listErr.Error() })
			} else {
				recovering := map[string]bool{}
				for _, r := range records {
					if r.State != "pending" {
						recovering[r.Kind+"\x00"+r.Root+"\x00"+r.Source] = true
					}
				}
				if readinessOffset >= len(records) {
					readinessOffset = 0
				}
				checks, started := 0, false
				observing := false
				for index := 0; index < len(records); index++ {
					recordIndex := (readinessOffset + index) % len(records)
					r := records[recordIndex]
					if now.Before(providerRetry[r.Kind]) {
						continue
					}
					if _, active := workers[r.Key]; active {
						continue
					}
					conflict := false
					for _, active := range working {
						if uploadTasksConflict(active, r) {
							conflict = true
							break
						}
					}
					if conflict {
						continue
					}
					if r.QueuedAt == "" {
						freezeUploadQueueTime(&r)
						if saveErr := s.store.Save(ctx, r); saveErr != nil {
							s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
							continue
						}
					}
					if !uploadRetryDue(r, now) {
						continue
					}
					if len(workers) >= concurrency {
						if r.State != "pending" || r.Intent == "delete" || uploadIndividuallyPaused(r, s.mode) || (!enabled && !manual && r.Intent != "run") {
							continue
						}
						checks++
						if checks > concurrency*2 {
							break
						}
						info, statErr := os.Lstat(r.Source)
						if statErr == nil && info.Mode().IsRegular() {
							observeUploadSource(observations, uploadLocalFile{Absolute: r.Source, Size: info.Size(), Modified: info.ModTime().UnixNano()}, now, uploadSourceAccess(r.Source))
						}
						continue
					}
					if r.State == "pending" {
						if recovering[r.Kind+"\x00"+r.Root+"\x00"+r.Source] {
							continue
						}
						if r.Intent != "delete" && ((!enabled && !manual && r.Intent != "run") || uploadIndividuallyPaused(r, s.mode)) {
							continue
						}
						sourceExists := uploadUnchanged(uploadLocalFile{Absolute: r.Source, Size: r.Size, Modified: r.Modified})
						allowed := false
						for _, m := range mappings {
							if m.Kind == r.Kind && m.ID == r.Root && m.LocalPath == r.LocalRoot {
								allowed = true
							}
						}
						if !allowed && sourceExists && r.Intent != "delete" {
							continue
						}
						if r.Intent != "delete" {
							checks++
							if checks > max(32, concurrency*4) {
								if !started && !observing {
									readinessOffset = recordIndex
								}
								break
							}
							info, statErr := os.Lstat(r.Source)
							if statErr == nil && info.Mode().IsRegular() {
								observed := uploadLocalFile{Absolute: r.Source, Size: info.Size(), Modified: info.ModTime().UnixNano()}
								if reason := observeUploadSource(observations, observed, now, uploadSourceAccess(r.Source)); reason != "" {
									if _, exists := observations[r.Source]; exists {
										observing = true
									}
									if r.ErrorKind == "locked" && r.Error == reason {
										continue
									}
									r.Error, r.ErrorKind = reason, "locked"
									r.NextRetry = now.Add(time.Second).UTC().Format(time.RFC3339Nano)
									if saveErr := s.store.Save(ctx, r); saveErr != nil {
										s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
									}
									continue
								}
							}
							if r.ErrorKind == "locked" {
								r.Error, r.ErrorKind, r.NextRetry = "", "", ""
								if saveErr := s.store.Save(ctx, r); saveErr != nil {
									s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
									continue
								}
							}
						}
						if r.Intent != "delete" && !sourceExists {
							info, statErr := os.Lstat(r.Source)
							if statErr == nil && info.Mode().IsRegular() {
								replacement := r
								replacement.Size, replacement.Modified = info.Size(), info.ModTime().UnixNano()
								mapping := UploadMapping{Kind: r.Kind, ID: r.Root, LocalPath: r.LocalRoot}
								replacement.Key = uploadKey(mapping, uploadLocalFile{Absolute: r.Source, Size: replacement.Size, Modified: replacement.Modified}, r.Policy)
								replacement.Parent, replacement.Stage, replacement.StageID, replacement.SHA1 = "", "", "", ""
								replacement.OldID, replacement.OldSHA1, replacement.StageSHA1 = "", "", ""
								replacement.Error, replacement.ErrorKind, replacement.NextRetry = "", "", ""
								replacement.Retries = 0
								replacement.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
								previous, getErr := s.store.Get(ctx, replacement.Key)
								if getErr != nil {
									s.update(func(st *UploadStatus) { st.Error = getErr.Error() })
									continue
								}
								if previous == nil {
									if saveErr := s.store.Save(ctx, replacement); saveErr != nil {
										s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
										continue
									}
								}
							}
							if r.Parent == "" {
								r.State, r.Hidden = "discarded", true
								if saveErr := s.store.Save(ctx, r); saveErr != nil {
									s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
								}
								continue
							}
						}
					} else if uploadIndividuallyPaused(r, s.mode) && r.State == "uploaded" {
						continue
					}
					worker, stop := context.WithCancel(ctx)
					started = true
					workers[r.Key], working[r.Key] = stop, r
					s.update(func(st *UploadStatus) {
						s.active[r.Key] = true
						if st.Current == "" {
							st.Current = r.Key
						}
						st.State = "uploading"
					})
					go func(r domain.UploadRecord) {
						defer stop()
						progress := domain.WithUploadProgress(worker, func(done, total int64, phase string) { s.reportBytes(r.Key, done, total, phase) })
						m := UploadMapping{Kind: r.Kind, ID: r.Root, Path: r.RemotePath, LocalPath: r.LocalRoot}
						f := uploadLocalFile{Absolute: r.Source, Relative: r.Relative, Size: r.Size, Modified: r.Modified}
						_, err := s.process(progress, m, f, r.Policy)
						results <- completion{r.Key, err}
					}(r)
				}
				if started || checks <= max(32, concurrency*4) {
					readinessOffset = 0
				}
			}
		}
		if manual && len(workers) == 0 && discovery == nil {
			pending, pendingErr := s.store.PendingCommits(ctx)
			if pendingErr != nil {
				s.update(func(st *UploadStatus) { st.Error = pendingErr.Error() })
			} else if !uploadBatchActive(pending) {
				s.mu.Lock()
				s.manualUpload = false
				s.mu.Unlock()
			}
		}
		if err = s.refreshUploadChanges(ctx); err != nil {
			s.update(func(st *UploadStatus) { st.Error = err.Error() })
		}
		discoveryDeadline := time.Now().Add(250 * time.Millisecond)
	waitForUploadEvent:
		select {
		case <-ctx.Done():
			return
		case event, ok := <-discovery:
			if !ok {
				discovery = nil
				s.mu.Lock()
				s.scanning = false
				s.mu.Unlock()
				continue
			}
			if event.err != nil {
				s.update(func(st *UploadStatus) { st.Error = event.err.Error() })
				continue
			}
			if event.directory != "" {
				if watch != nil && !knownDirs[event.directory] && watch.Add(event.directory) == nil {
					knownDirs[event.directory] = true
				}
			} else {
				m, f := event.mapping, event.file
				key := uploadKey(m, f, scanPolicy)
				if _, active := workers[key]; active {
					goto discoveryHandled
				}
				record, getErr := s.store.Get(ctx, key)
				if getErr != nil {
					s.update(func(st *UploadStatus) { st.Error = getErr.Error() })
					goto discoveryHandled
				}
				// Only an explicit scan may re-admit a cleared terminal task. Recovery receipts and visible tasks remain intact.
				if record == nil || (scanManual && record.Hidden && uploadTerminal(record.State)) {
					if s.mode == "stopped" {
						if saveErr := s.store.Save(ctx, domain.UploadRecord{Key: uploadControlKey, State: "control", Intent: "running"}); saveErr != nil {
							s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
							goto discoveryHandled
						}
						s.mu.Lock()
						s.mode = "running"
						s.mu.Unlock()
					}
					record = &domain.UploadRecord{Key: key, Kind: m.Kind, Root: m.ID, LocalRoot: m.LocalPath, RemotePath: m.Path, Relative: f.Relative, Source: f.Absolute, Size: f.Size, Modified: f.Modified, Policy: scanPolicy, Mode: scanMode, State: "pending", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
					record.QueuedAt = record.UpdatedAt
					if scanManual {
						record.Intent = "run"
					}
					if saveErr := s.store.Save(ctx, *record); saveErr != nil {
						s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
						goto discoveryHandled
					}
					s.publishDiscoveredUpload(*record)
					if len(workers) < concurrency {
						discoveryDeadline = time.Time{}
					}
				} else if record.QueuedAt == "" || (scanManual && !record.Hidden && !uploadTerminal(record.State) && record.Intent == "") {
					if scanManual && !record.Hidden && !uploadTerminal(record.State) && record.Intent == "" {
						record.Intent = "run"
					}
					if record.QueuedAt == "" {
						record.QueuedAt = record.UpdatedAt
						if record.QueuedAt == "" {
							record.QueuedAt = now.UTC().Format(time.RFC3339Nano)
						}
					}
					if saveErr := s.store.Save(ctx, *record); saveErr != nil {
						s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
					}
				}
			}
		discoveryHandled:
			if time.Now().Before(discoveryDeadline) {
				goto waitForUploadEvent
			}
		case result := <-results:
			finish(result)
		case command := <-s.commands:
			if command.scope != "" {
				if command.action != "clear_completed" && command.action != "clear_failed" {
					stopWorkers(func(key string) bool { return uploadCommandTargetsBusy(ctx, s, key, command) })
				}
				command.reply <- s.applyScopedUploadControl(ctx, command.action, command.scope, command.key)
				continue
			}
			s.mu.RLock()
			allowed := s.uploadActionsLocked()[command.action]
			s.mu.RUnlock()
			if !allowed {
				command.reply <- ErrUploadControl
				continue
			}
			if command.action == "stop" || command.action == "delete_all" || command.action == "pause" {
				stopDiscovery()
				dirty = true
			}
			if command.action == "scan" {
				s.mu.Lock()
				s.scanRequested = true
				s.mu.Unlock()
				command.reply <- nil
				continue
			}
			if command.action != "clear_completed" && command.action != "retry_all" {
				stopAll()
			}
			command.reply <- s.applyUploadControl(ctx, command.action)
		case _, ok := <-events:
			if ok {
				dirty = true
			} else {
				events = nil
			}
		case e, ok := <-watchErrors:
			if ok {
				s.update(func(st *UploadStatus) { st.Error = e.Error() })
				dirty = true
			} else {
				watchErrors = nil
			}
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

// uploadConcurrency applies the same safe default to absent, cleared and invalid persisted settings.
func uploadConcurrency(raw string) int {
	count, err := strconv.Atoi(raw)
	if err != nil || count < 1 || count > 32 {
		return 5
	}
	return count
}

// uploadTasksConflict prevents concurrent versions and overlapping mapping destinations from replacing each other.
// Keep-both reservations and directory creation additionally share the preparation lock.
func uploadTasksConflict(left, right domain.UploadRecord) bool {
	if filepath.Clean(left.Source) == filepath.Clean(right.Source) {
		return true
	}
	if left.Kind != right.Kind {
		return false
	}
	leftPath := filepath.ToSlash(filepath.Join(left.RemotePath, left.Relative))
	rightPath := filepath.ToSlash(filepath.Join(right.RemotePath, right.Relative))
	return strings.EqualFold(leftPath, rightPath) || (left.Root == right.Root && strings.EqualFold(left.Relative, right.Relative))
}

func uploadFileLocked(path string) bool {
	release, err := uploadSourceGuard(path)
	if err == nil {
		release()
	}
	return errors.Is(err, errUploadFileLocked)
}
