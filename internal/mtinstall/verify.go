package mtinstall

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Послестартовая проверка демона. Init-скрипт отчитывается мгновенно, а
// демон может не подняться вовсе (openwrt-скрипт пакета молча выходит при
// uci magitrickle.main.enabled != 1) или падать при старте: procd тогда
// перезапускает его через 5с и после 5 падений бросает («in a crash
// loop») - служба мертва. Причину демон пишет сам (procd шлёт её в
// logread), но установка о ней молчала: жалоба 24.11 (OpenWrt
// aarch64_cortex-a53, v0.8.12.1) - «демон завершился сразу после старта»
// без единого слова о причине. Кольцо logread мало и забивается
// DNS-debugом демона, поэтому причину берём не из журнала, а запуском
// бинаря вручную.

var (
	startWait = 10 * time.Second // появление демона после start (обычно <1с)
	stopWait  = 7 * time.Second  // смерть по TERM: procd добивает на 5-й секунде
	deathWait = 3 * time.Second  // смерть после kill -9
	probeWait = 6 * time.Second  // сколько демону жить в ручном запуске
	pollEvery = 250 * time.Millisecond
)

// daemonCtl - служба и бинарь MT после установки.
type daemonCtl struct {
	platform   string
	initScript string
	bin        string
	run        func(string, ...string) ([]byte, error)
	step       func(string, ...any)
}

func until(cond func() bool, max time.Duration) bool {
	deadline := time.Now().Add(max)
	for {
		if cond() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(pollEvery)
	}
}

func noDaemon() bool { return len(magitricklePids()) == 0 }

// gone - дождаться смерти демона после stop. procd-стоп мгновенен: TERM
// ушёл, процесс догасает до term_timeout (5с), а postinst пакета стартует
// службу сам - старт поверх живого старичка даёт двойной рестарт (0.62,
// 24.10.8: новый инстанс поднялся лишь после SIGKILL старого и тут же
// получил TERM). Не вышел за stopWait - добить.
func (d daemonCtl) gone() {
	if until(noDaemon, stopWait) {
		return
	}
	for pid := range magitricklePids() {
		d.step("демон (pid %d) не вышел по TERM за %s - добиваю", pid, stopWait)
		_, _ = d.run("kill", "-9", strconv.Itoa(pid))
	}
	if !until(noDaemon, deathWait) {
		d.step("процесс magitrickled жив и после kill -9 - продолжаю")
	}
}

// settled - демон появился после start и жив ещё postStartDelay (падающий
// при старте умирает в первую секунду).
func (d daemonCtl) settled() bool {
	if !until(func() bool { return !noDaemon() }, startWait) {
		return false
	}
	time.Sleep(postStartDelay)
	return !noDaemon()
}

// diagnose - демон службой не поднят: вернуть его последние слова.
func (d daemonCtl) diagnose() error {
	// openwrt-скрипт пакета при uci enabled != 1 молча не стартует
	// ничего, start возвращает успех. Секции может не быть вовсе: после
	// порчи opkg сохраняет пустой conffile, а версию пакета кладёт рядом
	// с суффиксом -opkg (роутер из жалобы 24.11); set без секции отвечает
	// «Invalid argument», commit без файла - «Entry not found»
	if d.platform != "keenetic" {
		out, err := d.run("uci", "-q", "get", "magitrickle.main.enabled")
		if enabled := strings.TrimSpace(string(out)); err != nil || enabled != "1" {
			d.step("служба MT выключена в uci (magitrickle.main.enabled=%q) - включаю и запускаю", enabled)
			_, _ = d.run("touch", "/etc/config/magitrickle")
			_, _ = d.run("uci", "set", "magitrickle.main=magitrickle")
			_, _ = d.run("uci", "set", "magitrickle.main.enabled=1")
			_, _ = d.run("uci", "set", "magitrickle.main.user=root")
			_, _ = d.run("uci", "commit", "magitrickle")
			_, _ = d.run(d.initScript, "start")
			if d.settled() {
				return nil
			}
		}
	}

	// служба гасится, чтобы procd не поднял экземпляр параллельно
	// ручному: тот умрёт от занятых порта и pid-файла, а не от своей ошибки
	_, _ = d.run(d.initScript, "stop")
	d.gone()
	d.step("демон службой не поднят - запускаю бинарь вручную, чтобы увидеть причину")
	out, alive := daemonProbe(d.bin, probeWait, d.platform)
	_, _ = d.run(d.initScript, "start")
	for _, l := range tailLines(out, 6) {
		d.step("демон: %s", l)
	}
	// после повторного запуска демон жив и стабилен - цель достигнута,
	// слова ручного запуска остаются в журнале (экземпляр мог подняться
	// и мимо нас: самолечение движка рестартит MT и во время установки)
	if d.settled() {
		d.step("демон поднялся после повторного запуска службы")
		return nil
	}
	if alive {
		return fmt.Errorf("демон завершился сразу после старта: вручную бинарь работает, службой не поднимается - смотрите журнал роутера (logread)")
	}
	words := lastWords(out)
	if words == "" {
		words = "демон не успел ничего вывести"
	}
	return fmt.Errorf("демон завершился сразу после старта: %s", words)
}

// daemonProbe - бинарь MT на форграунд на wait: то, что валит демона, он
// пишет в stdout сам. alive - дожил до конца окна. Подмена в тестах.
var daemonProbe = foregroundProbe

func foregroundProbe(bin string, wait time.Duration, platform string) (string, bool) {
	cmd := exec.Command(bin)
	cmd.Dir = "/"
	cmd.Env = probeEnv(platform)
	buf := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = buf, buf
	if err := cmd.Start(); err != nil {
		return err.Error(), false
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	select {
	case <-exited:
		return buf.String(), false
	case <-time.After(wait):
	}
	// по Interrupt демон снимает свои правила; KILL только если не успел
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case <-exited:
	case <-time.After(deathWait):
		_ = cmd.Process.Kill()
		<-exited
	}
	return buf.String(), true
}

// probeEnv - окружение init-скрипта: Кинетик ищет iptables в /opt/sbin
// (PATH из S99magitrickle), процессу mawg этих путей может не достаться.
func probeEnv(platform string) []string {
	if platform != "keenetic" {
		return nil
	}
	return append(os.Environ(), "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/opt/sbin:/opt/bin")
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// tailLines - последние строки вывода демона без цвета и DNS-debugа.
func tailLines(out string, n int) []string {
	var lines []string
	for _, l := range strings.Split(ansiRe.ReplaceAllString(out, ""), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, " DBG ") {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// lastWords - строка, объясняющая смерть: последняя с уровнем ошибки,
// иначе просто последняя.
func lastWords(out string) string {
	lines := tailLines(out, 40)
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if strings.Contains(l, " ERR ") || strings.Contains(l, " FTL ") || strings.Contains(l, "panic") {
			return clip(l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return clip(lines[len(lines)-1])
}

const wordsMax = 300

func clip(s string) string {
	r := []rune(s)
	if len(r) <= wordsMax {
		return s
	}
	return string(r[:wordsMax]) + "…"
}
