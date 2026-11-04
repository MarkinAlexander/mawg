package web

// Прямая загрузка бинаря движка через панель (112-док 4.1/4.5, слово
// владельца 04.11: «заливать распакованный бинарник напрямую куда надо
// через веб интерфейс») и прямые ссылки на релизные ассеты под арх
// роутера - распаковывать на своём ПК и заливать готовый бинарь.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mawg/internal/mihomo"
	"mawg/internal/mtinstall"
	"mawg/internal/platform"
	"mawg/internal/singbox"
)

// maxCoreUpload - потолок загрузки бинаря: обычный sing-box-extended
// ~100 МБ, lx/mihomo меньше; лишний лимит - защита от переполнения tmpfs.
const maxCoreUpload = 150 << 20

// uploadTmpDir - куда льём: tmpfs (ОЗУ), не трогаем забитую флешку.
var uploadTmpDir = "/tmp"

// postCoreBinary - POST /api/v1/cores/{id}/binary: multipart (поле
// file - бинарь, поле backup - 1/0 сохранять старый), streaming в tmpfs,
// затем конвейер замены (coreswap). Авторизация - общая (auth.Middleware).
func (s *Server) postCoreBinary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id != "singbox" && id != "mihomo" {
		writeErr(w, fmt.Errorf("неизвестное ядро %q", id))
		return
	}
	// tmpfs - это ОЗУ: до приёма сверяем объявленный размер со свободным
	if free := platform.DiskFreeBytes(uploadTmpDir); free > 0 {
		if want := r.ContentLength; want > 0 && want+16<<20 > free {
			writeErr(w, fmt.Errorf("в %s (это оперативная память) свободно %s - столько не вместить; перезагрузка роутера освобождает tmpfs", uploadTmpDir, mbSize(free)))
			return
		}
	}
	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, fmt.Errorf("ожидался multipart: %v", err))
		return
	}
	keepBackup := true
	staged := ""
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			writeErr(w, fmt.Errorf("загрузка прервалась: %v", perr))
			return
		}
		if part.FormName() == "backup" {
			val, _ := io.ReadAll(io.LimitReader(part, 16))
			if v := string(val); v == "0" || v == "false" {
				keepBackup = false
			}
			continue
		}
		if part.FileName() == "" {
			continue
		}
		dst := filepath.Join(uploadTmpDir, "mawg-core-upload-"+id+".bin")
		out, oerr := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if oerr != nil {
			writeErr(w, oerr)
			return
		}
		n, cerr := io.Copy(out, part) // MaxBytesReader валит копию при превышении
		if cerr != nil {
			out.Close()
			os.Remove(dst)
			writeErr(w, fmt.Errorf("загрузка не удалась: %v", cerr))
			return
		}
		out.Close()
		staged = dst
		s.store.LogEvent(id, "core-swap", fmt.Sprintf("бинарь загружен в панель: %s (%s)", part.FileName(), mbSize(n)))
	}
	if staged == "" {
		writeErr(w, fmt.Errorf("файл не передан: поле file с бинарем движка"))
		return
	}
	res, err := s.runCoreSwap(id, staged, keepBackup)
	if err != nil {
		os.Remove(staged) // при откате/отказе tmpfs не должен копить мусор
		s.store.LogEvent(id, "core-swap", "замена не удалась: "+err.Error())
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": res.Log})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "output": res.Log, "version": res.Version,
		"previous": res.Previous, "backup": res.Backup,
	})
}

// coreDownloadView - прямая ссылка на релизный ассет под арх роутера:
// пользователь скачивает на свой ПК, распаковывает и заливает бинарь
// кнопкой «Загрузить бинарь» (на забитых роутерах распаковка на месте
// не живёт). Список МИНИМАЛЕН (слово владельца 04.11: «слишком большой
// список, упрощать и очень сильно»): один рекомендованный ассет на
// движок - только то, что подходит этому роутеру.
type coreDownloadView struct {
	// Label - короткая подпись для пользователя («sing-box-extended 1.14.3»),
	// полное имя ассета - в Asset
	Label string `json:"label"`
	Asset string `json:"asset"`
	URL   string `json:"url"`
	Size  int64  `json:"size,omitempty"`
	Note  string `json:"note,omitempty"` // одна короткая подсказка, что делать с файлом
}

