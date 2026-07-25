# truthmachine-server-v2

Unified sensor server and operator TUI for the TruthMachine theatrical polygraph. Receives GSR, heart rate, and respiratory rate from an ESP32 over TCP, scores each channel in real time, and drives QLab via OSC.

---

## Running

```sh
go run .                              # run with default config (truthmachine.json auto-created)
go run . --config /path/to/cfg.json   # alternate config location

# Replay a recorded session without hardware:
go run . --replay-gsr ../gsr-server/truthmachine-2026-06-30.log \
          --replay-hr  ../heart-rate-server/heartrate-2026-06-30.log

go run . --no-stub-rr                 # disable synthetic RR if hardware is connected
```

On first run, `truthmachine.json` is written to the current directory with all defaults.

---

## Configuration — `truthmachine.json`

Fields marked **hot** can be edited while the server is running; changes take effect within 2 seconds without restarting. Fields marked **restart** require a server restart.

### Network

| Field | Default | Reload | Description |
|---|---|---|---|
| `listen_addr` | `":5000"` | restart | TCP address the server listens on for ESP32 sensor data. All three channels (GSR, HR, RR) share this single connection. |
| `osc_target` | `"192.168.1.100:53000"` | restart | QLab host and UDP port for outbound OSC cues. Set this to the Mac running QLab. |
| `osc_listen_addr` | `":8765"` | restart | UDP port for inbound OSC commands from QLab. Must match the OSC output destination configured in QLab. |

### Timing

| Field | Default | Reload | Description |
|---|---|---|---|
| `timing.calibrate_s` | `15` | **hot** | Duration of the calibration window in seconds. During calibration the EWMA baseline μ/σ is updated freely. |
| `timing.interrogate_s` | `8` | **hot** | Duration of the interrogation (scoring) window in seconds. The z-score is computed over this window. |
| `timing.cooldown_s` | `15` | **hot** | Cooldown period after interrogation before returning to IDLE. Baseline updates are paused during cooldown. |
| `timing.stale_after_s` | `2` | **hot** | Seconds without a sample before a channel is marked STALE. |
| `timing.disconnect_after_s` | `10` | **hot** | Seconds without a sample before a channel is marked DISCONNECTED and excluded from scoring. |

### Scoring

The combined lie-likelihood score is a weighted average of all connected, non-muted channels:
`L_combined = Σ(L_i × w_i) / Σ(w_i)`.
Disconnected or muted channels are excluded; the denominator renormalises automatically.

Per-channel score: `L = 100 × tanh(k × z / 2)` where `z` is the z-score and `k` is sensitivity.

| Field | Default | Reload | Description |
|---|---|---|---|
| `scoring.default_sensitivity` | `1.0` | **hot** | Starting value for the sensitivity multiplier `k`. Adjusted live with `[s]`. Range 0.1–5.0. Higher values amplify small deviations. |
| `scoring.weight_gsr` | `0.5` | **hot** | Weight of the GSR channel in the combined L score. |
| `scoring.weight_hr` | `0.3` | **hot** | Weight of the heart rate channel. |
| `scoring.weight_rr` | `0.2` | **hot** | Weight of the respiratory rate channel. |

### GSR channel

| Field | Default | Reload | Description |
|---|---|---|---|
| `gsr.baseline_alpha` | `0.005` | restart | EWMA learning rate for GSR baseline (μ, σ). Lower = slower adaptation. |
| `gsr.sigma_floor` | `10.0` | restart | Minimum σ to prevent division by zero at very stable readings. |
| `gsr.min_contact_adc` | `50` | restart | ADC value below which the skin contact is considered lost (OUT_OF_RANGE). |
| `gsr.max_contact_adc` | `4060` | restart | ADC value above which the contact is considered open-circuit (OUT_OF_RANGE). Valid range on ESP32 12-bit ADC is 0–4095. |

### Heart rate channel

| Field | Default | Reload | Description |
|---|---|---|---|
| `hr.baseline_alpha` | `0.005` | restart | EWMA learning rate for HR baseline. |
| `hr.sigma_floor` | `3.0` | restart | Minimum σ for HR in BPM. |
| `hr.min_valid_bpm` | `30` | restart | BPM below which readings are rejected as OUT_OF_RANGE. |
| `hr.max_valid_bpm` | `250` | restart | BPM above which readings are rejected as OUT_OF_RANGE. |

### Respiratory rate channel

| Field | Default | Reload | Description |
|---|---|---|---|
| `rr.baseline_alpha` | `0.01` | restart | EWMA learning rate for RR baseline. Slightly higher than GSR/HR because breath rate drifts more slowly. |
| `rr.sigma_floor` | `1.5` | restart | Minimum σ for RR in breaths per minute. |

### Stub (synthetic RR)

When no real RR hardware is connected, the server generates a synthetic sine-wave respiratory signal at 25 Hz. It disables itself automatically when real `rr,...` lines arrive on the TCP connection and re-enables after 5 seconds of silence.

