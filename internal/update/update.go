package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"p2pos/internal/config"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Service struct {
	configProvider FeedURLProvider
	shutdown       ShutdownRequester
	restarter      Restarter
	mu             sync.Mutex
}

type FeedURLProvider interface {
	UpdateFeedURL() (string, error)
}

type ShutdownRequester interface {
	RequestShutdown(reason string)
}

func NewService(configProvider FeedURLProvider, shutdown ShutdownRequester) *Service {
	return &Service{
		configProvider: configProvider,
		shutdown:       shutdown,
		restarter: NewChainRestarter(
			NewSystemdRestarter("p2pos"),
			&SpawnSelfRestarter{},
		),
	}
}

func NewServiceWithRestarter(configProvider FeedURLProvider, shutdown ShutdownRequester, restarter Restarter) *Service {
	if restarter == nil {
		restarter = NewChainRestarter(
			NewSystemdRestarter("p2pos"),
			&SpawnSelfRestarter{},
		)
	}
	return &Service{
		configProvider: configProvider,
		shutdown:       shutdown,
		restarter:      restarter,
	}
}

type Restarter interface {
	Restart(ctx context.Context) error
}

type RestartStrategy interface {
	Name() string
	Restart(ctx context.Context) error
}

type ChainRestarter struct {
	strategies []RestartStrategy
}

func NewChainRestarter(strategies ...RestartStrategy) *ChainRestarter {
	return &ChainRestarter{strategies: strategies}
}

func (r *ChainRestarter) Restart(ctx context.Context) error {
	var errs []error
	for _, strategy := range r.strategies {
		fmt.Printf("[UPDATE] Restart strategy: %s\n", strategy.Name())
		if err := strategy.Restart(ctx); err != nil {
			fmt.Printf("[UPDATE] Restart strategy %s failed: %v\n", strategy.Name(), err)
			errs = append(errs, fmt.Errorf("%s: %w", strategy.Name(), err))
			continue
		}
		fmt.Printf("[UPDATE] Restart strategy %s succeeded\n", strategy.Name())
		return nil
	}
	return fmt.Errorf("all restart strategies failed: %w", errorsJoin(errs))
}

type SystemdRestarter struct {
	serviceName string
}

func NewSystemdRestarter(serviceName string) *SystemdRestarter {
	return &SystemdRestarter{serviceName: serviceName}
}

func (r *SystemdRestarter) Name() string {
	return "systemd-restart"
}

func (r *SystemdRestarter) Restart(ctx context.Context) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("unsupported on %s", runtime.GOOS)
	}
	return exec.CommandContext(ctx, "systemctl", "restart", r.serviceName).Run()
}

type SpawnSelfRestarter struct{}

func (r *SpawnSelfRestarter) Name() string {
	return "spawn-self"
}

func (r *SpawnSelfRestarter) Restart(_ context.Context) error {
	return RestartApplication()
}

// GithubRelease represents a GitHub release
type GithubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// GetLatestVersion fetches latest release metadata from configured feed URL.
func GetLatestVersion(feedURL string) (string, string, error) {
	resp, err := http.Get(feedURL)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch release feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("release feed returned status %d", resp.StatusCode)
	}

	var release GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", fmt.Errorf("failed to parse GitHub release: %w", err)
	}

	// Get the appropriate binary for current OS
	binaryName := getBinaryName()
	var downloadURL string
	for _, asset := range release.Assets {
		if asset.Name == binaryName {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if downloadURL == "" {
		return "", "", fmt.Errorf("binary %s not found in release %s", binaryName, release.TagName)
	}

	return release.TagName, downloadURL, nil
}

func getBinaryName() string {
	switch runtime.GOOS {
	case "linux":
		return "p2pos-linux"
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "p2pos-darwin-arm64"
		}
		return "p2pos-darwin-amd64"
	case "windows":
		return "p2pos-windows.exe"
	default:
		return "p2pos"
	}
}

