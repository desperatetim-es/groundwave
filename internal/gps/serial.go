// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package gps

import (
	"errors"
	"io"
	"sort"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// SerialOpener opens a real serial port at 8N1 with a short read timeout.
func SerialOpener(name string, baud int) (io.ReadCloser, error) {
	p, err := serial.Open(name, &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	})
	if err != nil {
		return nil, friendlyOpenError(name, err)
	}
	if err := p.SetReadTimeout(500 * time.Millisecond); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func friendlyOpenError(name string, err error) error {
	var pe *serial.PortError
	if errors.As(err, &pe) {
		switch pe.Code() {
		case serial.PortBusy:
			return errors.New(name + " is in use by another program (close it and try again)")
		case serial.PermissionDenied:
			return errors.New("permission denied opening " + name + ". On Linux, add your user to the \"dialout\" group (sudo usermod -aG dialout $USER), then log out and back in")
		case serial.PortNotFound:
			return errors.New(name + " was not found; is the receiver plugged in?")
		}
	}
	return err
}

// SerialLister lists the serial ports the operating system reports.
func SerialLister() ([]PortInfo, error) {
	details, err := enumerator.GetDetailedPortsList()
	if err == nil {
		out := make([]PortInfo, 0, len(details))
		for _, d := range details {
			desc := d.Product
			if desc == "" {
				desc = "Serial port"
			}
			out = append(out, PortInfo{Name: d.Name, Description: desc, USB: d.IsUSB, VID: d.VID, PID: d.PID})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out, nil
	}
	// Fall back to the plain list when detailed enumeration isn't available.
	names, err2 := serial.GetPortsList()
	if err2 != nil {
		return nil, err2
	}
	out := make([]PortInfo, 0, len(names))
	for _, n := range names {
		out = append(out, PortInfo{Name: n, Description: "Serial port"})
	}
	return out, nil
}
