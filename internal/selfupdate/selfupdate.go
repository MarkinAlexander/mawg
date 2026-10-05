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

func newer(current, latest string) bool {
	c := strings.TrimPrefix(strings.TrimSpace(current), "v")
	l := strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if c == "" || c == "dev" || l == "" || c == l {
		return false
	}
	var cc, ll [3]int
	for i, part := range strings.SplitN(c, ".", 3) {
		if i < 3 {
			fmt.Sscanf(part, "%d", &cc[i])
		}
	}
	for i, part := range strings.SplitN(l, ".", 3) {
		if i < 3 {
			fmt.Sscanf(part, "%d", &ll[i])
		}
	}
	for i := 0; i < 3; i++ {
		if cc[i] != ll[i] {
			return ll[i] > cc[i]
		}
	}
	return false
}

type CheckInfo struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Update     bool   `json:"update"`
	URL        string `json:"url,omitempty"`
	Error      string `json:"error,omitempty"`
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
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
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
	cmd := exec.CommandContext(ctx, "sh", tmp)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
