package web

import (
	"context"
	"embed"
	"sync"

	"mawg/internal/auth"
	"mawg/internal/cascade"
	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/rotator"
	"mawg/internal/store"
	"mawg/internal/warp"
)

//go:embed ui
var uiFS embed.FS

type Server struct {
	store      *store.Store
	engine     *rotator.Engine
	backend    platform.Backend
	mt         *magitrickle.Client
	version    string
	auth       *auth.Auth
	tunMu      sync.Mutex
	mtCacheMu  sync.Mutex
	mtCache    *mtSnapshot
	ifaceCache respCache
	sysCache   respCache
	sbSrcCache respCache // источники ядра sing-box: отдельный слот (respCache одно-тельный)
	dlCache    respCache // прямые ссылки на ассеты бинарей движков (112-док 4.1)
	// черновик загруженного бинаря/пакета между фазами analyze/apply
	upMu    sync.Mutex
	upDraft uploadDraft
	IPGate  *auth.IPGate
	// warpReg - регистратор Cloudflare WARP (генератор конфигов);
	// nil = штатный клиент, подменяется в тестах.
	warpReg *warp.Client
	// фактически забинденные адрес/порт демона: UI отличает
	// "сохранено, но не перезапущено" от "уже применяется"
	BindAddr string
	BindPort int
}

func New(st *store.Store, e *rotator.Engine, b platform.Backend, mt *magitrickle.Client, version string, a *auth.Auth) *Server {
	return &Server{store: st, engine: e, backend: b, mt: mt, version: version, auth: a}
}

// cascSyncPool - пересборка правил каскадов, где этот пул ИСТОЧНИК
// (жалобы: новые/удалённые/выключенные конфиги не отражались в каскаде)
func (s *Server) cascSyncPool(name string) {
	if c := s.casc(); c != nil {
		go c.SyncPoolRules(context.Background(), "pool:"+name)
	}
}

func (s *Server) casc() *cascade.Manager {
	if s.engine == nil {
		return nil
	}
	return s.engine.Cascade()
}
