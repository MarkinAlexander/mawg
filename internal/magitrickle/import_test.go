package magitrickle

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseImportURLToHost(t *testing.T) {
	url := "https://www.ozon.ru/category/kryshki/?text=%D0%BA%D1%80&from_global=true"
	rs, bad, _ := ParseImport(url, "auto", true, false)
	if bad != 0 || len(rs) != 1 || rs[0].Type != "namespace" || rs[0].Rule != "www.ozon.ru" {
		t.Fatalf("got %+v bad=%d", rs, bad)
	}
}

func TestParseImportReduceToSecond(t *testing.T) {
	cases := map[string]string{
		"https://www.ozon.ru/x?q=1": "ozon.ru",
		"www.ozon.ru":               "ozon.ru",
		"a.b.ozon.ru":               "ozon.ru",
		"bbc.co.uk":                 "bbc.co.uk",
		"news.bbc.co.uk":            "bbc.co.uk",
		"ozon.ru":                   "ozon.ru",
		"ftp://files.example.com/f": "example.com",
	}
	for in, want := range cases {
		rs, bad, _ := ParseImport(in, "auto", true, true)
		if bad != 0 || len(rs) != 1 || rs[0].Rule != want {
			t.Fatalf("%q: got %+v bad=%d, want %s", in, rs, bad, want)
		}
	}
}

func TestParseImportIPAlwaysSubnet(t *testing.T) {
	for _, in := range []string{"149.154.160.0/20", "1.2.3.4", "2001:db8::1"} {
		rs, bad, _ := ParseImport(in, "auto", true, true)
		if bad != 0 || len(rs) != 1 || rs[0].Type != "subnet" || rs[0].Rule != in {
			t.Fatalf("%q: got %+v bad=%d", in, rs, bad)
		}
	}
	rs, _, _ := ParseImport("1.2.3.4", "domain", false, false)
	if len(rs) != 1 || rs[0].Type != "subnet" {
		t.Fatalf("IP при типе domain: %+v", rs)
	}
}

func TestParseImportTypesAndCleanup(t *testing.T) {
	text := "# комментарий\n" +
		"example.com, test.org; third.net\n" +
		"https://xn--80akahcmtlb0a.xn--p1ai/path\n" +
		"не_домен!\n" +
		"*.wild.com\n" +
		"host.example.com:443\n" +
		"example.com\n"
	rs, bad, _ := ParseImport(text, "auto", true, false)
	var got []string
	for _, r := range rs {
		got = append(got, r.Type+":"+r.Rule)
	}
	want := []string{
		"namespace:example.com", "namespace:test.org", "namespace:third.net",
		"namespace:xn--80akahcmtlb0a.xn--p1ai",
		"wildcard:*.wild.com", "namespace:host.example.com",
	}
	if bad != 1 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v bad=%d, want %v bad=1", got, bad, want)
	}
}

func TestParseImportDedupAndFixedType(t *testing.T) {
	rs, bad, _ := ParseImport("a.com\na.com\n b.com ,", "domain", false, false)
	if bad != 0 || len(rs) != 2 || rs[0].Type != "domain" || rs[1].Rule != "b.com" {
		t.Fatalf("got %+v bad=%d", rs, bad)
	}
}

func TestParseImportRegexLines(t *testing.T) {
	rs, bad, _ := ParseImport("^[a-z]+\\.example\\.com$\n^(www\\.)?test\\.org", "regex", true, true)
	if bad != 0 || len(rs) != 2 || rs[0].Type != "regex" || rs[0].Rule != "^[a-z]+\\.example\\.com$" {
		t.Fatalf("got %+v bad=%d", rs, bad)
	}
}

func TestParseImportSubnetTypeRejectsDomains(t *testing.T) {
	rs, bad, _ := ParseImport("example.com\n1.2.3.4", "subnet", true, false)
	if bad != 1 || len(rs) != 1 || rs[0].Rule != "1.2.3.4" {
		t.Fatalf("got %+v bad=%d", rs, bad)
	}
}

func TestParseImportRegexRejectsURLs(t *testing.T) {
	rs, bad, reasons := ParseImport("https://example.com/x\n^site\\.com$\nexample.org", "regex", true, false)
	if len(rs) != 1 || rs[0].Rule != "^site\\.com$" {
		t.Fatalf("got %+v", rs)
	}
	if bad != 2 || len(reasons) == 0 || !strings.Contains(reasons[0], "исправьте регулярное выражение") {
		t.Fatalf("bad=%d reasons=%v", bad, reasons)
	}
}

func TestParseImportBadSamples(t *testing.T) {
	_, bad, reasons := ParseImport("не_домен!\n%%%\nexample.com", "auto", true, false)
	if bad != 2 || len(reasons) != 2 || !strings.Contains(reasons[0], "не_домен!") {
		t.Fatalf("bad=%d reasons=%v", bad, reasons)
	}
}

func TestParseImportSubnetV6(t *testing.T) {
	rs, bad, _ := ParseImport("2001:db8::/32\n2001:db8::1\nexample.com", "subnet", true, false)
	if bad != 1 || len(rs) != 2 || rs[0].Rule != "2001:db8::/32" || rs[1].Rule != "2001:db8::1" {
		t.Fatalf("got %+v bad=%d", rs, bad)
	}
}

func TestNormalizeRule(t *testing.T) {
	if r, ok := NormalizeRule("namespace", "https://example.com/path?q=1", false); !ok || r.Rule != "example.com" {
		t.Fatalf("got %+v ok=%v", r, ok)
	}
	if _, ok := NormalizeRule("regex", "https://example.com", false); ok {
		t.Fatal("URL в регулярке должен отклоняться")
	}
}
