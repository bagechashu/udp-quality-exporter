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

// mean 均值（Mean） with Kahan summation for improved numerical stability
func mean(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum, c float64
	for _, x := range xs {
		y := x - c
		t := sum + y
		c = (t - sum) - y
		sum = t
	}
	return sum / float64(len(xs))
}

// percentile 百分位数（Percentile）
// Uses the Nearest Rank method with linear interpolation.
func percentile(xs []float64, p float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	pos := (p / 100) * float64(len(cp)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return cp[lower]
	}
	return cp[lower] + (cp[upper]-cp[lower])*(pos-float64(lower))
}

// variance 方差（Variance）
func variance(xs []float64) float64 {
	// Need at least two samples to compute variance
	if len(xs) < 2 {
		return 0
	}
	m := mean(xs)
	var sum float64
	for _, x := range xs {
		d := x - m
		sum += d * d
	}
	// 使用样本方差（n-1）而非总体方差（n）
	return sum / float64(len(xs)-1)
}

// stddev 标准差（Standard Deviation）
func stddev(xs []float64) float64 {
	return math.Sqrt(variance(xs))
}

// coefficientOfVariation 变异系数（Coefficient of Variation, CV）
// coefficientOfVariation returns the Coefficient of Variation (CV = stddev / mean)
// Safe for small or unstable samples.
func coefficientOfVariation(xs []float64) float64 {
	if len(xs) < 5 {
		return 0
	}
	m := mean(xs)
	if m < 1e-6 { // avoid division by near-zero mean
		return 0
	}
	return stddev(xs) / m
}

// Mean Absolute Deviation（平均绝对偏差）
func meanAD(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := mean(xs)
	var sum float64
	for _, x := range xs {
		sum += abs(x - m)
	}
	return sum / float64(len(xs))
}

// Median Absolute Deviation（中位数绝对偏差）
func medianAD(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	m := median(xs)
	devs := make([]float64, len(xs))
	for i, x := range xs {
		devs[i] = abs(x - m)
	}
	// Scale factor for consistency with standard deviation under normal distribution
	return 1.4826 * median(devs)
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 0 {
		return (cp[mid-1] + cp[mid]) / 2
	}
	return cp[mid]
}

func countSyncMap(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
