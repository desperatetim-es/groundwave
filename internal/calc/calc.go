// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package calc holds the antenna and RF calculations. They are standard
// rules of thumb and textbook formulas; real antennas must be tuned with an
// analyzer. Everything here is pure and has no I/O.
package calc

import (
	"fmt"
	"math"
	"math/cmplx"
)

const (
	speedOfLight = 299.792458 // metres per microsecond, so wavelength(m) = speedOfLight / f(MHz)
	metresPerFt  = 0.3048
	dBdToDBi     = 2.15
)

// ErrRange is returned for inputs outside what the calculator supports.
type ErrRange struct{ Msg string }

func (e *ErrRange) Error() string { return e.Msg }

func rangeErr(format string, a ...any) error { return &ErrRange{Msg: fmt.Sprintf(format, a...)} }

func finite(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return rangeErr("%s must be a number", name)
	}
	return nil
}

func checkFreq(f float64) error {
	if err := finite("frequency", f); err != nil {
		return err
	}
	if f < 0.01 || f > 300000 {
		return rangeErr("frequency must be between 0.01 and 300000 MHz")
	}
	return nil
}

// Length is a length in both units.
type Length struct {
	Feet   float64 `json:"feet"`
	Meters float64 `json:"meters"`
}

func lenFt(ft float64) Length { return Length{Feet: ft, Meters: ft * metresPerFt} }
func lenM(m float64) Length   { return Length{Feet: m / metresPerFt, Meters: m} }

// ---- wavelength ----------------------------------------------------------

type Fraction struct {
	Name   string `json:"name"`
	Length Length `json:"length"`
}

type WavelengthResult struct {
	FreqMHz    float64    `json:"freq_mhz"`
	Wavelength Length     `json:"wavelength"`
	Fractions  []Fraction `json:"fractions"`
}

// Wavelength returns the free-space wavelength and common fractions of it.
func Wavelength(fMHz float64) (WavelengthResult, error) {
	if err := checkFreq(fMHz); err != nil {
		return WavelengthResult{}, err
	}
	wl := speedOfLight / fMHz
	r := WavelengthResult{FreqMHz: fMHz, Wavelength: lenM(wl)}
	for _, p := range []struct {
		n string
		v float64
	}{{"1/4 wave", 0.25}, {"1/2 wave", 0.5}, {"5/8 wave", 0.625}, {"3/4 wave", 0.75}} {
		r.Fractions = append(r.Fractions, Fraction{Name: p.n, Length: lenM(wl * p.v)})
	}
	return r, nil
}

// ---- dipole, vertical, J-pole --------------------------------------------

type DipoleResult struct {
	FreqMHz   float64 `json:"freq_mhz"`
	Total     Length  `json:"total"`      // 468 / f(MHz) feet
	Leg       Length  `json:"leg"`        // each side of the centre insulator
	FreeSpace Length  `json:"free_space"` // exact half wavelength, for reference
}

// Dipole uses the common 468/f rule, which includes about 5% shortening for
// end effect with typical wire. Cut a little long and trim for lowest SWR.
func Dipole(fMHz float64) (DipoleResult, error) {
	if err := checkFreq(fMHz); err != nil {
		return DipoleResult{}, err
	}
	total := 468.0 / fMHz
	return DipoleResult{
		FreqMHz:   fMHz,
		Total:     lenFt(total),
		Leg:       lenFt(total / 2),
		FreeSpace: lenM(speedOfLight / fMHz / 2),
	}, nil
}

type VerticalResult struct {
	FreqMHz  float64 `json:"freq_mhz"`
	Radiator Length  `json:"radiator"` // 234 / f(MHz) feet
	Radial   Length  `json:"radial"`   // about 5% longer than the radiator
}

// Vertical returns a quarter-wave ground-plane vertical.
func Vertical(fMHz float64) (VerticalResult, error) {
	if err := checkFreq(fMHz); err != nil {
		return VerticalResult{}, err
	}
	rad := 234.0 / fMHz
	return VerticalResult{FreqMHz: fMHz, Radiator: lenFt(rad), Radial: lenFt(rad * 1.05)}, nil
}

