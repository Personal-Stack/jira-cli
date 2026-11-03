package generator

import (
	"regexp"
	"strings"
	"unicode"
)

// splitWords splits identifiers such as "getIssueLinkTypes", "getURLForX",
// "email-templates" or "universal_avatar" into lower-case words.
func splitWords(s string) []string {
	var words []string
	var cur []rune
	rs := []rune(s)
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r):
			// boundary before an upper-case letter that follows a lower-case
			// letter/digit, or that starts a new word after an acronym (URLFor).
			if len(cur) > 0 {
				prev := rs[i-1]
				nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					flush()
				}
			}
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

// kebab converts an identifier to kebab-case.
func kebab(s string) string { return strings.Join(splitWords(s), "-") }

var numericSuffixRe = regexp.MustCompile(`_\d+$`)

// trimSuffix removes the "_N" de-duplication suffix that Java OpenAPI
// generators append to operationIds.
func trimSuffix(opID string) string { return numericSuffixRe.ReplaceAllString(opID, "") }

// commandName derives the CLI command name from an operationId by removing
// the tag noun: tag "issue" + "getIssue" -> "get"; tag "issueLinkType" +
// "getIssueLinkTypes" -> "get". If nothing is left, the full name is kept.
func commandName(opID, tag string) string {
	w := splitWords(trimSuffix(opID))
	t := splitWords(tag)
	if len(t) == 0 || len(t) >= len(w) {
		return strings.Join(w, "-")
	}
	for i := 0; i+len(t) <= len(w); i++ {
		match := true
		for j := range t {
			a, b := w[i+j], t[j]
			if j == len(t)-1 {
				// tolerate singular/plural differences on the last word
				a, b = strings.TrimSuffix(a, "s"), strings.TrimSuffix(b, "s")
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			rest := append(append([]string{}, w[:i]...), w[i+len(t):]...)
			if len(rest) == 0 {
				break
			}
			return strings.Join(rest, "-")
		}
	}
	return strings.Join(w, "-")
}

// fullName is the fallback when the short name collides.
func fullName(opID string) string { return kebab(trimSuffix(opID)) }

// PackageName converts a profile name into a Go package name:
// "datacenter-10.3" -> "datacenter103".
func PackageName(profile string) string {
	var b strings.Builder
	for _, r := range profile {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// reservedCommands are top-level commands implemented by hand; generated
// groups must not shadow them.
var reservedCommands = map[string]bool{
	"auth": true, "config": true, "spec": true, "commands": true, "capabilities": true,
	"doctor": true, "help": true, "completion": true, "certs": true,
}

// reservedFlags are global or executor flags; generated parameter flags must
// not collide with them.
var reservedFlags = map[string]bool{
	"instance": true, "output": true, "verbose": true, "quiet": true, "no-color": true,
	"timeout": true, "dry-run": true, "config": true, "file": true, "stdin": true,
	"all": true, "max-pages": true, "yes": true, "help": true, "read-only": true,
	"content-type": true, "version": true,
}
