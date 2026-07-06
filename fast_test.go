package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestUploadURLPreservesQueryAndAddsRange(t *testing.T) {
	t.Parallel()

	raw := "https://oca.example.com/speedtest?c=in&n=23860&v=236&e=1783352862&t=signed-token"

	got, err := uploadURL(raw)
	if err != nil {
		t.Fatalf("uploadURL returned error: %v", err)
	}

	want := "https://oca.example.com/speedtest/range/0-0?c=in&n=23860&v=236&e=1783352862&t=signed-token"
	if got != want {
		t.Fatalf("uploadURL = %q, want %q", got, want)
	}
}

func TestUploadURLReplacesExistingRange(t *testing.T) {
	t.Parallel()

	raw := "https://oca.example.com/speedtest/range/0-26214400?token=test"

	got, err := uploadURL(raw)
	if err != nil {
		t.Fatalf("uploadURL returned error: %v", err)
	}

	want := "https://oca.example.com/speedtest/range/0-0?token=test"
	if got != want {
		t.Fatalf("uploadURL = %q, want %q", got, want)
	}
}

func TestUploadPayloadCanCompleteOnSlowLinks(t *testing.T) {
	t.Parallel()

	requiredMbps := mbps(uploadPayloadBytes, duration)
	if requiredMbps >= 21 {
		t.Fatalf("upload payload requires %.1f Mbps to complete inside %s", requiredMbps, duration)
	}
}

func TestUploadPostsOctetStreamAndCountsBytes(t *testing.T) {
	var requests atomic.Int32
	var uploaded atomic.Int64
	allowResponse := make(chan struct{})
	responseAllowed := atomic.Bool{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want %q", r.Method, http.MethodPost)
		}

		if got := r.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("content-type = %q, want %q", got, "application/octet-stream")
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}

		uploaded.Add(int64(len(body)))
		<-allowResponse
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := httpClient
	httpClient = server.Client()
	defer func() {
		httpClient = client
	}()

	var total atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		upload(ctx, server.URL+"/speedtest?token=test", &total)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for uploaded.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if requests.Load() == 0 {
		t.Fatal("upload never issued a request")
	}

	if uploaded.Load() == 0 {
		t.Fatal("server saw zero uploaded bytes")
	}

	if total.Load() == 0 {
		t.Fatal("upload counter did not record bytes before the server completed the request")
	}
	if total.Load() != uploaded.Load() {
		t.Fatalf("upload counter recorded %d bytes, server saw %d", total.Load(), uploaded.Load())
	}

	responseAllowed.Store(true)
	close(allowResponse)

	deadline = time.Now().Add(2 * time.Second)
	for total.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done

	if !responseAllowed.Load() {
		t.Fatal("test did not allow the server response")
	}

	if total.Load() < uploadPayloadBytes {
		t.Fatalf("upload counter recorded %d bytes, want at least %d", total.Load(), uploadPayloadBytes)
	}
	if total.Load()%uploadPayloadBytes != 0 {
		t.Fatalf("upload counter recorded %d bytes, want whole payload chunks", total.Load())
	}
}

func TestUploadUsesAllTargets(t *testing.T) {
	t.Parallel()

	model := NewModel([]string{
		"https://oca1.example.com/speedtest?token=test",
		"https://oca2.example.com/speedtest?token=test",
		"https://oca3.example.com/speedtest?token=test",
	})

	targets := model.measurementTargets(uploadPhase)
	if len(targets) != 3 {
		t.Fatalf("upload targets = %d, want 3", len(targets))
	}
	for i, target := range targets {
		if target != model.targets[i] {
			t.Fatalf("upload target %d = %q, want %q", i, target, model.targets[i])
		}
	}
}

func TestDownloadUsesAllTargets(t *testing.T) {
	t.Parallel()

	model := NewModel([]string{
		"https://oca1.example.com/speedtest?token=test",
		"https://oca2.example.com/speedtest?token=test",
		"https://oca3.example.com/speedtest?token=test",
	})

	targets := model.measurementTargets(downloadPhase)
	if len(targets) != 3 {
		t.Fatalf("download targets = %d, want 3", len(targets))
	}
}

func TestDefaultHTTPClientHasTimeout(t *testing.T) {
	if httpClient.Timeout != requestTimeout {
		t.Fatalf("httpClient.Timeout = %s, want %s", httpClient.Timeout, requestTimeout)
	}
}
