//go:build !personal

package main

import "github.com/supplider/supplider/backend/internal/httpapi"

// Non-personal tiers have no SQLite archive restore yet (the backup format
// ships with the personal adapter); restore endpoints stay disabled.

func applyPendingRestore(_ string) {}

func restoreFuncs(_ string) *httpapi.RestoreFuncs { return nil }
