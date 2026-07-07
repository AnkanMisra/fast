package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"
)

const (
	updateCacheFileName = "update.json"
	updateCheckInterval = 24 * time.Hour
)

type latestRelease struct {
	TagName   string    `json:"tag_name"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
}

type updateState struct {
	LastCheckedAt   time.Time `json:"last_checked_at,omitempty"`
	LastNotifiedAt  time.Time `json:"last_notified_at,omitempty"`
	LatestTag       string    `json:"latest_tag,omitempty"`
	LatestHTMLURL   string    `json:"latest_html_url,omitempty"`
	LatestCreatedAt time.Time `json:"latest_created_at,omitempty"`
}

type updateChecker struct {
	version          versionInfo
	client           *http.Client
	now              func() time.Time
	getenv           func(string) string
	interactive      func() bool
	userCacheDir     func() (string, error)
	latestReleaseURL string
}

func newUpdateChecker(version versionInfo) *updateChecker {
	return &updateChecker{
		version: version,
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
		now:              time.Now,
		getenv:           os.Getenv,
		interactive:      isInteractiveSession,
		userCacheDir:     os.UserCacheDir,
		latestReleaseURL: "https://api.github.com/repos/AnkanMisra/fast/releases/latest",
	}
}

func isInteractiveSession() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

func (u *updateChecker) enabled() bool {
	if !u.interactive() {
		return false
	}
	if u.getenv("CI") != "" {
		return false
	}
	if u.getenv("FAST_NO_UPDATE_NOTIFIER") != "" {
		return false
	}
	return true
}

func (u *updateChecker) shouldCheck(state updateState) bool {
	if state.LastCheckedAt.IsZero() {
		return true
	}
	return u.now().Sub(state.LastCheckedAt) >= updateCheckInterval
}

func (u *updateChecker) notice(state updateState) (string, bool) {
	release := latestRelease{
		TagName:   state.LatestTag,
		HTMLURL:   state.LatestHTMLURL,
		CreatedAt: state.LatestCreatedAt,
	}
	if !u.version.isOutdated(release) {
		return "", false
	}
	if !state.LastNotifiedAt.IsZero() && u.now().Sub(state.LastNotifiedAt) < updateCheckInterval {
		return "", false
	}

	return fmt.Sprintf(
		"A new version of %s is available: %s (you have %s)\nUpgrade with: go install github.com/AnkanMisra/fast@latest\nRelease notes: %s",
		appName,
		release.TagName,
		u.version.versionString(),
		release.HTMLURL,
	), true
}

func (u *updateChecker) updateCachePath() (string, error) {
	cacheDir, err := u.userCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, appName, updateCacheFileName), nil
}

func (u *updateChecker) loadState() (updateState, error) {
	cachePath, err := u.updateCachePath()
	if err != nil {
		return updateState{}, err
	}

	body, err := os.ReadFile(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return updateState{}, nil
		}
		return updateState{}, err
	}

	var state updateState
	if err := json.Unmarshal(body, &state); err != nil { //nolint:nilerr // corrupted cache is treated as empty state to trigger a fresh refresh
		return updateState{}, nil
	}
	return state, nil
}

func (u *updateChecker) saveState(state updateState) error {
	cachePath, err := u.updateCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := cachePath + ".tmp"
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, cachePath)
}

func (u *updateChecker) fetchLatestRelease(ctx context.Context) (latestRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.latestReleaseURL, nil)
	if err != nil {
		return latestRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := u.client.Do(req)
	if err != nil {
		return latestRelease{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return latestRelease{}, fmt.Errorf("latest release request failed: %s", resp.Status)
	}

	var release latestRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return latestRelease{}, err
	}
	return release, nil
}

func (u *updateChecker) refresh(ctx context.Context, state updateState) (updateState, error) {
	state.LastCheckedAt = u.now()
	release, err := u.fetchLatestRelease(ctx)
	if err != nil {
		saveErr := u.saveState(state)
		if saveErr != nil {
			return state, saveErr
		}
		return state, err
	}
	state.LatestTag = release.TagName
	state.LatestHTMLURL = release.HTMLURL
	state.LatestCreatedAt = release.CreatedAt
	return state, u.saveState(state)
}

func (u *updateChecker) prepare() (updateState, <-chan updateState) {
	state, err := u.loadState()
	if err != nil || !u.shouldCheck(state) {
		return state, nil
	}

	ch := make(chan updateState, 1)
	go func() {
		defer close(ch)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		refreshed, err := u.refresh(ctx, state)
		if err != nil {
			return
		}
		ch <- refreshed
	}()
	return state, ch
}

func (u *updateChecker) resolveState(state updateState, refreshed <-chan updateState) updateState {
	if refreshed == nil {
		return state
	}
	select {
	case updated, ok := <-refreshed:
		if ok {
			return updated
		}
	default:
	}
	return state
}

func (u *updateChecker) markNotified(state updateState) error {
	state.LastNotifiedAt = u.now()
	return u.saveState(state)
}
