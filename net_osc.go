package main

import (
	"fmt"
	"math"
	"net"
	"strconv"
	"time"

	"github.com/hypebeast/go-osc/osc"
)

// OSCBridge sends outbound OSC cues to QLab and receives inbound commands.
// Outbound: /cue/l{N}/start, /cue/bpm{NNN}/start, /cue/g{1-20}/start,
//
//	/cue/r{1-20}/start, /cue/p/start.
//
// Inbound:  /calibrate, /interrogate, /reset, /baseline, /sensitivity <k>,
//
//	/mute/gsr, /mute/hr, /mute/rr, /random_low, /manual_l <L>, /history.
type OSCBridge struct {
	host   string
	port   int
	log    *AppLogger
	events EventSink
	gsr    *GSRProcessor
	hr     *HRProcessor
	rr     *RRProcessor

	// throttle: prevent flooding QLab with every 100ms tick
	lastBPM time.Time
	lastGSR time.Time
	lastRR  time.Time
}

// newOSCBridge parses target ("host:port") and returns a ready bridge.
func newOSCBridge(target string, log *AppLogger, events EventSink, gsr *GSRProcessor, hr *HRProcessor, rr *RRProcessor) (*OSCBridge, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("osc_target %q: %w", target, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("osc_target port %q: %w", portStr, err)
	}
	return &OSCBridge{host: host, port: port, log: log, events: events, gsr: gsr, hr: hr, rr: rr}, nil
}

// startInbound binds listenAddr (e.g. ":8765") and dispatches inbound QLab commands.
// Returns an error if the port cannot be bound; otherwise runs in a background goroutine.
func (b *OSCBridge) startInbound(listenAddr string) error {
	d := osc.NewStandardDispatcher()

	add := func(addr string, fn func()) {
		d.AddMsgHandler(addr, func(msg *osc.Message) {
			b.log.LogOSC("in", msg.Address, nil)
			fn()
		})
	}

	// addFloat registers a handler for addresses that carry one numeric argument.
	// Malformed/missing arguments are logged and dropped rather than applied.
	addFloat := func(addr string, fn func(v float64)) {
		d.AddMsgHandler(addr, func(msg *osc.Message) {
			v, ok := firstFloatArg(msg)
			if !ok {
				b.log.Event(channelSystem, "osc_error", "dir", "in", "address", msg.Address, "err", "missing or invalid numeric argument")
				return
			}
			b.log.LogOSC("in", msg.Address, []any{v})
			fn(v)
		})
	}

	pushEvent := func(e ProcessorEvent) {
		e.T = time.Now()
		select {
		case b.events <- e:
		default: // drop if TUI event queue is full
		}
	}

	add("/calibrate", func() {
		b.gsr.StartCalibrate()
		b.hr.StartCalibrate()
		b.rr.StartCalibrate()
	})
	add("/interrogate", func() {
		// Must land before StartInterrogate() so the TUI's score-aggregation round
		// is armed before any ScoredMsg can arrive — without this, ScoredMsg is
		// silently dropped (pendingActive stays false) and no history row or
		// outbound /cue/l{N}/start is ever produced for an OSC-driven interrogation.
		select {
		case b.events <- ProcessorEvent{Kind: ProcEventInterrogateStart, T: time.Now()}:
		default:
		}
		b.gsr.StartInterrogate()
		b.hr.StartInterrogate()
		b.rr.StartInterrogate()
	})
	add("/reset", func() {
		b.gsr.FreshenBaseline()
		b.hr.FreshenBaseline()
		b.rr.FreshenBaseline()
		select {
		case b.events <- ProcessorEvent{Kind: ProcEventFreshen, T: time.Now()}:
		default: // drop if TUI event queue is full
		}
	})
	add("/baseline", func() {
		b.gsr.FreshenBaseline()
		b.hr.FreshenBaseline()
		b.rr.FreshenBaseline()
		pushEvent(ProcessorEvent{Kind: ProcEventBaselineRefresh})
	})
	addFloat("/sensitivity", func(k float64) {
		if k < 0.1 {
			k = 0.1
		}
		if k > 5.0 {
			k = 5.0
		}
		b.gsr.SetSensitivity(k)
		b.hr.SetSensitivity(k)
		b.rr.SetSensitivity(k)
		pushEvent(ProcessorEvent{Kind: ProcEventSensitivity, Value: k})
	})
	add("/mute/gsr", func() { pushEvent(ProcessorEvent{Kind: ProcEventMuteToggle, Channel: ChannelGSR}) })
	add("/mute/hr", func() { pushEvent(ProcessorEvent{Kind: ProcEventMuteToggle, Channel: ChannelHR}) })
	add("/mute/rr", func() { pushEvent(ProcessorEvent{Kind: ProcEventMuteToggle, Channel: ChannelRR}) })
	add("/random_low", func() {
		pushEvent(ProcessorEvent{Kind: ProcEventRandomLow})
	})
	addFloat("/manual_l", func(L float64) {
		pushEvent(ProcessorEvent{Kind: ProcEventManualL, Value: L})
	})
	add("/history", func() {
		pushEvent(ProcessorEvent{Kind: ProcEventShowHistory})
	})

	server := &osc.Server{Addr: listenAddr, Dispatcher: d}

	// Start server in background; detect early bind failure via short timeout.
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		return fmt.Errorf("osc inbound %s: %w", listenAddr, err)
	case <-time.After(60 * time.Millisecond):
		b.log.Event(channelSystem, "osc_listening", "addr", listenAddr)
		return nil
	}
}

