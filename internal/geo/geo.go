// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 KD9PSM

// Package geo holds the small geography routines shared by the GPS page, the
// callsign database and (later) the logbook: Maidenhead grid squares, distance
// and bearing. All are pure functions.
package geo

import (
	"errors"
	"math"
	"strings"
)

const earthRadiusKM = 6371.0088 // mean radius

// Grid returns the Maidenhead locator for a position, with the given number of
// character pairs (3 gives six characters such as "EN52vf"). Fields use upper
// case letters, subsquares lower case. Out-of-range input gives "".
func Grid(lat, lon float64, pairs int) string {
	if pairs < 1 {
		pairs = 1
	}
	if pairs > 4 {
		pairs = 4
	}
	if math.IsNaN(lat) || math.IsNaN(lon) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return ""
	}
	// The poles and the antimeridian belong to the last cell, not an invalid one.
	lon = math.Min(lon+180, 360-1e-9)
	lat = math.Min(lat+90, 180-1e-9)

	out := make([]byte, 0, pairs*2)
	out = append(out, byte('A'+int(lon/20)), byte('A'+int(lat/10))) // field: 20 x 10 degrees
	lon = math.Mod(lon, 20)
	lat = math.Mod(lat, 10)
	if pairs >= 2 { // square: 2 x 1 degrees
		out = append(out, byte('0'+int(lon/2)), byte('0'+int(lat)))
		lon = math.Mod(lon, 2)
		lat = math.Mod(lat, 1)
	}
	if pairs >= 3 { // subsquare: 5' x 2.5'
		out = append(out, byte('a'+int(lon*12)), byte('a'+int(lat*24)))
		lon = math.Mod(lon*12, 1)
		lat = math.Mod(lat*24, 1)
	}
	if pairs >= 4 { // extended square
		out = append(out, byte('0'+int(lon*10)), byte('0'+int(lat*10)))
	}
	return string(out)
}

// GridCenter returns the latitude and longitude of the centre of a 2, 4, 6 or
// 8 character Maidenhead locator.
func GridCenter(grid string) (lat, lon float64, err error) {
	g := strings.TrimSpace(grid)
	if n := len(g); n < 2 || n > 8 || n%2 != 0 {
		return 0, 0, errors.New("a grid square has 2, 4, 6 or 8 characters, for example EN52vf")
	}
	up := strings.ToUpper(g[:2])
	if up[0] < 'A' || up[0] > 'R' || up[1] < 'A' || up[1] > 'R' {
		return 0, 0, errors.New("the first two characters of a grid square must be letters A to R")
	}
	lon = float64(up[0]-'A')*20 - 180
	lat = float64(up[1]-'A')*10 - 90
	lonSize, latSize := 20.0, 10.0
	for i := 2; i < len(g); i += 2 {
		a, b := g[i], g[i+1]
		switch i {
		case 2, 6: // digits
			if a < '0' || a > '9' || b < '0' || b > '9' {
				return 0, 0, errors.New("grid square characters 3-4 and 7-8 must be digits")
			}
			lonSize /= 10 // a field (20 x 10 degrees) divides into 10 x 10 squares of 2 x 1 degrees
			latSize /= 10
			lon += float64(a-'0') * lonSize
			lat += float64(b-'0') * latSize
		case 4: // letters a-x
			la, lb := a|0x20, b|0x20
			if la < 'a' || la > 'x' || lb < 'a' || lb > 'x' {
				return 0, 0, errors.New("grid square characters 5-6 must be letters A to X")
			}
			lonSize /= 24
			latSize /= 24
			lon += float64(la-'a') * lonSize
			lat += float64(lb-'a') * latSize
		}
	}
	return lat + latSize/2, lon + lonSize/2, nil
}

// DistanceKM is the great-circle distance between two points in kilometres.
func DistanceKM(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := rad(lat1), rad(lat2)
	h := math.Pow(math.Sin((p2-p1)/2), 2) + math.Cos(p1)*math.Cos(p2)*math.Pow(math.Sin(rad(lon2-lon1)/2), 2)
	return 2 * earthRadiusKM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// Bearing is the initial true bearing from the first point to the second, in
// degrees from 0 to 360.
func Bearing(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2, dl := rad(lat1), rad(lat2), rad(lon2-lon1)
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}

var compassPoints = [16]string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}

// Compass names a bearing in degrees as one of 16 compass points ("WNW").
func Compass(deg float64) string {
	if math.IsNaN(deg) || math.IsInf(deg, 0) {
		return ""
	}
	d := math.Mod(deg, 360)
	if d < 0 {
		d += 360
	}
	return compassPoints[int((d+11.25)/22.5)%16]
}

// KMToMiles converts kilometres to statute miles.
func KMToMiles(km float64) float64 { return km * 0.621371192 }

func rad(d float64) float64 { return d * math.Pi / 180 }
