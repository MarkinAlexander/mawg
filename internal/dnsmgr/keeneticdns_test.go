package dnsmgr

import (
	"strings"
	"testing"
	"time"
)

// fake ndm: запоминает записи ip name-server; running-config отражает их
// (98-док: каждая запись верифицируется чтением - фейк это и эмулирует).
type fakeNdmc struct {
	servers  map[string]bool // ключ - args ("8.8.8.8 5353", `8.8.8.8 "" on ISP`)
	commands []string
	failNext  bool
	intercept bool
}

func newFakeNdmc() *fakeNdmc { return &fakeNdmc{servers: map[string]bool{}} }

func (f *fakeNdmc) run(script string, _ time.Duration) (string, error) {
	f.commands = append(f.commands, script)
	if f.failNext {
		f.failNext = false
		return "", errFakeShell
	}
	// скрипт - сегменты `ndmc -c '<rci>'`, склеенные " ; "
	for _, seg := range strings.Split(script, " ; ") {
		seg = strings.TrimSpace(seg)
		if !strings.HasPrefix(seg, "ndmc -c '") || !strings.HasSuffix(seg, "'") {
			return "", nil
		}
		cmd := strings.TrimSuffix(strings.TrimPrefix(seg, "ndmc -c '"), "'")
		switch {
		case cmd == "show running-config":
			var sb strings.Builder
			sb.WriteString("dns-proxy\n")
			if f.intercept {
				sb.WriteString("    intercept enable\n")
			}
			sb.WriteString("!\n")
			for a := range f.servers {
				sb.WriteString("ip name-server " + a + "\n")
			}
			sb.WriteString("ip no name-servers\n")
			return sb.String(), nil
		case cmd == "dns-proxy intercept enable":
			f.intercept = true
		case cmd == "dns-proxy no intercept enable":
			f.intercept = false
		case cmd == "system configuration save":
			// персист - просто фиксируем в commands
		case strings.HasPrefix(cmd, "ip name-server "):
			f.servers[strings.TrimSpace(strings.TrimPrefix(cmd, "ip name-server "))] = true
		case strings.HasPrefix(cmd, "no ip name-server "):
			delete(f.servers, strings.TrimSpace(strings.TrimPrefix(cmd, "no ip name-server ")))
		}
	}
	return "", nil
}

var errFakeShell = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "fake shell error" }

