package singbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Сироты движков (112-док 3.6/4.4): детект по фейковому /proc - comm,
// cmdline-маркер нашего экземпляра, держатели /dev/net/tun.

// fakeProc - каталог с процессами: pid -> {comm, cmdline, fd-симлинки}.
func fakeProc(t *testing.T, procs map[int]string, fds map[int][]string) string {
	t.Helper()
	dir := t.TempDir()
	oldProc, oldReadlink := procDir, readlinkFn
	procDir = dir
	// симлинки на Windows требуют привилегий - readlink подменяем таблицей
	links := map[string]string{}
	readlinkFn = func(p string) (string, error) {
		if s, ok := links[p]; ok {
			return s, nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { procDir, readlinkFn = oldProc, oldReadlink })

	for pid, cmdline := range procs {
		pdir := filepath.Join(dir, fmt.Sprint(pid))
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			t.Fatal(err)
		}
		argv := strings.Fields(cmdline)
		comm := argv[0]
		if i := strings.LastIndexByte(comm, '/'); i >= 0 {
			comm = comm[i+1:]
		}
		if err := os.WriteFile(filepath.Join(pdir, "comm"), []byte(comm), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pdir, "cmdline"), []byte(strings.Join(argv, "\x00")+"\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
		fdir := filepath.Join(pdir, "fd")
		if err := os.MkdirAll(fdir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i, tgt := range fds[pid] {
			if err := os.WriteFile(filepath.Join(fdir, fmt.Sprint(i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			links[filepath.Join(fdir, fmt.Sprint(i))] = tgt
		}
		links[filepath.Join(pdir, "exe")] = argv[0]
	}
	return dir
}

func TestOurEngineProcs(t *testing.T) {
	fakeProc(t, map[int]string{
		100: "/opt/bin/sing-box run -c /opt/etc/mawg/singbox/config.json",
		101: "/opt/bin/sing-box run -c /opt/etc/sing-box/config.json", // чужой сервис (shared)
		102: "/opt/bin/sing-box run -c /opt/etc/mawg/singbox/config.json",
		103: "/opt/bin/mihomo -d /tmp/mihomo-mawg -f /etc/mawg/mihomo/config.yaml",
	}, nil)

	ours := OurEngineProcs("sing-box", "/opt/etc/mawg/singbox/config.json", 0)
	if len(ours) != 2 {
		t.Fatalf("наших процессов %d (%v), хочу 2 (100, 102)", len(ours), DescribeProcs(ours))
	}
	for _, p := range ours {
		if p.PID != 100 && p.PID != 102 {
			t.Fatalf("чужой процесс попал в наши: %v", p)
		}
	}
	// свой pid исключается
	ours = OurEngineProcs("sing-box", "/opt/etc/mawg/singbox/config.json", 102)
	if len(ours) != 1 || ours[0].PID != 100 {
		t.Fatalf("exclude не работает: %v", DescribeProcs(ours))
	}
	// mihomo ищется по своему маркеру
	mh := OurEngineProcs("mihomo", "/tmp/mihomo-mawg", 0)
	if len(mh) != 1 || mh[0].PID != 103 {
		t.Fatalf("mihomo-процессы: %v", DescribeProcs(mh))
	}
}

func TestTunHolderProcs(t *testing.T) {
	fakeProc(t, map[int]string{
		100: "/opt/bin/sing-box run -c /opt/etc/mawg/singbox/config.json",
		101: "/opt/bin/wireguard-go tun0",
	}, map[int][]string{
		100: {"/dev/net/tun", "/dev/null"},
		101: {"/dev/net/tun"},
	})

	holders := TunHolderProcs(0)
	if len(holders) != 2 {
		t.Fatalf("держателей tun %d (%s), хочу 2", len(holders), DescribeProcs(holders))
	}
	ours := TunHolderProcs(100)
	if len(ours) != 1 || ours[0].PID != 101 || ours[0].Comm != "wireguard-go" {
		t.Fatalf("исключение своего pid: %v", DescribeProcs(ours))
	}
}
