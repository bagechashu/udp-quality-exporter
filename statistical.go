package main

import (
	"math"
	"sort"
	"sync"
)

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	k := int(float64(len(cp)-1) * p / 100.0)
	if k < 0 {
		k = 0
	}
	if k >= len(cp) {
		k = len(cp) - 1
	}
	return cp[k]
}

// variance 方差（Variance）
func variance(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	m := mean(xs)
	var sum float64
	for _, x := range xs {
		d := x - m
		sum += d * d
	}
	return sum / float64(len(xs))
}

// stddev 标准差（Standard Deviation）
func stddev(xs []float64) float64 {
	return math.Sqrt(variance(xs))
}

// coefficientOfVariation 变异系数（Coefficient of Variation, CV）
func coefficientOfVariation(xs []float64) float64 {
	m := mean(xs)
	if m == 0 {
		return 0
	}
	return stddev(xs) / m
}

func countSyncMap(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
