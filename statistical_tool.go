package main

import (
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

func countSyncMap(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
