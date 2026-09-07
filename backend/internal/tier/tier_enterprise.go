//go:build enterprise

package tier

// Current reports the build-time deployment tier.
func Current() Tier { return Enterprise }
