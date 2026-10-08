package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
)

// startPprof - профилировщик на 127.0.0.1:6060, только по MAWG_PPROF=1
// (диагностика нагрузки на роутере: go tool pprof по /debug/pprof/profile).
func startPprof() {
	go func() {
		log.Print(http.ListenAndServe("127.0.0.1:6060", nil))
	}()
}
