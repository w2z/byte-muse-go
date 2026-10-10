package application

import (
	"context"
	"regexp"
	"sort"
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
	Error     string `json:"error"` // 文件失败原因；目录及无错误时为空，返回前脱敏。
	Total     int    `json:"total"`
	Processed int    `json:"processed"`
	Failed    int    `json:"failed"`
	Percent   int    `json:"percent"`
	Bytes     int64  `json:"bytes"`
	Size      int64  `json:"size"`
	parent    string
	closed    bool
}

var scanErrorURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var scanErrorSecret = regexp.MustCompile(`(?i)(authorization|cookie|set-cookie|token|access_token|refresh_token|password|secret|api_key|sign)["']?\s*[:=]\s*[^\r\n]+`)

// scanFileError limits diagnostic text and removes URLs and credentials before exposing task details.
func scanFileError(err error) string {
	if err == nil {
		return ""
	}
	message := scanErrorURL.ReplaceAllString(err.Error(), "[链接已隐藏]")
	message = scanErrorSecret.ReplaceAllString(message, "$1=[已隐藏]")
	characters := []rune(strings.TrimSpace(message))
	if len(characters) > 2048 {
		return string(characters[:2048]) + "…"
	}
	return string(characters)
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

// startScanFile counts each file once and records its sanitized error; terminal updates are idempotent.
func startScanFile(ctx context.Context, file strmSourceFile, operation string) func(string, error) {
	return trackScanFile(ctx, file, operation, "processing")
}

// trackScanFile 注册文件进度并保留原始身份；扫描入队时提供实际输出名，重试不改写显示名称。
func trackScanFile(ctx context.Context, file strmSourceFile, operation, initialState string, outputName ...string) func(string, error) {
	tree, root, id := scanFileIdentity(ctx, file, operation)
	if tree == nil {
		return func(string, error) {}
	}
	tree.mu.Lock()
	if tree.rows[id] == nil {
		parent := tree.directory(root, file.Directory)
		name := file.Name
		if operation == "generate" {
			name = strmVideoName(file.Name)
		}
		if len(outputName) > 0 && outputName[0] != "" {
			name = outputName[0]
		}
		tree.add(ScanFileRow{ID: id, Name: name, Kind: "file", Operation: operation, State: initialState, Total: 1, parent: parent})
		for ancestor := parent; ancestor != ""; ancestor = tree.rows[ancestor].parent {
			tree.rows[ancestor].Total++
		}
	} else if row := tree.rows[id]; initialState == "processing" && row.State != "completed" && row.State != "skipped" {
		for ancestor := row.parent; ancestor != ""; ancestor = tree.rows[ancestor].parent {
			tree.rows[ancestor].Processed -= row.Processed
			tree.rows[ancestor].Failed -= row.Failed
		}
		row.State, row.Error = "processing", ""
		row.Processed, row.Failed, row.Percent, row.Bytes = 0, 0, 0, 0
	}
	tree.mu.Unlock()
	return func(state string, err error) {
		tree.mu.Lock()
		defer tree.mu.Unlock()
		row := tree.rows[id]
		if row.Processed != 0 {
			return
		}
		row.State, row.Processed = state, 1
		if state == "failed" || state == "interrupted" {
			row.Failed = 1
			row.Error = scanFileError(err)
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

// scanFilePriority is the single display order for files and computed directory snapshots.
func scanFilePriority(state string) int {
	switch state {
	case "processing", "scanning":
		return 0
	case "failed", "interrupted":
		return 2
	case "completed", "skipped":
		return 3
	default:
		return 1
	}
}

// page stably orders processing, waiting, errors and successes before pagination at every level.
func (tree *scanFileTree) page(parent string, page, size int, hideCompleted bool) ScanFilePage {
	tree.mu.Lock()
	ids := tree.children[parent]
	result := ScanFilePage{Items: []ScanFileRow{}, Available: true}
	for _, id := range ids {
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
		if !hideCompleted || row.State != "completed" {
			result.Items = append(result.Items, row)
		}
	}
	tree.mu.Unlock()
	sort.SliceStable(result.Items, func(first, second int) bool {
		return scanFilePriority(result.Items[first].State) < scanFilePriority(result.Items[second].State)
	})
	result.Total = len(result.Items)
	start := result.Total
	if page > 0 && size > 0 && page-1 <= result.Total/size {
		start = min(result.Total, (page-1)*size)
	}
	result.Items = result.Items[start:min(result.Total, start+size)]
	return result
}

// Files pages the latest execution only. A restart rebuilds this observational list from checkpoints.
func (s *ScanTasks) Files(ctx context.Context, id, parent string, page, size int, hideCompleted bool) (ScanFilePage, error) {
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
	return tree.page(parent, max(1, page), max(1, min(200, size)), hideCompleted), nil
}
