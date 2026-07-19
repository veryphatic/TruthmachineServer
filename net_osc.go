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
// Inbound:  /calibrate, /interrogate, /reset.
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
	if n > 100 {
		n = 100
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
	if now.Sub(b.lastGSR) >= time.Second && (gsr.Quality == QualityOK || gsr.IsEstimated) {
		b.send(fmt.Sprintf("/cue/g%d/start", oscLerp(0, 100, gsr.L, 1, 20)))
		b.lastGSR = now
	}

	rr := snaps[ChannelRR]
	if now.Sub(b.lastRR) >= time.Second && (rr.Quality == QualityOK || rr.IsStub) {
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

// oscLerp maps val from [srcMin,srcMax] → [dstMin,dstMax], clamped.
// Matches the formula used in the legacy server.
func oscLerp(srcMin, srcMax, val float64, dstMin, dstMax int) int {
	ratio := (math.Min(srcMax, math.Max(srcMin, val)) - srcMin) / (srcMax - srcMin)
	return int(ratio*float64(dstMax-dstMin)) + dstMin
}
