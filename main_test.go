package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelDefaultsToSequentialDownloadThenUpload(t *testing.T) {
	t.Parallel()

	now := time.Unix(100, 0)
	var mu sync.Mutex
	var started []Phase

	recordProbe := func(phase Phase) ProbeFunc {
		return func(ctx context.Context, url string, total *atomic.Int64) {
			mu.Lock()
			started = append(started, phase)
			mu.Unlock()

			total.Add(125_000)
			<-ctx.Done()
		}
	}

	model := NewModel([]string{"https://oca.example.com/speedtest?token=test"}, ModelConfig{
		Duration:     time.Second,
		TickInterval: time.Millisecond,
		Now: func() time.Time {
			return now
		},
		DownloadProbe: recordProbe(downloadPhase),
		UploadProbe:   recordProbe(uploadPhase),
	})

	runCommandAsync(model.Init())
	waitForStarted(t, &mu, &started, 1)
	defer model.stopPhase()

	mu.Lock()
	gotStarted := append([]Phase(nil), started...)
	mu.Unlock()

	if len(gotStarted) != 1 || gotStarted[0] != downloadPhase {
		t.Fatalf("started phases after Init = %v, want only download", gotStarted)
	}

	now = now.Add(time.Second)
	updated, cmd := model.Update(tickMsg(now))
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("download completion should start upload command")
	}

	runCommandAsync(cmd)
	waitForStarted(t, &mu, &started, 2)

	mu.Lock()
	gotStarted = append([]Phase(nil), started...)
	mu.Unlock()

	if gotStarted[1] != uploadPhase {
		t.Fatalf("second phase = %v, want upload; all phases = %v", gotStarted[1], gotStarted)
	}
}

func TestModelCompletesAfterSequentialUpload(t *testing.T) {
	t.Parallel()

	now := time.Unix(200, 0)
	model := NewModel([]string{"https://oca.example.com/speedtest?token=test"}, ModelConfig{
		Duration:     time.Second,
		TickInterval: time.Millisecond,
		Now: func() time.Time {
			return now
		},
		DownloadProbe: func(ctx context.Context, url string, total *atomic.Int64) {
			total.Add(125_000)
			<-ctx.Done()
		},
		UploadProbe: func(ctx context.Context, url string, total *atomic.Int64) {
			total.Add(250_000)
			<-ctx.Done()
		},
	})

	runCommandAsync(model.Init())
	waitUntil(t, func() bool {
		return model.download.bytes.Load() == 125_000
	})

	now = now.Add(time.Second)
	updated, cmd := model.Update(tickMsg(now))
	model = updated.(Model)

	if model.download.bytes.Load() != 125_000 {
		t.Fatalf("download bytes = %d, want 125000", model.download.bytes.Load())
	}

	if model.done {
		t.Fatal("model should not be done until upload finishes")
	}

	if cmd == nil {
		t.Fatal("download completion should return upload command")
	}

	runCommandAsync(cmd)
	waitUntil(t, func() bool {
		return model.upload.bytes.Load() == 250_000
	})

	now = now.Add(time.Second)
	updated, cmd = model.Update(tickMsg(now))
	model = updated.(Model)

	if model.upload.bytes.Load() != 250_000 {
		t.Fatalf("upload bytes = %d, want 250000", model.upload.bytes.Load())
	}

	if !model.done {
		t.Fatal("model should be done after upload measurement window")
	}

	if cmd == nil {
		t.Fatal("completion should return quit command")
	}
}

func TestSequentialTransitionSchedulesUploadAndNextTick(t *testing.T) {
	t.Parallel()

	now := time.Unix(250, 0)
	blockingProbe := func(ctx context.Context, url string, total *atomic.Int64) {
		total.Add(1)
		<-ctx.Done()
	}

	model := NewModel([]string{"https://oca.example.com/speedtest?token=test"}, ModelConfig{
		Duration:     time.Second,
		TickInterval: time.Millisecond,
		Now: func() time.Time {
			return now
		},
		DownloadProbe: blockingProbe,
		UploadProbe:   blockingProbe,
	})

	runCommandAsync(model.Init())
	waitUntil(t, func() bool {
		return model.download.bytes.Load() == 1
	})

	now = now.Add(time.Second)
	updated, cmd := model.Update(tickMsg(now))
	model = updated.(Model)
	defer model.stopPhase()

	msg := commandMessageWithin(t, cmd)
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("transition command returned %T, want tea.BatchMsg", msg)
	}
	if len(batch) != 2 {
		t.Fatalf("transition command batch length = %d, want 2", len(batch))
	}
}

func TestModelStartsDownloadAndUploadTogetherInSimultaneousMode(t *testing.T) {
	t.Parallel()

	now := time.Unix(300, 0)
	var mu sync.Mutex
	var started []Phase

	recordProbe := func(phase Phase) ProbeFunc {
		return func(ctx context.Context, url string, total *atomic.Int64) {
			mu.Lock()
			started = append(started, phase)
			mu.Unlock()

			total.Add(125_000)
			<-ctx.Done()
		}
	}

	model := NewModel([]string{"https://oca.example.com/speedtest?token=test"}, ModelConfig{
		Duration:     time.Second,
		TickInterval: time.Millisecond,
		Now: func() time.Time {
			return now
		},
		DownloadProbe: recordProbe(downloadPhase),
		UploadProbe:   recordProbe(uploadPhase),
		Simultaneous:  true,
	})

	runCommandAsync(model.Init())
	waitForStarted(t, &mu, &started, 2)
	defer model.stopPhase()

	mu.Lock()
	gotStarted := append([]Phase(nil), started...)
	mu.Unlock()

	wantStarted := map[Phase]bool{
		downloadPhase: true,
		uploadPhase:   true,
	}
	for _, phase := range gotStarted {
		delete(wantStarted, phase)
	}
	if len(wantStarted) > 0 {
		t.Fatalf("started phases = %v, missing %v", gotStarted, wantStarted)
	}
}

