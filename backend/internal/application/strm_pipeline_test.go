package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"bytemuse/backend/internal/domain"
)

type retryStrmPan115Stub struct {
	downloadPan115Stub
	list func(context.Context, string, int, int) (domain.Pan115FilePage, error)
}

func (stub *retryStrmPan115Stub) Files(ctx context.Context, directory string, offset, limit int) (domain.Pan115FilePage, error) {
	return stub.list(ctx, directory, offset, limit)
}

// TestStrmScanCooldownDoesNotStopWorkers verifies actual files finish while the scanner is cooling.
func TestStrmScanCooldownDoesNotStopWorkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	calls := map[string]int{}
	api := &retryStrmPan115Stub{downloadPan115Stub: downloadPan115Stub{address: "http://download.test"}}
	api.list = func(_ context.Context, directory string, offset, _ int) (domain.Pan115FilePage, error) {
		calls[directory]++
		if directory == "root" {
			return domain.Pan115FilePage{Files: []domain.Pan115File{{ID: "video", PickCode: "video", Name: "movie.mp4"}, {ID: "poster", PickCode: "poster", Name: "poster.jpg"}, {ID: "child", Name: "child", IsDirectory: true}}}, nil
		}
		if offset != 0 {
			t.Errorf("retry changed offset: %d", offset)
		}
		if calls[directory] <= 2 {
			return domain.Pan115FilePage{}, errors.New("scan unavailable")
		}
		return domain.Pan115FilePage{}, nil
	}
	service := newStrmTestService(t, root, api, nil, map[string]string{strmDownloadEnableSettingKey: "true", strmPathsSettingKey: strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "root", Path: "/root", LocalPath: "/out"}})})
	service.http.Transport = embyRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("poster"))}, nil
	})
	waits := 0
	ctx = context.WithValue(ctx, strmRetryKey{}, &strmRetryPolicy{wait: func(waitCtx context.Context, stage string, _ error) error {
		if stage != "扫描" {
			t.Errorf("unexpected cooldown: %s", stage)
		}
		waits++
		for {
			_, videoErr := os.Stat(filepath.Join(root, "out", "movie.strm"))
			_, posterErr := os.Stat(filepath.Join(root, "out", "poster.jpg"))
			if videoErr == nil && posterErr == nil {
				return nil
			}
			select {
			case <-waitCtx.Done():
				return waitCtx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}})
	result, err := service.Scan(ctx, "http://play.test", domain.StrmGenerateFull)
	if err != nil || waits != 2 || calls["root"] != 1 || calls["child"] != 3 || result.Created != 1 || result.Downloaded != 1 || result.Files != 1 {
		t.Fatalf("result=%+v calls=%v waits=%d err=%v", result, calls, waits, err)
	}
}

// TestStrmParallelProbeAndMinuteWait uses virtual time to verify real minute waits and one in-flight recovery probe.
func TestStrmParallelProbeAndMinuteWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queue, err := newStrmWorkQueue()
		if err != nil {
			t.Fatal(err)
		}
		defer queue.close()
		for index := 0; index < 40; index++ {
			if err := queue.push(strmWorkItem{Mapping: index}); err != nil {
				t.Fatal(err)
			}
		}
		queue.seal()
		var mutex sync.Mutex
		waits, active, probePeak, normalPeak := 0, 0, 0, 0
		recovered := false
		err = runStrmStage(context.Background(), queue, 5, 20, true, func(context.Context, strmWorkItem) error {
			mutex.Lock()
			probe := waits > 0 && !recovered
			active++
			if probe {
				probePeak = max(probePeak, active)
			} else if recovered {
				normalPeak = max(normalPeak, active)
			}
			mutex.Unlock()
			time.Sleep(time.Second)
			mutex.Lock()
			defer mutex.Unlock()
			active--
			if waits < 2 {
				return errors.New("offline")
			}
			if probe {
				recovered = true
			}
			return nil
		}, func(ctx context.Context) error {
			mutex.Lock()
			if active != 0 {
				t.Errorf("cooldown with active workers: %d", active)
			}
			waits++
			mutex.Unlock()
			before := time.Now()
			err := waitStrmRetry(ctx, "下载", errors.New("offline"))
			if elapsed := time.Since(before); elapsed != time.Minute {
				t.Errorf("cooldown=%s", elapsed)
			}
			return err
		})
		if err != nil || waits != 2 || probePeak != 1 || normalPeak != 5 {
			t.Fatalf("waits=%d probe=%d normal=%d err=%v", waits, probePeak, normalPeak, err)
		}
	})
}

