//go:build small_business

package tier

// Current reports the build-time deployment tier.
func Current() Tier { return SmallBusiness }
