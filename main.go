package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	// connections is the number of parallel downloads we use to saturate the
	// connection, the same as fast.com.
	connections = 5

	// duration is how long we measure the connection speed for.
	duration = 10 * time.Second

	// sparkWidth is the width, in cells, of the speed sparkline.
	sparkWidth = 20
)

const accentColor = "#2EF8BB"

var (
	speedStyle = lipgloss.NewStyle().Bold(true)
	unitStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sparkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(accentColor))
	peakStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	baseStyle  = lipgloss.NewStyle().Padding(1, 2)
)

const tickInterval = time.Second / 10

const warmupSamples = 3

type tickMsg time.Time

func tickCmd(t time.Time) tea.Msg {
	return tickMsg(t)
}

type Phase int

const (
	downloadPhase Phase = iota
	uploadPhase
)

type ProbeFunc func(context.Context, string, *atomic.Int64)

type ModelConfig struct {
	Duration      time.Duration
	TickInterval  time.Duration
	Now           func() time.Time
	DownloadProbe ProbeFunc
	UploadProbe   ProbeFunc
	Simultaneous  bool
}

type CLIConfig struct {
	Model       ModelConfig
	ShowVersion bool
}

type PhaseStats struct {
	bytes  *atomic.Int64
	start  time.Time
	total  int64
	base   int64
	baseAt time.Time
	speed  float64
	speeds []float64
	count  int
	peak   float64
}

type Model struct {
	targets []string
	config  ModelConfig

	download PhaseStats
	upload   PhaseStats

	ctx    context.Context
	cancel context.CancelFunc

	active   Phase
	done     bool
	quitting bool
}

func NewModel(targets []string, configs ...ModelConfig) Model {
	config := defaultModelConfig()
	if len(configs) > 0 {
		config = mergeModelConfig(config, configs[0])
	}

	m := Model{
		targets: targets,
		config:  config,
	}
	m.resetStats()
	m.resetContext()
	return m
}

func (m Model) Init() tea.Cmd {
	if m.config.Simultaneous {
		return tea.Batch(
			tea.Tick(m.config.TickInterval, tickCmd),
			m.startMeasurement(downloadPhase),
			m.startMeasurement(uploadPhase),
		)
	}

	return tea.Batch(
		tea.Tick(m.config.TickInterval, tickCmd),
		m.startMeasurement(downloadPhase),
	)
}

func defaultModelConfig() ModelConfig {
	return ModelConfig{
		Duration:      duration,
		TickInterval:  tickInterval,
		Now:           time.Now,
		DownloadProbe: download,
		UploadProbe:   upload,
	}
}

func mergeModelConfig(base, override ModelConfig) ModelConfig {
	if override.Duration > 0 {
		base.Duration = override.Duration
	}
	if override.TickInterval > 0 {
		base.TickInterval = override.TickInterval
	}
	if override.Now != nil {
		base.Now = override.Now
	}
	if override.DownloadProbe != nil {
		base.DownloadProbe = override.DownloadProbe
	}
	if override.UploadProbe != nil {
		base.UploadProbe = override.UploadProbe
	}
	if override.Simultaneous {
		base.Simultaneous = true
	}
	return base
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			m.stopPhase()
			return m, tea.Quit
		}

	case tickMsg:
		if m.done {
			return m, nil
		}

		now := time.Time(msg)
		m.sample(now)
		if m.phaseElapsed(now) >= m.config.Duration {
			m.stopPhase()
			if !m.config.Simultaneous && m.active == downloadPhase {
				m.active = uploadPhase
				m.resetContext()
				m.upload.start = now
				return m, tea.Batch(
					tea.Tick(m.config.TickInterval, tickCmd),
					m.startMeasurement(uploadPhase),
				)
			}

			m.done = true
			return m, tea.Quit
		}

		return m, tea.Tick(m.config.TickInterval, tickCmd)
	}

	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var s strings.Builder
	s.WriteString("download ")
	s.WriteString(m.renderStats(m.download))
	s.WriteString("\n\n")
	s.WriteString("upload   ")
	s.WriteString(m.renderStats(m.upload))

	style := baseStyle
	if m.done {
		style = style.PaddingBottom(2)
	}
	return style.Render(s.String())
}

func (m *Model) resetStats() {
	now := m.config.Now()
	m.download = PhaseStats{
		bytes: &atomic.Int64{},
		start: now,
	}
	m.upload = PhaseStats{
		bytes: &atomic.Int64{},
		start: now,
	}
	m.active = downloadPhase
}

func (m *Model) resetContext() {
	m.ctx, m.cancel = context.WithCancel(context.Background())
}

func (m *Model) sample(now time.Time) {
	if m.config.Simultaneous {
		sampleStats(&m.download, now)
		sampleStats(&m.upload, now)
		return
	}

	if m.active == downloadPhase {
		sampleStats(&m.download, now)
		return
	}
	sampleStats(&m.upload, now)
}