// TestStrmRetryWaitHonorsPauseAndCancel verifies automatic retry never overrides user control.
func TestStrmRetryWaitHonorsPauseAndCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		entered := make(chan struct{}, 1)
		ctx = context.WithValue(ctx, scanCheckpointKey{}, func(ctx context.Context) error {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		})
		done := make(chan error, 1)
		go func() { done <- waitStrmRetry(ctx, "扫描", errors.New("offline")) }()
		<-entered
		synctest.Wait()
		select {
		case <-done:
			t.Fatal("manual pause was ignored")
		default:
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

// TestStrmPipelineDiscoveryContinues verifies downloads cannot block later directories or mappings.
func TestStrmPipelineDiscoveryContinues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var files []domain.Pan115File
	for index := 0; index < 12; index++ {
		files = append(files, domain.Pan115File{ID: fmt.Sprint(index), PickCode: fmt.Sprint(index), Name: fmt.Sprintf("%d.jpg", index)})
	}
	api := &downloadPan115Stub{strmPan115Stub: strmPan115Stub{pages: map[string]domain.Pan115FilePage{"root": {Files: files}, "next": {Files: []domain.Pan115File{{ID: "movie", PickCode: "movie", Name: "movie.mp4"}}}}}, address: "http://download.test"}
	service := newStrmTestService(t, t.TempDir(), api, nil, map[string]string{strmDownloadEnableSettingKey: "true", strmPathsSettingKey: strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "root", Path: "/root", LocalPath: "/root"}, {Kind: "115", ID: "next", Path: "/next", LocalPath: "/next"}})})
	generated := make(chan struct{})
	var once sync.Once
	ctx = WithScanProgress(ctx, func(progress ScanProgress) {
		if progress.Processed == 1 {
			once.Do(func() { close(generated) })
		}
	})
	service.http.Transport = embyRoundTripper(func(request *http.Request) (*http.Response, error) {
		select {
		case <-generated:
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("poster"))}, nil
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})
	result, err := service.Scan(ctx, "http://play.test", domain.StrmGenerateIncremental)
	if err != nil || result.Created != 1 || result.Downloaded != 12 {
		t.Fatalf("downstream blocked discovery: %+v %v", result, err)
	}
}

// TestStrmFileRetryReplacesFailure verifies retry state and ancestor counts recover without duplication.
func TestStrmFileRetryReplacesFailure(t *testing.T) {
	tree := newScanFileTree("test")
	ctx := withScanFileMapping(context.WithValue(context.Background(), scanFileTreeKey{}, tree), domain.StrmMapping{Kind: "115", ID: "root", Path: "/root", LocalPath: "/out"})
	file := strmSourceFile{ID: "file", Name: "movie.mp4"}
	startScanFile(ctx, file, "generate")("failed", errors.New("offline"))
	finish := startScanFile(ctx, file, "generate")
	root := tree.page("", 1, 20, false).Items[0]
	row := tree.page(root.ID, 1, 20, false).Items[0]
	if row.State != "processing" || root.Processed != 0 || root.Failed != 0 {
		t.Fatalf("retry did not reopen: root=%+v row=%+v", root, row)
	}
	finish("completed", nil)
	root = tree.page("", 1, 20, false).Items[0]
	row = tree.page(root.ID, 1, 20, false).Items[0]
	if row.State != "completed" || row.Error != "" || root.Total != 1 || root.Processed != 1 || root.Failed != 0 {
		t.Fatalf("invalid retry result: root=%+v row=%+v", root, row)
	}
}