type JPoleResult struct {
	FreqMHz float64 `json:"freq_mhz"`
	VF      float64 `json:"vf"`
	Overall Length  `json:"overall"` // 3/4 wave
	Stub    Length  `json:"stub"`    // 1/4 wave matching section
}

// JPole returns a starting-point J-pole size. vf is the velocity factor of the
// material (about 0.95 for 450 ohm ladder line; solid copper pipe is closer to 0.97).
func JPole(fMHz, vf float64) (JPoleResult, error) {
	if err := checkFreq(fMHz); err != nil {
		return JPoleResult{}, err
	}
	if err := finite("velocity factor", vf); err != nil {
		return JPoleResult{}, err
	}
	if vf < 0.5 || vf > 1 {
		return JPoleResult{}, rangeErr("velocity factor must be between 0.5 and 1.0")
	}
	wl := speedOfLight / fMHz
	return JPoleResult{FreqMHz: fMHz, VF: vf, Overall: lenM(0.75 * wl * vf), Stub: lenM(0.25 * wl * vf)}, nil
}

// ---- SWR and mismatch ----------------------------------------------------

type MatchResult struct {
	Z0             float64  `json:"z0,omitempty"`
	Gamma          float64  `json:"gamma"`          // reflection coefficient magnitude, 0..1
	SWR            *float64 `json:"swr"`            // nil = infinite
	ReturnLossDB   *float64 `json:"return_loss_db"` // nil = infinite (perfect match)
	MismatchLossDB *float64 `json:"mismatch_loss_db"`
	ReflectedPct   float64  `json:"reflected_pct"`
}

func fromGamma(g float64) MatchResult {
	r := MatchResult{Gamma: g, ReflectedPct: g * g * 100}
	if g < 1 {
		s := (1 + g) / (1 - g)
		r.SWR = &s
		ml := -10 * math.Log10(1-g*g)
		r.MismatchLossDB = &ml
	}
	if g > 0 {
		rl := -20 * math.Log10(g)
		r.ReturnLossDB = &rl
	}
	return r
}

// MatchFromSWR derives everything else from an SWR of at least 1.
func MatchFromSWR(swr float64) (MatchResult, error) {
	if err := finite("SWR", swr); err != nil {
		return MatchResult{}, err
	}
	if swr < 1 || swr > 1000 {
		return MatchResult{}, rangeErr("SWR must be between 1 and 1000")
	}
	return fromGamma((swr - 1) / (swr + 1)), nil
}

// MatchFromReturnLoss derives everything else from a return loss in dB (> 0).
func MatchFromReturnLoss(rl float64) (MatchResult, error) {
	if err := finite("return loss", rl); err != nil {
		return MatchResult{}, err
	}
	if rl <= 0 || rl > 100 {
		return MatchResult{}, rangeErr("return loss must be between 0 and 100 dB")
	}
	return fromGamma(math.Pow(10, -rl/20)), nil
}

// MatchFromImpedance computes the match of a load R + jX on a line of
// characteristic impedance z0.
func MatchFromImpedance(z0, r, x float64) (MatchResult, error) {
	for _, v := range []struct {
		n string
		v float64
	}{{"line impedance", z0}, {"resistance", r}, {"reactance", x}} {
		if err := finite(v.n, v.v); err != nil {
			return MatchResult{}, err
		}
	}
	if z0 <= 0 || z0 > 10000 {
		return MatchResult{}, rangeErr("line impedance must be between 0 and 10000 ohms")
	}
	if r < 0 || r > 1e6 {
		return MatchResult{}, rangeErr("resistance must be between 0 and 1,000,000 ohms")
	}
	if math.Abs(x) > 1e6 {
		return MatchResult{}, rangeErr("reactance must be between -1,000,000 and 1,000,000 ohms")
	}
	zl := complex(r, x)
	zz := complex(z0, 0)
	res := fromGamma(cmplx.Abs((zl - zz) / (zl + zz)))
	res.Z0 = z0
	return res, nil
}

// ---- feedline loss -------------------------------------------------------

type CoaxResult struct {
	MatchedLossDB float64 `json:"matched_loss_db"` // loss with a perfect match
	TotalLossDB   float64 `json:"total_loss_db"`   // including the extra loss caused by SWR
	ExtraLossDB   float64 `json:"extra_loss_db"`
	PowerInW      float64 `json:"power_in_w,omitempty"`
	PowerOutW     float64 `json:"power_out_w,omitempty"` // delivered to the antenna
}

