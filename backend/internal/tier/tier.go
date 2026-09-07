// Package tier identifies which product tier the binary was built for.
//
// The active tier is selected ENTIRELY by Go build tags
// (personal | small_business | enterprise); with no tag the build defaults
// to personal behavior for developer convenience. This package is one of
// the ONLY three places tier differences may appear (the others being
// infra adapter packages and feature-flag config).
package tier

// Tier identifies a deployment target.
type Tier string

const (
	Personal      Tier = "personal"
	SmallBusiness Tier = "small_business"
	Enterprise    Tier = "enterprise"
)

func (t Tier) String() string { return string(t) }
