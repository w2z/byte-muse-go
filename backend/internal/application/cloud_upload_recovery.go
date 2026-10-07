package application

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

var errUploadAttention = errors.New("需处理：远端文件身份无法确认")

// matchesUpload requires content evidence for path-based providers and an exact ID for 115.
func matchesUpload(r *domain.UploadRecord, e uploadEntry, old bool) bool {
	id, hash, size := r.StageID, r.SHA1, r.Size
	if old {
		id, hash, size = r.OldID, r.OldSHA1, r.OldSize
	}
	if e.Directory || e.Size != size {
		return false
	}
	if r.Kind == "115" && id != "" && e.ID != id {
		return false
	}
	if hash != "" && e.SHA1 != "" {
		return strings.EqualFold(hash, e.SHA1)
	}
	return r.Kind == "115" && id != "" && e.ID == id
}

// selectUploadName excludes persisted reservations as well as visible remote entries.
func (s *UploadService) selectUploadName(ctx context.Context, r *domain.UploadRecord, entries []uploadEntry) error {
	reserved := map[string]bool{}
	records, err := s.store.PendingCommits(ctx)
	if err != nil {
		return err
	}
	for _, other := range records {
		if other.Key != r.Key && other.Kind == r.Kind && other.Parent == r.Parent {
			reserved[other.Target] = true
		}
	}
	base := path.Base(r.Relative)
	ext := path.Ext(base)
	for n := 0; ; n++ {
		name := base
		if n > 0 {
			name = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), n, ext)
		}
		_, found := findUploadEntry(entries, name)
		if !found && !reserved[name] {
			r.Target = name
			return nil
		}
	}
}

// commitUpload writes intent before remote mutations and reconciles ambiguous responses on retry.
// cleanup is persisted only after the installed object is verified; deletion never triggers a new upload.
func (s *UploadService) commitUpload(ctx context.Context, remote uploadRemote, r *domain.UploadRecord, save func() error) (string, error) {
	for attempt := 0; attempt < 12; attempt++ {
		entries, err := remote.List(ctx, r.Parent)
		if err != nil {
			return r.State, err
		}
		for _, name := range []string{r.Stage, r.Target, r.Backup} {
			count := 0
			for _, e := range entries {
				if e.Name == name {
					count++
				}
			}
			if count > 1 {
				return r.State, fmt.Errorf("%w：同名对象不唯一", errUploadAttention)
			}
		}
		stage, hasStage := findUploadEntry(entries, r.Stage)
		target, hasTarget := findUploadEntry(entries, r.Target)
		backup, hasBackup := findUploadEntry(entries, r.Backup)
		if (r.State == "backing_up" || r.State == "committing") && !hasStage && !hasTarget && hasBackup && matchesUpload(r, backup, true) {
			r.State = "restoring"
			if err = save(); err != nil {
				return r.State, err
			}
			continue
		}
		switch r.State {
		case "restoring":
			if hasBackup && !hasTarget && matchesUpload(r, backup, true) {
				if err = remote.Rename(ctx, r.Parent, backup, r.Target); err != nil {
					return r.State, err
				}
				continue
			}
			if !hasBackup && hasTarget && matchesUpload(r, target, true) {
				return r.State, fmt.Errorf("%w：上传临时文件丢失，已恢复旧文件原名", errUploadAttention)
			}
			return r.State, errUploadAttention
		case "uploaded":
			if !hasStage || !matchesUpload(r, stage, false) {
				return r.State, errUploadAttention
			}
			if r.Policy == "keep_both" {
				if err = s.selectUploadName(ctx, r, entries); err != nil {
					return r.State, err
				}
				_, hasTarget = findUploadEntry(entries, r.Target)
			}
			if hasTarget && r.Policy == "skip" {
				r.Intent = "skip"
				r.State = "discarding"
				if err = save(); err != nil {
					return r.State, err
				}
				return s.discardUpload(ctx, remote, r, save)
			}
			if hasTarget {
				if target.Directory || hasBackup {
					return r.State, errUploadAttention
				}
				r.OldID, r.OldSHA1, r.OldSize = target.ID, target.SHA1, target.Size
				if !matchesUpload(r, target, true) {
					return r.State, errUploadAttention
				}
				r.State = "backing_up"
			} else {
				r.State = "committing"
			}
			if err = save(); err != nil {
				return r.State, err
			}
		case "backing_up":
			if hasBackup {
				if !matchesUpload(r, backup, true) || hasTarget {
					return r.State, errUploadAttention
				}
			} else {
				if !hasTarget || !matchesUpload(r, target, true) {
					return r.State, errUploadAttention
				}
				if err = remote.Rename(ctx, r.Parent, target, r.Backup); err != nil {
					return r.State, err
				}
				continue
			}
			r.State = "committing"
			if err = save(); err != nil {
				return r.State, err
			}
		case "committing":
			if hasStage {
				if hasTarget || !matchesUpload(r, stage, false) {
					return r.State, errUploadAttention
				}
				if err = remote.Rename(ctx, r.Parent, stage, r.Target); err != nil {
					return r.State, err
				}
				continue
			}
			if !hasTarget || !matchesUpload(r, target, false) {
				return r.State, errUploadAttention
			}
			r.State = "cleanup"
			if err = save(); err != nil {
				return r.State, err
			}
		case "cleanup":
			if !hasTarget || !matchesUpload(r, target, false) {
				return r.State, errUploadAttention
			}
			if hasBackup {
				if !matchesUpload(r, backup, true) {
					return r.State, errUploadAttention
				}
				if err = remote.Delete(ctx, r.Parent, backup); err != nil {
					return r.State, err
				}
				continue
			}
			r.State = "completed"
			return r.State, save()
		default:
			return r.State, nil
		}
	}
	return r.State, fmt.Errorf("远端操作回查未收敛，保留当前阶段稍后重试")
}

