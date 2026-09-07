// Package datamodel defines the storage interfaces that ALL business code
// depends on. Concrete adapters live in sub-packages:
//
//   - datamodel/memory  (in-memory; tests, CLI sandboxes — all tiers)
//   - datamodel/sqlite  (personal + small-business; JSONB document columns)
//   - datamodel/mongo   (enterprise; BSON)                              [later]
//
// Adapters MUST pass the contract test suite in datamodel/contract — the
// same suite runs against SQLite today and MongoDB tomorrow, so query
// semantics (filtering, pagination, archive behavior) are guaranteed
// identical across tiers.
package datamodel

import "errors"

// ErrNotFound is returned by Get/Update/Delete when no live supplier matches.
var ErrNotFound = errors.New("datamodel: supplier not found")

// ErrConflict is returned when an ID collides on create (the service layer
// retries with a fresh generated ID).
var ErrConflict = errors.New("datamodel: supplier id conflict")

// ErrInvalidPagination is returned when a query cannot be decoded/executed.
var ErrInvalidPagination = errors.New("datamodel: invalid pagination cursor")
