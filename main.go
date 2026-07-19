package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	replayGSR := flag.String("replay-gsr", "", "path to a GSR JSONL log for replay (no hardware needed)")
	replayHR := flag.String("replay-hr", "", "path to an HR JSONL log for replay")
	replayQLab := flag.String("replay-qlab", "", "path to a v2 JSONL log with type:\"osc\" records")
	noStub := flag.Bool("no-stub-rr", false, "disable the synthetic RR stub")
	cfgPath := flag.String("config", "truthmachine.json", "path to JSON config file")
	flag.Parse()

	// Write default config if the file doesn't exist yet, then load it.
	if err := writeDefaultConfig(*cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "warn: could not write default config: %v\n", err)
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warn: config error (%v) — using defaults\n", err)
		cfg = defaultConfig()
	}

	// Apply startup-time package vars before any goroutines are created.
	listenAddr = cfg.ListenAddr
	oscTarget = cfg.OSCTarget
	gsrBaselineAlpha = cfg.GSR.BaselineAlpha
	gsrSigmaFloor = cfg.GSR.SigmaFloor
	gsrMinContact = cfg.GSR.MinContactADC
	gsrMaxContact = cfg.GSR.MaxContactADC
	gsrMaxContactRecover = cfg.GSR.MaxContactRecoverADC
	gsrDisplaySmoothN = cfg.GSR.DisplaySmoothN
	gsrClampMin = cfg.GSR.ClampMinADC
	gsrClampMax = cfg.GSR.ClampMaxADC
	hrBaselineAlpha = cfg.HR.BaselineAlpha
	hrSigmaFloor = cfg.HR.SigmaFloor
	hrMinValidBPM = cfg.HR.MinValidBPM
	hrMaxValidBPM = cfg.HR.MaxValidBPM
	hrWarmupDur = time.Duration(cfg.HR.WarmupS * float64(time.Second))
	hrClampMin = cfg.HR.ClampMinBPM
	hrClampMax = cfg.HR.ClampMaxBPM
	rrBaselineAlpha = cfg.RR.BaselineAlpha
	rrSigmaFloor = cfg.RR.SigmaFloor
	rrClampMin = cfg.RR.ClampMinBPM
	rrClampMax = cfg.RR.ClampMaxBPM
	stubPeriodSamples = stubPeriodFromBPM(cfg.Stub.BreatheBPM)

	// Apply hot-reloadable params.
	applyTimings(cfg.Timing)
	applyScoringCfg(cfg.Scoring)
	applyDegradedCfg(cfg.Degraded)

	log, err := newLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
	defer log.Close()

	events := make(EventSink, 100)

	gsrProc := newGSRProcessor(log, events)
	hrProc := newHRProcessor(log, events)
	rrProc := newRRProcessor(log, events)

	procs := []Processor{gsrProc, hrProc, rrProc}

	// 1s ticker fires Tick() on all processors for stale/disconnect detection and state timeouts.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			for _, p := range procs {
				p.Tick()
			}
		}
	}()

	stub := newRRStub(rrProc, gsrProc, hrProc)
	if err := startListener(gsrProc, hrProc, rrProc, stub, log); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}

	stubStop := make(chan struct{})
	if !*noStub {
		go stub.Run(stubStop)
	}

	stopWatch := watchConfig(*cfgPath, log, func(c Config) {
		applyTimings(c.Timing)
		applyScoringCfg(c.Scoring)
		applyDegradedCfg(c.Degraded)
	})
	defer stopWatch()

	bridge, err := newOSCBridge(cfg.OSCTarget, log, events, gsrProc, hrProc, rrProc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: OSC bridge disabled: %v\n", err)
		bridge = nil
	}
	if bridge != nil {
		if err := bridge.startInbound(cfg.OscListenAddr); err != nil {
			fmt.Fprintf(os.Stderr, "warn: OSC inbound disabled: %v\n", err)
		}
		go bridge.startPulse(hrProc, stubStop)
	}

	StartReplay(ReplayConfig{
		GSRLog:  *replayGSR,
		HRLog:   *replayHR,
		QLabLog: *replayQLab,
	}, gsrProc, hrProc, rrProc, events, log)

	initMuted := [3]bool{cfg.Mute.GSR, cfg.Mute.HR, cfg.Mute.RR}
	m := newModel(gsrProc, hrProc, rrProc, events, log, *cfgPath, initMuted, bridge)
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		close(stubStop)
		os.Exit(1)
	}

	close(stubStop)
}
