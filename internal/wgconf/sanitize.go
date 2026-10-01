package wgconf

import (
	"fmt"
	"regexp"
	"strings"
)

var poolNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,14}$`)

func SanitizePoolName(s string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(s))
	if !poolNameRe.MatchString(name) {
		return "", fmt.Errorf("pool name must match %s", poolNameRe.String())
	}
	return name, nil
}

func Slug(s string) string {
	base := strings.ToLower(strings.TrimSpace(s))
	if i := strings.LastIndex(base, "."); i > 0 {
		base = base[:i]
	}
	var b strings.Builder
	prevDash := true
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "conf"
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return strings.Trim(out, "-")
}

func UniqueSlug(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
	return base
}
