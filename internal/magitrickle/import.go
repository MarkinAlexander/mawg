package magitrickle

import (
	"net"
	"regexp"
	"strings"
)

var importSchemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)

var importDomainRe = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// двухчастные публичные суффиксы: усечение до второго уровня оставляет три
// метки, чтобы bbc.co.uk не превращался в co.uk
var secondLevelZones = map[string]bool{
	"co.uk": true, "org.uk": true, "me.uk": true, "ac.uk": true, "gov.uk": true,
	"com.au": true, "net.au": true, "org.au": true,
	"co.jp": true, "ne.jp": true, "or.jp": true,
	"com.tr": true, "com.br": true, "com.cn": true, "com.mx": true,
	"co.in": true, "co.nz": true, "co.za": true, "com.ua": true, "co.il": true,
}

const maxImportRules = 5000

// ParseImport: вставленный текст -> правила, число отброшенных строк и
// до трёх причин отбраковки.
func ParseImport(text, typ string, stripURL, toSecond bool) ([]Rule, int, []string) {
	if typ == "" {
		typ = "auto"
	}
	seen := map[string]bool{}
	var out []Rule
	skipped := 0
	var bad []string
	note := func(reason string) {
		if len(bad) < 3 {
			bad = append(bad, reason)
		}
	}
	add := func(r Rule) bool {
		key := r.Type + " " + r.Rule
		if seen[key] {
			return true
		}
		seen[key] = true
		out = append(out, r)
		return len(out) < maxImportRules
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if typ == "regex" {
			v := strings.Trim(line, "\"'`")
			if v == "" {
				skipped++
				continue
			}
			if strings.Contains(v, "://") || importDomainRe.MatchString(strings.ToLower(v)) {
				note(v + " - похоже на URL или домен, исправьте регулярное выражение")
				skipped++
				continue
			}
			if !add(Rule{Type: "regex", Rule: v}) {
				break
			}
			continue
		}
		stop := false
		for _, tok := range strings.FieldsFunc(line, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ',' || r == ';'
		}) {
			if r, ok := makeEntry(tok, typ, stripURL, toSecond); ok {
				if !add(r) {
					stop = true
					break
				}
			} else {
				note(tok + " - не похоже на " + typeLabel(typ))
				skipped++
			}
		}
		if stop {
			break
		}
	}
	return out, skipped, bad
}

func typeLabel(typ string) string {
	switch typ {
	case "subnet":
		return "подсеть (IP или IP/маска)"
	case "domain":
		return "домен"
	case "wildcard":
		return "маска"
	}
	return "домен"
}

func makeEntry(tok, typ string, stripURL, toSecond bool) (Rule, bool) {
	tok = strings.Trim(tok, "\"'`")
	if tok == "" {
		return Rule{}, false
	}
	if i := strings.LastIndex(tok, "@"); i >= 0 {
		tok = tok[i+1:]
	}
	if net.ParseIP(tok) != nil {
		return Rule{Type: "subnet", Rule: tok}, true
	}
	if _, _, err := net.ParseCIDR(tok); err == nil {
		return Rule{Type: "subnet", Rule: tok}, true
	}
	if typ == "subnet" {
		return Rule{}, false
	}
	host := tok
	if stripURL {
		host = importSchemeRe.ReplaceAllString(host, "")
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = strings.Trim(h, "[]")
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return Rule{}, false
	}
	if net.ParseIP(host) != nil {
		return Rule{Type: "subnet", Rule: host}, true
	}
	if strings.ContainsAny(host, "*?") {
		switch typ {
		case "domain", "regex":
			return Rule{}, false
		default:
			return Rule{Type: "wildcard", Rule: host}, true
		}
	}
	if toSecond {
		host = reduceToSecond(host)
	}
	if !importDomainRe.MatchString(host) {
		return Rule{}, false
	}
	switch typ {
	case "domain":
		return Rule{Type: "domain", Rule: host}, true
	case "wildcard":
		return Rule{Type: "wildcard", Rule: host}, true
	default:
		return Rule{Type: "namespace", Rule: host}, true
	}
}

func reduceToSecond(host string) string {
	labels := strings.Split(host, ".")
	if len(labels) <= 2 {
		return host
	}
	last2 := strings.Join(labels[len(labels)-2:], ".")
	if secondLevelZones[last2] {
		if len(labels) <= 3 {
			return host
		}
		return strings.Join(labels[len(labels)-3:], ".")
	}
	return last2
}

func NormalizeRule(typ, value string, toSecond bool) (Rule, bool) {
	rules, _, _ := ParseImport(value, typ, true, toSecond)
	if len(rules) == 0 {
		return Rule{}, false
	}
	return rules[0], true
}
