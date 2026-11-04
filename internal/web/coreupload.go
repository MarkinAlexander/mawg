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

// uploadDraft - загруженный и распознанный файл между фазами analyze/apply:
// пользователь подтверждает, ЧТО панель собирается менять (слово владельца
// 04.11: «понимать что пользователь ставит и что загружает»).
type uploadDraft struct {
	Staged string
	Kind   string // singbox | mihomo | magitrickle
	Label  string // человекоческая подпись для подтверждения
}

// detectUploadedBinary - ЧТО загрузил пользователь: бинарь sing-box /
// mihomo (ELF + version-запуск) или пакет MagiTrickle (.ipk/.apk).
func detectUploadedBinary(staged, origName string) (kind, version, arch string, err error) {
	lower := strings.ToLower(origName)
	if strings.HasSuffix(lower, ".ipk") || strings.HasSuffix(lower, ".apk") {
		if strings.Contains(lower, "magitrickle") {
			return "magitrickle", "", "", nil
		}
		return "", "", "", fmt.Errorf("пакет %q не похож на MagiTrickle (нужен .ipk/.apk форка magitrickle)", origName)
	}
	arch, err = swapELFArch(staged)
	if err != nil {
		return "", "", "", fmt.Errorf("это не бинарь движка и не пакет MT (упакованный архив? распакуйте на ПК): %v", err)
	}
	// mihomo: строгий парсер «Mihomo Meta vX.Y …»
	if v, e := mihomo.MihomoVersion(staged, swapRun); e == nil {
		return "mihomo", v, arch, nil
	}
	if out, e := swapRun(staged, "version"); e == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "sing-box version") {
		if v, ve := singbox.SingBoxVersion(staged, swapRun); ve == nil {
			return "singbox", v, arch, nil
		}
	}
	// arch знаем, ядро - нет: вызывающий сравнит арх первым (чужая
	// архитектура - главная причина неработающего version-запуска)
	return "", "", arch, fmt.Errorf("бинарь не похож на sing-box или mihomo (version-запуск не распознан)")
}

// kindLabel - подпись для подтверждения в диалоге.
func kindLabel(kind, version, name string) string {
	switch kind {
	case "singbox":
		return "ядро sing-box " + version
	case "mihomo":
		return "ядро mihomo " + version
	default:
		return "пакет MagiTrickle (" + filepath.Base(name) + ")"
	}
}

// postCoreBinaryUpload - ФАЗА 1, POST /api/v1/cores/binary: multipart
// (поле file), streaming в tmpfs + детект. Ничего не меняется - пользователь
// подтверждает распознанное, apply - отдельным запросом.
func (s *Server) postCoreBinaryUpload(w http.ResponseWriter, r *http.Request) {
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
	staged := filepath.Join(uploadTmpDir, "mawg-core-upload.bin")
	origName := ""
	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			writeErr(w, fmt.Errorf("загрузка прервалась: %v", perr))
			return
		}
		if part.FormName() == "backup" || part.FileName() == "" {
			continue
		}
		origName = part.FileName()
		out, oerr := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if oerr != nil {
			writeErr(w, oerr)
			return
		}
		n, cerr := io.Copy(out, part) // MaxBytesReader валит копию при превышении
		out.Close()
		if cerr != nil {
			os.Remove(staged)
			writeErr(w, fmt.Errorf("загрузка не удалась: %v", cerr))
			return
		}
		s.store.LogEvent("cores", "core-swap", fmt.Sprintf("файл загружен в панель: %s (%s)", origName, mbSize(n)))
	}
	if origName == "" {
		writeErr(w, fmt.Errorf("файл не передан: поле file с бинарем или пакетом"))
		return
	}
	kind, version, arch, derr := detectUploadedBinary(staged, origName)
	// чужая архитектура - отказ прежде всего остального (главная причина
	// «не похож на ядро»: version-запуск чужой арх просто не исполняется)
	routerArch, _, aerr := swapDetectArch()
	if arch != "" && aerr == nil && arch != routerArch {
		os.Remove(staged)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false,
			"error": fmt.Sprintf("архитектура не подходит: бинарь %s, роутер %s - ничего не изменено", arch, routerArch)})
		return
	}
	if derr != nil {
		os.Remove(staged)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": derr.Error()})
		return
	}
	label := kindLabel(kind, version, origName)
	s.setUploadingDraft(uploadDraft{Staged: staged, Kind: kind, Label: label})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "kind": kind, "label": label, "version": version,
		"arch": arch, "routerArch": routerArch,
		"needsBackup": kind != "magitrickle",
		"hint": uploadApplyHint(kind),
	})
}