func TestModelCompletesSimultaneousModeAfterSharedWindow(t *testing.T) {
	t.Parallel()

	now := time.Unix(400, 0)
	model := NewModel([]string{"https://oca.example.com/speedtest?token=test"}, ModelConfig{
		Duration:     time.Second,
		TickInterval: time.Millisecond,
		Now: func() time.Time {
			return now
		},
		DownloadProbe: func(ctx context.Context, url string, total *atomic.Int64) {
			total.Add(125_000)
			<-ctx.Done()
		},
		UploadProbe: func(ctx context.Context, url string, total *atomic.Int64) {
			total.Add(250_000)
			<-ctx.Done()
		},
		Simultaneous: true,
	})

	runCommandAsync(model.Init())
	waitUntil(t, func() bool {
		return model.download.bytes.Load() == 125_000 && model.upload.bytes.Load() == 250_000
	})

	now = now.Add(time.Second)
	updated, cmd := model.Update(tickMsg(now))
	model = updated.(Model)

	if !model.done {
		t.Fatal("model should be done after the shared measurement window")
	}

	if cmd == nil {
		t.Fatal("completion should return quit command")
	}
}

func TestSampleStatsIgnoresWarmupForPeak(t *testing.T) {
	t.Parallel()

	start := time.Unix(500, 0)
	var bytes atomic.Int64
	stats := PhaseStats{
		bytes: &bytes,
		start: start,
	}

	bytes.Add(25_000_000)
	sampleStats(&stats, start.Add(100*time.Millisecond))
	if stats.peak != 0 {
		t.Fatalf("peak after warmup sample = %.1f, want 0", stats.peak)
	}

	bytes.Add(125_000)
	sampleStats(&stats, start.Add(200*time.Millisecond))
	bytes.Add(125_000)
	sampleStats(&stats, start.Add(300*time.Millisecond))
	bytes.Add(125_000)
	sampleStats(&stats, start.Add(400*time.Millisecond))

	if stats.peak <= 0 {
		t.Fatal("peak should be recorded after warmup samples")
	}
	if stats.peak >= 20 {
		t.Fatalf("peak = %.1f Mbps, should ignore first buffered spike", stats.peak)
	}
}

func TestViewSeparatesDownloadAndUploadWithBlankLine(t *testing.T) {
	t.Parallel()

	model := NewModel([]string{"https://oca.example.com/speedtest?token=test"})
	model.download.speed = 76.3
	model.download.peak = 102
	model.download.speeds = []float64{20, 76.3}
	model.upload.speed = 84.5
	model.upload.peak = 162
	model.upload.speeds = []float64{30, 84.5}

	view := model.View()
	if !strings.Contains(view, "download") {
		t.Fatal("view should include download line")
	}

	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "download") {
			if i+2 >= len(lines) {
				t.Fatalf("view should include upload after download, got %q", view)
			}
			if strings.TrimSpace(lines[i+1]) != "" {
				t.Fatalf("line between download and upload should be visually blank, got %q", lines[i+1])
			}
			if !strings.Contains(lines[i+2], "upload") {
				t.Fatalf("upload should follow blank line, got %q", lines[i+2])
			}
			return
		}
	}

	t.Fatalf("download line not found in view %q", view)
}

func TestConfigFromArgsEnablesSimultaneousMode(t *testing.T) {
	t.Parallel()

	config, err := configFromArgs([]string{"--simultaneous"}, io.Discard)
	if err != nil {
		t.Fatalf("configFromArgs returned error: %v", err)
	}

	if !config.Simultaneous {
		t.Fatal("Simultaneous = false, want true")
	}
}

func TestConfigFromArgsDefaultsToSequentialMode(t *testing.T) {
	t.Parallel()

	config, err := configFromArgs(nil, io.Discard)
	if err != nil {
		t.Fatalf("configFromArgs returned error: %v", err)
	}

	if config.Simultaneous {
		t.Fatal("Simultaneous = true, want false")
	}
}

func TestConfigFromArgsShowsHelp(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	_, err := configFromArgs([]string{"--help"}, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}

	if !strings.Contains(output.String(), "-simultaneous") {
		t.Fatalf("help output = %q, want simultaneous option", output.String())
	}
}

func waitForStarted(t *testing.T, mu *sync.Mutex, started *[]Phase, want int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*started)
		mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	got := append([]Phase(nil), *started...)
	mu.Unlock()
	t.Fatalf("started phases = %v, want at least %d entries", got, want)
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before deadline")
}

func runCommandAsync(cmd tea.Cmd) {
	go func() {
		msg := cmd()
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			return
		}
		for _, batched := range batch {
			go batched()
		}
	}()
}

func commandMessageWithin(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()

	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()

	select {
	case msg := <-done:
		return msg
	case <-time.After(100 * time.Millisecond):
		t.Fatal("command did not return promptly")
		return nil
	}
}
