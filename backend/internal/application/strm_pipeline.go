package application

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"bytemuse/backend/internal/logging"
)

// strmWorkItem contains only discovery metadata; credentials and direct download URLs are never queued.
type strmWorkItem struct {
	Mapping  int
	File     strmSourceFile
	Attempts int
}

type strmSpool struct {
	file          *os.File
	read, written int64
}

// strmWorkQueue spills pending and retry work to private temporary files, keeping admission independent of workers.
// Existing page/success checkpoints rebuild these ephemeral queues after restart.
type strmWorkQueue struct {
	mu             sync.Mutex
	pending, retry strmSpool
	sealed         bool
	ready          chan struct{}
}

func newStrmWorkQueue() (*strmWorkQueue, error) {
	queue := &strmWorkQueue{ready: make(chan struct{}, 1)}
	for _, spool := range []*strmSpool{&queue.pending, &queue.retry} {
		file, err := os.CreateTemp("", "bytemuse-strm-queue-*")
		if err != nil {
			queue.close()
			return nil, err
		}
		spool.file = file
	}
	return queue, nil
}

func (queue *strmWorkQueue) close() {
	for _, spool := range []*strmSpool{&queue.pending, &queue.retry} {
		if spool.file != nil {
			spool.file.Close()
			os.Remove(spool.file.Name())
		}
	}
}

func (queue *strmWorkQueue) signal() {
	select {
	case queue.ready <- struct{}{}:
	default:
	}
}
func (queue *strmWorkQueue) seal() {
	queue.mu.Lock()
	queue.sealed = true
	queue.mu.Unlock()
	queue.signal()
}

func (queue *strmWorkQueue) push(item strmWorkItem) error {
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	spool := &queue.pending
	if item.Attempts > 0 {
		spool = &queue.retry
	}
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len(raw)))
	if _, err = spool.file.WriteAt(size[:], spool.written); err != nil {
		return err
	}
	if _, err = spool.file.WriteAt(raw, spool.written+8); err != nil {
		return err
	}
	spool.written += int64(8 + len(raw))
	queue.signal()
	return nil
}

func (queue *strmWorkQueue) pop(probe bool) (strmWorkItem, bool, error) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	first, second := &queue.pending, &queue.retry
	if probe {
		first, second = second, first
	}
	for _, spool := range []*strmSpool{first, second} {
		if spool.read == spool.written {
			continue
		}
		var size [8]byte
		if _, err := spool.file.ReadAt(size[:], spool.read); err != nil {
			return strmWorkItem{}, false, err
		}
		length := binary.LittleEndian.Uint64(size[:])
		if length > 16*1024*1024 {
			return strmWorkItem{}, false, errors.New("STRM 队列记录无效")
		}
		raw := make([]byte, int(length))
		if _, err := spool.file.ReadAt(raw, spool.read+8); err != nil {
			return strmWorkItem{}, false, err
		}
		var item strmWorkItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return item, false, err
		}
		spool.read += int64(8 + length)
		if spool.read == spool.written {
			if err := spool.file.Truncate(0); err != nil {
				return item, false, err
			}
			spool.read = 0
			spool.written = 0
		} else if spool.read >= 1024*1024 && spool.read >= spool.written-spool.read {
			if err := spool.compact(); err != nil {
				return item, false, err
			}
		}
		return item, true, nil
	}
	return strmWorkItem{}, false, nil
}

// compact reclaims consumed prefixes with bounded memory so endless retries cannot grow the spool indefinitely.
func (spool *strmSpool) compact() error {
	remaining := spool.written - spool.read
	buffer := make([]byte, 64*1024)
	for offset := int64(0); offset < remaining; {
		length := min(int64(len(buffer)), remaining-offset)
		if _, err := spool.file.ReadAt(buffer[:length], spool.read+offset); err != nil {
			return err
		}
		written, err := spool.file.WriteAt(buffer[:length], offset)
		if err != nil {
			return err
		}
		if int64(written) != length {
			return io.ErrShortWrite
		}
		offset += length
	}
	if err := spool.file.Truncate(remaining); err != nil {
		return err
	}
	spool.read, spool.written = 0, remaining
	return nil
}

