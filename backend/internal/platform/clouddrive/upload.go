package clouddrive

import (
	"bytemuse/backend/internal/domain"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"time"
)

// ErrUploadFailed means CD2 has reported a terminal cloud-transfer failure, rather than an uncertain timeout.
var ErrUploadFailed = errors.New("CloudDrive2 云端上传失败")

// rpcOne requires one protobuf response; empty responses are not silently treated as success.
func (c *Client) rpcOne(ctx context.Context, method string, payload []byte) ([]byte, error) {
	var data []byte
	err := c.withToken(ctx, func(token string) error {
		frames, e := c.call(ctx, method, payload, token)
		if e != nil {
			return e
		}
		if len(frames) != 1 {
			return fmt.Errorf("CloudDrive2 %s 未返回唯一结果", method)
		}
		data = frames[0]
		return nil
	})
	return data, err
}

// protoUint reads a scalar response such as fileHandle or bytesWritten.
func protoUint(data []byte) (uint64, error) {
	r := &protoReader{data: data}
	for !r.done() {
		field, wire, err := r.key()
		if err != nil {
			return 0, err
		}
		if field == 1 && wire == 0 {
			return r.varint()
		}
		if err = r.skip(wire); err != nil {
			return 0, err
		}
	}
	return 0, nil
}
func (w *protoWriter) number(field int, value uint64) { w.tag(field, 0); w.varint(value) }

// fileOperation validates business success after a mutating RPC.
func (c *Client) fileOperation(ctx context.Context, method string, payload []byte) error {
	raw, err := c.rpcOne(ctx, method, payload)
	if err != nil {
		return err
	}
	ok, message, err := decodeFileOperationResult(raw)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("CloudDrive2 %s 失败: %s", method, message)
	}
	return nil
}

// Rename renames a single staging or replacement object.
func (c *Client) Rename(ctx context.Context, target, name string) error {
	w := &protoWriter{}
	w.str(1, target)
	w.str(2, name)
	return c.fileOperation(ctx, "RenameFile", w.buf)
}

// Delete removes only an explicitly replaced object through the normal recycle-bin operation.
func (c *Client) Delete(ctx context.Context, target string) error {
	w := &protoWriter{}
	w.str(1, target)
	return c.fileOperation(ctx, "DeleteFile", w.buf)
}