// TestStrmStageCircuit verifies independent thresholds and exactly one probe after each cooldown.
func TestStrmStageCircuit(t *testing.T) {
	for _, threshold := range []int{10, 20} {
		t.Run(time.Duration(threshold).String(), func(t *testing.T) {
			queue, err := newStrmWorkQueue()
			if err != nil {
				t.Fatal(err)
			}
			defer queue.close()
			for index := 0; index < threshold+3; index++ {
				if err := queue.push(strmWorkItem{Mapping: index}); err != nil {
					t.Fatal(err)
				}
			}
			queue.seal()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mutex sync.Mutex
			attempts := 0
			probes := 0
			cooldowns := make(chan struct{}, 10)
			release := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- runStrmStage(ctx, queue, 1, threshold, true, func(context.Context, strmWorkItem) error {
					mutex.Lock()
					defer mutex.Unlock()
					attempts++
					if attempts <= threshold+1 {
						return errors.New("offline")
					}
					probes++
					return nil
				}, func(context.Context) error {
					cooldowns <- struct{}{}
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			for _, want := range []int{threshold, threshold + 1} {
				select {
				case <-cooldowns:
				case <-time.After(3 * time.Second):
					t.Fatal("missing cooldown")
				}
				mutex.Lock()
				got := attempts
				mutex.Unlock()
				if got != want {
					t.Fatalf("attempts=%d want=%d", got, want)
				}
				release <- struct{}{}
			}
			close(release)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("stage did not recover")
			}
		})
	}
}

// TestStrmStageDoesNotBlockDiscovery checks that a stalled consumer does not hold queue admission.
func TestStrmStageDoesNotBlockDiscovery(t *testing.T) {
	queue, err := newStrmWorkQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer queue.close()
	for index := 0; index < 1000; index++ {
		if err := queue.push(strmWorkItem{Mapping: index}); err != nil {
			t.Fatal(err)
		}
	}
	queue.seal()
	for index := 0; index < 1000; index++ {
		item, ok, err := queue.pop(false)
		if err != nil || !ok || item.Mapping != index {
			t.Fatalf("%+v %v %v", item, ok, err)
		}
	}
}

// TestStrmRetrySpoolReclaimsConsumedRecords bounds disk usage across indefinitely failing files.
func TestStrmRetrySpoolReclaimsConsumedRecords(t *testing.T) {
	queue, err := newStrmWorkQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer queue.close()
	for index := 0; index < 2; index++ {
		if err := queue.push(strmWorkItem{Attempts: 1, File: strmSourceFile{Name: strings.Repeat("x", 4096)}}); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 1000; index++ {
		item, ok, err := queue.pop(true)
		if err != nil || !ok {
			t.Fatalf("pop: %v %v", ok, err)
		}
		if err := queue.push(item); err != nil {
			t.Fatal(err)
		}
	}
	info, err := queue.retry.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 2*1024*1024 {
		t.Fatalf("consumed retry records leaked disk: %d", info.Size())
	}
}

// TestStrmGenerationRecoversWithoutStoppingDownloads exercises real disk errors and final counters.
func TestStrmGenerationRecoversWithoutStoppingDownloads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	root := t.TempDir()
	var files []domain.Pan115File
	for index := 0; index < 12; index++ {
		name := fmt.Sprintf("movie%d.mp4", index)
		files = append(files, domain.Pan115File{ID: name, PickCode: name, Name: name})
		if err := os.MkdirAll(filepath.Join(root, "out", fmt.Sprintf("movie%d.strm", index)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files = append(files, domain.Pan115File{ID: "poster", PickCode: "poster", Name: "poster.jpg"})
	api := &downloadPan115Stub{strmPan115Stub: strmPan115Stub{pages: map[string]domain.Pan115FilePage{"root": {Files: files}}}, address: "http://download.test"}
	service := newStrmTestService(t, root, api, nil, map[string]string{strmDownloadEnableSettingKey: "true", strmPathsSettingKey: strmTestMappings(t, []domain.StrmMapping{{Kind: "115", ID: "root", Path: "/root", LocalPath: "/out"}})})
	service.http.Transport = embyRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("poster"))}, nil
	})
	waits := 0
	ctx = context.WithValue(ctx, strmRetryKey{}, &strmRetryPolicy{wait: func(waitCtx context.Context, stage string, _ error) error {
		if stage != "生成 STRM" {
			t.Errorf("unexpected stage %s", stage)
		}
		waits++
		for {
			if _, err := os.Stat(filepath.Join(root, "out", "poster.jpg")); err == nil {
				break
			}
			select {
			case <-waitCtx.Done():
				return waitCtx.Err()
			case <-time.After(time.Millisecond):
			}
		}
		for index := 0; index < 12; index++ {
			if err := os.Remove(filepath.Join(root, "out", fmt.Sprintf("movie%d.strm", index))); err != nil {
				return err
			}
		}
		return nil
	}})
	var progress ScanProgress
	ctx = WithScanProgress(ctx, func(update ScanProgress) { progress = update })
	result, err := service.Scan(ctx, "http://play.test", domain.StrmGenerateIncremental)
	if err != nil || waits != 1 || result.Created != 12 || result.Downloaded != 1 || result.Failed != 0 || result.Mappings[0].Message != "" || progress.Processed != 12 || progress.Total != 12 {
		t.Fatalf("result=%+v progress=%+v waits=%d err=%v", result, progress, waits, err)
	}
}

// TestStrmCheckpointFailureStopsStage prevents endless retries after durable completion storage fails.
func TestStrmCheckpointFailureStopsStage(t *testing.T) {
	queue, err := newStrmWorkQueue()
	if err != nil {
		t.Fatal(err)
	}
	defer queue.close()
	if err := queue.push(strmWorkItem{}); err != nil {
		t.Fatal(err)
	}
	queue.seal()
	cause := errors.New("checkpoint storage unavailable")
	err = runStrmStage(context.Background(), queue, 1, 10, true, func(context.Context, strmWorkItem) error { return strmCheckpointFailure(cause) }, func(context.Context) error {
		t.Error("storage failure must stop, not cooldown")
		return context.Canceled
	})
	if !errors.Is(err, cause) {
		t.Fatalf("lost storage error: %v", err)
	}
}