| Field | Default | Reload | Description |
|---|---|---|---|
| `stub.breathe_bpm` | `14` | restart | Synthetic breathing rate in breaths per minute. 14 BPM = ~107 samples per cycle at 25 Hz. |

### Mute defaults

Channel mute state is written back to this section whenever the operator toggles `[u]` in the TUI, so the last mute state is restored on the next startup.

| Field | Default | Reload | Description |
|---|---|---|---|
| `mute.gsr` | `false` | restart | Start with GSR muted (excluded from combined L). |
| `mute.hr` | `false` | restart | Start with HR muted. |
| `mute.rr` | `false` | restart | Start with RR muted. |

---

## OSC API

### Inbound — QLab → TruthMachine (default port `8765`)

Configure QLab's OSC output destination to `<server-ip>:8765`.

| Address | Arguments | Effect |
|---|---|---|
| `/calibrate` | none | Starts a calibration window on all channels. Duration: `timing.calibrate_s`. Baseline μ/σ updates freely throughout; a CALIBRATED marker row is added to the interrogation history when it completes. |
| `/interrogate` | none | Starts a scoring window on all channels. Duration: `timing.interrogate_s`. Fires `/cue/l{N}/start` when the window closes. Followed by a cooldown of `timing.cooldown_s` before returning to IDLE. |
| `/reset` | none | Soft reset: freshens the baseline on all channels (re-enables EWMA updates, exits cooldown early). Does **not** clear scoring history or signal quality state. Use this mid-show if a performer's skin conductance drifts significantly. |

### Outbound — TruthMachine → QLab (configured via `osc_target`)

Configure QLab's OSC input to match the port in `osc_target`.

| Address | When fired | Description |
|---|---|---|
| `/cue/l{N}/start` | After each interrogation, manual-L injection (`[m]`), and random-low (`[r]`) | Lie likelihood score. N is 1–100. This is the primary show-control cue. |
| `/cue/bpm{NNN}/start` | At most once per second while HR is connected | Current heart rate as a three-digit zero-padded integer (e.g. `/cue/bpm072/start`). Requires HR channel to be connected and in range. |
| `/cue/g{N}/start` | At most once per second while GSR is calibrated | GSR deception level lane. N is 1–20, mapped from the GSR channel's L value (0→1, 100→20). |
| `/cue/r{N}/start` | At most once per second while RR is active (hardware or stub) | Respiratory deception level lane. N is 1–20, mapped from the RR channel's L value. |
| `/cue/p/start` | Continuously at current BPM rate | Heartbeat pulse. Interval updates when a new HR reading arrives. Defaults to 60 BPM when HR is disconnected. |

---

## Operator hotkeys

| Key | Action |
|---|---|
| `c` | Calibrate all channels |
| `i` | Interrogate — opens scoring window, fires L cue to QLab when done |
| `s` | Set sensitivity multiplier (0.1–5.0) |
| `b` | Freshen baseline (same as OSC `/reset`) |
| `m` | Inject a manual L value (0–100) — also fires `/cue/l{N}/start` to QLab |
| `r` | Inject a random low score (1–50) — also fires `/cue/l{N}/start` to QLab |
| `u` then `g`/`h`/`r` | Toggle mute for GSR / Heart Rate / Resp Rate; mute state saved to config |
| `l` | Toggle full-screen log view (shows all OSC in/out by default) |
| `?` | Help screen |
| `q` `q` | Quit (press twice within 2.5s) |

---

## Testing OSC without QLab

The `go-osc-sender-receiver` tool in the repo root lets you manually fire inbound commands and watch outbound cues in a terminal.

**Port mapping for local dev:**

| What | Default config value | What to set on the tool |
|---|---|---|
| Server receives commands on | `osc_listen_addr: ":8765"` | `-send localhost:8765` |
| Server sends cues to | `osc_target: "localhost:53000"` | `-listen :53000` |

```sh
cd go-osc-sender-receiver
go run . -send localhost:8765 -listen :53000
```

Then type OSC commands at the prompt:

```
/calibrate
/interrogate
/reset
```

You'll see outbound cues from the server printed as they arrive:

```
[15:04:23.441] RECV  /cue/p/start
[15:04:31.002] RECV  /cue/l73/start
[15:04:31.003] RECV  /cue/bpm072/start
```

For a show setup where QLab runs on a different machine, change `osc_target` in `truthmachine.json` to point at the QLab Mac (e.g. `"192.168.1.100:53000"`) and ensure QLab's OSC output destination is set to `<server-ip>:8765`.

---

## Wire protocol (ESP32 → server)

The ESP32 sends newline-delimited text to `:5000`. All three channels share one TCP connection:

```
gsr,{boardMs},{raw_adc}
hr,{boardMs},{bpm}
rr,{boardMs},{raw_adc}
hb,{boardMs},0
```

`boardMs` is milliseconds since the ESP32 booted. The server uses it for ordering and stuck-detection. Heartbeat lines (`hb`) are accepted but need no processing.
