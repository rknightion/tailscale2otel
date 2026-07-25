// Package entityage holds the shared age-distribution vocabulary for tailnet
// entity lifecycle signals (#426).
//
// Devices, users, credentials, webhooks, OAuth apps and posture integrations all
// expose a creation timestamp, and each collector emits an age histogram. They
// share one bucket set on purpose: a single set of bounds means one dashboard
// legend, one alert threshold vocabulary, and comparable panels across entity
// families. Per-collector bounds would make "90 days" mean a different bucket
// edge depending on which panel you were looking at.
//
// Deliberately NOT here: per-entity timestamp series. Those are already covered
// (and gated) by the per-entity cardinality toggles on the individual
// collectors; this package is only about bounded fleet-level distributions.
package entityage

import "time"

const day = float64(24 * time.Hour / time.Second)

// bounds are the histogram bucket boundaries in seconds: 1d, 7d, 30d, 90d,
// 180d, 1y, 2y. Chosen to match how operators actually reason about tailnet
// hygiene — "created this week", "older than a quarter", "older than a year" —
// rather than a mechanical power-of-two ladder.
var bounds = []float64{
	1 * day,
	7 * day,
	30 * day,
	90 * day,
	180 * day,
	365 * day,
	730 * day,
}

// BucketsSeconds returns the shared age histogram bounds, in seconds.
//
// It returns a fresh copy on every call: the OTEL SDK retains the bounds slice
// it is handed, so returning the package-level slice would let one collector's
// accidental mutation silently corrupt every other collector's histogram.
func BucketsSeconds() []float64 {
	out := make([]float64, len(bounds))
	copy(out, bounds)
	return out
}

// Seconds returns the age of an entity created at created, as of now.
//
// ok is false when created is the zero time, which is how every decoder in this
// repo represents "the provider did not supply this timestamp". Callers must
// skip emission in that case rather than recording a zero age — issue #426 is
// explicit that unknown provider values are omitted, not reported as 0, because
// a fleet of "brand new" entities is a materially wrong answer.
//
// A creation timestamp in the future (clock skew between the control plane and
// this process) clamps to 0 rather than producing a negative observation, which
// the OTEL histogram API would otherwise record in the lowest bucket.
func Seconds(created, now time.Time) (float64, bool) {
	if created.IsZero() {
		return 0, false
	}
	d := now.Sub(created).Seconds()
	if d < 0 {
		return 0, true
	}
	return d, true
}