// DownloadBinary downloads the binary from the given URL
func DownloadBinary(url, targetPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	// Write to temporary file first
	tmpFile := targetPath + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer f.Close()

	totalSize := resp.ContentLength
	var downloaded int64
	nextPercent := int64(5)
	nextUnknownLogBytes := int64(5 * 1024 * 1024) // 5 MiB
	startTime := time.Now().UTC()
	buf := make([]byte, 32*1024)

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				os.Remove(tmpFile)
				return fmt.Errorf("failed to write binary: %w", err)
			}
			downloaded += int64(n)

			elapsed := time.Since(startTime).Seconds()
			if elapsed <= 0 {
				elapsed = 0.001
			}
			speedMBps := float64(downloaded) / 1024.0 / 1024.0 / elapsed

			if totalSize > 0 {
				percent := downloaded * 100 / totalSize
				for percent >= nextPercent && nextPercent <= 100 {
					fmt.Printf(
						"[UPDATE] Download progress: %d%% (%0.2f/%0.2f MiB) speed: %0.2f MiB/s\n",
						nextPercent,
						float64(downloaded)/1024.0/1024.0,
						float64(totalSize)/1024.0/1024.0,
						speedMBps,
					)
					nextPercent += 5
				}
			} else if downloaded >= nextUnknownLogBytes {
				fmt.Printf(
					"[UPDATE] Downloaded: %0.2f MiB speed: %0.2f MiB/s\n",
					float64(downloaded)/1024.0/1024.0,
					speedMBps,
				)
				nextUnknownLogBytes += 5 * 1024 * 1024
			}
		}

		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			os.Remove(tmpFile)
			return fmt.Errorf("failed to read download stream: %w", readErr)
		}
	}

	// Make executable on Unix-like systems
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpFile, 0755); err != nil {
			os.Remove(tmpFile)
			return fmt.Errorf("failed to make binary executable: %w", err)
		}
	}

	// Replace old binary with new one
	// On Windows, we need to stop the process first
	if err := os.Rename(tmpFile, targetPath); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	return nil
}

// CheckAndUpdate checks for updates and applies them if available.
func CheckAndUpdate(feedURL string) (bool, error) {
	latestVersion, downloadURL, err := GetLatestVersion(feedURL)
	if err != nil {
		return false, fmt.Errorf("failed to check for updates: %w", err)
	}

	// Remove 'v' prefix if present for comparison
	currentVer := strings.TrimPrefix(config.AppVersion, "v")
	latestVer := strings.TrimPrefix(latestVersion, "v")

	if currentVer >= latestVer {
		fmt.Printf("[UPDATE] Already at latest version: %s\n", config.AppVersion)
		return false, nil
	}

	fmt.Printf("[UPDATE] New version available: %s (current: %s)\n", latestVersion, config.AppVersion)
	fmt.Printf("[UPDATE] Downloading from: %s\n", downloadURL)

	// Get the path to the current executable
	exePath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("failed to get executable path: %w", err)
	}

	// Download the new binary
	fmt.Println("[UPDATE] Downloading new version...")
	if err := DownloadBinary(downloadURL, exePath); err != nil {
		return false, fmt.Errorf("failed to update binary: %w", err)
	}

	fmt.Printf("[UPDATE] Successfully updated to version %s\n", latestVersion)
	return true, nil
}

func (s *Service) RunOnce(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	feedURL, err := s.configProvider.UpdateFeedURL()
	if err != nil {
		return fmt.Errorf("load update feed url failed: %w", err)
	}

	fmt.Println("[UPDATE] Checking for updates...")
	updated, err := CheckAndUpdate(feedURL)
	if err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	if !updated {
		return nil
	}

	if err := s.restarter.Restart(ctx); err != nil {
		return fmt.Errorf("updated but restart failed: %w", err)
	}

	if s.shutdown != nil {
		s.shutdown.RequestShutdown("update-applied")
	}

	return nil
}

// RestartApplication restarts the current application
func RestartApplication() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	cmd := exec.Command(exePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to restart application: %w", err)
	}

	return nil
}

func errorsJoin(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msg := ""
	for i, err := range errs {
		if i > 0 {
			msg += "; "
		}
		msg += err.Error()
	}
	return fmt.Errorf("%s", msg)
}
