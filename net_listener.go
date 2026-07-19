package main

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var listenAddr = ":5000" // set from Config.ListenAddr at startup

// startListener opens a TCP listener on :5000 and accepts connections indefinitely.
// Each line must be prefixed with the channel name:
//
//	gsr,{boardMs},{raw_adc}
//	hr,{boardMs},{bpm}
//	rr,{boardMs},{raw_adc}
//
// Unknown prefixes are silently skipped. The stub is notified whenever a real
// rr,... line arrives so it can pause synthetic output.
func startListener(gsr *GSRProcessor, hr *HRProcessor, rr *RRProcessor, stub *RRStub, log *AppLogger) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	log.Event(channelSystem, "listener_start", "addr", listenAddr)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Event(channelSystem, "listener_error", "err", err.Error())
				continue
			}
			go handleConn(conn, gsr, hr, rr, stub, log)
		}
	}()

	return nil
}

func handleConn(conn net.Conn, gsr *GSRProcessor, hr *HRProcessor, rr *RRProcessor, stub *RRStub, log *AppLogger) {
	defer conn.Close()
	remote := conn.RemoteAddr().String()
	log.Event(channelSystem, "connected", "remote", remote)
	hr.OnConnect()

	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		parseLine(sc.Text(), gsr, hr, rr, stub)
	}

	log.Event(channelSystem, "disconnected", "remote", remote)
	gsr.OnDisconnect()
	hr.OnDisconnect()
	rr.OnDisconnect()
}

// parseLine routes one text line to the correct processor.
// Format: "{channel},{boardMs},{value}\n"  (trailing newline stripped by Scanner)
func parseLine(line string, gsr *GSRProcessor, hr *HRProcessor, rr *RRProcessor, stub *RRStub) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	// Split on first comma only to get the channel prefix.
	idx := strings.IndexByte(line, ',')
	if idx < 0 {
		return
	}
	ch := line[:idx]
	rest := line[idx+1:]

	// Parse "boardMs,value" from the remainder.
	parts := strings.SplitN(rest, ",", 2)
	if len(parts) != 2 {
		return
	}
	boardMs, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return
	}
	val, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return
	}

	switch ch {
	case "gsr":
		gsr.Ingest(val, boardMs)
	case "hr":
		hr.Ingest(val, boardMs)
	case "rr":
		stub.NotifyRealData()
		rr.Ingest(val, boardMs)
	// "hb" (heartbeat) lines are accepted but need no processing here.
	}
}
