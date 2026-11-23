// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package calc

import (
	"errors"
	"math"
	"testing"
)

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.5f, want %.5f (±%g)", name, got, want, tol)
	}
}

func TestWavelength(t *testing.T) {
	r, err := Wavelength(146.52)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "wavelength m", r.Wavelength.Meters, 2.04611, 0.0001)
	near(t, "wavelength ft", r.Wavelength.Feet, 6.7131, 0.001)
	near(t, "quarter wave m", r.Fractions[0].Length.Meters, 0.51153, 0.0001)
	if r.Fractions[1].Name != "1/2 wave" {
		t.Fatal("fraction order changed")
	}
	// known: 14.2 MHz is about 21.1 m
	r, _ = Wavelength(14.2)
	near(t, "20 m band", r.Wavelength.Meters, 21.112, 0.001)
}

func TestDipole(t *testing.T) {
	r, err := Dipole(14.2)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "total ft", r.Total.Feet, 32.9577, 0.001) // 468/14.2
	near(t, "leg ft", r.Leg.Feet, 16.4789, 0.001)
	near(t, "total m", r.Total.Meters, 10.0456, 0.001)
	near(t, "free space ft", r.FreeSpace.Feet, 34.64, 0.01) // half wavelength
	if r.FreeSpace.Feet <= r.Total.Feet {
		t.Error("end-effect length should be shorter than free space")
	}
}

func TestVertical(t *testing.T) {
	r, _ := Vertical(7.15)
	near(t, "radiator ft", r.Radiator.Feet, 32.7273, 0.001) // 234/7.15
	near(t, "radial ft", r.Radial.Feet, 34.3636, 0.001)
}

func TestJPole(t *testing.T) {
	r, err := JPole(146.52, 0.95)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "overall in", r.Overall.Feet*12, 57.4, 0.2)
	near(t, "stub in", r.Stub.Feet*12, 19.1, 0.1)
	if _, err := JPole(146.52, 1.5); err == nil {
		t.Error("vf 1.5 accepted")
	}
}

func TestMatchKnownValues(t *testing.T) {
	// 75 ohm load on 50 ohm line: SWR 1.5, RL 13.98 dB, 4% reflected
	m, err := MatchFromImpedance(50, 75, 0)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "swr", *m.SWR, 1.5, 1e-9)
	near(t, "gamma", m.Gamma, 0.2, 1e-9)
	near(t, "return loss", *m.ReturnLossDB, 13.979, 0.001)
	near(t, "reflected %", m.ReflectedPct, 4, 1e-9)
	near(t, "mismatch loss", *m.MismatchLossDB, 0.1773, 0.001)

	// SWR 2: gamma 1/3, RL 9.54 dB, 11.1% reflected
	m, _ = MatchFromSWR(2)
	near(t, "gamma", m.Gamma, 1.0/3, 1e-9)
	near(t, "return loss", *m.ReturnLossDB, 9.542, 0.001)
	near(t, "reflected %", m.ReflectedPct, 11.111, 0.001)

	// round trip through return loss
	m2, _ := MatchFromReturnLoss(*m.ReturnLossDB)
	near(t, "swr round trip", *m2.SWR, 2, 1e-9)
}

func TestMatchEdgeCases(t *testing.T) {
	m, _ := MatchFromSWR(1)
	if m.ReturnLossDB != nil || m.Gamma != 0 {
		t.Error("perfect match should have infinite return loss")
	}
	m, _ = MatchFromImpedance(50, 0, 0) // short circuit
	if m.SWR != nil || m.Gamma != 1 {
		t.Error("short circuit should have infinite SWR")
	}
	// pure reactance: all power reflected, infinite SWR
	m, _ = MatchFromImpedance(50, 0, 50)
	if m.SWR != nil {
		t.Error("pure reactance should reflect everything")
	}
	// reactance raises SWR above the resistive-only value
	a, _ := MatchFromImpedance(50, 50, 0)
	b, _ := MatchFromImpedance(50, 50, 50)
	near(t, "matched swr", *a.SWR, 1, 1e-9)
	if *b.SWR <= 1.5 {
		t.Errorf("50+j50 on 50 should be SWR 2.618, got %v", *b.SWR)
	}
	near(t, "50+j50 swr", *b.SWR, 2.618, 0.001)
	if _, err := MatchFromSWR(0.5); err == nil {
		t.Error("SWR below 1 accepted")
	}
	if _, err := MatchFromSWR(math.NaN()); err == nil {
		t.Error("NaN accepted")
	}
	if _, err := MatchFromImpedance(0, 50, 0); err == nil {
		t.Error("zero line impedance accepted")
	}
}

func TestCoax(t *testing.T) {
	r, err := Coax(3, 100, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "matched", r.MatchedLossDB, 3, 1e-9)
	near(t, "total at SWR 1", r.TotalLossDB, 3, 1e-9)
	near(t, "power out", r.PowerOutW, 50.12, 0.01)

	// 3 dB cable at SWR 3 (gamma 0.5): total 3.968 dB
	r, _ = Coax(3, 100, 3, 0)
	near(t, "total at SWR 3", r.TotalLossDB, 3.968, 0.005)
	if r.ExtraLossDB <= 0 || r.PowerOutW != 0 {
		t.Errorf("unexpected: %+v", r)
	}
	// SWR matters little on a short, low-loss run and a lot on a lossless... check monotonic
	hi, _ := Coax(1, 50, 5, 0)
	lo, _ := Coax(1, 50, 1.5, 0)
	if hi.TotalLossDB <= lo.TotalLossDB {
		t.Error("higher SWR should not reduce loss")
	}
	// zero length: no loss at all
	z, _ := Coax(3, 0, 3, 0)
	near(t, "zero length", z.TotalLossDB, 0, 1e-9)
	if _, err := Coax(-1, 10, 1, 0); err == nil {
		t.Error("negative loss accepted")
	}
}

func TestPower(t *testing.T) {
	r, err := Power(5, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	near(t, "5 W in dBm", r.TxDBm, 36.99, 0.01)
	near(t, "5 W in dBW", r.TxDBW, 6.99, 0.01)
	near(t, "0 dBi EIRP", r.EIRPW, 5, 1e-9)

	// 100 W into a 2.15 dBi antenna with no loss: EIRP 164 W, ERP 100 W
	r, _ = Power(100, 0, 2.15)
	near(t, "EIRP", r.EIRPW, 164.06, 0.05)
	near(t, "ERP", r.ERPW, 100, 0.01)
	near(t, "gain dBd", r.GainDBd, 0, 1e-9)

	// 3 dB of line loss halves EIRP
	r, _ = Power(100, 3, 0)
	near(t, "EIRP with loss", r.EIRPW, 50.12, 0.01)
	if _, err := Power(0, 0, 0); err == nil {
		t.Error("zero power accepted")
	}
}

func TestFrequencyRange(t *testing.T) {
	for _, f := range []float64{0, -1, 0.001, 1e9, math.NaN(), math.Inf(1)} {
		_, err := Dipole(f)
		var re *ErrRange
		if !errors.As(err, &re) {
			t.Errorf("frequency %v not rejected with ErrRange: %v", f, err)
		}
	}
}
