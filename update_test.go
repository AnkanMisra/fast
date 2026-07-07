package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestResolveVersionInfoPrefersOverrides(t *testing.T) {
	t.Parallel()

	info := resolveVersionInfo(
		&debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260707074651-64a5af45a06e"}},
		"v0.1.0",
		"abcdef1234567890",
		"2026-07-07T08:00:00Z",
	)

	if info.Kind != versionKindRelease {
		t.Fatalf("Kind = %q, want %q", info.Kind, versionKindRelease)
	}
	if info.Version != "v0.1.0" {
		t.Fatalf("Version = %q, want %q", info.Version, "v0.1.0")
	}
	if info.Commit != "abcdef1234567890" {
		t.Fatalf("Commit = %q, want %q", info.Commit, "abcdef1234567890")
	}
	if info.Date != "2026-07-07T08:00:00Z" {
		t.Fatalf("Date = %q, want %q", info.Date, "2026-07-07T08:00:00Z")
	}
}

func TestResolveVersionInfoDetectsPseudoVersion(t *testing.T) {
	t.Parallel()

	info := resolveVersionInfo(
		&debug.BuildInfo{
			Main: debug.Module{Version: "v0.0.0-20260707074651-64a5af45a06e"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "64a5af45a06e"},
				{Key: "vcs.time", Value: "2026-07-07T07:46:51Z"},
			},
		},
		"",
		"",
		"",
	)

	if info.Kind != versionKindPseudo {
		t.Fatalf("Kind = %q, want %q", info.Kind, versionKindPseudo)
	}
	if info.Version != "v0.0.0-20260707074651-64a5af45a06e" {
		t.Fatalf("Version = %q", info.Version)
	}
	if info.PseudoTime.IsZero() {
		t.Fatal("PseudoTime should be populated")
	}
}

func TestVersionInfoOutdatedForTaggedRelease(t *testing.T) {
	t.Parallel()

	info := versionInfo{Kind: versionKindRelease, Version: "v0.1.0"}
	release := latestRelease{
		TagName:   "v0.2.0",
		HTMLURL:   "https://github.com/AnkanMisra/fast/releases/tag/v0.2.0",
		CreatedAt: time.Date(2026, 7, 7, 8, 0, 0, 0, time.UTC),
	}

	if !info.isOutdated(release) {
		t.Fatal("release build should be marked outdated")
	}
}

func TestVersionInfoPseudoBuildUsesReleaseTime(t *testing.T) {
	t.Parallel()

	pseudoTime := time.Date(2026, 7, 7, 7, 46, 51, 0, time.UTC)
	info := versionInfo{
		Kind:       versionKindPseudo,
		Version:    "v0.0.0-20260707074651-64a5af45a06e",
		PseudoTime: pseudoTime,
	}

	newerRelease := latestRelease{
		TagName:   "v0.1.0",
		CreatedAt: pseudoTime.Add(time.Hour),
	}
	if !info.isOutdated(newerRelease) {
		t.Fatal("older pseudo build should be marked outdated")
	}

	olderRelease := latestRelease{
		TagName:   "v0.1.0",
		CreatedAt: pseudoTime.Add(-time.Hour),
	}
	if info.isOutdated(olderRelease) {
		t.Fatal("newer pseudo build should not be marked outdated against older release")
	}
}

func TestUpdateCheckerEnabledHonorsEnvironmentAndTTY(t *testing.T) {
	t.Parallel()

	checker := newUpdateChecker(versionInfo{Kind: versionKindRelease, Version: "v0.1.0"})
	checker.interactive = func() bool { return true }
	checker.getenv = func(key string) string { return "" }

	if !checker.enabled() {
		t.Fatal("checker should be enabled by default")
	}

	checker.interactive = func() bool { return false }
	if checker.enabled() {
		t.Fatal("checker should be disabled when not interactive")
	}

	checker.interactive = func() bool { return true }
	checker.getenv = func(key string) string {
		if key == "CI" {
			return "true"
		}
		return ""
	}
	if checker.enabled() {
		t.Fatal("checker should be disabled in CI")
	}

	checker.getenv = func(key string) string {
		if key == "FAST_NO_UPDATE_NOTIFIER" {
			return "1"
		}
		return ""
	}
	if checker.enabled() {
		t.Fatal("checker should be disabled when opted out")
	}
}

