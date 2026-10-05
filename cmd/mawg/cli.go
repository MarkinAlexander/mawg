package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"mawg/internal/auth"
	"mawg/internal/selfupdate"
	"mawg/internal/store"
)

// initScript - скрипт управления сервисом на платформе.
func initScript(plat string) string {
	if plat == store.PlatformKeenetic {
		return "/opt/etc/init.d/S99mawg"
	}
	return "/etc/init.d/mawg"
}

func runServiceCmd(plat, action string) int {
	script := initScript(plat)
	if _, err := os.Stat(script); err != nil {
		fmt.Fprintf(os.Stderr, "init-скрипт не найден: %s\n", script)
		return 1
	}
	cmd := exec.Command(script, action)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("mawg: %s ok\n", action)
	return 0
}

// cmdStatus показывает живость демона и снимок состояния панелью на 127.0.0.1.
func cmdStatus(plat, dir string, portOverride int) int {
	script := initScript(plat)
	alive := false
	if out, err := exec.Command(script, "status").CombinedOutput(); err == nil {
		low := strings.ToLower(string(out))
		// keenetic echo'ит running/stopped; procd на openwrt молчит у живого
		alive = strings.Contains(low, "running") || !strings.Contains(low, "stopped")
	}
	status := "stopped"
	if alive {
		status = "running"
	}

	st, err := store.Open(dir)
	port := portOverride
	if port == 0 {
		_, p, _, _ := st.ServerSettings()
		port = p
	}
	fmt.Printf("mawg %s: %s, panel http://127.0.0.1:%d\n", version, status, port)
	if err != nil || !alive {
		return 0
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/status", port), nil)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("панель не отвечает:", err)
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		fmt.Println("панель требует входа (авторизация включена)")
		return 0
	}
	var data struct {
		Pools []struct {
			Name       string `json:"name"`
			Mode       string `json:"mode"`
			LastResult string `json:"lastResult"`
		} `json:"pools"`
	}
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return 0
	}
	for _, p := range data.Pools {
		res := p.LastResult
		if res == "" {
			res = "-"
		}
		fmt.Printf("  пул %-10s %-9s %s\n", p.Name, p.Mode, res)
	}
	return 0
}

func cmdUpdate() int {
	fmt.Println("проверяю обновления...")
	info := selfupdate.Check(context.Background(), version)
	if info.Error != "" {
		fmt.Println("ошибка:", info.Error)
		return 1
	}
	fmt.Printf("текущая: %s, последняя: %s\n", info.Current, info.Latest)
	if !info.Update {
		fmt.Println("обновлений нет")
		return 0
	}
	fmt.Println("скачиваю и устанавливаю (install.sh с проверкой sha256)...")
	if err := selfupdate.Run(context.Background()); err != nil {
		fmt.Println("обновление не удалось:", err)
		return 1
	}
	fmt.Println("готово")
	return 0
}

func cmdAuth(dir, action string) int {
	switch action {
	case "off":
		fmt.Println("ВНИМАНИЕ: вы отключаете авторизацию веб-панели.")
		fmt.Println("Панель и API будут открыты ЛЮБОМУ, кто достучится до порта.")
		fmt.Println("Если порт открыт в интернет или в большую сеть - это небезопасно.")
		fmt.Print("Продолжить? наберите yes: ")
		reader := bufio.NewReader(os.Stdin)
		if strings.TrimSpace(readLine(reader)) != "yes" {
			fmt.Println("отменено")
			return 1
		}
		st, err := store.Open(dir)
		if err != nil {
			fmt.Println("store:", err)
			return 1
		}
		addr, port, allowed, _ := st.ServerSettings()
		if err := st.SetServerSettings(addr, port, allowed, true); err != nil {
			fmt.Println(err)
			return 1
		}
		st.LogEvent("settings", "server", "авторизация ВЫКЛЮЧЕНА из консоли")
		fmt.Println("авторизация выключена; перезапустите сервис: mawg restart")
		return 0
	case "on":
		a := auth.Open(dir)
		if !a.HasCreds() {
			fmt.Println("нет сохранённой учётки: сначала задайте пароль (mawg -reset-auth)")
			return 1
		}
		st, err := store.Open(dir)
		if err != nil {
			fmt.Println("store:", err)
			return 1
		}
		addr, port, allowed, _ := st.ServerSettings()
		if err := st.SetServerSettings(addr, port, allowed, false); err != nil {
			fmt.Println(err)
			return 1
		}
		st.LogEvent("settings", "server", "авторизация включена из консоли")
		fmt.Println("авторизация включена; перезапустите сервис: mawg restart")
		return 0
	default:
		fmt.Println("использование: mawg auth off|on")
		return 2
	}
}
