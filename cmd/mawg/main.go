package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"mawg/internal/auth"
	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/platform/keenetic"
	"mawg/internal/platform/openwrt"
	"mawg/internal/rotator"
	"mawg/internal/store"
	"mawg/internal/web"
)

var version = "dev"

func detectPlatform() (string, string) {
	if _, err := os.Stat("/etc/openwrt_release"); err == nil {
		return store.PlatformOpenwrt, "/etc/mawg"
	}
	if _, err := os.Stat("/bin/ndmc"); err == nil {
		return store.PlatformKeenetic, "/opt/etc/mawg"
	}
	if _, err := os.Stat("/opt/bin/ndmc"); err == nil {
		return store.PlatformKeenetic, "/opt/etc/mawg"
	}
	return "", ""
}

const logMaxBytes = 256 * 1024

type rotatingWriter struct {
	path string
	file *os.File
	size int64
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	if w.size+int64(len(p)) > logMaxBytes {
		w.file.Close()
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|logExtraFlags(), 0o600)
		if err != nil {
			return 0, err
		}
		w.file = f
		w.size = 0
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func setupLogging(platform string) {
	var path string
	switch platform {
	case store.PlatformKeenetic:
		path = "/tmp/mawg.log"
	default:
		path = "/var/log/mawg.log"
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|logExtraFlags(), 0o600)
	if err != nil {
		return
	}
	info, _ := f.Stat()
	rw := &rotatingWriter{path: path, file: f}
	if info != nil {
		rw.size = info.Size()
	}
	log.SetOutput(io.MultiWriter(os.Stderr, rw))
}

func readLine(r *bufio.Reader) string {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		log.Fatal("ввод прерван")
	}
	return line
}

const usage = `mawg - Multi-AWG Changer ` + `
управление пулами WireGuard/AmneziaWG с ротацией и фоллбеком

Использование:
  mawg                    запустить демона (обычно это делает init-скрипт)
  mawg help               эта справка
  mawg status             состояние демона и пулов
  mawg start              запустить сервис (init-скрипт платформы)
  mawg stop               остановить сервис
  mawg restart            перезапустить сервис
  mawg update             проверить и установить обновление с GitHub
  mawg settings           текущие настройки панели (авторизация, порт, адрес, доступ)
  mawg port <1-65535>     сменить порт веб-панели
  mawg listen <ip>        сменить адрес прослушивания (0.0.0.0 | 127.0.0.1 | IP)
  mawg reset access       сбросить доступ: порт 8090, адрес 0.0.0.0, разрешённые IP очищены
  mawg auth off           выключить авторизацию панели (НЕБЕЗОПАСНО)
  mawg auth on            включить авторизацию панели
  mawg allow <ip|cidr>    добавить адрес в список разрешённых к панели
  mawg allow list         показать разрешённые адреса
  mawg allow clear        очистить список разрешённых адресов
  mawg links parse <x>    разобрать ссылку/.conf/подписку в модель узлов (JSON);
                          x = url | файл | - (stdin)
  mawg links fetch <url>  скачать подписку: заголовки + модель узлов
  mawg singbox discover   релизы ядра sing-box-lx: матрица arch x flavor обоих источников
  mawg -reset-auth        задать новый пароль панели интерактивно
  mawg -password <pass>   задать пароль без вопросов

Смена порта, адреса, allowlist и авторизации вступает в силу после
"mawg restart". Всё то же настраивается в веб-панели: Настройки.

Флаги демона:
  -platform openwrt|keenetic   платформа (по умолчанию определяется сама)
  -base <dir>                  каталог данных
  -port <n>                    порт веб-панели (перекрывает настройки)
  -listen <ip>                 адрес прослушивания (перекрывает настройки)
  -no-auth                     разово запустить без авторизации (аварийный)`

