package kernel

import "math/rand/v2"

// JitterSource yields a uniform random fraction in [0,1) used to de-synchronize
// retry backoff (full jitter). It is the ONLY randomness in the runtime; the value
// is recorded on the ActionFailed trigger so engine replay stays deterministic.
type JitterSource interface{ Fraction() float64 }

type randJitter struct{ r *rand.Rand }

// NewJitterSource returns the default seeded JitterSource. The RNG is seeded
// from two independent random uint64 values so that each call produces a
// distinct sequence even within the same process.
func NewJitterSource() JitterSource {
	return randJitter{r: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))} //nolint:gosec // G404: retry-backoff jitter is not security-sensitive; math/rand is intentional.
}

func (j randJitter) Fraction() float64 { return j.r.Float64() }