// startPulse fires /cue/p/start at the current HR BPM rate. Runs until stop is closed.
func (b *OSCBridge) startPulse(hrProc *HRProcessor, stop <-chan struct{}) {
	const defaultBPM = 60.0
	bpm := defaultBPM
	ticker := time.NewTicker(bpmInterval(bpm))
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			b.send("/cue/p/start")
			snap := hrProc.Snapshot()
			newBPM := snap.DisplayValue
			if newBPM < 20 || newBPM > 240 {
				newBPM = defaultBPM
			}
			if newBPM != bpm {
				bpm = newBPM
				ticker.Reset(bpmInterval(bpm))
			}
		}
	}
}

func bpmInterval(bpm float64) time.Duration {
	ms := 60000.0 / bpm
	return time.Duration(ms) * time.Millisecond
}

// SendL sends /cue/l{N}/start to QLab after an interrogation completes.
// Called from the TUI finalizeScore and on manual-L / random-low injections.
func (b *OSCBridge) SendL(L float64) {
	n := int(math.Round(L))
	if n < 1 {
		n = 1
	}
	if n > 99 {
		n = 99
	}
	b.send(fmt.Sprintf("/cue/l%d/start", n))
}

// SendPeriodic sends throttled per-channel cues from the 100ms TUI tick.
// Each channel type sends at most once per second.
func (b *OSCBridge) SendPeriodic(snaps [3]ChannelSnapshot) {
	now := time.Now()

	hr := snaps[ChannelHR]
	if now.Sub(b.lastBPM) >= time.Second &&
		((hr.Quality != QualityDisconnected && hr.DisplayValue > 0) || hr.IsEstimated) {
		bpm := int(math.Round(hr.DisplayValue))
		b.send(fmt.Sprintf("/cue/bpm%03d/start", bpm))
		b.lastBPM = now
	}

	gsr := snaps[ChannelGSR]
	if now.Sub(b.lastGSR) >= 3*time.Second && (gsr.Quality == QualityOK || gsr.IsEstimated) {
		b.send(fmt.Sprintf("/cue/g%d/start", oscLerp(0, 100, gsr.L, 1, 20)))
		b.lastGSR = now
	}

	rr := snaps[ChannelRR]
	if now.Sub(b.lastRR) >= 3*time.Second && (rr.Quality == QualityOK || rr.IsStub || rr.IsEstimated) {
		b.send(fmt.Sprintf("/cue/r%d/start", oscLerp(0, 100, rr.L, 1, 20)))
		b.lastRR = now
	}
}

// send creates a UDP client, sends a no-arg OSC message, and logs it.
func (b *OSCBridge) send(address string) {
	client := osc.NewClient(b.host, b.port)
	msg := osc.NewMessage(address)
	if err := client.Send(msg); err != nil {
		b.log.Event(channelSystem, "osc_error", "dir", "out", "address", address, "err", err.Error())
		return
	}
	b.log.LogOSC("out", address, nil)
}

// firstFloatArg extracts the first OSC argument as a float64, accepting
// whichever numeric (or numeric-string) type the sender tagged it as —
// QLab and other OSC senders vary between float32/int32/string for typed args.
func firstFloatArg(msg *osc.Message) (float64, bool) {
	if len(msg.Arguments) == 0 {
		return 0, false
	}
	switch v := msg.Arguments[0].(type) {
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

// oscLerp maps val from [srcMin,srcMax] → [dstMin,dstMax], clamped.
// Matches the formula used in the legacy server.
func oscLerp(srcMin, srcMax, val float64, dstMin, dstMax int) int {
	ratio := (math.Min(srcMax, math.Max(srcMin, val)) - srcMin) / (srcMax - srcMin)
	return int(ratio*float64(dstMax-dstMin)) + dstMin
}
