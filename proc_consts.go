package main

// Timing accessors (calDuration, intDuration, cooldownDur, staleAfter,
// disconnectAfter) are hot-reloadable atomics defined in config.go.
// Per-channel signal-quality consts live in their respective proc_*.go files.
