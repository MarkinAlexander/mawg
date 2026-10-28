package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ID источников ядра sing-box (90-док: мультиисточник вместо единственного
// lx-форка; порядок Sources = приоритет фолбэка PickAsset, первый элемент -
// дефолт установки по слову владельца 2026-10-28).
const (
	SourceExtID   = "extended"
	SourceLXID    = "lx"
	SourceLXOwnID = "lx-own"
	SourceUpID    = "upstream"
	digestPrefix  = "sha256:"
)

// Source - источник релизов ядра: репозиторий + конвенции его ассетов.
type Source struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	// ID - ключ выбора источника (маркер, UI, установщик).
	ID    string `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note"`
	// CompactMarker - суффикс сжатого ассета перед .tar.gz (".upx" у lx,
	// "-compressed" у extended); "" - сжатого профиля нет (апстрим).
	CompactMarker string `json:"-"`
	// SumsFile - в релизе есть файл SHA256SUMS; иначе сумма берётся из
	// digest-поля ассета в GitHub API.
	SumsFile bool `json:"-"`
	// FullCaps - источник понимает расширения lx: xhttp, vless-шифрование,
	// AWG-поля wireguard. У апстрима их нет - узлы честно скрываются.
	FullCaps bool `json:"-"`
}

func (s Source) String() string { return s.Owner + "/" + s.Repo }

// Sources - установка на выбор (слово владельца 2026-10-28: дефолт
// sing-box-extended; 29.10 добавлен ОРИГИНАЛЬНЫЙ lx-форк Leadaxe - он
// живее всех: база 1.14.4, релизы почти ежедневно; наш MarkinAlexander-
// форк остался как источник upx-сборок под ВСЕ платформы, включая x86,
// которых у Leadaxe нет). Источник фиксируется в маркере установки.
var Sources = []Source{
	{
		Owner: "shtorm-7", Repo: "sing-box-extended",
		ID:            SourceExtID,
		Title:         "sing-box-extended (форк shtorm-7)",
		Note:          "форк shtorm-7: AWG-обфускация, xhttp, vless-шифрование плюс новые протоколы; сжатые -compressed сборки под все платформы",
		CompactMarker: "-compressed",
		FullCaps:      true,
	},
	{
		Owner: "Leadaxe", Repo: "sing-box-lx",
		ID:            SourceLXID,
		Title:         "sing-box-lx (оригинал Leadaxe)",
		Note:          "оригинальный lx-форк: самая свежая база и частые релизы; самое экономное по памяти; upx-сборки только arm64/mipsle-softfloat",
		CompactMarker: ".upx",
		SumsFile:      true,
		FullCaps:      true,
	},
	{
		Owner: "MarkinAlexander", Repo: "sing-box-lx",
		ID:            SourceLXOwnID,
		Title:         "sing-box-lx (наш форк)",
		Note:          "наш форк lx: база старее, но upx-профиль под ВСЕ платформы (для тесной флешы x86/386, у Leadaxe таких упакованных нет)",
		CompactMarker: ".upx",
		SumsFile:      true,
		FullCaps:      true,
	},
	{
		Owner: "SagerNet", Repo: "sing-box",
		ID:    SourceUpID,
		Title: "sing-box (официальный апстрим)",
		Note:  "официальный SagerNET: без AWG-полей, xhttp и vless-шифрования - такие узлы будут скрыты с подсказкой поставить lx/extended",
	},
}

