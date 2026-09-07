//go:build !personal && !small_business && !enterprise

package tier

// Current defaults to personal behavior when no build tag is set, so plain
// `go build` / `go test` works during development. Release builds always
// pass an explicit tag via the Makefile.
func Current() Tier { return Personal }
