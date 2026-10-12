package web

import (
	"context"
	"os"

	"mawg/internal/magitrickle"
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
