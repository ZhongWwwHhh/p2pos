package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var Version string = "dev"

// GithubRelease represents a GitHub release
type GithubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// GetLatestVersion fetches the latest release version from GitHub
func GetLatestVersion(owner, repo string) (string, string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", owner, repo)

	resp, err := http.Get(url)
	if err != nil {
		return "", "", fmt.Errorf("failed to fetch GitHub release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
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

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to write binary: %w", err)
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

// CheckAndUpdate checks for updates and applies them if available
func CheckAndUpdate(owner, repo string) error {
	latestVersion, downloadURL, err := GetLatestVersion(owner, repo)
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	// Remove 'v' prefix if present for comparison
	currentVer := strings.TrimPrefix(Version, "v")
	latestVer := strings.TrimPrefix(latestVersion, "v")

	if currentVer >= latestVer {
		fmt.Printf("[UPDATE] Already at latest version: %s\n", Version)
		return nil
	}

	fmt.Printf("[UPDATE] New version available: %s (current: %s)\n", latestVersion, Version)
	fmt.Printf("[UPDATE] Downloading from: %s\n", downloadURL)

	// Get the path to the current executable
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Download the new binary
	fmt.Println("[UPDATE] Downloading new version...")
	if err := DownloadBinary(downloadURL, exePath); err != nil {
		return fmt.Errorf("failed to update binary: %w", err)
	}

	fmt.Printf("[UPDATE] Successfully updated to version %s\n", latestVersion)

	// 如果运行在 Linux 并且使用 systemd 管理服务，优先尝试通过 systemctl 重启服务
	if runtime.GOOS == "linux" {
		fmt.Println("[UPDATE] Attempting to restart systemd service 'p2pos'...")
		if err := exec.Command("systemctl", "restart", "p2pos").Run(); err == nil {
			fmt.Println("[UPDATE] systemd restart succeeded, exiting for service to restart")
			os.Exit(0)
		} else {
			fmt.Printf("[UPDATE] systemctl restart failed: %v\n", err)
		}
	}

	// 回退：直接重启当前程序（Spawn 新进程并退出当前进程）
	fmt.Println("[UPDATE] Restarting application directly...")
	if err := RestartApplication(); err != nil {
		fmt.Printf("[UPDATE] Failed to restart application: %v\n", err)
	}

	return nil
}

// StartUpdateChecker starts a background goroutine that checks for updates periodically
func StartUpdateChecker(owner, repo string, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			fmt.Println("[UPDATE] Checking for updates...")
			if err := CheckAndUpdate(owner, repo); err != nil {
				fmt.Printf("[UPDATE] Check failed: %v\n", err)
			}
		}
	}()
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

	os.Exit(0)
	return nil
}