func TestNameServerArgs(t *testing.T) {
	cases := []struct {
		spare, iface, want string
		wantErr            bool
	}{
		{"8.8.8.8", "", "8.8.8.8", false},
		{"8.8.8.8", "ISP", `8.8.8.8 "" on ISP`, false},
		{"8.8.8.8:5353", "", "", true}, // порт: ndm второе поле читает как домен
		{"127.0.0.1", "", "", true},    // loopback: ndm не принимает (invalid IP address)
		{"dns.google", "", "", true},   // имя - ndm резолвить нечем
		{"::1", "", "", true},          // IPv6 - отдельная команда
		{"8.8.8.8", "IS'P", "", true},  // инъекция в ndmc
		{"", "", "", true},
	}
	for _, c := range cases {
		got, err := NameServerArgs(c.spare, c.iface)
		if c.wantErr {
			if err == nil {
				t.Fatalf("NameServerArgs(%q,%q): ждали ошибку, получили %q", c.spare, c.iface, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("NameServerArgs(%q,%q): %v", c.spare, c.iface, err)
		}
		if got != c.want {
			t.Fatalf("NameServerArgs(%q,%q) = %q, ждали %q", c.spare, c.iface, got, c.want)
		}
	}
}

func TestApplyKeeneticDNS(t *testing.T) {
	t.Run("spare ставит и верифицирует запись", func(t *testing.T) {
		f := newFakeNdmc()
		applied, err := ApplyKeeneticDNS(f.run, "spare", "8.8.8.8", "ISP", "")
		if err != nil {
			t.Fatal(err)
		}
		if applied != `8.8.8.8 "" on ISP` {
			t.Fatalf("applied = %q", applied)
		}
		if !strings.Contains(strings.Join(f.commands, "\n"), "system configuration save") {
			t.Fatalf("нет сохранения конфига: %v", f.commands)
		}
	})
	t.Run("mt снят - честная ошибка без записи в ndm", func(t *testing.T) {
		f := newFakeNdmc()
		_, err := ApplyKeeneticDNS(f.run, "mt", "", "", "")
		if err == nil {
			t.Fatal("режим mt должен возвращать ошибку (невыразим в ndm)")
		}
		if !strings.Contains(err.Error(), "снят") || len(f.commands) != 0 {
			t.Fatalf("err=%v commands=%v", err, f.commands)
		}
	})
	t.Run("спар с портом отвергается - второе поле ndm это домен", func(t *testing.T) {
		f := newFakeNdmc()
		_, err := ApplyKeeneticDNS(f.run, "spare", "8.8.8.8:5353", "", "")
		if err == nil {
			t.Fatal("порт должен отвергаться (ndm принял бы его как домен)")
		}
		if !strings.Contains(err.Error(), "домен") || len(f.commands) != 0 {
			t.Fatalf("err=%v commands=%v", err, f.commands)
		}
	})
	t.Run("loopback отвергается до вызова ndmc", func(t *testing.T) {
		f := newFakeNdmc()
		_, err := ApplyKeeneticDNS(f.run, "spare", "127.0.0.1", "", "")
		if err == nil {
			t.Fatal("loopback должен отвергаться (ndm: invalid IP address)")
		}
		if !strings.Contains(err.Error(), "loopback") || len(f.commands) != 0 {
			t.Fatalf("err=%v commands=%v", err, f.commands)
		}
	})
	t.Run("смена режима снимает прежнюю запись", func(t *testing.T) {
		f := newFakeNdmc()
		if _, err := ApplyKeeneticDNS(f.run, "spare", "8.8.8.8", "", ""); err != nil {
			t.Fatal(err)
		}
		applied, err := ApplyKeeneticDNS(f.run, "spare", "9.9.9.9", "", `8.8.8.8`)
		if err != nil {
			t.Fatal(err)
		}
		if applied != "9.9.9.9" || f.servers["8.8.8.8"] {
			t.Fatalf("прежняя запись жива: applied=%q servers=%v", applied, f.servers)
		}
	})
	t.Run("off снимает запись панели и только её", func(t *testing.T) {
		f := newFakeNdmc()
		f.servers["9.9.9.9"] = true // чужая запись пользователя - не наша
		if _, err := ApplyKeeneticDNS(f.run, "spare", "8.8.8.8", "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyKeeneticDNS(f.run, "off", "", "", `8.8.8.8`); err != nil {
			t.Fatal(err)
		}
		if f.servers["8.8.8.8"] || !f.servers["9.9.9.9"] {
			t.Fatalf("снятие задело не то: %v", f.servers)
		}
	})
	t.Run("Кинетик не принял запись - честная ошибка", func(t *testing.T) {
		f := newFakeNdmc()
		// фейк, который не запоминает (RCI отклонил): show running-config
		// вернёт пусто - верификация чтением поймает
		f.commands = nil
		f.servers = map[string]bool{}
		broken := func(script string, _ time.Duration) (string, error) {
			if strings.Contains(script, "show running-config") {
				return "", nil
			}
			return "", nil
		}
		if _, err := ApplyKeeneticDNS(broken, "spare", "8.8.8.8", "", ""); err == nil || !strings.Contains(err.Error(), "не применил") {
			t.Fatalf("ждали «не применил», получили %v", err)
		}
	})
}

func TestKeeneticDNSLive(t *testing.T) {
	f := newFakeNdmc()
	f.servers["8.8.8.8"] = true
	if !KeeneticDNSLive(f.run, "8.8.8.8") {
		t.Fatal("запись есть, live=false")
	}
	if KeeneticDNSLive(f.run, "9.9.9.9") {
		t.Fatal("записи нет, live=true")
	}
	// "ip no name-servers" не матчится префиксом "ip name-server"
	if KeeneticDNSLive(f.run, "") {
		t.Fatal("пустые args не матчатся")
	}
}

func TestNameServerPresent(t *testing.T) {
	// реальный конфиг Кинетика владельца (инцидент 01.11): глобальная
	// запись панели снята, но остались чужие per-interface записи с тем
	// же адресом - префиксный матч ловил их и «Убрать запись» вечно
	// ошибался, бейдж «живёт» висел на несуществующей записи
	config := "dns-proxy\n    tls upstream 1.1.1.1\n!\n" +
		"ip name-server 8.8.8.8 \"\" on Wireguard5\n" +
		"ip name-server 8.8.4.4 \"\" on Wireguard5\n" +
		"ip no name-servers\n"
	if nameServerPresent(config, "8.8.8.8") {
		t.Fatal("глобальной записи нет, а префиксный матч нашёл чужую per-interface")
	}
	if nameServerPresent(config, "8.8.4.4") {
		t.Fatal("чужая запись совпала")
	}
	config += "ip name-server 8.8.8.8\n"
	if !nameServerPresent(config, "8.8.8.8") {
		t.Fatal("точная глобальная запись не найдена")
	}
	if nameServerPresent(config, `8.8.8.8 "" on ISP`) {
		t.Fatal("запись с iface не должна матчить глобальную")
	}
}

func TestSetKeeneticIntercept(t *testing.T) {
	f := newFakeNdmc()
	if err := SetKeeneticIntercept(f.run, true); err != nil {
		t.Fatal(err)
	}
	if !f.intercept {
		t.Fatal("перехват не включился")
	}
	cfg, err := KeeneticRunningConfig(f.run)
	if err != nil || !ParseKeeneticOwn(cfg).Intercept {
		t.Fatalf("парсер не увидел перехват: %v", err)
	}
	if err := SetKeeneticIntercept(f.run, false); err != nil {
		t.Fatal(err)
	}
	cfg, _ = KeeneticRunningConfig(f.run)
	if ParseKeeneticOwn(cfg).Intercept {
		t.Fatal("перехват не выключился")
	}
}

func TestParseKeeneticOwnIntercept(t *testing.T) {
	with := ParseKeeneticOwn("dns-proxy\n    rebind-protect auto\n    intercept enable\n    filter profile DnsProfile0 intercept enable\n!")
	if !with.Intercept {
		t.Fatal("глобальный intercept не увиден")
	}
	without := ParseKeeneticOwn("dns-proxy\n    filter profile DnsProfile0 intercept enable\n!")
	if without.Intercept {
		t.Fatal("профильный intercept принят за глобальный")
	}
}