// SourceByID - профиль источника по ключу выбора.
func SourceByID(id string) (Source, bool) {
	for _, s := range Sources {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

// SourceIDByRepo - id источника по строке маркера "owner/repo" (предвыбор
// диалога: установленное ядро Leadaxe и наш форк по версии неотличимы).
func SourceIDByRepo(ownerRepo string) (string, bool) {
	for _, s := range Sources {
		if s.String() == ownerRepo {
			return s.ID, true
		}
	}
	return "", false
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

// FlavorAssets - имена файлов plain/upx ассета одной архитектуры; суммы -
// digest из GitHub API (у источников без SHA256SUMS-файла).
type FlavorAssets struct {
	Plain    string
	PlainSz  int64
	PlainSHA string
	UPX      string
	UPXSz    int64
	UPXSHA   string
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
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	HTMLURL    string    `json:"html_url"`
	Published  time.Time `json:"published_at"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
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

// apiBase - урл GitHub API (подменяется в тестах).
var apiBase = "https://api.github.com"

func fetchReleases(ctx context.Context, src Source) ([]Release, error) {
	// большие ответы (мегабайтный JSON без gzip) на капризных маршрутах
	// обрезаются без ошибки HTTP (живой кейс Кинетика): один ретрай на
	// транспортный/декод-сбой; статусные ответы (403 рейт-лимит) не
	// ретраим - бессмысленно и дороже по лимиту
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(700 * time.Millisecond):
			}
		}
		rels, err := fetchReleasesOnce(ctx, src)
		if err == nil {
			return rels, nil
		}
		lastErr = err
		if isStatusErr(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

type statusErr struct{ code int }

func (e statusErr) Error() string { return fmt.Sprintf("github ответил %d", e.code) }

func isStatusErr(err error) bool {
	var se statusErr
	return errors.As(err, &se)
}

func fetchReleasesOnce(ctx context.Context, src Source) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/"+src.Owner+"/"+src.Repo+"/releases?per_page=5", nil)
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
		return nil, fmt.Errorf("github ответил %d для %s: %w", resp.StatusCode, src, statusErr{resp.StatusCode})
	}
	var raw []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("ответ %s обрезан/битый: %v", src, err)
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		// GitHub отдаёт первыми prereleases (у апстрима - v1.15.0-alpha,
		// у Leadaxe - lx.24-rc): альфы без части платформ, rc - сырьё,
		// на роутеры такое не ставим
		if r.Prerelease {
			continue
		}
		out = append(out, buildRelease(r, src))
	}
	return out, nil
}

func buildRelease(r ghRelease, src Source) Release {
	rel := Release{
		Tag: r.TagName, URL: r.HTMLURL, Published: r.Published,
		Assets: len(r.Assets), Matrix: map[string]Flavors{}, Names: map[string]FlavorAssets{},
	}
	for _, a := range r.Assets {
		if a.Name == "SHA256SUMS" {
			rel.SHA256SUM = true
			continue
		}
		arch, compact, ok := parseAssetName(a.Name, src.CompactMarker)
		if !ok {
			continue
		}
		sum := strings.TrimPrefix(a.Digest, digestPrefix)
		f := rel.Matrix[arch]
		n := rel.Names[arch]
		if compact {
			f.UPX = true
			n.UPX, n.UPXSz, n.UPXSHA = a.Name, a.Size, sum
		} else {
			f.Plain = true
			n.Plain, n.PlainSz, n.PlainSHA = a.Name, a.Size, sum
		}
		rel.Matrix[arch] = f
		rel.Names[arch] = n
	}
	return rel
}

// parseAssetName разбирает "sing-box-<ver>-linux-<arch>[<compact>].tar.gz";
// compact-маркер зависит от источника (.upx / -compressed / нет). Варианты
// -glibc/-musl/-purego (динамические/альтернативные сборки) не наши - skip.
func parseAssetName(name, compactMarker string) (arch string, compact bool, ok bool) {
	if !strings.HasSuffix(name, ".tar.gz") {
		return "", false, false
	}
	base := strings.TrimSuffix(name, ".tar.gz")
	if compactMarker != "" && strings.HasSuffix(base, compactMarker) {
		compact = true
		base = strings.TrimSuffix(base, compactMarker)
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
	if strings.Contains(arch, "-glibc") || strings.Contains(arch, "-musl") || strings.Contains(arch, "-purego") {
		return "", false, false
	}
	return arch, compact, true
}

func SortedArches(m map[string]Flavors) []string {
	out := make([]string, 0, len(m))
	for a := range m {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
