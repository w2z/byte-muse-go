package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// UploadMapping maps a server-local directory to a provider directory; Path is display-only for 115.
type UploadMapping struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Path      string `json:"path"`
	LocalPath string `json:"local_path"`
}

// parseUploadMappings is shared by settings validation and the watcher. It never creates source directories.
func parseUploadMappings(raw string) ([]UploadMapping, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var mappings []UploadMapping
	if err := json.Unmarshal([]byte(raw), &mappings); err != nil || !strings.HasPrefix(strings.TrimSpace(raw), "[") {
		return nil, errors.New("目录映射必须为 JSON 数组")
	}
	seen := map[string]bool{}
	for i := range mappings {
		m := &mappings[i]
		m.LocalPath = strings.TrimSpace(m.LocalPath)
		m.ID = strings.TrimSpace(m.ID)
		m.Path = strings.TrimSpace(m.Path)
		if (m.Kind != "115" && m.Kind != "cd2") || m.ID == "" || m.Path == "" || !filepath.IsAbs(m.LocalPath) {
			return nil, errors.New("需要选择本地绝对目录、网盘类型及网盘目录")
		}
		if strings.ContainsRune(m.LocalPath, 0) || strings.ContainsRune(m.ID, 0) {
			return nil, errors.New("目录包含无效字符")
		}
		if m.Kind == "115" && strings.Trim(m.ID, "0123456789") != "" {
			return nil, errors.New("115 目录 ID 必须为数字")
		}
		if m.Kind == "cd2" && (!strings.HasPrefix(m.ID, "/") || strings.Contains(m.ID, "/../")) {
			return nil, errors.New("CD2 目录必须为绝对路径")
		}
		m.LocalPath = filepath.Clean(m.LocalPath)
		key := m.Kind + "\x00" + m.ID + "\x00" + m.LocalPath
		if seen[key] {
			return nil, errors.New("目录映射重复")
		}
		seen[key] = true
	}
	return mappings, nil
}

// uploadLocalFile is a discovery snapshot; later reads must revalidate size and modification time.
type uploadLocalFile struct {
	Absolute, Relative string
	Size, Modified     int64
}

type uploadDiscovery struct {
	mapping   UploadMapping
	file      uploadLocalFile
	directory string
	err       error
}

// discoverUploadFiles walks once and sends bounded, ordered events; only the scheduler persists and admits tasks.
func discoverUploadFiles(ctx context.Context, mappings []UploadMapping, stream chan<- uploadDiscovery) {
	defer close(stream)
	send := func(event uploadDiscovery) error {
		select {
		case stream <- event:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, mapping := range mappings {
		err := walkUploadFiles(ctx, mapping, func(directory string) { _ = send(uploadDiscovery{mapping: mapping, directory: directory}) }, func(file uploadLocalFile) error { return send(uploadDiscovery{mapping: mapping, file: file}) })
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if send(uploadDiscovery{mapping: mapping, err: err}) != nil {
				return
			}
		}
	}
}

// uploadSnapshot recursively discovers regular files, without following links outside the selected source.
func uploadSnapshot(ctx context.Context, m UploadMapping) ([]uploadLocalFile, error) {
	var files []uploadLocalFile
	err := walkUploadFiles(ctx, m, nil, func(file uploadLocalFile) error {
		files = append(files, file)
		return nil
	})
	return files, err
}

// walkUploadFiles publishes each regular file in lexical traversal order without waiting for the whole tree.
func walkUploadFiles(ctx context.Context, m UploadMapping, visitDirectory func(string), visitFile func(uploadLocalFile) error) error {
	info, err := os.Lstat(m.LocalPath)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("上传源必须为真实目录")
	}
	return filepath.WalkDir(m.LocalPath, func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if name != m.LocalPath && errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			if visitDirectory != nil {
				visitDirectory(name)
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(m.LocalPath, name)
		if err != nil {
			return err
		}
		return visitFile(uploadLocalFile{Absolute: name, Relative: filepath.ToSlash(relative), Size: info.Size(), Modified: info.ModTime().UnixNano()})
	})
}

// UploadDirectories lists actual server directories. An empty path or / on Windows lists drive roots.
func UploadDirectories(raw string) (map[string]any, error) {
	target := strings.TrimSpace(raw)
	dirs := []map[string]string{}
	if (target == "" || target == "/") && filepath.Separator == '\\' {
		for drive := 'A'; drive <= 'Z'; drive++ {
			p := fmt.Sprintf("%c:/", drive)
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				dirs = append(dirs, map[string]string{"name": p, "path": p})
			}
		}
		return map[string]any{"path": "/", "directories": dirs}, nil
	}
	if target == "" {
		target = "/"
	}
	if !filepath.IsAbs(target) {
		return nil, errors.New("需要绝对目录")
	}
	target = filepath.Clean(target)
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			dirs = append(dirs, map[string]string{"name": entry.Name(), "path": filepath.ToSlash(filepath.Join(target, entry.Name()))})
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i]["name"] < dirs[j]["name"] })
	return map[string]any{"path": filepath.ToSlash(target), "directories": dirs}, nil
}
