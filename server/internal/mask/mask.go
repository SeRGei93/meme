// Package mask removes credentials from captured text before it is persisted
// or sent to an LLM. It favours a small set of high-signal patterns so ordinary
// prose and source code survive unchanged.
package mask

import (
	"regexp"
	"strings"
)

// Redacted is the placeholder that replaces a detected secret.
const Redacted = "[REDACTED]"

var tokenPatterns = []*regexp.Regexp{
	// GitHub personal access tokens and app tokens.
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
	// GitLab personal access tokens.
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`),
	// AWS access key ids (long-lived and temporary).
	regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
	// Google API keys.
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`),
	// Slack tokens.
	regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}\b`),
	// npm and Hugging Face tokens.
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`),
	regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}\b`),
	// Stripe secret and restricted keys.
	regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b`),
	// OpenAI, Anthropic and compatible provider keys.
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
	// Second Brain client tokens.
	regexp.MustCompile(`\bsb_(?:opencode|jarvis|ui|admin)_[A-Za-z0-9_-]{20,}\b`),
	// JWTs: three base64url segments.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
}

var (
	privateKeyPattern = regexp.MustCompile(
		`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----.*?-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`,
	)
	unterminatedPrivateKeyPattern = regexp.MustCompile(
		`(?ms)-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----(?:\r?\n|$).*`,
	)
	authSchemePattern  = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+(?P<token>[A-Za-z0-9._~+/=-]{6,})`)
	urlPasswordPattern = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9+.-]*://[^\s/:@]*:)(?P<password>[^\s/@]+)(@)`)
	cliSecretPattern   = regexp.MustCompile(
		`(?i)(--(?:password|passwd|pwd|token|secret|api[-_]?key)(?:=|\s+))` +
			`(?P<value>"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|` + "`[^`]*`" + `|[^\s;&]+)`,
	)
	nextAssignmentPattern = regexp.MustCompile(`[ \t]+(?:--|[A-Za-z0-9_.-]+[ \t]*(?::=|=>|=|:))`)
	assignmentPattern     = regexp.MustCompile(
		`(?i)(["']?)(?P<key>(?:[A-Za-z0-9]+[_.-])*(?:password|passwd|pwd|secret|api[_-]?key|apikey|access[_-]?key|private[_-]?key|auth[_-]?token|access[_-]?token|refresh[_-]?token|token)|(?:[A-Za-z0-9]+[_.-])+pass)` +
			`(?:[_.-](?:value|secret|token|key|password|pwd))*` +
			`(["']?)(?P<separator>[ \t]*(?::=|=>|=|:)[ \t]*)` +
			`(?P<value>"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|` + "`[^`]*`" + `|[^\s;&]+)`,
	)
)

