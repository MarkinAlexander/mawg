package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "mawg_session"
	sessionTTL    = 7 * 24 * time.Hour
	maxFails      = 5
	failLock      = 30 * time.Second
	passwordMin   = 8
	pbkdf2Iters   = 100000
)

const passAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

type credFile struct {
	Login string `json:"login"`
	Hash  string `json:"hash"`
}

type failState struct {
	count int
	until time.Time
}

type Auth struct {
	Enabled bool
	Base    string

	mu       sync.Mutex
	login    string
	hash     string
	sessions map[string]time.Time
	fails    map[string]*failState
	FirstRun bool
}

// Open загружает учётку без генерации (CLI-сброс пароля).
func Open(base string) *Auth {
	a := &Auth{Enabled: true, Base: base, login: "admin", sessions: map[string]time.Time{}, fails: map[string]*failState{}}
	if c, err := a.load(); err == nil {
		a.login, a.hash = c.Login, c.Hash
	}
	return a
}

// New при отсутствии auth.json генерирует admin + случайный пароль,
// подсказку first-auth.txt и баннер в лог.
func New(base string, enabled bool) *Auth {
	a := &Auth{Enabled: enabled, Base: base, login: "admin", sessions: map[string]time.Time{}, fails: map[string]*failState{}}
	if !enabled {
		return a
	}
	if c, err := a.load(); err == nil {
		a.login, a.hash = c.Login, c.Hash
		return a
	}
	pass, err := genPassword(10)
	if err != nil {
		log.Fatalf("auth: %v", err)
	}
	a.login = "admin"
	a.hash = hashPassword(pass)
	a.FirstRun = true
	if err := a.save(); err != nil {
		log.Fatalf("auth: %v", err)
	}
	hint := fmt.Sprintf("логин: %s\nпароль: %s", a.login, pass)
	writePrivate(filepath.Join(base, "first-auth.txt"), []byte(hint))
	log.Printf("[auth] первичный вход в веб-панель: логин %s, пароль %s (смените в Настройках; подсказка: %s)",
		a.login, pass, filepath.Join(base, "first-auth.txt"))
	return a
}

func (a *Auth) load() (credFile, error) {
	var c credFile
	data, err := os.ReadFile(a.authPath())
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if c.Login == "" || c.Hash == "" {
		return c, fmt.Errorf("пустая учётка")
	}
	return c, nil
}

func (a *Auth) save() error {
	data, err := json.Marshal(credFile{Login: a.login, Hash: a.hash})
	if err != nil {
		return err
	}
	return writePrivate(a.authPath(), data)
}

func (a *Auth) authPath() string { return filepath.Join(a.Base, "auth.json") }

func writePrivate(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func hashPassword(pass string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	key, _ := pbkdf2.Key(sha256.New, pass, salt, pbkdf2Iters, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iters,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

func verifyHash(hash, pass string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iters := 0
	fmt.Sscanf(parts[1], "%d", &iters)
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || iters < 1 {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, pass, salt, iters, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(key, want) == 1
}

func genPassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = passAlphabet[int(v)%len(passAlphabet)]
	}
	return string(out), nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		return c.Value
	}
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// valid: токен из cookie или Bearer жив и не истёк (скользящее продление).
func (a *Auth) valid(r *http.Request) bool {
	tok := sessionToken(r)
	if tok == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[tok]
	if !ok || time.Now().After(exp) {
		return false
	}
	a.sessions[tok] = time.Now().Add(sessionTTL)
	return true
}

// Middleware: под замком всё /api/, кроме login/logout; статика открыта.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	if a == nil || !a.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/v1/auth/")
		if p == "login" || p == "logout" {
			next.ServeHTTP(w, r)
			return
		}
		if a.valid(r) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"требуется вход"}`))
	})
}

func (a *Auth) writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}

// current: файл перечитывается, чтобы CLI-сброс действовал без рестарта.
func (a *Auth) current() (string, string) {
	if c, err := a.load(); err == nil {
		a.login, a.hash = c.Login, c.Hash
	}
	return a.login, a.hash
}

// HandleLogin: rate limit по IP, сессия в cookie + токен в ответе
// (для скриптов, которые ходят с Authorization: Bearer).
func (a *Auth) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.writeErr(w, http.StatusBadRequest, "кривой запрос")
		return
	}
	ip := clientIP(r)
	a.mu.Lock()
	login, hash := a.current()
	if f := a.fails[ip]; f != nil && time.Now().Before(f.until) {
		left := int(time.Until(f.until).Seconds()) + 1
		a.mu.Unlock()
		a.writeErr(w, http.StatusTooManyRequests, fmt.Sprintf("слишком много попыток, подождите %dс", left))
		return
	}
	ok := subtle.ConstantTimeCompare([]byte(req.Login), []byte(login)) == 1 && verifyHash(hash, req.Password)
	if ok {
		delete(a.fails, ip)
	} else {
		f := a.fails[ip]
		if f == nil {
			f = &failState{}
			a.fails[ip] = f
		}
		f.count++
		if f.count >= maxFails {
			f.until = time.Now().Add(failLock)
			f.count = 0
		}
	}
	var token string
	if ok {
		b := make([]byte, 32)
		rand.Read(b)
		token = base64.RawURLEncoding.EncodeToString(b)
		a.sessions[token] = time.Now().Add(sessionTTL)
	}
	a.mu.Unlock()
	if !ok {
		a.writeErr(w, http.StatusUnauthorized, "неверный логин или пароль")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(sessionTTL.Seconds()),
	})
	// подсказка живёт до первого входа
	os.Remove(filepath.Join(a.Base, "first-auth.txt"))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"token":%q}`, token)
}

func (a *Auth) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if tok := sessionToken(r); tok != "" {
		a.mu.Lock()
		delete(a.sessions, tok)
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// HandlePassword: смена пароля из веб-морды (middleware уже проверил
// сессию). Успешная смена тоже подчищает подсказку, если вход ещё
// не случился.
func (a *Auth) HandlePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Old      string `json:"old"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.writeErr(w, http.StatusBadRequest, "кривой запрос")
		return
	}
	if len(req.Password) < passwordMin {
		a.writeErr(w, http.StatusBadRequest, fmt.Sprintf("пароль короче %d символов", passwordMin))
		return
	}
	a.mu.Lock()
	if !verifyHash(a.hash, req.Old) {
		a.mu.Unlock()
		a.writeErr(w, http.StatusUnauthorized, "старый пароль неверен")
		return
	}
	a.hash = hashPassword(req.Password)
	err := a.save()
	a.mu.Unlock()
	if err != nil {
		a.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	os.Remove(filepath.Join(a.Base, "first-auth.txt"))
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ResetPassword: консольный сброс (root на роутере). Подсказку
// first-auth.txt не трогает - тот, кто сбрасывает, пароль и так знает.
func (a *Auth) ResetPassword(newPass string) error {
	if len(newPass) < passwordMin {
		return fmt.Errorf("пароль короче %d символов", passwordMin)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hash = hashPassword(newPass)
	return a.save()
}

func (a *Auth) Login() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.login
}