func sampleStats(stats *PhaseStats, now time.Time) {
	total := stats.bytes.Load()
	stats.count++
	stats.total = total

	if stats.count <= warmupSamples {
		stats.speed = mbps(total, now.Sub(stats.start))
		if stats.count == warmupSamples {
			stats.base = total
			stats.baseAt = now
		}
		stats.speeds = append(stats.speeds, stats.speed)
		return
	}

	bytes := total - stats.base
	if bytes < 0 {
		bytes = 0
	}

	stats.speed = mbps(bytes, now.Sub(stats.baseAt))
	stats.speeds = append(stats.speeds, stats.speed)
	if stats.speed > stats.peak {
		stats.peak = stats.speed
	}
}

func (m Model) phaseElapsed(now time.Time) time.Duration {
	if m.config.Simultaneous || m.active == downloadPhase {
		return now.Sub(m.download.start)
	}
	return now.Sub(m.upload.start)
}

func (m *Model) stopPhase() {
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Model) startMeasurement(phase Phase) tea.Cmd {
	probe := m.config.DownloadProbe
	bytes := m.download.bytes
	if phase == uploadPhase {
		probe = m.config.UploadProbe
		bytes = m.upload.bytes
	}
	work := m.measurementWork(phase)
	ctx := m.ctx

	return func() tea.Msg {
		var wg sync.WaitGroup
		for _, url := range work {
			wg.Add(1)
			go func() {
				defer wg.Done()
				probe(ctx, url, bytes)
			}()
		}
		wg.Wait()
		return nil
	}
}

func (m Model) measurementTargets(phase Phase) []string {
	return append([]string(nil), m.targets...)
}

func (m Model) measurementWork(phase Phase) []string {
	targets := m.measurementTargets(phase)
	if phase != uploadPhase || len(targets) == 0 || len(targets) >= uploadConnections {
		return targets
	}

	work := make([]string, uploadConnections)
	for i := range work {
		work[i] = targets[i%len(targets)]
	}
	return work
}

func (m Model) renderStats(stats PhaseStats) string {
	// Cap each readout at 999.9 and switch to Gbps beyond that, keeping a fixed
	// width so the unit, sparkline, and peak never shift horizontally.
	speed, unit := scale(stats.speed)
	var s strings.Builder
	s.WriteString(speedStyle.Render(fmt.Sprintf("%5.1f", speed)))
	s.WriteString(unitStyle.Render(" " + unit))
	s.WriteString(" ")
	s.WriteString(sparkStyle.Render(sparkline(stats.speeds, stats.peak, sparkWidth)))
	if stats.peak > 0 {
		peak, peakUnit := scale(stats.peak)
		label := fmt.Sprintf("  peak %.0f", peak)
		// Only label the peak's unit when it differs from the live reading's.
		if peakUnit != unit {
			label += " " + peakUnit
		}
		s.WriteString(peakStyle.Render(label))
	}
	return s.String()
}

// mbps converts a number of bytes transferred over a duration into megabits per
// second, the unit fast.com reports.
func mbps(bytes int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) * 8 / d.Seconds() / 1e6
}

// scale converts a speed in Mbps to its display magnitude and unit, switching to
// Gbps once it would read past 999.9 Mbps so the value never exceeds "999.9".
func scale(speed float64) (float64, string) {
	if speed >= 999.95 {
		return speed / 1000, "Gbps"
	}
	return speed, "Mbps"
}

func main() {
	config, err := configFromArgs(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	versionInfo := currentVersionInfo()
	if config.ShowVersion {
		if _, err := fmt.Fprintln(os.Stdout, versionInfo.cliString()); err != nil {
			log.Fatal(err)
		}
		return
	}

	var checker *updateChecker
	var cachedUpdateState updateState
	var refreshedState <-chan updateState
	if c := newUpdateChecker(versionInfo); c.enabled() {
		checker = c
		cachedUpdateState, refreshedState = c.prepare()
	}

	urls, err := targets(connections)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) {
			fmt.Fprintln(os.Stderr, "No internet connection.")
			os.Exit(1)
		}
		log.Fatal(err)
	}

	finalModel, err := tea.NewProgram(NewModel(urls, config.Model)).Run()
	if err != nil {
		log.Fatal(err)
	}
	model, ok := finalModel.(Model)
	if checker == nil || (ok && model.quitting) {
		return
	}

	cachedUpdateState = checker.resolveState(cachedUpdateState, refreshedState)
	notice, ok := checker.notice(cachedUpdateState)
	if !ok {
		return
	}
	if _, err := fmt.Fprintln(os.Stderr, notice); err != nil {
		return
	}
	_ = checker.markNotified(cachedUpdateState)
}

func configFromArgs(args []string, output io.Writer) (CLIConfig, error) {
	flags := flag.NewFlagSet("fast", flag.ContinueOnError)
	flags.SetOutput(output)
	simultaneous := flags.Bool("simultaneous", false, "measure download and upload at the same time")
	showVersion := flags.Bool("version", false, "print version information")
	if err := flags.Parse(args); err != nil {
		return CLIConfig{}, err
	}
	return CLIConfig{
		Model:       ModelConfig{Simultaneous: *simultaneous},
		ShowVersion: *showVersion,
	}, nil
}
