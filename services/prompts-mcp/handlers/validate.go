package handlers

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

// ErrInvalidPathSegment is returned when a prompt's ID or Domain cannot be used
// safely as a filesystem path segment. Callers that serve HTTP should map this
// to 400 Bad Request (see errors.Is).
var ErrInvalidPathSegment = errors.New("invalid path segment")

// maxPathSegmentLen bounds a single path component so we never hit ENAMETOOLONG.
const maxPathSegmentLen = 128

// pathSegmentRe is deliberately strict: no dots, no slashes, no separators of any
// kind. Because '.' is excluded, "." and ".." can never be produced, which closes
// the traversal vector (B6) at the character-class level rather than relying on
// post-hoc cleaning.
var pathSegmentRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// validatePathSegment ensures a single untrusted string is safe to Join into a path.
func validatePathSegment(field, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidPathSegment, field)
	}
	if len(value) > maxPathSegmentLen {
		return fmt.Errorf("%w: %s exceeds %d characters", ErrInvalidPathSegment, field, maxPathSegmentLen)
	}
	if !pathSegmentRe.MatchString(value) {
		return fmt.Errorf("%w: %s=%q must match %s", ErrInvalidPathSegment, field, value, pathSegmentRe.String())
	}
	// Belt and braces: even a regex-clean segment must still be a local path.
	if !filepath.IsLocal(value) {
		return fmt.Errorf("%w: %s=%q is not a local path", ErrInvalidPathSegment, field, value)
	}
	return nil
}

// ValidatePromptPath validates every prompt field that is used to build an
// on-disk path. Returns a wrapped ErrInvalidPathSegment on failure.
func ValidatePromptPath(id, domain string) error {
	if err := validatePathSegment("domain", domain); err != nil {
		return err
	}
	return validatePathSegment("id", id)
}