func uploadApplyHint(kind string) string {
	switch kind {
	case "magitrickle":
		return "Пакет установится штатным менеджером (opkg/apk), служба MagiTrickle перезапустится; группы и конфиг сохраняются."
	default:
		return "Движок будет остановлен на секунды, бинарь заменён, ядро запущено и проверено; при неудаче вернётся прежний (если включена копия)."
	}
}

// postCoreBinaryApply - ФАЗА 2, POST /api/v1/cores/binary/apply
// {"backup":bool}: применить распознанный файл (ядра - конвейер замены,
// MT - установка пакета из файла).
func (s *Server) postCoreBinaryApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Backup bool `json:"backup"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("ожидался JSON {backup}"))
		return
	}
	draft, ok := s.takeUploadDraft()
	if !ok {
		writeErr(w, fmt.Errorf("нет распознанной загрузки: выберите файл заново"))
		return
	}
	switch draft.Kind {
	case "magitrickle":
		s.store.LogEvent("magitrickle", "core-swap", "установка пакета из загруженного файла: "+draft.Label)
		ctx, cancel := context.WithTimeout(r.Context(), 290*time.Second)
		defer cancel()
		res, err := mtinstall.Install(ctx, mtinstall.Options{
			Platform: s.backend.Name(), LocalPackage: draft.Staged,
		})
		if err != nil {
			os.Remove(draft.Staged)
			s.store.LogEvent("magitrickle", "core-swap", "установка не удалась: "+err.Error())
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": res.Log})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": res.Log})
	default:
		res, err := s.runCoreSwap(draft.Kind, draft.Staged, req.Backup)
		if err != nil {
			os.Remove(draft.Staged)
			s.store.LogEvent(draft.Kind, "core-swap", "замена не удалась: "+err.Error())
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": res.Log})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "output": res.Log, "version": res.Version,
			"previous": res.Previous,
		})
	}
}

func (s *Server) setUploadingDraft(d uploadDraft) {
	s.upMu.Lock()
	s.upDraft = d
	s.upMu.Unlock()
}

func (s *Server) takeUploadDraft() (uploadDraft, bool) {
	s.upMu.Lock()
	defer s.upMu.Unlock()
	if s.upDraft.Staged == "" {
		return uploadDraft{}, false
	}
	d := s.upDraft
	s.upDraft = uploadDraft{}
	return d, true
}

// assetLink - один файл релиза (прямая ссылка).
type assetLink struct {
	Asset string `json:"asset"`
	URL   string `json:"url"`
	Size  int64  `json:"size,omitempty"`
}

// coreDownloadView - сборка движка под арх роутера: обычный бинарь и,
// если источник делает сжатый профиль (upx / -compressed), он же сжатый -
// на забитых роутерах качать и заливать меньше (слова владельца 04.11).
// Список МИНИМАЛЕН: одна рекомендованная сборка sing-box (остальные в
// singboxAlt), один mihomo, один пакет MT - только этот роутер.
type coreDownloadView struct {
	// Label - короткая подпись («sing-box-extended 1.14.3-extended-2.7.3»)
	Label   string     `json:"label"`
	Plain   *assetLink `json:"plain,omitempty"`   // обычная сборка
	Compact *assetLink `json:"compact,omitempty"` // сжатая (upx/-compressed)
	Note    string     `json:"note,omitempty"`    // одна короткая подсказка
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
		// (порядок Sources = приоритет установки), остальные - альтернативы;
		// каждая сборка - обычный и (если есть) сжатый профиль
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
					Plain: &assetLink{
						Asset: n.Plain, Size: n.PlainSz,
						URL: fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
							rep.Source, rel.Tag, n.Plain),
					},
					Note: "архив tar.gz - распаковать на ПК",
				}
				if n.UPX != "" {
					item.Compact = &assetLink{
						Asset: n.UPX, Size: n.UPXSz,
						URL: fmt.Sprintf("https://github.com/%s/releases/download/%s/%s",
							rep.Source, rel.Tag, n.UPX),
					}
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
					Plain: &assetLink{Asset: pick.Asset, URL: pick.URL(), Size: pick.Size},
					Note:  "gz-файл - распаковать на ПК (gunzip)",
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
						Plain: &assetLink{Asset: a.Name, URL: a.URL, Size: a.Size},
						Note:  "пакет MT - вручную только если панель недоступна",
					})
				}
			}
		}
		return json.Marshal(out)
	})
}
