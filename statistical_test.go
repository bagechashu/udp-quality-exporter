package main

import (
	"math"
	"sync"
	"testing"
)

const epsilon = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}

func TestAbs(t *testing.T) {
	tests := []struct {
		input, want float64
	}{
		{-5, 5},
		{0, 0},
		{3.2, 3.2},
	}
	for _, tt := range tests {
		if got := abs(tt.input); !almostEqual(got, tt.want) {
			t.Errorf("abs(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestMean(t *testing.T) {
	tests := []struct {
		input []float64
		want  float64
	}{
		{[]float64{1, 2, 3}, 2},
		{[]float64{}, 0},
		{[]float64{5}, 5},
		{[]float64{5, 100, 20}, 41.666666666666664},
	}
	for _, tt := range tests {
		if got := mean(tt.input); !almostEqual(got, tt.want) {
			t.Errorf("mean(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestPercentile(t *testing.T) {
	tests := []struct {
		input []float64
		p     float64
		want  float64
	}{
		{[]float64{1, 2, 3, 4, 5}, 50, 3},
		{[]float64{5, 1, 2, 3, 4}, 0, 1},
		{[]float64{5, 1, 2, 3, 4}, 100, 5},
		{[]float64{}, 50, 0},
	}
	for _, tt := range tests {
		if got := percentile(tt.input, tt.p); !almostEqual(got, tt.want) {
			t.Errorf("percentile(%v, %v) = %v, want %v", tt.input, tt.p, got, tt.want)
		}
	}
}

func TestVarianceAndStddev(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5}
	wantVar := 2.0
	wantStd := math.Sqrt(wantVar)

	if got := variance(data); !almostEqual(got, wantVar) {
		t.Errorf("variance(%v) = %v, want %v", data, got, wantVar)
	}

	if got := stddev(data); !almostEqual(got, wantStd) {
		t.Errorf("stddev(%v) = %v, want %v", data, got, wantStd)
	}
}

func TestCoefficientOfVariation(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5}
	wantCV := stddev(data) / mean(data)
	if got := coefficientOfVariation(data); !almostEqual(got, wantCV) {
		t.Errorf("coefficientOfVariation(%v) = %v, want %v", data, got, wantCV)
	}

	if got := coefficientOfVariation([]float64{0, 0, 0}); !almostEqual(got, 0) {
		t.Errorf("coefficientOfVariation zeros = %v, want 0", got)
	}
}

func TestMad(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5}
	wantMad := (abs(1-3) + abs(2-3) + abs(3-3) + abs(4-3) + abs(5-3)) / 5
	if got := mad(data); !almostEqual(got, wantMad) {
		t.Errorf("mad(%v) = %v, want %v", data, got, wantMad)
	}

	if got := mad([]float64{}); !almostEqual(got, 0) {
		t.Errorf("mad(empty) = %v, want 0", got)
	}
}

func TestCountSyncMap(t *testing.T) {
	var m sync.Map
	m.Store("a", 1)
	m.Store("b", 2)
	m.Store("c", 3)

	if got := countSyncMap(&m); got != 3 {
		t.Errorf("countSyncMap = %v, want 3", got)
	}

	var empty sync.Map
	if got := countSyncMap(&empty); got != 0 {
		t.Errorf("countSyncMap(empty) = %v, want 0", got)
	}
}
