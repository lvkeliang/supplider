//go:build personal

package tier

// Current reports the build-time deployment tier.
func Current() Tier { return Personal }
