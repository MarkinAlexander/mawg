package web

import (
	"context"
	"os"
	"strconv"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/mtinstall"
	"mawg/internal/provision"
)

// mtSourcesCapable - понимает ли подключённый MT поле sources. Порядок:
// живой GET /api/v1/version (форк rev5+), фолбэк - configVersion в
// дисковом config.yaml (rev4 в поле: эндпоинта ещё нет, а sources есть).
// Ошибки = не поддерживается: панель прячет режимы источников, а
// policy-эндпоинт отвергает их, чтобы не создавать иллюзию управления.
func (s *Server) mtSourcesCapable(ctx context.Context) bool {
	if v, err := s.mtClient().GetVersion(ctx); err == nil && v != "" {
		return magitrickle.SourcesSupported(v)
	}
	if s.backend == nil {
		return false
	}
	path := s.backend.MTConfigPath()
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return magitrickle.SourcesSupported(magitrickle.ConfigVersion(data))
}

// mtDnsUpstreamsCapable - умеет ли подключённый MT отдавать состояние
// каскада апстримов dns-proxy и принимать правку списка (форк rev8+,
// GET/PUT /api/v1/dns/upstreams). Порядок тот же, что у sources:
// живой /api/v1/version, фолбэк - configVersion дискового config.yaml.
func (s *Server) mtDnsUpstreamsCapable(ctx context.Context) bool {
	if v, err := s.mtClient().GetVersion(ctx); err == nil && v != "" {
		return magitrickle.DnsUpstreamsSupported(v)
	}
	if s.backend == nil {
		return false
	}
	path := s.backend.MTConfigPath()
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return magitrickle.DnsUpstreamsSupported(magitrickle.ConfigVersion(data))
}

// mtDohUpstreamsCapable - умеет ли подключённый MT зашифрованные апстримы
// каскада (форк rev11+, DoT tls:// и DoH https://). Порядок тот же:
// живой /api/v1/version, фолбэк - configVersion дискового config.yaml.
func (s *Server) mtDohUpstreamsCapable(ctx context.Context) bool {
	if v, err := s.mtClient().GetVersion(ctx); err == nil && v != "" {
		return magitrickle.DohUpstreamsSupported(v)
	}
	if s.backend == nil {
		return false
	}
	path := s.backend.MTConfigPath()
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return magitrickle.DohUpstreamsSupported(magitrickle.ConfigVersion(data))
}

// mtRealVersion - версия РАБОТАЮЩЕГО бинаря MagiTrickle, а не opkg-пакета:
// наш форк ставится поверх апстримного пакета, и «0.8.2-1» ничего не
// говорит о ревизии. Порядок: живой GET /api/v1/version (форк rev5+),
// затем configVersion дискового config.yaml (строку пишет бинарник,
// последним сохранявший конфиг - rev3+).
func (s *Server) mtRealVersion(ctx context.Context) string {
	if v, err := s.mtClient().GetVersion(ctx); err == nil && v != "" {
		return v
	}
	if s.backend == nil {
		return ""
	}
	path := s.backend.MTConfigPath()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return magitrickle.ConfigVersion(data)
}

// mtLatestRelease - последний релиз mawg-форка MT для кнопки «обновить»
// (кэш 10 минут; оффлайн/ошибка = nil, кнопки не будет). Подмена для тестов.
var mtLatestRelease = func(ctx context.Context) *mtinstall.Release {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rel, err := mtinstall.LatestRelease(cctx)
	if err != nil {
		return nil
	}
	return rel
}

// enrichMagitrickleVersion - в системной проверке показываем ревизию
// бинаря, а не пакета: «0.8.2-rev4-mawgfix2 (пакет 0.8.2-1)» сразу видно,
// что стоит форк. Ревизию не определили - честная пометка. Стоит апстримный
// бинарь (без rev) - предлагаем обновление до mawg-форка пакетом из релиза.
// Форк старше последнего релиза - кнопка «обновить до revN» (слово
// владельца 2026-10-18: «тут тоже нельзя обновить MT с гитхаба»).
func (s *Server) enrichMagitrickleVersion(items []provision.Item) {
	latestRev := 0
	if rel := mtLatestRelease(context.Background()); rel != nil {
		latestRev = magitrickle.RevOf(rel.TagName)
	}
	for i := range items {
		if items[i].ID != "magitrickle" || !items[i].Installed {
			continue
		}
		pkg := items[i].Version
		if v := s.mtRealVersion(context.Background()); v != "" {
			if pkg != "" && pkg != v {
				items[i].Version = v + " (пакет " + pkg + ")"
			} else {
				items[i].Version = v
			}
			cur := magitrickle.RevOf(v)
			switch {
			case cur == 0:
				items[i].Action = provision.MtForkAction
				items[i].ActionLabel = "обновить до mawg-форка"
				items[i].Confirm = "Стоит MagiTrickle без ревизии форка (" + v + "): источники групп и API каскада DNS недоступны. mawg установит пакет mawg-форка из релиза GitHub штатным менеджером пакетов; конфиг сохраняется, группы перезапустятся."
			case latestRev > cur:
				items[i].Action = provision.MtForkAction
				items[i].ActionLabel = "обновить до rev" + strconv.Itoa(latestRev)
				items[i].Confirm = "Вышел релиз mawg-форка rev" + strconv.Itoa(latestRev) + " (установлена rev" + strconv.Itoa(cur) + "). mawg скачает пакет из GitHub и обновит штатным менеджером пакетов; конфиг и группы сохраняются, служба перезапустится."
			}
		} else {
			items[i].Note = "показана версия пакета; ревизия бинаря не определена (конфиг ещё не сохранялся этим бинарем)"
		}
	}
}