func TestUpdateCheckerCadenceAndNoticeCadence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	checker := newUpdateChecker(versionInfo{Kind: versionKindRelease, Version: "v0.1.0"})
	checker.now = func() time.Time { return now }

	state := updateState{
		LastCheckedAt:   now.Add(-23 * time.Hour),
		LastNotifiedAt:  now.Add(-23 * time.Hour),
		LatestTag:       "v0.2.0",
		LatestHTMLURL:   "https://github.com/AnkanMisra/fast/releases/tag/v0.2.0",
		LatestCreatedAt: now.Add(-48 * time.Hour),
	}

	if checker.shouldCheck(state) {
		t.Fatal("state should still be fresh for checks")
	}

	if _, ok := checker.notice(state); ok {
		t.Fatal("notice should still be suppressed inside the 24h window")
	}

	checker.now = func() time.Time { return now.Add(25 * time.Hour) }
	if !checker.shouldCheck(state) {
		t.Fatal("state should be stale after 24h")
	}
	if _, ok := checker.notice(state); !ok {
		t.Fatal("notice should be shown again after 24h")
	}
}

func TestUpdateCheckerFetchLatestRelease(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			t.Fatalf("path = %q, want /releases/latest", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"https://github.com/AnkanMisra/fast/releases/tag/v0.2.0","created_at":"2026-07-07T08:00:00Z"}`))
	}))
	defer server.Close()

	checker := newUpdateChecker(versionInfo{Kind: versionKindRelease, Version: "v0.1.0"})
	checker.client = server.Client()
	checker.latestReleaseURL = server.URL + "/releases/latest"

	release, err := checker.fetchLatestRelease(context.Background())
	if err != nil {
		t.Fatalf("fetchLatestRelease returned error: %v", err)
	}
	if release.TagName != "v0.2.0" {
		t.Fatalf("TagName = %q, want v0.2.0", release.TagName)
	}
	if release.HTMLURL == "" {
		t.Fatal("HTMLURL should be set")
	}
}

func TestUpdateCheckerPersistsState(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	checker := newUpdateChecker(versionInfo{Kind: versionKindRelease, Version: "v0.1.0"})
	checker.now = func() time.Time { return now }
	checker.userCacheDir = func() (string, error) { return dir, nil }

	state := updateState{
		LastCheckedAt:   now,
		LatestTag:       "v0.2.0",
		LatestHTMLURL:   "https://github.com/AnkanMisra/fast/releases/tag/v0.2.0",
		LatestCreatedAt: now.Add(-time.Hour),
	}
	if err := checker.saveState(state); err != nil {
		t.Fatalf("saveState returned error: %v", err)
	}

	loaded, err := checker.loadState()
	if err != nil {
		t.Fatalf("loadState returned error: %v", err)
	}
	if loaded.LatestTag != "v0.2.0" {
		t.Fatalf("LatestTag = %q, want v0.2.0", loaded.LatestTag)
	}

	cacheFile := filepath.Join(dir, appName, updateCacheFileName)
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("expected cache file to exist at %s: %v", cacheFile, err)
	}
}

func TestUpdateCheckerNoticeIncludesUpgradeCommand(t *testing.T) {
	t.Parallel()

	checker := newUpdateChecker(versionInfo{Kind: versionKindRelease, Version: "v0.1.0"})
	checker.now = func() time.Time { return time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC) }

	state := updateState{
		LatestTag:       "v0.2.0",
		LatestHTMLURL:   "https://github.com/AnkanMisra/fast/releases/tag/v0.2.0",
		LatestCreatedAt: time.Date(2026, 7, 7, 8, 0, 0, 0, time.UTC),
	}

	notice, ok := checker.notice(state)
	if !ok {
		t.Fatal("notice should be shown")
	}
	if !strings.Contains(notice, "go install github.com/AnkanMisra/fast@latest") {
		t.Fatalf("notice = %q, want upgrade command", notice)
	}
	if !strings.Contains(notice, "v0.2.0") {
		t.Fatalf("notice = %q, want latest version", notice)
	}
}

func TestUpdateCheckerPrepareResolveAndMarkNotified(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	releaseCreatedAt := now.Add(-2 * time.Hour)
	releaseReady := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-releaseReady
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"https://github.com/AnkanMisra/fast/releases/tag/v0.2.0","created_at":"2026-07-07T08:00:00Z"}`))
	}))
	defer server.Close()

	checker := newUpdateChecker(versionInfo{Kind: versionKindRelease, Version: "v0.1.0"})
	checker.now = func() time.Time { return now }
	checker.userCacheDir = func() (string, error) { return dir, nil }
	checker.client = server.Client()
	checker.latestReleaseURL = server.URL

	cached, refreshed := checker.prepare()
	if !cached.LastCheckedAt.IsZero() {
		t.Fatalf("cached.LastCheckedAt = %v, want zero value", cached.LastCheckedAt)
	}
	if refreshed == nil {
		t.Fatal("prepare should start a refresh channel")
	}

	if resolved := checker.resolveState(cached, refreshed); resolved.LatestTag != "" {
		t.Fatalf("resolveState should return cached state before refresh completes, got tag %q", resolved.LatestTag)
	}

	close(releaseReady)

	var resolved updateState
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resolved = checker.resolveState(cached, refreshed)
		if resolved.LatestTag != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if resolved.LatestTag != "v0.2.0" {
		t.Fatalf("resolved.LatestTag = %q, want v0.2.0", resolved.LatestTag)
	}
	if resolved.LastCheckedAt != now {
		t.Fatalf("resolved.LastCheckedAt = %v, want %v", resolved.LastCheckedAt, now)
	}
	if resolved.LatestCreatedAt != releaseCreatedAt {
		t.Fatalf("resolved.LatestCreatedAt = %v, want %v", resolved.LatestCreatedAt, releaseCreatedAt)
	}

	if err := checker.markNotified(resolved); err != nil {
		t.Fatalf("markNotified returned error: %v", err)
	}

	loaded, err := checker.loadState()
	if err != nil {
		t.Fatalf("loadState returned error: %v", err)
	}
	if loaded.LastNotifiedAt != now {
		t.Fatalf("loaded.LastNotifiedAt = %v, want %v", loaded.LastNotifiedAt, now)
	}
}

