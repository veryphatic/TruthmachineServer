package main

// hysteresisAbove reports whether value should be treated as "above" a boundary,
// using separate enter/exit thresholds so a signal lingering near a single line
// doesn't flap the verdict. currentlyAbove is the previous verdict.
func hysteresisAbove(value, enterAbove, exitBelow float64, currentlyAbove bool) bool {
	if currentlyAbove {
		return value > exitBelow
	}
	return value > enterAbove
}
