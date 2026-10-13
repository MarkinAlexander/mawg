package web

import (
	"context"
	"os"

	"mawg/internal/magitrickle"
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

// enrichMagitrickleVersion - в системной проверке показываем ревизию
// бинаря, а не пакета: «0.8.2-rev4-mawgfix2 (пакет 0.8.2-1)» сразу видно,
// что стоит форк. Ревизию не определили - честная пометка.
func (s *Server) enrichMagitrickleVersion(items []provision.Item) {
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
		} else {
			items[i].Note = "показана версия пакета; ревизия бинаря не определена (конфиг ещё не сохранялся этим бинарем)"
		}
	}
}
