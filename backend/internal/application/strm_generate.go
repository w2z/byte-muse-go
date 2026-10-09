package application

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bytemuse/backend/internal/domain"
	"bytemuse/backend/internal/platform/pan115"
)

type strmGenerateOutcome struct{ created, changed, skipped bool }

// strmVideoName 用 STRM 后缀替换源视频后缀，保留名称内的分段和版本标识，与海报和 NFO 同名。
func strmVideoName(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name)) + ".strm"
}

// generateStrmFile 统一手动与事件生成的命名和写入：替换视频后缀，播放地址仍使用原始网盘标识。
func (s *StrmService) generateStrmFile(ctx context.Context, root, target, kind, base string, mode domain.StrmGenerateMode, file strmSourceFile) (strmGenerateOutcome, error) {
	var outcome strmGenerateOutcome
	done, err := taskUnitDone(ctx, "strm-file", target, file.Directory, file.ID)
	if err != nil {
		return outcome, strmCheckpointFailure(err)
	}
	if done {
		outcome.skipped = true
		return outcome, nil
	}
	if strings.ContainsAny(file.Name, `/\`) {
		return outcome, errors.New("STRM 文件名包含路径分隔符")
	}
	absolute := filepath.Join(target, filepath.FromSlash(file.Directory), strmVideoName(file.Name))
	if !withinStrmRoot(root, absolute) {
		return outcome, errors.New("STRM 文件路径超出根目录")
	}
	identifier := file.ID
	if kind == domain.StrmKindPan115 {
		identifier = strings.TrimSpace(file.PickCode)
		if identifier == "" {
			return outcome, errors.New("115 文件缺少 pick_code，无法生成播放链接")
		}
	}
	if mode == domain.StrmGenerateIncremental {
		exists, err := strmFileExists(absolute)
		if err != nil {
			return outcome, fmt.Errorf("读取本地 strm 文件失败：%w", err)
		}
		if exists {
			outcome.skipped = true
			return outcome, strmCheckpointFailure(completeTaskUnit(ctx, "strm-file", target, file.Directory, file.ID))
		}
	}
	content := strmPlayURL(base, kind, identifier) + "\n"
	// 单文件事件跨批执行，不能仅靠扫描内的冲突表。已有目标必须仍指向同一网盘文件；
	// 允许同一文件更换访问域名，但拒绝覆盖其他源文件或无法确认归属的播放地址。
	existing, readErr := os.ReadFile(absolute)
	if readErr == nil {
		previous, previousErr := url.Parse(strings.TrimSpace(string(existing)))
		current, currentErr := url.Parse(strings.TrimSpace(content))
		if previousErr != nil || currentErr != nil || previous.EscapedPath() != current.EscapedPath() || previous.RawQuery != current.RawQuery {
			return outcome, fmt.Errorf("去除视频后缀后文件名冲突：%s", filepath.Base(absolute))
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return outcome, fmt.Errorf("读取本地 strm 文件失败：%w", readErr)
	}
	outcome.created, outcome.changed, err = s.writeManagedStrm(ctx, file, absolute, content)
	if err != nil {
		return outcome, fmt.Errorf("写入 strm 文件失败：%w", err)
	}
	outcome.skipped = !outcome.changed
	return outcome, strmCheckpointFailure(completeTaskUnit(ctx, "strm-file", target, file.Directory, file.ID))
}

// strmPipelineProgress serializes discovery and completion callbacks from independent stages.
type strmPipelineProgress struct {
	mu               sync.Mutex
	ctx              context.Context
	total, processed int
	advance          func()
}

func (progress *strmPipelineProgress) update(found, finished bool, phase, current string) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if found {
		progress.total++
	}
	if finished {
		progress.processed++
		if progress.advance != nil {
			progress.advance()
		}
	}
	reportScanProgress(progress.ctx, phase, progress.processed, progress.total, current)
}

type strmMappingWork struct {
	ctx    context.Context
	target string
	walk   strmWalk
}

// scanMappings uses one scanner and two task-wide disk queues; a stalled stage cannot block later mappings.
// Full cleanup is checkpointed before admission so retries never delete successful output again.
func (s *StrmService) scanMappings(ctx context.Context, root string, mappings []domain.StrmMapping, base string, mode domain.StrmGenerateMode, formats []string, progress *strmPipelineProgress, walks ...strmWalk) ([]domain.StrmScanMapping, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, retry := ctx.Value(strmRetryKey{}).(*strmRetryPolicy); retry {
		ctx = pan115.WithExternalRetry(ctx)
	}
	generate, err := newStrmWorkQueue()
	if err != nil {
		return nil, err
	}
	defer generate.close()
	download, err := newStrmWorkQueue()
	if err != nil {
		return nil, err
	}
	defer download.close()
	entries := make([]domain.StrmScanMapping, len(mappings))
	scopes := make([]strmMappingWork, len(mappings))
	for index, mapping := range mappings {
		if err := scanCheckpoint(ctx); err != nil {
			return entries, err
		}
		entries[index] = domain.StrmScanMapping{Kind: mapping.Kind, Path: mapping.Path, LocalPath: mapping.LocalPath}
		mappingCtx := withScanFileMapping(ctx, mapping)
		mappingCtx, err = s.managedContext(mappingCtx, mapping, formats)
		if err != nil {
			return entries, err
		}
		walk, walkErr := s.mappingWalk(mappingCtx, mapping, formats)
		if len(walks) > index {
			walk, walkErr = walks[index], nil
		}
		if walkErr != nil {
			entries[index].Message = scanFileError(walkErr)
			continue
		}
		target, _, prepareErr := resolveStrmPath(root, mapping.LocalPath)
		cleanupKey := journalKey("cleanup", mapping.Kind, mapping.ID, mapping.LocalPath)
		var cleaned bool
		if prepareErr == nil {
			_, prepareErr = loadTaskCheckpoint(mappingCtx, cleanupKey, &cleaned)
		}
		if prepareErr == nil && mode == domain.StrmGenerateFull && !cleaned {
			entries[index].Deleted, prepareErr = clearStrmContentContext(mappingCtx, root, target)
			if prepareErr == nil {
				prepareErr = saveTaskCheckpoint(mappingCtx, cleanupKey, true)
			}
		}
		if prepareErr == nil {
			prepareErr = os.MkdirAll(target, 0755)
		}
		if prepareErr != nil {
			entries[index].Message = "准备本地 strm 目录失败：" + scanFileError(prepareErr)
			continue
		}
		scopes[index] = strmMappingWork{ctx: mappingCtx, target: target, walk: walk}
	}
	_, retry := ctx.Value(strmRetryKey{}).(*strmRetryPolicy)
	var mutex sync.Mutex
	done := make(chan error, 2)
	start := func(queue *strmWorkQueue, workers, threshold int, operation, label string) {
		go func() {
			stageErr := runStrmStage(ctx, queue, workers, threshold, retry, func(workerCtx context.Context, item strmWorkItem) error {
				scope := scopes[item.Mapping]
				mapping := mappings[item.Mapping]
				workCtx, stop := context.WithCancel(scope.ctx)
				unlink := context.AfterFunc(workerCtx, stop)
				defer func() { unlink(); stop() }()
				workCtx = pan115.WithCooldownReporter(workCtx, func(time.Duration) {})
				finish := startScanFile(workCtx, item.File, operation)
				var skipped, created bool
				var workErr error
				if operation == "generate" {
					outcome, err := s.generateStrmFile(workCtx, root, scope.target, mapping.Kind, base, mode, item.File)
					skipped, created, workErr = outcome.skipped, outcome.created, err
				} else {
					skipped, workErr = taskUnitDone(workCtx, "download", scope.target, item.File.Directory, item.File.ID)
					workErr = strmCheckpointFailure(workErr)
					if workErr == nil && !skipped {
						skipped, workErr = s.downloadStrmMedia(workCtx, root, scope.target, mapping.Kind, item.File, mode)
						if workErr == nil {
							workErr = strmCheckpointFailure(completeTaskUnit(workCtx, "download", scope.target, item.File.Directory, item.File.ID))
						}
					}
				}
				state := "completed"
				if workErr != nil {
					state = "failed"
					if ctx.Err() != nil {
						state = "interrupted"
					}
				} else if skipped {
					state = "skipped"
				}
				finish(state, workErr)
				mutex.Lock()
				entry := &entries[item.Mapping]
				if workErr != nil {
					if item.Attempts == 0 {
						if operation == "generate" {
							entry.Failed++
						} else {
							entry.DownloadFailed++
						}
					}
					entry.Message = scanFileError(workErr)
				} else {
					if item.Attempts > 0 {
						if operation == "generate" {
							entry.Failed--
						} else {
							entry.DownloadFailed--
						}
					}
					if operation == "generate" {
						if skipped {
							entry.Unchanged++
						} else if created {
							entry.Created++
						}
					} else if skipped {
						entry.DownloadSkipped++
					} else {
						entry.Downloaded++
					}
					if entry.Failed == 0 && entry.DownloadFailed == 0 {
						entry.Message = ""
					}
				}
				mutex.Unlock()
				if operation == "generate" && (workErr == nil || !retry) {
					progress.update(false, true, "processing", mapping.Path)
				}
				return workErr
			}, func(waitCtx context.Context) error {
				progress.update(false, false, "cooling", label+"暂停，60 秒后单项重试")
				return waitStrmRetry(waitCtx, label, nil)
			})
			if stageErr != nil {
				cancel()
			}
			done <- stageErr
		}()
	}
	start(generate, 1, 10, "generate", "生成 STRM")
	start(download, strmDownloadWorkers, 20, "download", "下载")
	var scanErr error
	// 不同源文件去掉视频后缀后可能同名；入队前拒绝冲突，禁止后写覆盖或静默跳过。
	destinations := make(map[string]string)
	preservedDownloads := make(map[string]string)
	scanMessages := make([]string, len(mappings))
	for index, scope := range scopes {
		if scope.walk == nil {
			continue
		}
		mapping := mappings[index]
		if err := scanCheckpoint(scope.ctx); err != nil {
			scanErr = err
			cancel()
			break
		}
		walkCtx := withScanDiscovery(scope.ctx, func() { progress.update(true, false, "processing", mapping.Path) })
		walkCtx = pan115.WithCooldownReporter(walkCtx, func(wait time.Duration) {
			progress.update(false, false, "cooling", pan115CooldownNotice(mapping.Path, wait))
		})
		walkCtx = context.WithValue(walkCtx, strmRetryProgressKey{}, func() {
			progress.update(false, false, "cooling", mapping.Path+"：扫描暂停，60 秒后单项重试")
		})
		finish := scanFileDirectory(walkCtx, "")
		walkErr := scope.walk(walkCtx, func(file strmSourceFile) error {
			if err := scanCheckpoint(walkCtx); err != nil {
				return err
			}
			operation, queue := "generate", generate
			name := strmVideoName(file.Name)
			if isStrmMedia(file.Name, formats) {
				operation, queue = "download", download
				name = file.Name
			} else {
				mutex.Lock()
				entries[index].Files++
				mutex.Unlock()
			}
			absolute := filepath.Join(scope.target, filepath.FromSlash(file.Directory), name)
			destination := strings.ToLower(absolute)
			source := journalKey(mapping.Kind, file.ID, file.Directory, file.Name)
			previous, exists := destinations[destination]
			if !exists && operation == "download" && mode != domain.StrmGenerateFull {
				// 记录首次发现时已存在的附件，避免把本轮刚下载的文件当成可跳过的旧文件。
				info, err := os.Lstat(absolute)
				if err == nil && info.Mode().IsRegular() {
					preservedDownloads[destination] = absolute
				}
			}
			if exists && previous != source && preservedDownloads[destination] != absolute {
				if operation == "download" {
					return fmt.Errorf("网盘附件文件名冲突：%s", name)
				}
				return fmt.Errorf("去除视频后缀后文件名冲突：%s", name)
			}
			destinations[destination] = source
			trackScanFile(walkCtx, file, operation, "waiting")
			return queue.push(strmWorkItem{Mapping: index, File: file})
		})
		finish(walkErr == nil)
		if walkErr != nil {
			scanMessages[index] = scanFileError(walkErr)
			if retry || ctx.Err() != nil {
				scanErr = walkErr
				cancel()
				break
			}
		}
	}
	generate.seal()
	download.seal()
	firstErr, secondErr := <-done, <-done
	for index, message := range scanMessages {
		if message != "" {
			entries[index].Message = message
		}
	}
	return entries, errors.Join(scanErr, firstErr, secondErr)
}
