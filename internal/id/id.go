package id

import (
	"strings"

	"github.com/oklog/ulid/v2"
)

// New returns a sortable identifier with an optional domain prefix.
func New(prefix string) string {
	value := strings.ToLower(ulid.Make().String())
	if prefix == "" {
		return value
	}
	return prefix + "_" + value
}