func TestUpdateCheckerRefreshFailureStillPersistsCheckCadence(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	existingCreatedAt := now.Add(-48 * time.Hour)
	checker := newUpdateChecker(versionInfo{Kind: versionKindRelease, Version: "v0.1.0"})
	checker.now = func() time.Time { return now }
	checker.userCacheDir = func() (string, error) { return dir, nil }
	checker.client = &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       http.NoBody,
				Header:     make(http.Header),
			}, nil
		}),
	}
	checker.latestReleaseURL = "https://api.github.com/repos/AnkanMisra/fast/releases/latest"

	state := updateState{
		LatestTag:       "v0.1.1",
		LatestHTMLURL:   "https://github.com/AnkanMisra/fast/releases/tag/v0.1.1",
		LatestCreatedAt: existingCreatedAt,
	}

	refreshed, err := checker.refresh(context.Background(), state)
	if err == nil {
		t.Fatal("refresh should return the upstream error")
	}
	if refreshed.LastCheckedAt != now {
		t.Fatalf("refreshed.LastCheckedAt = %v, want %v", refreshed.LastCheckedAt, now)
	}
	if refreshed.LatestTag != state.LatestTag {
		t.Fatalf("refreshed.LatestTag = %q, want %q", refreshed.LatestTag, state.LatestTag)
	}
	if refreshed.LatestCreatedAt != existingCreatedAt {
		t.Fatalf("refreshed.LatestCreatedAt = %v, want %v", refreshed.LatestCreatedAt, existingCreatedAt)
	}

	loaded, loadErr := checker.loadState()
	if loadErr != nil {
		t.Fatalf("loadState returned error: %v", loadErr)
	}
	if loaded.LastCheckedAt != now {
		t.Fatalf("loaded.LastCheckedAt = %v, want %v", loaded.LastCheckedAt, now)
	}
	if loaded.LatestTag != state.LatestTag {
		t.Fatalf("loaded.LatestTag = %q, want %q", loaded.LatestTag, state.LatestTag)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
