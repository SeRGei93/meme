package auth

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var clientNamePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// ValidationError identifies the request field that failed a domain rule.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// ValidateClient applies the client rules from the v1 contract.
func ValidateClient(name string, kind Kind, scopes []Scope) error {
	switch {
	case name == "":
		return &ValidationError{Field: "name", Message: "name is required"}
	case len(name) > 100:
		return &ValidationError{Field: "name", Message: "name must be at most 100 characters"}
	case !clientNamePattern.MatchString(name):
		return &ValidationError{Field: "name", Message: `name may contain only letters, digits, ".", "_", ":", and "-"`}
	case !kind.Valid():
		return &ValidationError{Field: "kind", Message: fmt.Sprintf("kind must be one of %s", kindList())}
	case len(scopes) == 0:
		return &ValidationError{Field: "scopes", Message: "scopes must contain at least one scope"}
	case len(scopes) > 5:
		return &ValidationError{Field: "scopes", Message: "scopes must contain at most 5 scopes"}
	}

	seen := make(map[Scope]bool, len(scopes))
	for _, scope := range scopes {
		if !scope.Valid() {
			return &ValidationError{Field: "scopes", Message: fmt.Sprintf("unknown scope %q", scope)}
		}
		if seen[scope] {
			return &ValidationError{Field: "scopes", Message: fmt.Sprintf("duplicate scope %q", scope)}
		}
		seen[scope] = true
	}

	return nil
}

func kindList() string {
	values := make([]string, len(allKinds))
	for i, kind := range allKinds {
		values[i] = string(kind)
	}
	return strings.Join(values, ", ")
}

// ScopeStrings converts scopes for storage or JSON responses.
func ScopeStrings(scopes []Scope) []string {
	values := make([]string, len(scopes))
	for i, scope := range scopes {
		values[i] = string(scope)
	}
	return values
}

func isOneOf[T comparable](value T, allowed []T) bool {
	return slices.Contains(allowed, value)
}
