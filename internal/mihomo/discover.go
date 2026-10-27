package mihomo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Релизы MetaCubeX/mihomo: ассеты вида mihomo-linux-<arch>-v<ver>.gz
// (gz-бинарь), отдельного файла сумм нет - у каждого ассета в API есть
// digest sha256. Источник один, апстримовый.

type Release struct {
	Tag   string
	// Names - имя ассета по архитектуре (mipsle-softfloat, 386, amd64, ...),
	// Sizes - размер в байтах (для текстов о месте).
	Names  map[string]string
	Sizes  map[string]int64
	Digest map[string]string // имя ассета -> "sha256:<hex>" (может отсутствовать)
}

type ghAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type ghRelease struct {
	TagName    string     `json:"tag_name"`
	Published  time.Time  `json:"published_at"`
	Prerelease bool       `json:"prerelease"`
	Assets     []ghAsset  `json:"assets"`
}

func DiscoverReleases(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/MetaCubeX/mihomo/releases?per_page=5", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github недоступен: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github ответил %d для MetaCubeX/mihomo", resp.StatusCode)
	}
	var raw []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		rel := Release{Tag: r.TagName, Names: map[string]string{}, Sizes: map[string]int64{}, Digest: map[string]string{}}
		for _, a := range r.Assets {
			arch, ok := parseAssetName(a.Name)
			if !ok {
				continue
			}
			rel.Names[arch] = a.Name
			rel.Sizes[arch] = a.Size
			if a.Digest != "" {
				rel.Digest[a.Name] = a.Digest
			}
		}
		out = append(out, rel)
	}
	return out, nil
}

// parseAssetName разбирает «mihomo-linux-<arch>-v<ver>.gz»: архитектура -
// всё между префиксом и версией (последнее «-v<цифра>…» перед .gz).
// Варианты «…-go120-v…» не берём (совместимые сборки под старый Go).
func parseAssetName(name string) (string, bool) {
	if !strings.HasPrefix(name, "mihomo-linux-") || !strings.HasSuffix(name, ".gz") {
		return "", false
	}
	base := strings.TrimSuffix(strings.TrimPrefix(name, "mihomo-linux-"), ".gz")
	i := strings.LastIndex(base, "-v")
	if i <= 0 || i+2 >= len(base) || base[i+2] < '0' || base[i+2] > '9' {
		return "", false
	}
	arch := base[:i]
	if arch == "" || strings.Contains(arch, "-go") || strings.ContainsAny(arch, "./") {
		return "", false
	}
	return arch, true
}

// PickAsset - первый (самый свежий) релиз с ассетом под архитектуру.
type PickedAsset struct {
	Tag    string
	Asset  string
	Size   int64
	SHA256 string // hex без префикса; пусто = digest недоступен
}

func (p PickedAsset) URL() string {
	return fmt.Sprintf("https://github.com/MetaCubeX/mihomo/releases/download/%s/%s", p.Tag, p.Asset)
}

func PickAsset(releases []Release, arch string) (*PickedAsset, error) {
	var have []string
	for _, rel := range releases {
		if name, ok := rel.Names[arch]; ok {
			sum := ""
			if d := rel.Digest[name]; strings.HasPrefix(d, "sha256:") {
				sum = strings.TrimPrefix(d, "sha256:")
			}
			return &PickedAsset{Tag: rel.Tag, Asset: name, Size: rel.Sizes[arch], SHA256: sum}, nil
		}
		for a := range rel.Names {
			have = append(have, a)
		}
	}
	if len(have) == 0 {
		return nil, fmt.Errorf("в релизах MetaCubeX/mihomo нет доступных ассетов")
	}
	return nil, fmt.Errorf("в релизах mihomo нет ассета для %s: есть %s", arch, strings.Join(have, ", "))
}
