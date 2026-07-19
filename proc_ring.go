package main

// ringBuf is a fixed-capacity circular buffer of float64 values.
// Unlike the per-server versions (fixed array size), this uses a slice so different
// processors can have different capacities (GSR=600, HR=240, RR=1500).
type ringBuf struct {
	data []float64
	head int // next write index
	fill int // valid entries (capped at size)
	size int
}

func newRingBuf(size int) ringBuf {
	return ringBuf{data: make([]float64, size), size: size}
}

func (r *ringBuf) push(v float64) {
	r.data[r.head] = v
	r.head = (r.head + 1) % r.size
	if r.fill < r.size {
		r.fill++
	}
}

// mean returns the arithmetic mean of the most-recent n samples (newest = index 0).
func (r *ringBuf) mean(n int) float64 {
	if r.fill == 0 || n <= 0 {
		return 0
	}
	if n > r.fill {
		n = r.fill
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += r.data[(r.head-1-i%r.size+r.size*2)%r.size]
	}
	return sum / float64(n)
}

// variance returns population variance over the most-recent n samples.
func (r *ringBuf) variance(n int) float64 {
	if n > r.fill {
		n = r.fill
	}
	if n < 2 {
		return 0
	}
	m := r.mean(n)
	vsum := 0.0
	for i := 0; i < n; i++ {
		d := r.data[(r.head-1-i%r.size+r.size*2)%r.size] - m
		vsum += d * d
	}
	return vsum / float64(n)
}

// sparklineLen is the number of most-recent samples kept for the TUI sparkline.
const sparklineLen = 100

// lastSparkline copies the last sparklineLen values into a fixed array, oldest first.
// Positions before available data are zero-padded.
// Used to populate ChannelSnapshot.Sparkline without allocating.
func (r *ringBuf) lastSparkline() [sparklineLen]float64 {
	var out [sparklineLen]float64
	use := r.fill
	if use > sparklineLen {
		use = sparklineLen
	}
	start := sparklineLen - use
	for i := 0; i < use; i++ {
		// i=0 → oldest of the 'use' most-recent values; i=use-1 → newest
		backSteps := use - 1 - i
		pos := ((r.head - 1 - backSteps) % r.size + r.size) % r.size
		out[start+i] = r.data[pos]
	}
	return out
}