// discardUpload removes an abandoned private stage and preserves all final and backup objects.
func (s *UploadService) discardUpload(ctx context.Context, remote uploadRemote, r *domain.UploadRecord, save func() error) (string, error) {
	if cd, ok := remote.(uploadCDRemote); ok {
		if err := cd.c.ControlUpload(ctx, path.Join(r.Parent, r.Stage), "cancel"); err != nil {
			return r.State, err
		}
	}
	entries, err := remote.List(ctx, r.Parent)
	if err != nil {
		return r.State, err
	}
	// Legacy records may have backed up the old target before persisting their next phase.
	// Never terminate cleanup while a recovery backup still exists.
	if _, exists := findUploadEntry(entries, r.Backup); exists {
		return r.State, fmt.Errorf("%w：旧文件备份仍存在，保留恢复记录", errUploadAttention)
	}
	if e, ok := findUploadEntry(entries, r.Stage); ok {
		count := 0
		for _, entry := range entries {
			if entry.Name == r.Stage {
				count++
			}
		}
		owned := r.StageID != "" && ((r.Kind == "115" && e.ID == r.StageID) || (r.Kind == "cd2" && r.StageSHA1 != "" && strings.EqualFold(r.StageSHA1, e.SHA1) && r.StageSize == e.Size))
		if e.Directory || count != 1 || !owned {
			return r.State, errUploadAttention
		}
		if err = remote.Delete(ctx, r.Parent, e); err != nil {
			return r.State, err
		}
		entries, err = remote.List(ctx, r.Parent)
		if err != nil {
			return r.State, err
		}
		if _, ok = findUploadEntry(entries, r.Stage); ok {
			return r.State, fmt.Errorf("临时文件删除后仍然存在")
		}
	}
	r.State = "discarded"
	if r.Intent == "stop" || r.Intent == "delete" {
		r.State = "stopped"
	}
	if r.Intent == "skip" {
		r.State = "skipped"
	}
	if r.Intent == "delete" {
		r.Hidden = true
	}
	return r.State, save()
}
