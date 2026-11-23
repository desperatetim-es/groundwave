// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// DefaultOptions wires a Manager to real serial ports. With simulate set, a
// built-in test receiver named "simulator" is added to the port list; it
// produces valid NMEA from the computer's own clock at a fixed, neutral
// position, which lets the whole feature be exercised without hardware.
func DefaultOptions(configPath string, simulate bool) Options {
	o := Options{ConfigPath: configPath, Open: SerialOpener, List: SerialLister}
	if !simulate {
		return o
	}
	o.List = func() ([]PortInfo, error) {
		ports, err := SerialLister()
		if err != nil {
			ports = nil // the simulator still works if enumeration fails
		}
		return append(ports, PortInfo{Name: SimulatorPort, Description: "Built-in test receiver (no hardware)"}), nil
	}
	o.Open = func(name string, baud int) (io.ReadCloser, error) {
		if name == SimulatorPort {
			return NewSimulator(), nil
		}
		return SerialOpener(name, baud)
	}
	return o
}

// Eight simulated satellites (PRN, elevation, azimuth, SNR), six of them used.
var (
	simGSV = []string{
		"GPGSV,2,1,08,02,64,110,42,05,48,200,38,09,22,305,33,13,71,040,45",
		"GPGSV,2,2,08,15,35,255,29,20,12,150,24,24,55,330,41,29,28,075,31",
	}
	simGSA = "GNGSA,A,3,02,05,13,15,24,29,,,,,,,1.8,1.0,1.5"
)

// Simulator is an io.ReadCloser that emits one GNRMC and one GNGGA sentence
// per second, labelled with the computer's own clock.
type Simulator struct {
	pr   *io.PipeReader
	pw   *io.PipeWriter
	once sync.Once
	stop chan struct{}
}

// NewSimulator starts a simulated receiver at 39.0 N, 95.0 W.
func NewSimulator() *Simulator {
	pr, pw := io.Pipe()
	s := &Simulator{pr: pr, pw: pw, stop: make(chan struct{})}
	go s.run()
	return s
}

func (s *Simulator) run() {
	for {
		// Wake just after each second boundary, like a real receiver would.
		now := time.Now()
		next := now.Truncate(time.Second).Add(time.Second)
		select {
		case <-s.stop:
			return
		case <-time.After(time.Until(next) + 100*time.Millisecond):
		}
		t := next.UTC()
		hms := t.Format("150405.00")
		date := t.Format("020106")
		rmc := fmt.Sprintf("GNRMC,%s,A,3900.0000,N,09500.0000,W,0.0,0.0,%s,,,A", hms, date)
		gga := fmt.Sprintf("GNGGA,%s,3900.0000,N,09500.0000,W,1,09,1.0,300.0,M,0.0,M,,", hms)
		bodies := []string{gga, rmc, simGSA}
		bodies = append(bodies, simGSV...)
		for _, body := range bodies {
			if _, err := fmt.Fprintf(s.pw, "$%s*%s\r\n", body, Checksum(body)); err != nil {
				return
			}
		}
	}
}

// Read implements io.Reader.
func (s *Simulator) Read(p []byte) (int, error) { return s.pr.Read(p) }

// Close implements io.Closer.
func (s *Simulator) Close() error {
	s.once.Do(func() {
		close(s.stop)
		s.pw.Close()
		s.pr.Close()
	})
	return nil
}
