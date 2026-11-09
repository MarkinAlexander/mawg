package dnsmgr

import (
	"strings"
	"testing"
	"time"
)

func TestParseKeeneticHosts(t *testing.T) {
	cfg := `ip host ntc.party 192.0.2.28
ip host www.instagram.com 192.0.2.174
ip host www.instagram.com 192.0.2.175
ipv6 host my.box fd00::10
ip no name-servers
`
	// ndm допускает несколько ip host на одно имя - парсер сливает по
	// имени, последний IP выигрывает; ipv6 host - отдельная запись имени
	out := ParseKeeneticHosts(cfg)
	if len(out) != 3 {
		t.Fatalf("записей %d, want 3: %+v", len(out), out)
	}
	byName := map[string]HostEntry{}
	for _, e := range out {
		byName[e.Name] = e
	}
	ntc := byName["ntc.party"]
	if ntc.IPv4 != "192.0.2.28" || ntc.Source != "ndm" || ntc.IPv6 != "" {
		t.Fatalf("ntc.party: %+v", ntc)
	}
	ig := byName["www.instagram.com"]
	if ig.IPv4 != "192.0.2.175" {
		t.Fatalf("instagram: %+v (последний IP)", ig)
	}
	box := byName["my.box"]
	if box.IPv6 != "fd00::10" || box.IPv4 != "" {
		t.Fatalf("my.box: %+v (ipv6 host - отдельно)", box)
	}
}

func TestParseHostsFile(t *testing.T) {
	content := `# комментарий
127.0.0.1 localhost
192.168.1.10 nas.home nas
fd00::10 nas.home
мусор без ip
`
	out := parseHostsFile(content, "hosts")
	var nas4, nas6 *HostEntry
	for i := range out {
		if out[i].Name == "nas.home" && out[i].IPv4 != "" {
			nas4 = &out[i]
		}
		if out[i].Name == "nas.home" && out[i].IPv6 != "" {
			nas6 = &out[i]
		}
	}
	if nas4 == nil || nas4.IPv4 != "192.168.1.10" || nas4.Source != "hosts" {
		t.Fatalf("nas.home v4: %+v", out)
	}
	if nas6 == nil || nas6.IPv6 != "fd00::10" {
		t.Fatalf("nas.home v6: %+v", out)
	}
}

func TestValidateHostNameAndIP(t *testing.T) {
	if n, err := ValidateHostName("  Dzen.RU. "); err != nil || n != "dzen.ru" {
		t.Fatalf("нормализация: %q %v", n, err)
	}
	if _, err := ValidateHostName("две слова"); err == nil {
		t.Fatal("пробелы должны отвергаться")
	}
	if _, err := ValidateHostName(""); err == nil {
		t.Fatal("пустое имя отвергается")
	}
	if v, err := ValidateHostIP(" 192.168.1.1 ", false); err != nil || v != "192.168.1.1" {
		t.Fatalf("v4: %q %v", v, err)
	}
	if _, err := ValidateHostIP("fd00::10", false); err == nil {
		t.Fatal("IPv6 в поле v4 отвергается")
	}
	if _, err := ValidateHostIP("", true); err != nil {
		t.Fatalf("пустой IP легален: %v", err)
	}
}

// фейковый ndm: держит ip host записи, running-config отражает их
type fakeNdmcHosts struct {
	hosts map[string]string
	saved []string
}

func (f *fakeNdmcHosts) run(script string, timeout time.Duration) (string, error) {
	if strings.Contains(script, "show running-config") {
		var b strings.Builder
		for name, ip := range f.hosts {
			b.WriteString("ip host " + name + " " + ip + "\n")
		}
		return b.String(), nil
	}
	f.saved = append(f.saved, script)
	if strings.HasPrefix(script, "ndmc -c 'ip host ") || strings.HasPrefix(script, "ndmc -c 'no ip host ") {
		cmd := strings.TrimPrefix(strings.Split(script, " ; ")[0], "ndmc -c '")
		cmd = strings.TrimSuffix(cmd, "'")
		fields := strings.Fields(cmd)
		switch {
		case fields[0] == "ip" && fields[1] == "host" && len(fields) == 4:
			f.hosts[fields[2]] = fields[3]
		case fields[0] == "ip" && fields[1] == "host" && len(fields) == 4 && fields[2] == "no":
			// no-форма ниже
		case fields[0] == "no" && len(fields) == 4 && fields[1] == "ip" && fields[2] == "host":
			delete(f.hosts, fields[3])
		}
	}
	return "", nil
}

func TestApplyKeeneticHosts(t *testing.T) {
	f := &fakeNdmcHosts{hosts: map[string]string{"old.example": "1.1.1.1", "same.example": "192.0.2.9"}}
	prev := []HostWant{{Name: "old.example", IPv4: "1.1.1.1"}, {Name: "same.example", IPv4: "192.0.2.9"}}
	want := []HostWant{{Name: "same.example", IPv4: "192.0.2.9"}, {Name: "new.example", IPv4: "8.8.8.8"}}
	if err := ApplyKeeneticHosts(f.run, want, prev); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.hosts["old.example"]; ok {
		t.Fatal("исчезнувшая запись не удалена")
	}
	if f.hosts["new.example"] != "8.8.8.8" {
		t.Fatalf("новая запись не поставлена: %v", f.hosts)
	}
	if f.hosts["same.example"] != "192.0.2.9" {
		t.Fatalf("неизменная запись тронута: %v", f.hosts)
	}
	if _, ok := f.hosts["same.example2"]; ok {
		t.Fatal("лишнее")
	}
}
