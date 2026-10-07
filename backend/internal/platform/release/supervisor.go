package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"bytemuse/backend/internal/ports"
)

// Activation 是启动器与安装器唯一共享的切换请求，不接受任意可执行路径。
type Activation struct {
	Directory string `json:"directory"`
	Version   string `json:"version"`
}

// writeRecord 使用同目录临时文件和 rename 提交，避免启动器读取半份 JSON。
func writeRecord(root, name string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, "record-")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, filepath.Join(root, name))
}

func readActivation(root, name string) (Activation, error) {
	var value Activation
	body, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return value, err
	}
	if json.Unmarshal(body, &value) != nil || !packageVersion.MatchString(value.Version) || !strings.HasPrefix(value.Directory, "staging-") || filepath.Base(value.Directory) != value.Directory || strings.ContainsAny(value.Directory, "/\\:") {
		return value, errors.New("升级切换记录无效")
	}
	return value, nil
}

// Supervise 作为容器 PID 1 保持存活，只替换子服务；退出信号先传递给子进程并等待退出。
// 切换前持久化目标版本，启动失败也不自动降级，避免旧程序访问新数据库结构。
func Supervise(ctx context.Context, root, executable, web, version, address string, shutdown time.Duration, output io.Writer) error {
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	state := (&PackageInstaller{Root: root}).Status()
	if state.Phase == "downloading" {
		if _, err := os.Stat(filepath.Join(root, "pending.json")); os.IsNotExist(err) {
			_ = writeRecord(root, "status.json", ports.UpgradeStatus{Enabled: true, Phase: "failed", Target: state.Target, Error: "上次升级下载被中断，请重试"})
		}
	}
	active, err := readActivation(root, "active.json")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if active.Version != "" {
		// 显式换用更高版本镜像时采用镜像；否则容器重启继续运行已升级的版本。
		if newerVersion(version, active.Version) {
			if err := os.Remove(filepath.Join(root, "active.json")); err != nil {
				return err
			}
			_ = writeRecord(root, "status.json", ports.UpgradeStatus{Enabled: true, Phase: "idle"})
			active = Activation{}
		}
	}
	for {
		binary, staticDir, current := executable, web, version
		if active.Version != "" {
			binary = filepath.Join(root, "releases", active.Directory, "bytemuse")
			staticDir = filepath.Join(root, "releases", active.Directory, "web")
			current = active.Version
		}
		command := exec.Command(binary, "serve")
		command.Env = childEnvironment(root, staticDir, current)
		command.Stdout = output
		command.Stderr = output
		if err := command.Start(); err != nil {
			_ = writeRecord(root, "status.json", ports.UpgradeStatus{Enabled: true, Phase: "failed", Target: current, Error: "新服务无法启动，请检查容器日志"})
			return fmt.Errorf("start service: %w", err)
		}
		exited := make(chan error, 1)
		go func() { exited <- command.Wait() }()
		ticker := time.NewTicker(time.Second)
		started := time.Now()
		healthy := false
		state := (&PackageInstaller{Root: root}).Status()
		checking := active.Version != "" && state.Phase == "restarting"
		var next Activation
		var runErr error
	loop:
		for {
			select {
			case <-ctx.Done():
				stopService(command, exited, shutdown)
				ticker.Stop()
				return nil
			case runErr = <-exited:
				break loop
			case <-ticker.C:
				if checking && !healthy {
					healthy = serviceReady(address)
					if healthy {
						_ = writeRecord(root, "status.json", ports.UpgradeStatus{Enabled: true, Phase: "success", Target: current})
					} else if time.Since(started) > 2*time.Minute {
						checking = false
						_ = writeRecord(root, "status.json", ports.UpgradeStatus{Enabled: true, Phase: "failed", Target: current, Error: "新服务未在两分钟内就绪，请检查容器日志"})
					}
				}
				pending, err := readActivation(root, "pending.json")
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					_ = os.Remove(filepath.Join(root, "pending.json"))
					_ = writeRecord(root, "status.json", ports.UpgradeStatus{Enabled: true, Phase: "failed", Error: "升级切换记录无效"})
					continue
				}
				if !newerVersion(pending.Version, current) {
					_ = os.Remove(filepath.Join(root, "pending.json"))
					continue
				}
				if err := writeRecord(root, "active.json", pending); err != nil {
					continue
				}
				_ = os.Remove(filepath.Join(root, "pending.json"))
				_ = writeRecord(root, "status.json", ports.UpgradeStatus{Enabled: true, Phase: "restarting", Target: pending.Version})
				next = pending
				stopService(command, exited, shutdown)
				break loop
			}
		}
		ticker.Stop()
		if next.Version == "" {
			_ = writeRecord(root, "status.json", ports.UpgradeStatus{Enabled: true, Phase: "failed", Target: current, Error: "服务已退出，请检查容器日志"})
			return fmt.Errorf("service exited: %v", runErr)
		}
		active = next
	}
}

// childEnvironment 确保升级后的前端目录与后端版本同时切换，排除旧镜像环境中的同名键。
func childEnvironment(root, web, version string) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "BYTEMUSE_VERSION=") && !strings.HasPrefix(entry, "WEB_STATIC_DIR=") && !strings.HasPrefix(entry, "BYTEMUSE_UPGRADE_ROOT=") && !strings.HasPrefix(entry, "BYTEMUSE_SUPERVISED=") {
			env = append(env, entry)
		}
	}
	return append(env, "BYTEMUSE_VERSION="+version, "WEB_STATIC_DIR="+web, "BYTEMUSE_UPGRADE_ROOT="+root, "BYTEMUSE_SUPERVISED=1")
}

func stopService(command *exec.Cmd, exited <-chan error, timeout time.Duration) {
	_ = command.Process.Signal(syscall.SIGTERM)
	timer := time.NewTimer(timeout + 5*time.Second)
	defer timer.Stop()
	select {
	case <-exited:
	case <-timer.C:
		_ = command.Process.Kill()
		<-exited
	}
}

func serviceReady(address string) bool {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	response, err := client.Get("http://127.0.0.1:" + port + "/health/ready")
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func newerVersion(left, right string) bool {
	return packageVersion.MatchString(left) && packageVersion.MatchString(right) && ports.CompareVersions(left, right) > 0
}
