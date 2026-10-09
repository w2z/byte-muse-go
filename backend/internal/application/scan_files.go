package application

import (
	"context"
	"strings"
	"sync"

	"bytemuse/backend/internal/domain"
)

// ScanFileRow is a task-local directory or file snapshot, never a playback URL.
type ScanFileRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
	State     string `json:"state"`
	Total     int    `json:"total"`
	Processed int    `json:"processed"`
	Failed    int    `json:"failed"`
	Percent   int    `json:"percent"`
	Bytes     int64  `json:"bytes"`
	Size      int64  `json:"size"`
	parent    string
	closed    bool
}

// ScanFilePage contains only direct children; totals grow as traversal discovers files.
type ScanFilePage struct {
	Items     []ScanFileRow `json:"items"`
	Total     int           `json:"total"`
	Available bool          `json:"available"`
}

type scanFileTreeKey struct{}
type scanFileMappingKey struct{}
type scanFileTree struct {
	mu       sync.Mutex
	id       string
	rows     map[string]*ScanFileRow
	children map[string][]string
}

func newScanFileTree(id string) *scanFileTree {
	return &scanFileTree{id: id, rows: map[string]*ScanFileRow{}, children: map[string][]string{}}
}

func (tree *scanFileTree) add(row ScanFileRow) *ScanFileRow {
	if existing := tree.rows[row.ID]; existing != nil {
		return existing
	}
	tree.rows[row.ID] = &row
	tree.children[row.parent] = append(tree.children[row.parent], row.ID)
	return &row
}

func withScanFileMapping(ctx context.Context, mapping domain.StrmMapping) context.Context {
	tree, _ := ctx.Value(scanFileTreeKey{}).(*scanFileTree)
	if tree == nil {
		return ctx
	}
	id := journalKey(mapping.Kind, mapping.ID, mapping.LocalPath)
	tree.mu.Lock()
	tree.add(ScanFileRow{ID: id, Name: mapping.Path, Kind: "directory", State: "scanning"})
	tree.mu.Unlock()
	return context.WithValue(ctx, scanFileMappingKey{}, id)
}

func (tree *scanFileTree) directory(root, relative string) string {
	parent := root
	segments := strings.Split(strings.Trim(relative, "/"), "/")
	for index, name := range segments {
		if name == "" {
			continue
		}
		id := journalKey(root, "directory", strings.Join(segments[:index+1], "/"))
		tree.add(ScanFileRow{ID: id, Name: name, Kind: "directory", State: "scanning", parent: parent})
		parent = id
	}
	return parent
}

// scanFileDirectory closes discovery separately from downloads still running underneath it.
func scanFileDirectory(ctx context.Context, relative string) func(bool) {
	tree, _ := ctx.Value(scanFileTreeKey{}).(*scanFileTree)
	root, _ := ctx.Value(scanFileMappingKey{}).(string)
	if tree == nil || root == "" {
		return func(bool) {}
	}
	tree.mu.Lock()
	id := tree.directory(root, relative)
	tree.mu.Unlock()
	return func(success bool) {
		tree.mu.Lock()
		defer tree.mu.Unlock()
		row := tree.rows[id]
		row.closed = success
		if !success {
			row.State = "interrupted"
		}
	}
}

func scanFileIdentity(ctx context.Context, file strmSourceFile, operation string) (*scanFileTree, string, string) {
	tree, _ := ctx.Value(scanFileTreeKey{}).(*scanFileTree)
	root, _ := ctx.Value(scanFileMappingKey{}).(string)
	if tree == nil || root == "" {
		return nil, "", ""
	}
	return tree, root, journalKey(root, file.Directory, file.ID, file.Name, operation)
}

// startScanFile counts each discovered file once; terminal updates are idempotent.
func startScanFile(ctx context.Context, file strmSourceFile, operation string) func(string) {
	tree, root, id := scanFileIdentity(ctx, file, operation)
	if tree == nil {
		return func(string) {}
	}
	tree.mu.Lock()
	if tree.rows[id] == nil {
		parent := tree.directory(root, file.Directory)
		name := file.Name
		if operation == "generate" {
			name += ".strm"
		}
		tree.add(ScanFileRow{ID: id, Name: name, Kind: "file", Operation: operation, State: "processing", Total: 1, parent: parent})
		for ancestor := parent; ancestor != ""; ancestor = tree.rows[ancestor].parent {
			tree.rows[ancestor].Total++
		}
	}
	tree.mu.Unlock()
	return func(state string) {
		tree.mu.Lock()
		defer tree.mu.Unlock()
		row := tree.rows[id]
		if row.Processed != 0 {
			return
		}
		row.State, row.Processed = state, 1
		if state == "failed" || state == "interrupted" {
			row.Failed = 1
		}
		if state == "completed" || state == "skipped" {
			row.Percent = 100
		}
		for ancestor := row.parent; ancestor != ""; ancestor = tree.rows[ancestor].parent {
			tree.rows[ancestor].Processed++
			tree.rows[ancestor].Failed += row.Failed
		}
	}
}

func reportScanFileBytes(ctx context.Context, file strmSourceFile, bytes, size int64) {
	tree, _, id := scanFileIdentity(ctx, file, "download")
	if tree == nil {
		return
	}
	tree.mu.Lock()
	defer tree.mu.Unlock()
	if row := tree.rows[id]; row != nil {
		row.Bytes, row.Size = bytes, max(int64(0), size)
		if size > 0 {
			row.Percent = int(min(int64(99), bytes*100/size))
		}
	}
}

func (tree *scanFileTree) page(parent string, page, size int) ScanFilePage {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	ids := tree.children[parent]
	result := ScanFilePage{Items: []ScanFileRow{}, Total: len(ids), Available: true}
	start := len(ids)
	if page > 0 && size > 0 && page-1 <= len(ids)/size {
		start = min(len(ids), (page-1)*size)
	}
	for _, id := range ids[start:min(len(ids), start+size)] {
		row := *tree.rows[id]
		if row.Kind == "directory" {
			if row.Total > 0 {
				row.Percent = row.Processed * 100 / row.Total
			}
			if row.closed && row.Processed == row.Total {
				row.State, row.Percent = "completed", 100
				if row.Failed > 0 {
					row.State = "failed"
				}
			} else if row.State != "interrupted" {
				row.State = "scanning"
				if row.closed {
					row.State = "processing"
				}
				row.Percent = min(99, row.Percent)
			}
		}
		result.Items = append(result.Items, row)
	}
	return result
}

// Files pages the latest execution only. A restart rebuilds this observational list from checkpoints.
func (s *ScanTasks) Files(ctx context.Context, id, parent string, page, size int) (ScanFilePage, error) {
	latest, err := s.repo.Latest(ctx, "strm")
	if err != nil {
		return ScanFilePage{}, err
	}
	if latest == nil || latest.ID != id {
		return ScanFilePage{}, ErrScanTaskConflict
	}
	s.mu.Lock()
	tree := s.files
	s.mu.Unlock()
	if tree == nil || tree.id != id {
		return ScanFilePage{Items: []ScanFileRow{}, Available: false}, nil
	}
	return tree.page(parent, max(1, page), max(1, min(200, size))), nil
}
