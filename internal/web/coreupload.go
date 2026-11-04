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
// не живёт).
type coreDownloadView struct {
	Source string `json:"source,omitempty"`
	Title  string `json:"title,omitempty"`
	Asset  string `json:"asset"`
	URL    string `json:"url"`
	Size   int64  `json:"size,omitempty"`
	Note   string `json:"note,omitempty"` // gz-бинарь/пакет - что делать после скачивания
}

// getCoreDownloads - GET /api/v1/cores/downloads: прямые ссылки на
// ассеты всех движков под арх роутера (sing-box по источникам, mihomo,
// пакеты MT). Кэш 10 минут - GitHub API.
func (s *Server) getCoreDownloads(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	s.dlCache.serve(w, 10*time.Minute, func() ([]byte, error) {
		arch, _, _ := singbox.DetectLxArch()
		out := struct {
			Arch       string             `json:"arch"`
			Singbox    []coreDownloadView `json:"singbox"`
			Mihomo     []coreDownloadView `json:"mihomo"`
			Magitrickle []coreDownloadView `json:"magitrickle"`
		}{Arch: arch, Singbox: []coreDownloadView{}, Mihomo: []coreDownloadView{}, Magitrickle: []coreDownloadView{}}

		// sing-box: свежий ассет каждого источника (plain-профиль)
		for _, rep := range singbox.Discovery(ctx) {
			if rep.Error != "" {
				continue
			}
			for _, rel := range rep.Releases {
				n, ok := rel.Names[arch]
				if !ok || n.Plain == "" {
					continue
				}
				out.Singbox = append(out.Singbox, coreDownloadView{
					Source: rep.Source.ID, Title: rep.Source.Title,
					Asset: n.Plain,
					URL: fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
						rep.Source, rel.Tag, n.Plain),
					Size: n.PlainSz,
					Note: "архив tar.gz: распакуйте на ПК, внутри бинарь sing-box",
				})
				break
			}
		}
		// mihomo: gz-бинарь (распаковать gunzip)
		if rels, derr := mihomo.DiscoverReleases(ctx); derr == nil {
			if pick, perr := mihomo.PickAsset(rels, arch); perr == nil {
				out.Mihomo = append(out.Mihomo, coreDownloadView{
					Asset: pick.Asset, URL: pick.URL(), Size: pick.Size,
					Note: "gz-бинарь: распакуйте на ПК (gunzip), внутри готовый бинарь mihomo",
				})
			}
		}
		// MT: пакеты .ipk/.apk - ставятся штатным менеджером, не заливкой
		if rel, lerr := mtinstall.LatestRelease(ctx); lerr == nil && rel != nil {
			for _, a := range rel.Assets {
				note := "пакет для ручной установки: opkg install / apk add"
				if a.Name == "" {
					continue
				}
				out.Magitrickle = append(out.Magitrickle, coreDownloadView{
					Asset: a.Name, URL: a.URL, Size: a.Size, Note: note,
				})
			}
		}
		return json.Marshal(out)
	})
}
