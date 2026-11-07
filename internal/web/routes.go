package web

// Хендлеры/логика маршрутизации (из server.go).

import (
	"io/fs"
	"net/http"
	"strings"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/status", s.getStatus)
	mux.HandleFunc("GET /api/v1/slots", s.getSlots)
	mux.HandleFunc("POST /api/v1/slots/create", s.createSlot)
	mux.HandleFunc("GET /api/v1/ifaces", s.getIfaces)
	mux.HandleFunc("POST /api/v1/ifaces/{device}/mode", s.setIfaceMode)
	mux.HandleFunc("PUT /api/v1/ifaces/{device}/probe", s.setIfaceProbe)
	mux.HandleFunc("GET /api/v1/wanprobe", s.getWANProbe)
	mux.HandleFunc("GET /api/v1/dns/probe", s.dnsProbe)
	mux.HandleFunc("GET /api/v1/dns/probe/servers", s.dnsProbeServersGet)
	mux.HandleFunc("PUT /api/v1/dns/probe/servers", s.dnsProbeServersSet)
	mux.HandleFunc("GET /api/v1/dns/keenetic/own", s.dnsKeeneticOwn)
	mux.HandleFunc("GET /api/v1/dns/keenetic/dns", s.keeneticDNSGet)
	mux.HandleFunc("PUT /api/v1/dns/keenetic/dns", s.keeneticDNSSet)
	mux.HandleFunc("GET /api/v1/dns/state", s.dnsState)
	mux.HandleFunc("POST /api/v1/dns/dnsmasq", s.dnsDnsmasq)
	mux.HandleFunc("GET /api/v1/dns/upstreams", s.dnsUpstreams)
	mux.HandleFunc("PUT /api/v1/dns/upstreams", s.dnsUpstreamsSet)
	mux.HandleFunc("GET /api/v1/dns/policy", s.getDnsPolicy)
	mux.HandleFunc("PUT /api/v1/dns/policy", s.putDnsPolicy)
	mux.HandleFunc("POST /api/v1/dns/policy/probe", s.postDnsPolicyProbe)
	mux.HandleFunc("GET /api/v1/dns/zones", s.getDnsZones)
	mux.HandleFunc("PUT /api/v1/dns/zones", s.putDnsZones)
	mux.HandleFunc("POST /api/v1/dns/zones/{id}/probe", s.postDnsZonesProbe)
	mux.HandleFunc("PUT /api/v1/wanprobe", s.putWANProbe)
	mux.HandleFunc("GET /api/v1/excludes", s.getExcludes)
	mux.HandleFunc("PUT /api/v1/excludes", s.putExcludes)
	mux.HandleFunc("GET /api/v1/excludes/disabled", s.getExcludesDisabled)
	mux.HandleFunc("PUT /api/v1/excludes/disabled", s.putExcludesDisabled)
	mux.HandleFunc("GET /api/v1/rcitoken", s.getRCIToken)
	mux.HandleFunc("PUT /api/v1/rcitoken", s.putRCIToken)
	mux.HandleFunc("POST /api/v1/pools/{name}/rename", s.renamePool)
	mux.HandleFunc("GET /api/v1/events", s.getEvents)
	mux.HandleFunc("POST /api/v1/pools", s.createPool)
	mux.HandleFunc("POST /api/v1/pools/amnezia-free", s.createFreePool)
	mux.HandleFunc("POST /api/v1/pools/amnezia-free/captcha", s.answerFreeCaptcha)
	mux.HandleFunc("POST /api/v1/pools/{name}/free/reissue", s.reissueFreePool)
	mux.HandleFunc("GET /api/v1/warp/presets", s.warpPresets)
	mux.HandleFunc("POST /api/v1/warp/generate", s.warpGenerate)
	mux.HandleFunc("POST /api/v1/pools/{name}/warp", s.poolWarpAdd)
	mux.HandleFunc("POST /api/v1/pools/from-source", s.createPoolFromSource)
	mux.HandleFunc("POST /api/v1/links/inspect", s.inspectSource)
	mux.HandleFunc("POST /api/v1/singbox/mode", s.postSingboxMode)
	mux.HandleFunc("POST /api/v1/singbox/cache-file", s.postCacheFile)
	mux.HandleFunc("GET /api/v1/cores", s.getCores)
	mux.HandleFunc("GET /api/v1/cores/singbox/sources", s.getSingboxSources)
	mux.HandleFunc("GET /api/v1/cores/downloads", s.getCoreDownloads)
	mux.HandleFunc("POST /api/v1/cores/{id}/power", s.postCorePower)
	mux.HandleFunc("POST /api/v1/cores/binary", s.postCoreBinaryUpload)
	mux.HandleFunc("POST /api/v1/cores/binary/apply", s.postCoreBinaryApply)
	mux.HandleFunc("POST /api/v1/pools/{name}/refresh-source", s.refreshSource)
	mux.HandleFunc("PUT /api/v1/pools/{name}", s.updatePool)
	mux.HandleFunc("DELETE /api/v1/pools/{name}", s.deletePool)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs", s.uploadConfigs)
	mux.HandleFunc("GET /api/v1/pools/{name}/premium", s.getPremium)
	mux.HandleFunc("GET /api/v1/pools/{name}/members", s.getPoolMembers)
	mux.HandleFunc("GET /api/v1/pools/{name}/engine", s.getPoolEngine)
	mux.HandleFunc("PUT /api/v1/pools/{name}/engine/select", s.putPoolEngineSelect)
	mux.HandleFunc("POST /api/v1/pools/{name}/engine/retest", s.postPoolEngineRetest)
	mux.HandleFunc("POST /api/v1/pools/{name}/engine/toggle", s.postPoolEngineToggle)
	mux.HandleFunc("POST /api/v1/pools/{name}/smart-cascade/check-now", s.smartCascadeCheckNow)
	mux.HandleFunc("POST /api/v1/pools/{name}/smart-cascade/on", s.smartCascadeOn)
	mux.HandleFunc("POST /api/v1/pools/{name}/smart-cascade/off", s.smartCascadeOff)
	mux.HandleFunc("GET /api/v1/pools/{name}/configs/{file}", s.getPoolConfigRaw)
	mux.HandleFunc("POST /api/v1/pools/{name}/premium", s.importPremium)
	mux.HandleFunc("POST /api/v1/pools/{name}/premium/country", s.switchPremium)
	mux.HandleFunc("DELETE /api/v1/pools/{name}/configs/{file}", s.deleteConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs/{file}/enable", s.enableConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs/{file}/move", s.moveConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/rotate", s.postRotate)
	mux.HandleFunc("POST /api/v1/pools/{name}/check", s.postCheck)
	mux.HandleFunc("POST /api/v1/pools/{name}/enable", s.postEnable)
	mux.HandleFunc("POST /api/v1/pools/{name}/disable", s.postDisable)
	mux.HandleFunc("POST /api/v1/pools/{name}/activate", s.postActivate)
	mux.HandleFunc("GET /api/v1/magitrickle", s.getMagitrickle)
	mux.HandleFunc("GET /api/v1/system/check", s.getSystemCheck)
	mux.HandleFunc("POST /api/v1/system/install", s.postSystemInstall)
	mux.HandleFunc("GET /api/v1/system/engines", s.getEnginesDiag)
	mux.HandleFunc("GET /api/v1/system/diagnostics", s.getDiagnostics)

	mux.HandleFunc("GET /api/v1/bundles", s.getBundles)
	mux.HandleFunc("POST /api/v1/bundles", s.createBundle)
	mux.HandleFunc("PUT /api/v1/bundles/{name}", s.updateBundle)
	mux.HandleFunc("DELETE /api/v1/bundles/{name}", s.deleteBundle)

	mux.HandleFunc("GET /api/v1/mt/groups", s.mtGetGroups)
	mux.HandleFunc("GET /api/v1/mt/duplicates", s.mtDuplicates)
	mux.HandleFunc("GET /api/v1/mt/interfaces", s.mtGetInterfaces)
	mux.HandleFunc("GET /api/v1/mt/presets", s.mtGetPresets)
	mux.HandleFunc("POST /api/v1/mt/groups", s.mtCreateGroup)
	mux.HandleFunc("POST /api/v1/mt/device-group", s.mtCreateDeviceGroup)
	mux.HandleFunc("DELETE /api/v1/mt/device-group/{name}", s.mtDeleteDeviceGroup)
	mux.HandleFunc("GET /api/v1/device-bindings", s.getDeviceBindings)
	mux.HandleFunc("PUT /api/v1/mt/groups/{id}", s.mtUpdateGroup)
	mux.HandleFunc("POST /api/v1/mt/groups/order", s.mtGroupsOrder)
	mux.HandleFunc("POST /api/v1/mt/groups/import", s.mtImportGroups)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/order", s.mtRulesOrder)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/enable", s.mtToggleGroup)
	mux.HandleFunc("DELETE /api/v1/mt/groups/{id}", s.mtDeleteGroup)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules", s.mtCreateRule)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/import", s.mtImportRules)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/enable", s.mtRulesEnableAll)
	mux.HandleFunc("PUT /api/v1/mt/groups/{id}/rules/{rid}", s.mtUpdateRule)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/{rid}/enable", s.mtToggleRule)
	mux.HandleFunc("DELETE /api/v1/mt/groups/{id}/rules/{rid}", s.mtDeleteRule)
	mux.HandleFunc("POST /api/v1/mt/presets/{id}/apply", s.mtApplyPreset)
	mux.HandleFunc("POST /api/v1/mt/backup", s.mtBackupNow)
	mux.HandleFunc("GET /api/v1/mt/backups", s.mtBackupList)
	mux.HandleFunc("GET /api/v1/mt/backups/{file}", s.mtBackupGet)
	mux.HandleFunc("POST /api/v1/mt/restore", s.mtRestore)
	mux.HandleFunc("GET /api/v1/mt/backup-keep", s.getBackupKeep)
	mux.HandleFunc("PUT /api/v1/mt/backup-keep", s.putBackupKeep)
	mux.HandleFunc("GET /api/v1/settings/server", s.getServerSettings)
	mux.HandleFunc("GET /api/v1/lan/clients", s.lanClients)
	mux.HandleFunc("PUT /api/v1/settings/server", s.putServerSettings)
	mux.HandleFunc("GET /api/v1/system/update/check", s.updateCheck)
	mux.HandleFunc("POST /api/v1/system/update/run", s.updateRun)
	mux.HandleFunc("POST /api/v1/system/restart", s.postSystemRestart)
	mux.HandleFunc("POST /api/v1/system/mt-restart", s.postMTRestart)
	mux.HandleFunc("GET /api/v1/cascades/iface-endpoints", s.ifaceEndpointsHint)
	mux.HandleFunc("GET /api/v1/cascades", s.cascList)
	mux.HandleFunc("POST /api/v1/cascades", s.cascCreate)
	mux.HandleFunc("DELETE /api/v1/cascades/{group}", s.cascDelete)
	mux.HandleFunc("PUT /api/v1/mt/groups/{id}/policy", s.mtSetPolicy)
	mux.HandleFunc("GET /api/v1/mt/policy-cycles", s.getPolicyCycles)
	mux.HandleFunc("PUT /api/v1/mt/policy-cycles", s.putPolicyCycles)

	mux.HandleFunc("GET /api/v1/subs", s.getSubs)
	mux.HandleFunc("POST /api/v1/subs", s.postSubs)
	mux.HandleFunc("POST /api/v1/subs/{name}/refresh", s.refreshSub)
	mux.HandleFunc("DELETE /api/v1/subs/{name}", s.deleteSub)

	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}
	// основная панель - Vue на /app/; вход с корня идёт туда. Легаси-панель
	// удалена в Ф3.9 (44-док §5.9). {$} - только точный корень, прочая
	// статика (favicon.ico) - как раньше.
	mux.Handle("GET /{$}", http.RedirectHandler("/app/", http.StatusFound))
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// новая панель (Vue, исходники в ui-src) собирается в ui/app командой
	// `npm run build`. Имя ui/app, а не dist -
	// go:embed не встраивает каталоги из .gitignore (там занят dist/).
	appSub, err := fs.Sub(uiFS, "ui/app")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /app", http.RedirectHandler("/app/", http.StatusMovedPermanently))
	mux.Handle("GET /app/", http.StripPrefix("/app/", http.FileServer(http.FS(appSub))))
	if s.auth != nil {
		mux.HandleFunc("POST /api/v1/auth/login", s.auth.HandleLogin)
		mux.HandleFunc("POST /api/v1/auth/logout", s.auth.HandleLogout)
		mux.HandleFunc("POST /api/v1/auth/password", s.auth.HandlePassword)
	}
	// авторизация снаружи, лимит тела внутри (статика без лимита не нужна -
	// там нет тела); allowlist режет всё самым внешним слоем
	var h http.Handler = s.auth.Middleware(limitBody(mux))
	if s.IPGate != nil {
		h = s.IPGate.Middleware(h)
	}
	return h
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(1 << 20)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			limit = 32 << 20
		}
		// заливка бинаря движка: обычный бинарь до ~100 МБ (extended),
		// 32 МБ multipart-лимита не хватает (112-док 4.1)
		if strings.HasSuffix(r.URL.Path, "/cores/binary") {
			limit = maxCoreUpload
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}
