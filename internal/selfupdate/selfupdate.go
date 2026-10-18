package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	Repo         = "MarkinAlexander/mawg"
	InstallURL   = "https://raw.githubusercontent.com/" + Repo + "/main/install.sh"
	latestAPIURL = "https://api.github.com/repos/" + Repo + "/releases/latest"
)

type Release struct {
	Tag    string `json:"tag_name"`
	URL    string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func Latest(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := httpGet(ctx, latestAPIURL)
	if err != nil {
		return Release{}, fmt.Errorf("github недоступен: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github ответил %d", resp.StatusCode)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return Release{}, err
	}
	if rel.Tag == "" {
		return Release{}, fmt.Errorf("в ответе нет релиза")
	}
	return rel, nil
}

// verBase убирает суффикс дев-сборки (v0.6.11-dev39, git-describe
// 0.6.12-49-gabc): обновление сравнивает базовые тройки. Слово владельца
// 2026-10-18: дев-версии обязаны уметь обновляться на релизы - так он
// проверяет механизм автообновления (дев 6.11 -> релиз 6.12 = обновление;
// дев 6.11 -> релиз 6.11 = нет, дев свежее своего релиза).
func verBase(v string) string {
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i]
	}
	return v
}

func verParts(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ".") {
		n := 0
		fmt.Sscanf(part, "%d", &n)
		out = append(out, n)
	}
	return out
}

func newer(current, latest string) bool {
	c := verBase(strings.TrimPrefix(strings.TrimSpace(current), "v"))
	l := verBase(strings.TrimPrefix(strings.TrimSpace(latest), "v"))
	if c == "" || c == "dev" || l == "" || c == l {
		return false
	}
	// N частей (косметические 4-частные релизы вида 0.7.3.1): недостающие
	// части = 0, сравнение лексикографическое по числам
	cc, ll := verParts(c), verParts(l)
	for i := 0; i < len(cc) || i < len(ll); i++ {
		a, b := 0, 0
		if i < len(cc) {
			a = cc[i]
		}
		if i < len(ll) {
			b = ll[i]
		}
		if a != b {
			return b > a
		}
	}
	return false
}

type CheckInfo struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Update  bool   `json:"update"`
	URL     string `json:"url,omitempty"`
	Error   string `json:"error,omitempty"`
}

func Check(ctx context.Context, current string) CheckInfo {
	rel, err := Latest(ctx)
	if err != nil {
		return CheckInfo{Current: current, Error: err.Error()}
	}
	return CheckInfo{
		Current: current,
		Latest:  rel.Tag,
		Update:  newer(current, rel.Tag),
		URL:     rel.URL,
	}
}

// Run ставит обновление через install.sh релиза (sha256 проверяется им).
func Run(ctx context.Context) error {
	// -u = режим обновления: без opkg/apk update и проверки пакетов, только
	// скачивание бинаря существующим curl/wget. Инсталляционный путь гонял
	// pkg update на каждый mawg update - на медленных зеркалах это минуты.
	// Таймаут всё равно щедрый: качать бинарь с github тоже небыстро.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resp, err := httpGet(ctx, InstallURL)
	if err != nil {
		return fmt.Errorf("не скачать install.sh: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("install.sh ответил %d", resp.StatusCode)
	}
	tmp := "/tmp/mawg-install.sh"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 1<<20)); err != nil {
		f.Close()
		return err
	}
	f.Close()
	cmd := exec.CommandContext(ctx, "sh", tmp, "-u")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