func main() {
	platformName := flag.String("platform", "", "platform override: openwrt | keenetic")
	base := flag.String("base", "", "config directory")
	port := flag.Int("port", 0, "web ui port (0 = из настроек)")
	noAuth := flag.Bool("no-auth", false, "disable web panel authentication (open api)")
	resetAuth := flag.Bool("reset-auth", false, "set a new panel password interactively and exit")
	password := flag.String("password", "", "set panel password non-interactively and exit")
	flag.Usage = func() { fmt.Print(usage) }
	flag.Parse()

	cmd := strings.TrimSpace(flag.Arg(0))
	if cmd == "help" || cmd == "-h" {
		fmt.Print(usage)
		return
	}

	switch cmd {
	case "links":
		os.Exit(cmdLinks(flag.Args()[1:]))
	case "singbox":
		os.Exit(cmdSingbox(flag.Args()[1:]))
	}

	plat := *platformName
	dir := *base
	if plat == "" || dir == "" {
		detected, defaultDir := detectPlatform()
		if plat == "" {
			plat = detected
		}
		if dir == "" {
			dir = defaultDir
		}
	}
	if plat == "" || dir == "" {
		log.Fatal("cannot detect platform, use -platform and -base")
	}
	setupLogging(plat)

	switch cmd {
	case "start", "stop", "restart":
		os.Exit(runServiceCmd(plat, cmd))
	case "status":
		os.Exit(cmdStatus(plat, dir, *port))
	case "update":
		os.Exit(cmdUpdate())
	case "auth":
		os.Exit(cmdAuth(dir, flag.Arg(1)))
	case "settings":
		os.Exit(cmdSettings(dir))
	case "port":
		os.Exit(cmdSetPort(dir, flag.Arg(1)))
	case "listen":
		os.Exit(cmdSetListen(dir, flag.Arg(1)))
	case "reset":
		os.Exit(cmdResetAccess(dir, flag.Arg(1)))
	case "allow":
		os.Exit(cmdAllow(dir, flag.Arg(1)))
	case "":
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if *resetAuth || *password != "" {
		a := auth.Open(dir)
		np := *password
		if np == "" {
			reader := bufio.NewReader(os.Stdin)
			fmt.Print("новый пароль (минимум 8 символов, виден при вводе): ")
			np = strings.TrimSpace(readLine(reader))
			fmt.Print("повторите: ")
			if strings.TrimSpace(readLine(reader)) != np {
				log.Fatal("пароли не совпадают")
			}
		}
		if err := a.ResetPassword(np); err != nil {
			log.Fatal(err)
		}
		log.Printf("пароль панели обновлён (логин %s)", a.Login())
		return
	}

	authenticator := auth.New(dir, !*noAuth)

	var backend platform.Backend
	switch plat {
	case store.PlatformOpenwrt:
		backend = openwrt.New()
	case store.PlatformKeenetic:
		backend = keenetic.New()
	default:
		log.Fatalf("unknown platform %q", plat)
	}
	if err := backend.Detect(); err != nil {
		log.Fatalf("platform detect failed: %v", err)
	}

	st, err := store.Open(dir)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	if kb, ok := backend.(*keenetic.Backend); ok {
		kb.SetTokenProvider(st.RCIToken)
	}
	mt := magitrickle.New("http://127.0.0.1:8080")
	shadowPath := filepath.Join(dir, "magitrickle-shadow.json")
	magitrickle.OnRepair = func(names []string) {
		st.LogEvent("magitrickle", "repair", "magitrickle терял группы, возвращены: "+strings.Join(names, ", "))
	}
	magitrickle.SetShadowStorage(
		func() []magitrickle.Group {
			data, err := os.ReadFile(shadowPath)
			if err != nil {
				return nil
			}
			var out struct {
				Groups []magitrickle.Group `json:"groups"`
			}
			if json.Unmarshal(data, &out) != nil {
				return nil
			}
			return out.Groups
		},
		func(groups []magitrickle.Group) {
			data, err := json.MarshalIndent(struct {
				Groups []magitrickle.Group `json:"groups"`
			}{Groups: groups}, "", " ")
			if err != nil {
				return
			}
			tmp := shadowPath + ".tmp"
			if os.WriteFile(tmp, data, 0o600) == nil {
				os.Rename(tmp, shadowPath)
			}
		},
	)
	engine := rotator.New(st, backend, mt)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	listenAddr, cfgPort, _, authDisabled := st.ServerSettings()
	if *port != 0 {
		cfgPort = *port
	}
	authenticator.SetEnabled(!authDisabled && !*noAuth)

	srv := web.New(st, engine, backend, mt, version, authenticator)
	srv.IPGate = auth.NewIPGate(st.AllowedIPList())
	srv.BindAddr, srv.BindPort = listenAddr, cfgPort
	engine.Start(ctx)

	bind := fmt.Sprintf("%s:%d", listenAddr, cfgPort)
	log.Printf("mawg %s: platform=%s base=%s web=http://%s:%d", version, plat, dir, listenAddr, cfgPort)
	httpServer := &http.Server{Addr: bind, Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		httpServer.Close()
	}()
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
	engine.Stop()
}
