package domain

import (
	"crypto/rand"
	"fmt"
	"time"
)

// ID prefixes.
const (
	PrefixSupplier = "sup"
)

// NewSupplierID generates an id of the form "sup_2026_000123" (year + 6-digit
// random sequence). Collisions are astronomically unlikely for a local-first
// personal database, but the service layer retries on conflict anyway.
func NewSupplierID() string {
	return fmt.Sprintf("%s_%d_%06d", PrefixSupplier, time.Now().Year(), randInt(1_000_000))
}

func randInt(n int) int {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not recoverable; fall back to time-based.
		return int(time.Now().UnixNano() % int64(n))
	}
	// Take 40 bits — plenty for n <= 1e6.
	v := uint64(b[0])<<32 | uint64(b[1])<<24 | uint64(b[2])<<16 | uint64(b[3])<<8 | uint64(b[4])
	return int(v % uint64(n))
}

// qualRank orders Chinese construction qualification levels.
var qualRank = map[string]int{
	"特级": 5,
	"一级": 4,
	"二级": 3,
	"三级": 2,
	"甲级": 4, // design/consulting qualifications
	"乙级": 3,
	"丙级": 2,
}

// QualRank returns the comparable rank of a qualification level
// (higher = stronger). Unknown levels rank 0.
func QualRank(level string) int {
	return qualRank[level]
}

// QualRankKnown reports whether level is a recognized, comparable
// qualification level. A hard filter built on an unrecognized value
// (typo like "肆级" / "1级") must be rejected rather than silently
// disabling the filter (QualRank returns 0 for unknown input).
func QualRankKnown(level string) bool {
	_, ok := qualRank[level]
	return ok
}

// TopQualification returns the level label and rank of the supplier's
// highest-ranked qualification ("" / 0 when none is ranked).
func TopQualification(s *Supplier) (level string, rank int) {
	for _, q := range s.Qualifications {
		if r := QualRank(q.Level); r > rank {
			level, rank = q.Level, r
		}
	}
	return level, rank
}

// HighestQualRank returns the numeric rank of the supplier's top
// qualification (0 if none). Used by the MinQualRank filter.
func HighestQualRank(s *Supplier) int {
	_, rank := TopQualification(s)
	return rank
}
