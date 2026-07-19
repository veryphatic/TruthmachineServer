# CHANGELOG

## 16/7/26

### GSR contact-quality hysteresis
- Added a reusable `hysteresisAbove(value, enterAbove, exitBelow, currentlyAbove)` helper (`proc_hysteresis.go`), intended to be picked up by HR/RR later without a refactor.
- `GSRProcessor` now gates contact loss with a dead-band instead of a single hard line: enters `OUT_OF_RANGE` at `MaxContactADC` (4060, unchanged) but only recovers to `OK` once raw drops below the new `MaxContactRecoverADC` (4050). Fixes rapid `OK`/`OUT_OF_RANGE` chatter (observed 5+ flips in ~9s) when raw ADC hovered right on the old single threshold during a finger-crease electrode placement.
- The live periodic "drone" cue (`GSRProcessor.Snapshot`) now scores off `ring.mean(DisplaySmoothN)` (8 samples, ~0.8s) instead of the single last raw sample, so brief noise/artifacts no longer jerk the ambient cue. The operator TUI's instantaneous `DisplayValue` is unaffected, as is the `/interrogate` window-averaged score path.
- New config fields: `gsr.max_contact_recover_adc` (4050) and `gsr.display_smooth_n` (8) in `GSRCfg` / `truthmachine.json`.

### Operator display wording
- The operator TUI now shows `CHECK CONTACT` instead of the raw `OUT_OF_RANGE` state name for GSR (`tui_channels.go`'s `qualityDisplayLabel`), so a contact-loss artifact reads as an actionable instruction rather than an alarm. Internal/logged state names (`quality_change` events, JSONL) are unchanged.

### HR warmup fix
- Replaced the indefinite `warmupRequired` guard (which waited for an explicit `bpm==0` reset sample after connect before trusting any reading) with a fixed time window (`warmupUntil`, default 2s via new `hr.warmup_s` config field). The old guard never cleared against this sensor/firmware, which never emits a `0` — so HR quality was getting stuck at `OUT_OF_RANGE` (`display=0`) for entire sessions even while valid BPM data streamed in the whole time.

### Replay tooling fixes (`net_replay.go`)
- `loadRecords` now captures the `"channel"` field from each JSONL line. `replayGSR` and `replayHR` skip records belonging to other channels, fixing cross-channel contamination when replaying a unified v2 log (e.g. HR/RR samples were previously being ingested into the GSR processor as if they were ADC readings, and a stray `calibrated` event from one channel could be applied to the wrong processor).
- `replayHR` was reading a nonexistent `"bpm"` JSON field; v2 logs use `"raw"` for every channel's sample uniformly (per `logger.go`). Every replayed HR sample was silently coming through as `0`. Now reads `"raw"` first, falling back to `"bpm"` for old single-channel-server logs.

### Testing
- Added `verify_hr_warmup_test.go`, a real-time JSONL replay harness that drives `HRProcessor` directly (bypassing the TCP listener/TUI) and asserts the session ends in `OK` quality with a plausible BPM — verifies the warmup fix against real captured hardware data.