// getCoreDownloads - GET /api/v1/cores/downloads: прямые ссылки ПОД ЭТОТ
// роутер: sing-box - рекомендованный источник (дефолтный, остальные в
// singboxAlt), mihomo - один gz-бинарь, MT - один пакет платформы
// (правило выбора то же, что у установки). Кэш 10 минут - GitHub API.
func (s *Server) getCoreDownloads(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	s.dlCache.serve(w, 10*time.Minute, func() ([]byte, error) {
		arch, _, _ := singbox.DetectLxArch()
		out := struct {
			Arch        string             `json:"arch"`
			Singbox     []coreDownloadView `json:"singbox"`
			SingboxAlt  []coreDownloadView `json:"singboxAlt"`
			Mihomo      []coreDownloadView `json:"mihomo"`
			Magitrickle []coreDownloadView `json:"magitrickle"`
		}{Arch: arch, Singbox: []coreDownloadView{}, SingboxAlt: []coreDownloadView{}, Mihomo: []coreDownloadView{}, Magitrickle: []coreDownloadView{}}

		// sing-box: первый источник с ассетом под арх = рекомендованный
		// (порядок Sources = приоритет установки), остальные - альтернативы
		for _, rep := range singbox.Discovery(ctx) {
			if rep.Error != "" {
				continue
			}
			for _, rel := range rep.Releases {
				n, ok := rel.Names[arch]
				if !ok || n.Plain == "" {
					continue
				}
				ver := strings.TrimPrefix(rel.Tag, "v")
				item := coreDownloadView{
					Label: rep.Source.Title + " " + ver,
					Asset: n.Plain,
					URL: fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
						rep.Source, rel.Tag, n.Plain),
					Size: n.PlainSz,
					Note: "архив tar.gz - распаковать на ПК",
				}
				if len(out.Singbox) == 0 {
					out.Singbox = append(out.Singbox, item)
				} else {
					out.SingboxAlt = append(out.SingboxAlt, item)
				}
				break
			}
		}
		// mihomo: gz-бинарь (распаковать gunzip)
		if rels, derr := mihomo.DiscoverReleases(ctx); derr == nil {
			if pick, perr := mihomo.PickAsset(rels, arch); perr == nil {
				out.Mihomo = append(out.Mihomo, coreDownloadView{
					Label: "mihomo " + pick.Tag,
					Asset: pick.Asset, URL: pick.URL(), Size: pick.Size,
					Note: "gz-файл - распаковать на ПК (gunzip)",
				})
			}
		}
		// MT: ОДИН пакет этого роутера - правило выбора как у установки
		// (DetectTarget + PackageManager + PickAsset); не определился -
		// секции не будет, MT ставится кнопкой из панели
		if rel, lerr := mtinstall.LatestRelease(ctx); lerr == nil && rel != nil {
			platform := "openwrt"
			if s.backend.Name() == "keenetic" {
				platform = "entware"
			}
			if target, _, terr := mtinstall.DetectTarget(s.backend.Name()); terr == nil {
				_, ext := mtinstall.PackageManager(s.backend.Name())
				if a, perr := mtinstall.PickAsset(rel, platform, target, ext); perr == nil {
					out.Magitrickle = append(out.Magitrickle, coreDownloadView{
						Label: "MagiTrickle " + strings.TrimPrefix(rel.TagName, "v") + " (" + target + ")",
						Asset: a.Name, URL: a.URL, Size: a.Size,
						Note: "пакет MT - вручную только если панель недоступна",
					})
				}
			}
		}
		return json.Marshal(out)
	})
}
