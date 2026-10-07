package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"github.com/fsnotify/fsnotify"
	"io/fs"
	"path/filepath"
	"sort"
	"time"
)

// Run owns all scheduling and controls. Recovery does not depend on a surviving source or mapping.
// Disabled monitoring stops discovery/new transfers; already-started replacements still reconcile.
func (s *UploadService) Run(ctx context.Context) {
	s.mu.Lock()
	s.commands = make(chan uploadCommand)
	s.mode = "running"
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
	results := make(chan completion, 1)
	busy := ""
	var cancel context.CancelFunc
	finish := func(result completion) {
		busy = ""
		cancel = nil
		s.update(func(st *UploadStatus) {
			st.Current = ""
			if result.err != nil {
				st.Error = result.err.Error()
			} else {
				st.Error = ""
			}
		})
		if err := s.refreshUploadView(ctx); err != nil {
			s.update(func(st *UploadStatus) { st.Error = err.Error() })
		}
	}
	stopWorker := func() {
		if cancel != nil {
			cancel()
			finish(<-results)
		}
	}
	defer stopWorker()
	signature := ""
	nextScan := time.Time{}
	dirty := true
	enabled := false
	policy := "skip"
	var mappings []UploadMapping
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
			next := values["CLOUD_UPLOAD_ENABLE"] + values["CLOUD_UPLOAD_PATHS"] + values["CLOUD_UPLOAD_CONFLICT"]
			if signature != next {
				stopWorker()
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
			}
		}
		now := time.Now()
		if enabled && (dirty || !now.Before(nextScan)) {
			dirty = false
			nextScan = now.Add(30 * time.Second)
			for _, m := range mappings {
				if watch != nil {
					_ = filepath.WalkDir(m.LocalPath, func(p string, d fs.DirEntry, err error) error {
						if ctx.Err() != nil {
							return ctx.Err()
						}
						if err != nil {
							return err
						}
						if d.IsDir() && !knownDirs[p] {
							if watch.Add(p) == nil {
								knownDirs[p] = true
							}
						}
						return nil
					})
				}
				files, scanErr := uploadSnapshot(ctx, m)
				if scanErr != nil {
					s.update(func(st *UploadStatus) { st.Error = scanErr.Error() })
					continue
				}
				for _, f := range files {
					key := uploadKey(m, f, policy)
					record, getErr := s.store.Get(ctx, key)
					if getErr != nil {
						s.update(func(st *UploadStatus) { st.Error = getErr.Error() })
						continue
					}
					if record == nil {
						if s.mode == "stopped" {
							if saveErr := s.store.Save(ctx, domain.UploadRecord{Key: uploadControlKey, State: "control", Intent: "running"}); saveErr != nil {
								s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
								continue
							}
							s.mu.Lock()
							s.mode = "running"
							s.mu.Unlock()
						}
						record = &domain.UploadRecord{Key: key, Kind: m.Kind, Root: m.ID, LocalRoot: m.LocalPath, RemotePath: m.Path, Relative: f.Relative, Source: f.Absolute, Size: f.Size, Modified: f.Modified, Policy: policy, State: "pending", UpdatedAt: now.UTC().Format(time.RFC3339Nano)}
						if saveErr := s.store.Save(ctx, *record); saveErr != nil {
							s.update(func(st *UploadStatus) { st.Error = saveErr.Error() })
						}
					}
				}
			}
		}
		if busy == "" {
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
				sort.Slice(records, func(i, j int) bool {
					if (records[i].State == "pending") != (records[j].State == "pending") {
						return records[i].State != "pending"
					}
					if records[i].UpdatedAt == records[j].UpdatedAt {
						return records[i].Key < records[j].Key
					}
					return records[i].UpdatedAt < records[j].UpdatedAt
				})
				for _, r := range records {
					if !uploadRetryDue(r, now) {
						continue
					}
					if r.State == "pending" {
						if recovering[r.Kind+"\x00"+r.Root+"\x00"+r.Source] {
							continue
						}
						sourceExists := uploadUnchanged(uploadLocalFile{Absolute: r.Source, Size: r.Size, Modified: r.Modified})
						if sourceExists && (!enabled || s.mode == "paused") {
							continue
						}
						allowed := false
						for _, m := range mappings {
							if m.Kind == r.Kind && m.ID == r.Root && m.LocalPath == r.LocalRoot {
								allowed = true
							}
						}
						if !allowed && sourceExists {
							continue
						}
						discovered, _ := time.Parse(time.RFC3339Nano, r.UpdatedAt)
						if now.Sub(discovered) < 5*time.Second {
							continue
						}
					} else if s.mode == "paused" && r.State == "uploaded" {
						continue
					}
					busy = r.Key
					worker, stop := context.WithCancel(ctx)
					cancel = stop
					s.update(func(st *UploadStatus) { st.Current = r.Key; st.State = "uploading" })
					go func(r domain.UploadRecord) {
						defer stop()
						progress := domain.WithUploadProgress(worker, func(done, total int64, phase string) { s.reportBytes(r.Key, done, total, phase) })
						m := UploadMapping{Kind: r.Kind, ID: r.Root, Path: r.RemotePath, LocalPath: r.LocalRoot}
						f := uploadLocalFile{Absolute: r.Source, Relative: r.Relative, Size: r.Size, Modified: r.Modified}
						_, err := s.process(progress, m, f, r.Policy)
						results <- completion{r.Key, err}
					}(r)
					break
				}
			}
		}
		if err = s.refreshUploadView(ctx); err != nil {
			s.update(func(st *UploadStatus) { st.Error = err.Error() })
		}
		select {
		case <-ctx.Done():
			return
		case result := <-results:
			finish(result)
		case command := <-s.commands:
			s.mu.RLock()
			allowed := s.uploadActionsLocked()[command.action]
			s.mu.RUnlock()
			if !allowed {
				command.reply <- ErrUploadControl
				continue
			}
			if command.action != "clear_completed" {
				stopWorker()
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
		}
	}
}
