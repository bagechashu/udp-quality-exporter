package main

import (
	"sync"
	"time"
)

// ---------------------- 基础结构 ----------------------

type sample struct {
	ts time.Time
}

// 环形缓冲区实现
type ringBuffer struct {
	data  []sample
	start int
	size  int
	cap   int
}

func newRingBuffer(cap int) *ringBuffer {
	if cap <= 0 {
		cap = 16
	}
	return &ringBuffer{data: make([]sample, cap), cap: cap}
}

func (r *ringBuffer) append(s sample) {
	if r.size < r.cap {
		r.data[(r.start+r.size)%r.cap] = s
		r.size++
	} else {
		r.data[r.start] = s
		r.start = (r.start + 1) % r.cap
	}
}

func (r *ringBuffer) slice() []sample {
	out := make([]sample, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.data[(r.start+i)%r.cap]
	}
	return out
}

func (r *ringBuffer) trimBeforeFast(cutoff time.Time) {
	// 移动 start，减少 size
	n := r.size
	idx := 0
	for idx < n && r.data[(r.start+idx)%r.cap].ts.Before(cutoff) {
		idx++
	}
	if idx > 0 {
		r.start = (r.start + idx) % r.cap
		r.size -= idx
	}
}

func (r *ringBuffer) trimBefore(cutoff time.Time) {
	for r.size > 0 && r.data[r.start].ts.Before(cutoff) {
		r.start = (r.start + 1) % r.cap
		r.size--
	}
}

// 滑动窗口结构
type slidingWindow struct {
	mu     sync.Mutex
	buffer *ringBuffer
	window time.Duration
}

func newSlidingWindow(window time.Duration, cap int) *slidingWindow {
	if cap <= 0 {
		cap = 16
	}
	return &slidingWindow{buffer: newRingBuffer(cap), window: window}
}

func (w *slidingWindow) add(s sample) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer.append(s)
	w.buffer.trimBeforeFast(s.ts.Add(-w.window))
}

// last 返回窗口中最后一个样本的时间戳，如果没有样本返回零时间并返回 false
func (w *slidingWindow) last() (time.Time, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffer.size == 0 {
		return time.Time{}, false
	}
	idx := (w.buffer.start + w.buffer.size - 1) % w.buffer.cap
	return w.buffer.data[idx].ts, true
}

func (w *slidingWindow) metrics() (jitter float64, avgInterval float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	samples := w.buffer.slice()
	n := len(samples)
	if n < 2 {
		return 0, 0
	}
	intervals := make([]float64, 0, n-1)
	for i := 1; i < n; i++ {
		iv := samples[i].ts.Sub(samples[i-1].ts).Seconds() * 1000 // ms
		intervals = append(intervals, iv)
	}
	var sum float64
	for _, iv := range intervals {
		sum += iv
	}
	avgInterval = sum / float64(len(intervals))
	var jitterSum float64
	for i := 1; i < len(intervals); i++ {
		d := intervals[i] - intervals[i-1]
		if d < 0 {
			d = -d
		}
		jitterSum += d
	}
	if len(intervals) > 1 {
		jitter = jitterSum / float64(len(intervals)-1)
	}
	return
}

func (w *slidingWindow) metricsFast() (jitter float64, avgInterval float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffer.size < 3 {
		return 0, 0
	}
	var sum, jitterSum float64
	var prev, prevIv float64
	for i := 0; i < w.buffer.size; i++ {
		idx := (w.buffer.start + i) % w.buffer.cap
		if i == 0 {
			prev = float64(w.buffer.data[idx].ts.UnixNano()) / 1e6
			continue
		}
		cur := float64(w.buffer.data[idx].ts.UnixNano()) / 1e6
		iv := cur - prev
		sum += iv
		if i > 1 {
			jitterSum += abs(iv - prevIv)
		}
		prevIv = iv
		prev = cur
	}
	n := float64(w.buffer.size - 1)
	avgInterval = sum / n
	if n > 1 {
		jitter = jitterSum / (n - 1)
	}
	return
}
