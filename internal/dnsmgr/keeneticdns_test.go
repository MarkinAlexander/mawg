package dnsmgr

import (
	"strings"
	"testing"
	"time"
)

// fake ndm: запоминает записи ip name-server; running-config отражает их
// (98-док: каждая запись верифицируется чтением - фейк это и эмулирует).
type fakeNdmc struct {
	servers map[string]bool // ключ - args ("8.8.8.8 5353", `8.8.8.8 "" on ISP`)
	commands []string
	failNext bool
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
			for a := range f.servers {
				sb.WriteString("    ip name-server " + a + "\n")
			}
			sb.WriteString("    ip no name-servers\n")
			return sb.String(), nil
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
		{"8.8.8.8:5353", "", "8.8.8.8 5353", false},
		{"8.8.8.8", "ISP", `8.8.8.8 "" on ISP`, false},
		{"dns.google", "", "", true},  // имя - ndm резолвить нечем
		{"::1", "", "", true},         // IPv6 - отдельная команда
		{"8.8.8.8", "IS'P", "", true}, // инъекция в ndmc
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
	t.Run("mt - 127.0.0.1 3553", func(t *testing.T) {
		f := newFakeNdmc()
		applied, err := ApplyKeeneticDNS(f.run, "mt", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if applied != ndmMTArgs || !f.servers[ndmMTArgs] {
			t.Fatalf("applied=%q servers=%v", applied, f.servers)
		}
	})
	t.Run("смена режима снимает прежнюю запись", func(t *testing.T) {
		f := newFakeNdmc()
		if _, err := ApplyKeeneticDNS(f.run, "spare", "8.8.8.8", "", ""); err != nil {
			t.Fatal(err)
		}
		applied, err := ApplyKeeneticDNS(f.run, "mt", "", "", `8.8.8.8`)
		if err != nil {
			t.Fatal(err)
		}
		if applied != ndmMTArgs || f.servers["8.8.8.8"] {
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
		if _, err := ApplyKeeneticDNS(broken, "spare", "8.8.8.8", "", ""); err == nil || !strings.Contains(err.Error(), "не принял") {
			t.Fatalf("ждали «не принял», получили %v", err)
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
	config := "dns-proxy\n    tls upstream 1.1.1.1\n!\n" +
		"ip name-server 8.8.8.8 \"\" on ISP\n" +
		"ip no name-servers\n"
	if !nameServerPresent(config, `8.8.8.8 "" on ISP`) {
		t.Fatal("точная запись не найдена")
	}
	if nameServerPresent(config, "8.8.4.4") {
		t.Fatal("чужая запись совпала")
	}
}