var (
	lowerCamelCase = regexp.MustCompile(`^[a-z]+(?:[A-Z][A-Za-z0-9]*)+$`)
	upperCamelCase = regexp.MustCompile(`^[A-Z][a-z0-9]+(?:[A-Z][A-Za-z0-9]*)+$`)
	dottedPath     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+$`)
)

var safeAssignmentValues = map[string]struct{}{
	"authentication": {},
	"authorization":  {},
	"boolean":        {},
	"changeme":       {},
	"choose":         {},
	"credentials":    {},
	"empty":          {},
	"error":          {},
	"example":        {},
	"expired":        {},
	"expires":        {},
	"false":          {},
	"invalid":        {},
	"missing":        {},
	"minimum":        {},
	"maximum":        {},
	"length":         {},
	"nil":            {},
	"none":           {},
	"null":           {},
	"number":         {},
	"placeholder":    {},
	"redacted":       {},
	"required":       {},
	"string":         {},
	"true":           {},
	"undefined":      {},
}

// Text returns value with detected secrets replaced by Redacted.
func Text(value string) string {
	value = privateKeyPattern.ReplaceAllString(value, Redacted)
	value = unterminatedPrivateKeyPattern.ReplaceAllString(value, Redacted)

	for _, pattern := range tokenPatterns {
		value = pattern.ReplaceAllString(value, Redacted)
	}

	value = maskAuthScheme(value)
	value = maskAssignments(value)
	value = maskCLISecrets(value)
	value = maskURLPasswords(value)
	return value
}

func maskAuthScheme(value string) string {
	return replaceGroup(value, authSchemePattern, "token", func(token string) (string, bool) {
		if !looksLikeAuthToken(token) {
			return "", false
		}
		return Redacted, true
	})
}

func looksLikeAuthToken(token string) bool {
	_, safe := safeAssignmentValues[strings.ToLower(token)]
	return !safe
}

func maskURLPasswords(value string) string {
	return replaceGroup(value, urlPasswordPattern, "password", func(password string) (string, bool) {
		if isTemplate(password) {
			return "", false
		}
		return Redacted, true
	})
}

func maskCLISecrets(value string) string {
	return replaceGroup(value, cliSecretPattern, "value", func(secret string) (string, bool) {
		if !shouldMaskAssignment("password", "=", secret) {
			return "", false
		}
		return maskedAssignmentValue(secret), true
	})
}

func maskAssignments(text string) string {
	keyGroup := assignmentPattern.SubexpIndex("key")
	separatorGroup := assignmentPattern.SubexpIndex("separator")
	valueGroup := assignmentPattern.SubexpIndex("value")
	if keyGroup < 0 || separatorGroup < 0 || valueGroup < 0 {
		return text
	}

	matches := assignmentPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}

	var builder strings.Builder
	last := 0
	for _, match := range matches {
		valueStart, valueEnd := match[valueGroup*2], match[valueGroup*2+1]
		if valueStart < last {
			continue
		}
		key := text[match[keyGroup*2]:match[keyGroup*2+1]]
		separator := text[match[separatorGroup*2]:match[separatorGroup*2+1]]
		assignment := text[valueStart:valueEnd]
		if !shouldMaskAssignment(key, separator, assignment) {
			continue
		}
		if isPasswordKey(key) {
			if _, _, quoted := unquote(assignment); !quoted {
				if _, _, trailing := trailingQuote(assignment); !trailing {
					valueEnd = extendPasswordValue(text, valueEnd)
					assignment = text[valueStart:valueEnd]
				}
			}
		}

		builder.WriteString(text[last:valueStart])
		builder.WriteString(maskedAssignmentValue(assignment))
		last = valueEnd
	}
	builder.WriteString(text[last:])
	return builder.String()
}

// extendPasswordValue keeps an unquoted passphrase together until the end of
// its line, but stops before another flag or assignment so those are masked
// independently.
func extendPasswordValue(text string, start int) int {
	if start >= len(text) || (text[start] != ' ' && text[start] != '\t') {
		return start
	}
	end := start
	for end < len(text) && text[end] != '\n' && text[end] != ';' && text[end] != '&' {
		end++
	}
	rest := text[start:end]
	if next := nextAssignmentPattern.FindStringIndex(rest); next != nil {
		end = start + next[0]
	}
	for end > start && (text[end-1] == ' ' || text[end-1] == '\t' || text[end-1] == '\r') {
		end--
	}
	return end
}

func shouldMaskAssignment(key, separator, value string) bool {
	separator = strings.TrimSpace(separator)
	if value == "" {
		return false
	}
	// `password == ""` and `password === null` are comparisons, not assignments.
	if strings.HasPrefix(value, "=") && (separator == "=" || separator == ":=" || separator == "=>") {
		return false
	}

	_, inner, quoted := unquote(value)
	if quoted {
		return maskQuotedValue(key, inner)
	}
	if inner, _, trailing := trailingQuote(value); trailing {
		return looksLikeSecretLiteral(inner)
	}
	return looksLikeSecretLiteral(value)
}

func maskQuotedValue(key, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || isTemplate(value) {
		return false
	}
	if !isPasswordKey(key) && looksLikeCodeReference(value) {
		return false
	}
	_, safe := safeAssignmentValues[strings.ToLower(value)]
	return !safe
}

func isPasswordKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "password") ||
		strings.Contains(key, "passwd") ||
		strings.Contains(key, "pwd") ||
		strings.Contains(key, "secret") ||
		strings.HasSuffix(key, "pass") ||
		strings.Contains(key, "_pass") ||
		strings.Contains(key, "-pass")
}

func looksLikeCodeReference(value string) bool {
	return lowerCamelCase.MatchString(value) ||
		upperCamelCase.MatchString(value) ||
		dottedPath.MatchString(value) ||
		strings.ContainsAny(value, "()")
}

func looksLikeSecretLiteral(value string) bool {
	if len(value) < 6 {
		return false
	}
	if _, safe := safeAssignmentValues[strings.ToLower(value)]; safe {
		return false
	}
	if isTemplate(value) {
		return false
	}
	if strings.ContainsAny(value, "(){}[]<>\"'`") {
		return false
	}
	if dottedPath.MatchString(value) ||
		lowerCamelCase.MatchString(value) ||
		upperCamelCase.MatchString(value) {
		return false
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"get_", "find_", "load_", "read_", "fetch_", "resolve_", "lookup_", "require_", "env_"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	return true
}

func isTemplate(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "$") || strings.HasPrefix(value, "%") ||
		strings.HasPrefix(value, "{{") || strings.HasPrefix(value, "<") {
		return true
	}
	return strings.Contains(value, "${") ||
		strings.Contains(value, "{{") ||
		strings.Contains(value, "%(")
}

func maskedAssignmentValue(value string) string {
	if quote, _, quoted := unquote(value); quoted {
		return string(quote) + Redacted + string(quote)
	}
	if _, quote, trailing := trailingQuote(value); trailing {
		return Redacted + string(quote)
	}
	return Redacted
}

func unquote(value string) (quote byte, inner string, ok bool) {
	if len(value) < 2 {
		return 0, "", false
	}
	switch value[0] {
	case '"', '\'', '`':
		if value[len(value)-1] == value[0] {
			return value[0], value[1 : len(value)-1], true
		}
	}
	return 0, "", false
}

func trailingQuote(value string) (inner string, quote byte, ok bool) {
	if len(value) < 2 {
		return "", 0, false
	}
	switch value[len(value)-1] {
	case '"', '\'', '`':
		return value[:len(value)-1], value[len(value)-1], true
	}
	return "", 0, false
}

// replaceGroup replaces one named capture group in every match. Returning false
// from replace leaves that match untouched.
func replaceGroup(value string, pattern *regexp.Regexp, group string, replace func(string) (string, bool)) string {
	groupIndex := pattern.SubexpIndex(group)
	if groupIndex < 0 {
		return value
	}
	matches := pattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value
	}

	var builder strings.Builder
	last := 0
	for _, match := range matches {
		start, end := match[groupIndex*2], match[groupIndex*2+1]
		replacement, ok := replace(value[start:end])
		if !ok {
			continue
		}
		builder.WriteString(value[last:start])
		builder.WriteString(replacement)
		last = end
	}
	builder.WriteString(value[last:])
	return builder.String()
}
