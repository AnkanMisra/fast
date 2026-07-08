package main

import "testing"

func TestSparklineSkipsLeadingZeroValues(t *testing.T) {
	t.Parallel()

	got := sparkline([]float64{0, 0, 0, 80, 80, 80}, 80, 6)
	if []rune(got)[0] == ' ' {
		t.Fatalf("sparkline = %q, should not start with blank cells after leading zero samples", got)
	}
}

func TestSparklineClampsValuesAbovePeak(t *testing.T) {
	t.Parallel()

	got := sparkline([]float64{200, 0, 10}, 10, 4)
	if len([]rune(got)) != 4 {
		t.Fatalf("sparkline length = %d, want 4", len([]rune(got)))
	}
}
