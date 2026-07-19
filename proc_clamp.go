package main

// clampFloat64 floors/ceils v into [min, max]. Used to keep physiologically
// implausible-but-still-in-contact spikes from reaching the ring buffer,
// baseline, or scorer — distinct from the wider contact/validity range used
// for quality classification, which always runs on the unclamped value.
func clampFloat64(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
