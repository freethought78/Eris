// Package stats keeps samples and prints median / 95th percentile / max.
package stats

import (
	"fmt"
	"sort"
)

// Series is a list of samples in milliseconds.
type Series struct{ v []float64 }

// AddNs records a sample given in nanoseconds.
func (s *Series) AddNs(ns int64) { s.v = append(s.v, float64(ns)/1e6) }

// Len is the number of samples.
func (s *Series) Len() int { return len(s.v) }

// Reset drops all samples.
func (s *Series) Reset() { s.v = s.v[:0] }

func (s *Series) pct(p float64) float64 {
	c := append([]float64(nil), s.v...)
	sort.Float64s(c)
	i := int(p * float64(len(c)-1))
	return c[i]
}

// String is "median / p95 / max" in ms, or "-" with no samples.
func (s *Series) String() string {
	if len(s.v) == 0 {
		return "-"
	}
	return fmt.Sprintf("%5.1f /%5.1f /%5.1f", s.pct(0.5), s.pct(0.95), s.pct(1))
}

// Median in ms (0 with no samples).
func (s *Series) Median() float64 {
	if len(s.v) == 0 {
		return 0
	}
	return s.pct(0.5)
}
