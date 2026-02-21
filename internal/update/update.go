package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"p2pos/internal/config"
	"p2pos/internal/logging"
)

type Service struct {
	configProvider FeedURLProvider
	shutdown       ShutdownRequester
	mu             sync.Mutex
}

type FeedURLProvider interface {
	UpdateFeedURL() (string, error)
	UpdateChannel() string
}

type ShutdownRequester interface {
	RequestShutdown(reason string)
}

func NewService(configProvider FeedURLProvider, shutdown ShutdownRequester) *Service {
	return &Service{
		configProvider: configProvider,
		shutdown:       shutdown,
	}
}

// GithubRelease represents a GitHub release
type GithubRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// GetLatestVersion fetches latest release metadata from configured feed URL.
func GetLatestVersion(feedURL, channel string) (string, string, error) {
	release, err := pickRelease(feedURL, channel)
	if err != nil {
		return "", "", err
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

func pickRelease(feedURL, channel string) (GithubRelease, error) {
	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch == "" {
		ch = "stable"
	}

	if ch == "develop" {
		if listURL, ok := toGitHubReleasesListURL(feedURL); ok {
			releases, err := fetchReleaseList(listURL)
			if err == nil {
				for _, r := range releases {
					if r.Draft {
						continue
					}
					return r, nil
				}
			}
		}
	}

	// Stable path (or develop fallback): use feedURL release payload.
	release, err := fetchSingleRelease(feedURL)
	if err == nil {
		if release.Draft || (ch == "stable" && release.Prerelease) {
			return GithubRelease{}, fmt.Errorf("release %s not allowed for channel %s", release.TagName, ch)
		}
		return release, nil
	}

	// Fallback: if feed URL actually returns a list, pick according to channel.
	releases, listErr := fetchReleaseList(feedURL)
	if listErr != nil {
		return GithubRelease{}, fmt.Errorf("failed to fetch release feed: %w", err)
	}
	for _, r := range releases {
		if r.Draft {
			continue
		}
		if ch == "stable" && r.Prerelease {
			continue
		}
		return r, nil
	}

	return GithubRelease{}, fmt.Errorf("no eligible release found for channel %s", ch)
}

func fetchSingleRelease(feedURL string) (GithubRelease, error) {
	resp, err := http.Get(feedURL)
	if err != nil {
		return GithubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return GithubRelease{}, fmt.Errorf("release feed returned status %d", resp.StatusCode)
	}
	var release GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return GithubRelease{}, err
	}
	return release, nil
}

func fetchReleaseList(feedURL string) ([]GithubRelease, error) {
	resp, err := http.Get(feedURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release feed returned status %d", resp.StatusCode)
	}
	var releases []GithubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

func toGitHubReleasesListURL(feedURL string) (string, bool) {
	u, err := url.Parse(feedURL)
	if err != nil {
		return "", false
	}
	if u.Host != "api.github.com" {
		return "", false
	}
	if !strings.HasSuffix(u.Path, "/releases/latest") {
		return "", false
	}
	u.Path = strings.TrimSuffix(u.Path, "/latest")
	q := u.Query()
	q.Set("per_page", "20")
	u.RawQuery = q.Encode()
	return u.String(), true
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
					logging.Log("UPDATE", "download_progress", map[string]string{
						"percent":  fmt.Sprintf("%d", nextPercent),
						"download": fmt.Sprintf("%0.2fMiB", float64(downloaded)/1024.0/1024.0),
						"total":    fmt.Sprintf("%0.2fMiB", float64(totalSize)/1024.0/1024.0),
						"speed":    fmt.Sprintf("%0.2fMiB_s", speedMBps),
					})
					nextPercent += 5
				}
			} else if downloaded >= nextUnknownLogBytes {
				logging.Log("UPDATE", "download_progress", map[string]string{
					"download": fmt.Sprintf("%0.2fMiB", float64(downloaded)/1024.0/1024.0),
					"speed":    fmt.Sprintf("%0.2fMiB_s", speedMBps),
				})
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
func CheckAndUpdate(feedURL, channel string) (bool, error) {
	latestVersion, downloadURL, err := GetLatestVersion(feedURL, channel)
	if err != nil {
		return false, fmt.Errorf("failed to check for updates: %w", err)
	}

	// Remove 'v' prefix if present for comparison
	currentVer := strings.TrimPrefix(config.AppVersion, "v")
	latestVer := strings.TrimPrefix(latestVersion, "v")

	if currentVer >= latestVer {
		logging.Log("UPDATE", "already_latest", map[string]string{
			"version": config.AppVersion,
		})
		return false, nil
	}

	logging.Log("UPDATE", "new_version", map[string]string{
		"latest":  latestVersion,
		"current": config.AppVersion,
	})
	logging.Log("UPDATE", "download_from", map[string]string{
		"url": downloadURL,
	})

	// Get the path to the current executable
	exePath, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("failed to get executable path: %w", err)
	}

	// Download the new binary
	logging.Log("UPDATE", "download_start", nil)
	if err := DownloadBinary(downloadURL, exePath); err != nil {
		return false, fmt.Errorf("failed to update binary: %w", err)
	}

	logging.Log("UPDATE", "updated", map[string]string{
		"version": latestVersion,
	})
	return true, nil
}

func (s *Service) RunOnce(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	feedURL, err := s.configProvider.UpdateFeedURL()
	if err != nil {
		return fmt.Errorf("load update feed url failed: %w", err)
	}
	channel := s.configProvider.UpdateChannel()

	logging.Log("UPDATE", "check", map[string]string{
		"channel": channel,
	})
	updated, err := CheckAndUpdate(feedURL, channel)
	if err != nil {
		return fmt.Errorf("check failed: %w", err)
	}
	if !updated {
		return nil
	}

	logging.Log("UPDATE", "applied_shutdown", nil)
	if s.shutdown != nil {
		s.shutdown.RequestShutdown("update-applied")
	}

	return nil
}
