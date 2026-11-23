// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

package geo

import (
	"math"
	"strings"
	"testing"
)

func TestGrid(t *testing.T) {
	for _, c := range []struct {
		lat, lon float64
		pairs    int
		want     string
	}{
		{42.213, -88.237, 3, "EN52vf"},
		{42.213, -88.237, 2, "EN52"},
		{42.213, -88.237, 1, "EN"},
		{51.5, -0.12, 3, "IO91wm"},   // London
		{-33.86, 151.2, 3, "QF56od"}, // Sydney
		{35.68, 139.69, 3, "PM95uq"}, // Tokyo
		{0, 0, 3, "JJ00aa"},
		{90, 180, 3, "RR99xx"},   // the pole and the antimeridian stay valid
		{-90, -180, 3, "AA00aa"}, // south-west corner
		{39.0, -95.0, 3, "EM29ma"},
	} {
		if got := Grid(c.lat, c.lon, c.pairs); got != c.want {
			t.Errorf("Grid(%v, %v, %d) = %q, want %q", c.lat, c.lon, c.pairs, got, c.want)
		}
	}
	if Grid(91, 0, 3) != "" || Grid(0, 181, 3) != "" || Grid(math.NaN(), 0, 3) != "" {
		t.Error("invalid coordinates produced a grid")
	}
	if got := Grid(42.213, -88.237, 4); !strings.HasPrefix(got, "EN52vf") || len(got) != 8 {
		t.Errorf("extended grid = %q", got)
	}
}

func TestGridCenter(t *testing.T) {
	// EN52vf by hand: lon -180+80+10 + 21.5*(5/60) = -88.2083; lat -90+130+2 + 5.5*(2.5/60) = 42.2292
	lat, lon, err := GridCenter("EN52vf")
	if err != nil || math.Abs(lat-42.2292) > 0.001 || math.Abs(lon+88.2083) > 0.001 {
		t.Errorf("EN52vf centre = %v, %v (%v)", lat, lon, err)
	}
	// 4 characters: the middle of a 2 x 1 degree square
	lat, lon, _ = GridCenter("EN52")
	if math.Abs(lat-42.5) > 1e-9 || math.Abs(lon+89) > 1e-9 {
		t.Errorf("EN52 centre = %v, %v", lat, lon)
	}
	// 2 characters: the middle of the 20 x 10 degree field
	lat, lon, _ = GridCenter("JJ")
	if math.Abs(lat-5) > 1e-9 || math.Abs(lon-10) > 1e-9 {
		t.Errorf("JJ centre = %v, %v", lat, lon)
	}
	// case does not matter, and the centre of a cell lies inside that cell
	if a, _, _ := GridCenter("en52VF"); math.Abs(a-42.2292) > 0.001 {
		t.Errorf("lower-case input: %v", a)
	}
	for _, g := range []string{"EN52vf", "IO91wm", "QF56od", "PM95uq", "AA00aa", "RR99xx", "EM29ma", "EN52vf37"} {
		la, lo, err := GridCenter(g)
		if err != nil {
			t.Fatalf("%s: %v", g, err)
		}
		if back := Grid(la, lo, len(g)/2); back != g {
			t.Errorf("round trip %s -> (%v, %v) -> %s", g, la, lo, back)
		}
	}
	for _, bad := range []string{"", "E", "EN5", "EN52v", "ZZ", "EN5x", "EN52zz", "EN52vf3", "EN52vf3x", "E!"} {
		if _, _, err := GridCenter(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDistanceAndBearing(t *testing.T) {
	// Published reference values.
	near := func(name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.2f, want %.2f (±%v)", name, got, want, tol)
		}
	}
	near("London-Paris km", DistanceKM(51.5074, -0.1278, 48.8566, 2.3522), 343.5, 1.5)
	near("New York-Los Angeles km", DistanceKM(40.7128, -74.0060, 34.0522, -118.2437), 3936, 10)
	near("London-Paris bearing", Bearing(51.5074, -0.1278, 48.8566, 2.3522), 148.1, 0.6)
	near("New York-Los Angeles bearing", Bearing(40.7128, -74.0060, 34.0522, -118.2437), 273.7, 0.6)
	// symmetry and identities
	near("zero distance", DistanceKM(42, -88, 42, -88), 0, 1e-9)
	near("one degree of latitude", DistanceKM(0, 0, 1, 0), 111.2, 0.1)
	near("antipodes", DistanceKM(0, 0, 0, 180), math.Pi*6371.0088, 0.01)
	near("due north", Bearing(10, 20, 11, 20), 0, 1e-9)
	near("due east on the equator", Bearing(0, 0, 0, 1), 90, 1e-9)
	near("due south", Bearing(10, 20, 9, 20), 180, 1e-9)
	near("due west on the equator", Bearing(0, 0, 0, -1), 270, 1e-9)
	if d1, d2 := DistanceKM(40, -75, 35, -120), DistanceKM(35, -120, 40, -75); math.Abs(d1-d2) > 1e-9 {
		t.Errorf("distance is not symmetric: %v vs %v", d1, d2)
	}
	if b := Bearing(0, 0, 0, 0); b < 0 || b >= 360 {
		t.Errorf("bearing out of range: %v", b)
	}
}

func TestCompass(t *testing.T) {
	for deg, want := range map[float64]string{
		0: "N", 11.24: "N", 11.26: "NNE", 45: "NE", 90: "E", 135: "SE", 180: "S", 225: "SW", 270: "W", 273.7: "W", 315: "NW",
		348.74: "NNW", 348.76: "N", 359.9: "N", 360: "N", 720: "N", -90: "W", -1: "N", 148.1: "SSE",
	} {
		if got := Compass(deg); got != want {
			t.Errorf("Compass(%v) = %q, want %q", deg, got, want)
		}
	}
	if Compass(math.NaN()) != "" || Compass(math.Inf(1)) != "" {
		t.Error("an invalid bearing was named")
	}
	if got := KMToMiles(100); math.Abs(got-62.1371) > 0.001 {
		t.Errorf("100 km = %v miles", got)
	}
}
