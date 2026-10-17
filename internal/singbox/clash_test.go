package singbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// clashTestServer - фейковая clash_api: отдаёт /proxies, пишет тела PUT
// и пути GET (для проверок select/delay).
func clashTestServer(t *testing.T) (*httptest.Server, *[]string, *[]string) {
	t.Helper()
	var puts, gets []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /proxies", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"proxies":{
			"mawg-sub": {"type":"URLTest","now":"узел-2","all":["узел-1","узел-2"],
				"history":[{"time":"2026-10-16T10:00:00Z","delay":0},{"time":"2026-10-16T10:01:00Z","delay":120}]},
			"узел-1": {"type":"Vless","history":[{"time":"2026-10-16T09:00:00Z","delay":250}]},
			"узел-2": {"type":"Vless","history":[]}
		}}`))
	})
	mux.HandleFunc("PUT /proxies/", func(w http.ResponseWriter, r *http.Request) {
		puts = append(puts, r.URL.Path+" "+r.URL.Query().Encode())
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		puts = append(puts, string(buf[:n]))
		w.Write([]byte(`{}`))
	})
	mux.HandleFunc("GET /group/{name}/delay", func(w http.ResponseWriter, r *http.Request) {
		gets = append(gets, r.URL.Path+"?"+r.URL.RawQuery)
		w.Write([]byte(`{"узел-1":333,"узел-2":111}`))
	})
	// как живой clash: путь БЕЗ /delay - это инфо группы (объект), а не
	// карта задержек; если клиент опять потеряет суффикс - декод упадёт
	// и тест поймает (живой урок 0.42: /group/{name} без /delay)
	mux.HandleFunc("GET /group/", func(w http.ResponseWriter, r *http.Request) {
		gets = append(gets, r.URL.Path+"?"+r.URL.RawQuery)
		w.Write([]byte(`{"type":"URLTest","name":"mawg-sub","now":"узел-2","all":["узел-1","узел-2"],"history":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &puts, &gets
}

// база из тест-сервера: 127.0.0.1:<порт>
func srvBase(srv *httptest.Server) int {
	addr := srv.Listener.Addr().String()
	idx := strings.LastIndex(addr, ":")
	base := 0
	for _, ch := range addr[idx+1:] {
		base = base*10 + int(ch-'0')
	}
	return base
}

func TestClashProxiesAndLastDelay(t *testing.T) {
	srv, _, _ := clashTestServer(t)
	proxies, err := ClashProxies(context.Background(), srvBase(srv))
	if err != nil {
		t.Fatal(err)
	}
	g, ok := proxies["mawg-sub"]
	if !ok || g.Type != "URLTest" || g.Now != "узел-2" || len(g.All) != 2 {
		t.Fatalf("группа не распарсена: %+v", g)
	}
	// history с нулевой последней точкой: берём последний >0
	if d := LastDelay(g); d != 120 {
		t.Fatalf("LastDelay группы = %d, want 120", d)
	}
	if d := LastDelay(proxies["узел-1"]); d != 250 {
		t.Fatalf("LastDelay узел-1 = %d, want 250", d)
	}
	// пустая история - 0 (не измерен/не отвечал)
	if d := LastDelay(proxies["узел-2"]); d != 0 {
		t.Fatalf("LastDelay узел-2 = %d, want 0", d)
	}
}

func TestClashSelectAndGroupDelay(t *testing.T) {
	srv, puts, gets := clashTestServer(t)
	base := srvBase(srv)
	ctx := context.Background()

	if err := ClashSelect(ctx, base, "mawg-sub", "узел-1"); err != nil {
		t.Fatal(err)
	}
	if len(*puts) != 2 || !strings.HasPrefix((*puts)[0], "/proxies/mawg-sub") {
		t.Fatalf("PUT select не дошёл: %v", *puts)
	}
	if !strings.Contains((*puts)[1], "узел-1") {
		t.Fatalf("тело select без имени: %q", (*puts)[1])
	}

	delays, err := ClashGroupDelay(ctx, base, "mawg-sub", "http://1.1.1.1/", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if delays["узел-1"] != 333 || delays["узел-2"] != 111 {
		t.Fatalf("delays: %v", delays)
	}
	if len(*gets) != 1 || !strings.HasPrefix((*gets)[0], "/group/mawg-sub/delay?") || !strings.Contains((*gets)[0], "url=http%3A%2F%2F1.1.1.1%2F") || !strings.Contains((*gets)[0], "timeout=5000") {
		t.Fatalf("GET group/delay с кривыми параметрами: %v", *gets)
	}
}

// база <=0 и недоступный движок - честные ошибки, не паника
func TestClashErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := ClashProxies(ctx, 0); err == nil {
		t.Fatal("base=0 должен давать ошибку")
	}
	if _, err := ClashProxies(ctx, 1); err == nil {
		t.Fatal("недоступный порт должен давать ошибку")
	}
}
