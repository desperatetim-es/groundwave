// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/desperatetim-es/groundwave/internal/calc"
)

// calc serves the read-only calculators: GET /api/calc/{name}?param=value.
func (s *srv) calc(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var (
		res any
		err error
	)
	switch r.PathValue("name") {
	case "wavelength":
		res, err = withNum(q, "f", func(f float64) (any, error) { return calc.Wavelength(f) })
	case "dipole":
		res, err = withNum(q, "f", func(f float64) (any, error) { return calc.Dipole(f) })
	case "vertical":
		res, err = withNum(q, "f", func(f float64) (any, error) { return calc.Vertical(f) })
	case "jpole":
		res, err = jpole(q)
	case "swr":
		res, err = swr(q)
	case "coax":
		res, err = coax(q)
	case "power":
		res, err = power(q)
	default:
		writeErr(w, http.StatusNotFound, errors.New("unknown calculator"))
		return
	}
	var rangeErr *calc.ErrRange
	var badParam *paramError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.As(err, &rangeErr):
		writeErr(w, http.StatusUnprocessableEntity, err)
	case errors.As(err, &badParam):
		writeErr(w, http.StatusBadRequest, err)
	default:
		writeErr(w, http.StatusInternalServerError, err)
	}
}

type paramError struct{ msg string }

func (e *paramError) Error() string { return e.msg }

// num reads a number from the query. A missing value uses def when one is
// given (non-nil) and is an error otherwise.
func num(q url.Values, key string, def *float64) (float64, error) {
	raw := strings.TrimSpace(q.Get(key))
	if raw == "" {
		if def != nil {
			return *def, nil
		}
		return 0, &paramError{fmt.Sprintf("%s is required", key)}
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, &paramError{fmt.Sprintf("%s must be a number", key)}
	}
	return v, nil
}

func d(v float64) *float64 { return &v }

func withNum(q url.Values, key string, fn func(float64) (any, error)) (any, error) {
	v, err := num(q, key, nil)
	if err != nil {
		return nil, err
	}
	return fn(v)
}

func jpole(q url.Values) (any, error) {
	f, err := num(q, "f", nil)
	if err != nil {
		return nil, err
	}
	vf, err := num(q, "vf", d(0.95))
	if err != nil {
		return nil, err
	}
	return calc.JPole(f, vf)
}

// swr accepts an SWR, a return loss in dB, or a load impedance (r, x, z0).
func swr(q url.Values) (any, error) {
	switch {
	case q.Get("swr") != "":
		v, err := num(q, "swr", nil)
		if err != nil {
			return nil, err
		}
		return calc.MatchFromSWR(v)
	case q.Get("rl") != "":
		v, err := num(q, "rl", nil)
		if err != nil {
			return nil, err
		}
		return calc.MatchFromReturnLoss(v)
	}
	r, err := num(q, "r", nil)
	if err != nil {
		return nil, err
	}
	x, err := num(q, "x", d(0))
	if err != nil {
		return nil, err
	}
	z0, err := num(q, "z0", d(50))
	if err != nil {
		return nil, err
	}
	return calc.MatchFromImpedance(z0, r, x)
}

func coax(q url.Values) (any, error) {
	loss, err := num(q, "loss", nil)
	if err != nil {
		return nil, err
	}
	length, err := num(q, "length", nil)
	if err != nil {
		return nil, err
	}
	s, err := num(q, "swr", d(1))
	if err != nil {
		return nil, err
	}
	p, err := num(q, "power", d(0))
	if err != nil {
		return nil, err
	}
	return calc.Coax(loss, length, s, p)
}

func power(q url.Values) (any, error) {
	tx, err := num(q, "tx", nil)
	if err != nil {
		return nil, err
	}
	loss, err := num(q, "loss", d(0))
	if err != nil {
		return nil, err
	}
	gain, err := num(q, "gain", d(0))
	if err != nil {
		return nil, err
	}
	return calc.Power(tx, loss, gain)
}
