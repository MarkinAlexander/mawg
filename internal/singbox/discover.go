package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type Source struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
}

func (s Source) String() string { return s.Owner + "/" + s.Repo }

var Sources = []Source{
	{Owner: "Leadaxe", Repo: "sing-box-lx"},
	{Owner: "MarkinAlexander", Repo: "sing-box-lx"},
}

const (
	FlavorPlain = "plain"
	FlavorUPX   = "upx"
)

type Flavors struct {
	Plain bool `json:"plain"`
	UPX   bool `json:"upx"`
}

type Release struct {
	Tag       string             `json:"tag"`
	URL       string             `json:"url"`
	Published time.Time          `json:"published"`
	SHA256SUM bool               `json:"sha256sums"`
	Assets    int                `json:"assets"`
	Matrix    map[string]Flavors `json:"matrix"`
	// Names - имена и размеры ассетов по архитектуре, для скачивания
	Names map[string]FlavorAssets `json:"-"`
}

// FlavorAssets - имена файлов plain/upx ассета одной архитектуры.
type FlavorAssets struct {
	Plain   string
	PlainSz int64
	UPX     string
	UPXSz   int64
}

type SourceReport struct {
	Source   Source    `json:"source"`
	Error    string    `json:"error,omitempty"`
	Releases []Release `json:"releases,omitempty"`
}

func (r *SourceReport) Latest() *Release {
	if len(r.Releases) == 0 {
		return nil
	}
	return &r.Releases[0]
}

type ghAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	HTMLURL    string    `json:"html_url"`
	Published  time.Time `json:"published_at"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

func releasesURL(s Source) string {
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=5", s.Owner, s.Repo)
}

func Discovery(ctx context.Context) []SourceReport {
	out := make([]SourceReport, 0, len(Sources))
	for _, src := range Sources {
		rep := SourceReport{Source: src}
		rels, err := fetchReleases(ctx, src)
		if err != nil {
			rep.Error = err.Error()
		} else {
			rep.Releases = rels
		}
		out = append(out, rep)
	}
	return out
}

func fetchReleases(ctx context.Context, src Source) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL(src), nil)
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
		return nil, fmt.Errorf("github ответил %d для %s", resp.StatusCode, src)
	}
	var raw []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		out = append(out, buildRelease(r))
	}
	return out, nil
}

func buildRelease(r ghRelease) Release {
	rel := Release{
		Tag: r.TagName, URL: r.HTMLURL, Published: r.Published,
		Assets: len(r.Assets), Matrix: map[string]Flavors{}, Names: map[string]FlavorAssets{},
	}
	for _, a := range r.Assets {
		if a.Name == "SHA256SUMS" {
			rel.SHA256SUM = true
			continue
		}
		arch, upx, ok := parseAssetName(a.Name)
		if !ok {
			continue
		}
		f := rel.Matrix[arch]
		n := rel.Names[arch]
		if upx {
			f.UPX = true
			n.UPX, n.UPXSz = a.Name, a.Size
		} else {
			f.Plain = true
			n.Plain, n.PlainSz = a.Name, a.Size
		}
		rel.Matrix[arch] = f
		rel.Names[arch] = n
	}
	return rel
}

func parseAssetName(name string) (arch string, upx bool, ok bool) {
	if !strings.HasSuffix(name, ".tar.gz") {
		return "", false, false
	}
	base := strings.TrimSuffix(name, ".tar.gz")
	if upx = strings.HasSuffix(base, ".upx"); upx {
		base = strings.TrimSuffix(base, ".upx")
	}
	const sep = "-linux-"
	i := strings.LastIndex(base, sep)
	if i < 0 || !strings.HasPrefix(base, "sing-box-") {
		return "", false, false
	}
	arch = base[i+len(sep):]
	if arch == "" || strings.ContainsAny(arch, "./") {
		return "", false, false
	}
	return arch, upx, true
}

func SortedArches(m map[string]Flavors) []string {
	out := make([]string, 0, len(m))
	for a := range m {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