// Coax computes feedline loss. lossPer100 is the cable's matched loss in dB per
// 100 units at the operating frequency (from its datasheet), length is in the
// same unit (feet or metres), swr is the SWR at the antenna (use 1 if unknown),
// and powerW is optional (0 = not requested).
//
// Total loss with a mismatched load: 10*log10((A^2 - g^2) / (A * (1 - g^2))),
// where A = 10^(matched loss / 10) and g is the load reflection coefficient.
func Coax(lossPer100, length, swr, powerW float64) (CoaxResult, error) {
	for _, v := range []struct {
		n string
		v float64
	}{{"loss per 100", lossPer100}, {"length", length}, {"SWR", swr}, {"power", powerW}} {
		if err := finite(v.n, v.v); err != nil {
			return CoaxResult{}, err
		}
	}
	if lossPer100 < 0 || lossPer100 > 100 {
		return CoaxResult{}, rangeErr("loss per 100 must be between 0 and 100 dB")
	}
	if length < 0 || length > 100000 {
		return CoaxResult{}, rangeErr("length must be between 0 and 100000")
	}
	if swr < 1 || swr > 1000 {
		return CoaxResult{}, rangeErr("SWR must be between 1 and 1000")
	}
	if powerW < 0 || powerW > 1e6 {
		return CoaxResult{}, rangeErr("power must be between 0 and 1,000,000 W")
	}
	ml := lossPer100 * length / 100
	g := (swr - 1) / (swr + 1)
	a := math.Pow(10, ml/10)
	total := 10 * math.Log10((a*a-g*g)/(a*(1-g*g)))
	if total < 0 { // numerical noise near zero
		total = 0
	}
	res := CoaxResult{MatchedLossDB: ml, TotalLossDB: total, ExtraLossDB: total - ml}
	if powerW > 0 {
		res.PowerInW = powerW
		res.PowerOutW = powerW * math.Pow(10, -total/10)
	}
	return res, nil
}

// ---- power and gain ------------------------------------------------------

type PowerResult struct {
	TxPowerW   float64 `json:"tx_power_w"`
	TxDBm      float64 `json:"tx_dbm"`
	TxDBW      float64 `json:"tx_dbw"`
	LineLossDB float64 `json:"line_loss_db"`
	GainDBi    float64 `json:"gain_dbi"`
	GainDBd    float64 `json:"gain_dbd"`
	EIRPW      float64 `json:"eirp_w"`
	ERPW       float64 `json:"erp_w"`
	EIRPDBm    float64 `json:"eirp_dbm"`
}

// Power converts transmitter power to dBm/dBW and computes EIRP and ERP given
// feedline loss (dB, >= 0) and antenna gain in dBi.
func Power(txW, lineLossDB, gainDBi float64) (PowerResult, error) {
	for _, v := range []struct {
		n string
		v float64
	}{{"power", txW}, {"line loss", lineLossDB}, {"gain", gainDBi}} {
		if err := finite(v.n, v.v); err != nil {
			return PowerResult{}, err
		}
	}
	if txW <= 0 || txW > 1e6 {
		return PowerResult{}, rangeErr("power must be greater than 0 and at most 1,000,000 W")
	}
	if lineLossDB < 0 || lineLossDB > 100 {
		return PowerResult{}, rangeErr("line loss must be between 0 and 100 dB")
	}
	if gainDBi < -50 || gainDBi > 100 {
		return PowerResult{}, rangeErr("gain must be between -50 and 100 dBi")
	}
	eirp := txW * math.Pow(10, (gainDBi-lineLossDB)/10)
	return PowerResult{
		TxPowerW:   txW,
		TxDBm:      10 * math.Log10(txW*1000),
		TxDBW:      10 * math.Log10(txW),
		LineLossDB: lineLossDB,
		GainDBi:    gainDBi,
		GainDBd:    gainDBi - dBdToDBi,
		EIRPW:      eirp,
		ERPW:       eirp / math.Pow(10, dBdToDBi/10),
		EIRPDBm:    10 * math.Log10(eirp*1000),
	}, nil
}