// Upload writes bounded chunks to a staging file and then waits for CD2's cloud transfer to finish.
// A closed handle alone is only acceptance by CD2, never proof of cloud completion.
func (c *Client) Upload(ctx context.Context, parent, name string, f *os.File, size int64) error {
	w := &protoWriter{}
	w.str(1, parent)
	w.str(2, name)
	raw, err := c.rpcOne(ctx, "CreateFile", w.buf)
	if err != nil {
		return err
	}
	handle, err := protoUint(raw)
	if err != nil || handle == 0 {
		return fmt.Errorf("CD2 未返回可用上传句柄")
	}
	closed := false
	defer func() {
		if !closed {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			w := &protoWriter{}
			w.number(1, handle)
			_ = c.fileOperation(cleanup, "CloseFile", w.buf)
			_ = c.ControlUpload(cleanup, path.Join(parent, name), "pause")
			// The application owns durable cleanup; never discard its recovery evidence here.
		}
	}()
	buffer := make([]byte, 1024*1024)
	for offset := int64(0); offset < size; {
		n, e := f.ReadAt(buffer[:min(int64(len(buffer)), size-offset)], offset)
		if e != nil && e != io.EOF {
			return e
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		w := &protoWriter{}
		w.number(1, handle)
		w.number(2, uint64(offset))
		w.number(3, uint64(n))
		w.bytes(4, buffer[:n])
		raw, e := c.rpcOne(ctx, "WriteToFile", w.buf)
		if e != nil {
			return e
		}
		written, e := protoUint(raw)
		if e != nil {
			return e
		}
		if written != uint64(n) {
			return io.ErrShortWrite
		}
		offset += int64(n)
		domain.ReportUploadProgress(ctx, offset, size, "transferring")
	}
	w = &protoWriter{}
	w.number(1, handle)
	if err = c.fileOperation(ctx, "CloseFile", w.buf); err != nil {
		return err
	}
	closed = true
	return c.WaitUpload(ctx, path.Join(parent, name))
}

// WaitUpload checks provider transfer state; cached directory presence is not sufficient evidence.
func (c *Client) WaitUpload(ctx context.Context, target string) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer stop()
			resultErr = errors.Join(resultErr, c.ControlUpload(cleanup, target, "pause"))
		}
	}()
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		raw, err := c.uploadTaskPage(ctx, target)
		if err != nil {
			return err
		}
		state, found, err := uploadState(raw, target)
		if transferred, total, ok := uploadBytes(raw, target); ok {
			domain.ReportUploadProgress(ctx, transferred, total, "uploading")
		}
		if err != nil {
			return err
		}
		if found {
			if state == 4 {
				if err = c.ControlUpload(ctx, target, "resume"); err != nil {
					return err
				}
			}
			if state == 5 {
				return nil
			}
			if state == 2 || state == 6 || state == 8 || state == 9 || state == 10 {
				return fmt.Errorf("%w，状态 %d", ErrUploadFailed, state)
			}
		}
		if !found {
			// Finished tasks can disappear from the transfer list. A refreshed cloud SHA1 is independent completion evidence.
			entries, e := c.ListSubFiles(ctx, path.Dir(target))
			if e != nil {
				return e
			}
			for _, entry := range entries {
				if entry.Name == path.Base(target) && !entry.Directory && entry.SHA1 != "" {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// uploadTaskPage searches all filtered pages; getAll is unsupported by current CD2 servers.
func (c *Client) uploadTaskPage(ctx context.Context, target string) ([]byte, error) {
	for page := uint64(0); page < 10000; page++ {
		w := &protoWriter{}
		w.number(2, 100)
		w.number(3, page)
		w.str(4, path.Base(target))
		raw, err := c.rpcOne(ctx, "GetUploadFileList", w.buf)
		if err != nil {
			return nil, err
		}
		_, found, err := uploadState(raw, target)
		if err != nil || found {
			return raw, err
		}
		count := 0
		reader := &protoReader{data: raw}
		for !reader.done() {
			field, wire, e := reader.key()
			if e != nil {
				return nil, e
			}
			if field == 2 && wire == 2 {
				count++
			}
			if e = reader.skip(wire); e != nil {
				return nil, e
			}
		}
		if count < 100 {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("CD2 上传任务分页超出上限")
}

// ControlUpload changes only the task bound to this private staging path, never global CD2 transfers.
func (c *Client) ControlUpload(ctx context.Context, target, action string) error {
	raw, err := c.uploadTaskPage(ctx, target)
	if err != nil {
		return err
	}
	key := uploadTaskKey(raw, target)
	if key == "" {
		return nil
	}
	method := ""
	switch action {
	case "pause":
		method = "PauseUploadFiles"
	case "resume":
		method = "ResumeUploadFiles"
	case "cancel":
		method = "CancelUploadFiles"
	default:
		return fmt.Errorf("无效的 CD2 任务操作")
	}
	w := &protoWriter{}
	w.str(1, key)
	_, err = c.rpcOne(ctx, method, w.buf)
	return err
}

func uploadTaskKey(raw []byte, target string) string {
	reader := &protoReader{data: raw}
	for !reader.done() {
		f, w, e := reader.key()
		if e != nil {
			return ""
		}
		if f != 2 || w != 2 {
			if reader.skip(w) != nil {
				return ""
			}
			continue
		}
		row, e := reader.bytes()
		if e != nil {
			return ""
		}
		r := &protoReader{data: row}
		key, name := "", ""
		for !r.done() {
			f, w, e = r.key()
			if e != nil {
				return ""
			}
			if (f == 1 || f == 2) && w == 2 {
				v, e := r.bytes()
				if e != nil {
					return ""
				}
				if f == 1 {
					key = string(v)
				} else {
					name = string(v)
				}
			} else if r.skip(w) != nil {
				return ""
			}
		}
		if name == target {
			return key
		}
	}
	return ""
}

// uploadBytes reads only the selected cloud transfer's size and transferred byte fields.
func uploadBytes(data []byte, target string) (int64, int64, bool) {
	r := &protoReader{data: data}
	for !r.done() {
		f, w, e := r.key()
		if e != nil {
			return 0, 0, false
		}
		if f != 2 || w != 2 {
			if r.skip(w) != nil {
				return 0, 0, false
			}
			continue
		}
		raw, e := r.bytes()
		if e != nil {
			return 0, 0, false
		}
		row := &protoReader{data: raw}
		name := ""
		var size, done uint64
		for !row.done() {
			f, w, e = row.key()
			if e != nil {
				return 0, 0, false
			}
			if f == 2 && w == 2 {
				v, err := row.bytes()
				if err != nil {
					return 0, 0, false
				}
				name = string(v)
			} else if (f == 3 || f == 4) && w == 0 {
				v, err := row.varint()
				if err != nil {
					return 0, 0, false
				}
				if f == 3 {
					size = v
				} else {
					done = v
				}
			} else if row.skip(w) != nil {
				return 0, 0, false
			}
		}
		if name == target {
			return int64(done), int64(size), true
		}
	}
	return 0, 0, false
}

// uploadState reads the matching transfer only; unrelated uploads never affect this task.
func uploadState(data []byte, target string) (int, bool, error) {
	r := &protoReader{data: data}
	for !r.done() {
		field, wire, err := r.key()
		if err != nil {
			return 0, false, err
		}
		if field != 2 || wire != 2 {
			if err = r.skip(wire); err != nil {
				return 0, false, err
			}
			continue
		}
		raw, err := r.bytes()
		if err != nil {
			return 0, false, err
		}
		entry := &protoReader{data: raw}
		name := ""
		state := 0
		for !entry.done() {
			f, w, e := entry.key()
			if e != nil {
				return 0, false, e
			}
			if f == 2 && w == 2 {
				v, e := entry.bytes()
				if e != nil {
					return 0, false, e
				}
				name = string(v)
			} else if f == 8 && w == 0 {
				v, e := entry.varint()
				if e != nil {
					return 0, false, e
				}
				state = int(v)
			} else if e = entry.skip(w); e != nil {
				return 0, false, e
			}
		}
		if name == target {
			return state, true, nil
		}
	}
	return 0, false, nil
}
