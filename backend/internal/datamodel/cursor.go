package datamodel

import (
	"encoding/base64"
	"encoding/json"
)

// EncodeCursor serializes a keyset position for clients. Opaque by contract.
func EncodeCursor(c Cursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor parses a client-supplied cursor. Empty string decodes to the
// zero cursor (start of collection); malformed input returns
// ErrInvalidPagination.
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidPagination
	}
	var c Cursor
	if err := json.Unmarshal(b, &c); err != nil {
		return Cursor{}, ErrInvalidPagination
	}
	return c, nil
}
