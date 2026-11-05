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

// ValidateClient applies the client rules from the v1 contract. Legacy kinds
// are normalized before validation. An agent client must carry an effective
// source; ui and admin clients have none. Store.CreateClient derives the
// default source before calling this function.
func ValidateClient(name string, kind Kind, source string, scopes []Scope) error {
	kind = NormalizeKind(kind)

	switch {
	case name == "":
		return &ValidationError{Field: "name", Message: "name is required"}
	case len(name) > 100:
		return &ValidationError{Field: "name", Message: "name must be at most 100 characters"}
	case !clientNamePattern.MatchString(name):
		return &ValidationError{Field: "name", Message: `name may contain only letters, digits, ".", "_", ":", and "-"`}
	case !kind.Stored():
		return &ValidationError{Field: "kind", Message: fmt.Sprintf("kind must be one of %s", kindList())}
	case len(scopes) == 0:
		return &ValidationError{Field: "scopes", Message: "scopes must contain at least one scope"}
	case len(scopes) > 5:
		return &ValidationError{Field: "scopes", Message: "scopes must contain at most 5 scopes"}
	}

	if kind.AllowsSource() {
		if source == "" {
			return &ValidationError{Field: "source", Message: "source is required for agent clients"}
		}
		if !ValidSourceSlug(source) {
			return &ValidationError{Field: "source", Message: fmt.Sprintf(
				"source must be a lowercase slug of 1-%d characters ([a-z0-9._-], starting with a letter or digit)", MaxSourceLen)}
		}
	} else if source != "" {
		return &ValidationError{Field: "source", Message: fmt.Sprintf("source must be empty for kind %s", kind)}
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
	values := make([]string, len(storedKinds))
	for i, kind := range storedKinds {
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