func (queue *strmWorkQueue) finished() bool {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.sealed && queue.pending.read == queue.pending.written && queue.retry.read == queue.retry.written
}

type strmWorkResult struct {
	item strmWorkItem
	err  error
}

// strmCheckpointError stops the task when durable completion cannot be read or recorded.
type strmCheckpointError struct{ error }

func (err *strmCheckpointError) Unwrap() error { return err.error }

func strmCheckpointFailure(err error) error {
	if err == nil {
		return nil
	}
	return &strmCheckpointError{err}
}

// runStrmStage owns its failure streak. Once tripped it drains in-flight work, waits, then dispatches exactly one failed item.
// Only that probe can reopen the stage; another failure restarts the full cooldown.
func runStrmStage(ctx context.Context, queue *strmWorkQueue, workers, threshold int, retry bool, execute func(context.Context, strmWorkItem) error, wait func(context.Context) error) error {
	workerCtx, cancel := context.WithCancel(ctx)
	results := make(chan strmWorkResult, workers)
	var running sync.WaitGroup
	defer running.Wait()
	defer cancel()
	inflight, failures := 0, 0
	cooling, probe := false, false
	for {
		if err := scanCheckpoint(workerCtx); err != nil {
			return err
		}
		if cooling && inflight == 0 {
			if err := wait(workerCtx); err != nil {
				return err
			}
			cooling = false
			probe = true
		}
		limit := workers
		if probe {
			limit = 1
		}
		for !cooling && inflight < limit {
			item, ok, err := queue.pop(probe)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			inflight++
			running.Add(1)
			go func() {
				defer running.Done()
				err := scanCheckpoint(workerCtx)
				if err == nil {
					err = execute(workerCtx, item)
				}
				results <- strmWorkResult{item, err}
			}()
		}
		if inflight == 0 && queue.finished() {
			return nil
		}
		select {
		case <-workerCtx.Done():
			return workerCtx.Err()
		case <-queue.ready:
		case result := <-results:
			inflight--
			if result.err != nil {
				var checkpointErr *strmCheckpointError
				if errors.As(result.err, &checkpointErr) {
					return result.err
				}
				if workerCtx.Err() != nil {
					return workerCtx.Err()
				}
				if retry {
					result.item.Attempts++
					if err := queue.push(result.item); err != nil {
						return err
					}
					failures++
					if probe || failures >= threshold {
						cooling = true
						probe = false
					}
				}
			} else if !cooling {
				failures = 0
				probe = false
			}
		}
	}
}

type strmRetryKey struct{}
type strmRetryProgressKey struct{}
type strmRetryPolicy struct {
	wait func(context.Context, string, error) error
}

// waitStrmRetry never restarts a process or overrides manual pause/cancel. Tests inject a clock at this boundary.
func waitStrmRetry(ctx context.Context, stage string, cause error) error {
	if report, ok := ctx.Value(strmRetryProgressKey{}).(func()); ok {
		report()
	}
	if policy, _ := ctx.Value(strmRetryKey{}).(*strmRetryPolicy); policy != nil && policy.wait != nil {
		return policy.wait(ctx, stage, cause)
	}
	logging.Error(logging.CategoryStrmGenerate, stage+"暂停，60 秒后单项重试", "error", scanFileError(cause))
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if err := scanCheckpoint(ctx); err != nil {
				return err
			}
		case <-timer.C:
			return scanCheckpoint(ctx)
		}
	}
}

// retryStrmScan retries the same directory page, not the whole traversal, so discoveries are not counted twice.
func retryStrmScan(ctx context.Context, request func() error) error {
	for {
		if err := scanCheckpoint(ctx); err != nil {
			return err
		}
		err := request()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, enabled := ctx.Value(strmRetryKey{}).(*strmRetryPolicy); !enabled {
			return err
		}
		if err := waitStrmRetry(ctx, "扫描", err); err != nil {
			return err
		}
	}
}
