package web

import (
	"net/http"
	"strings"
	"testing"

	"mawg/internal/store"
	"mawg/internal/wgconf"
)

// Антиобфускация и уровни в карточке пула (слово владельца): голому
// конфигу - AWG-вариант, 2.0 - снутый вариант (комментарии остаются),
// 3.x - никаких преобразований.

const protonLikeWebConf = `[Interface]
PrivateKey = RRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRRR=
Address = 10.2.0.2/32
MTU = 1280
Jc = 3
Jmin = 1
Jmax = 3
I1 = <b 0xc2ababababababababababababababababababababababababababababababababababababababababababababababababababababababababababab>

[Peer]
# Server: US-FREE#34
PublicKey = DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD=
Endpoint = 149.102.254.89:51820
AllowedIPs = 0.0.0.0/0, ::/0
`

func TestPoolConfigLevelDownloads(t *testing.T) {
	ts, _, st := warpTestServer(t)
	if _, err := st.CreatePool("proton2", store.PoolSettings{Platform: "fake"}); err != nil {
		t.Fatal(err)
	}
	cfg2, err := wgconf.Parse([]byte(protonLikeWebConf))
	if err != nil {
		t.Fatalf("фикстура 2.0: %v", err)
	}
	if _, _, err := st.AddConfigs("proton2", []wgconf.NamedConfig{{OriginalName: "us34", Raw: []byte(protonLikeWebConf), Config: cfg2}}); err != nil {
		t.Fatal(err)
	}
	// 3.x - рядом (HP-ключ делает уровень awg3)
	raw3 := strings.Replace(strings.Replace(strings.Replace(protonLikeWebConf, "SSSSSSSSSSzLHCHCqpUPL9mypYmlDoEpbkl9+60DCBs9TTT=", strings.Repeat("D", 43)+"=", 1), "51820", "51821", 1), "MTU = 1280", "MTU = 1280\nHeaderProtectionKey = AAAA", 1)
	cfg3, err := wgconf.Parse([]byte(raw3))
	if err != nil {
		t.Fatalf("фикстура 3.x: %v", err)
	}
	if _, _, err := st.AddConfigs("proton2", []wgconf.NamedConfig{{OriginalName: "awg3", Raw: []byte(raw3), Config: cfg3}}); err != nil {
		t.Fatal(err)
	}

	// /status несёт уровень - панель по нему рисует кнопки
	statusBody := warpGetBody(mustGet(t, ts.URL+"/api/v1/status"))
	if !strings.Contains(statusBody, `"awg":"awg2"`) || !strings.Contains(statusBody, `"awg":"awg3"`) {
		t.Error("в /status нет уровней конфигов")
	}

	// 2.0: variant=plain - антиобфускация, комментарии остаются
	plain := warpGetBody(mustGet(t, ts.URL+"/api/v1/pools/proton2/configs/us34.conf?variant=plain"))
	if strings.Contains(plain, "Jc = 3") || strings.Contains(plain, "I1 = <b") {
		t.Error("поля обфускации не снялись")
	}
	if !strings.Contains(plain, "# Server: US-FREE#34") || !strings.Contains(plain, "Endpoint = 149.102.254.89:51820") {
		t.Error("снутый конфиг потерял комментарии/пиры")
	}
	if _, err := wgconf.Parse([]byte(plain)); err != nil {
		t.Errorf("снутый конфиг не разбирается: %v", err)
	}

	// 2.0: variant=awg - конфиг уже обфусцирован, отдаётся как есть
	awgBack := warpGetBody(mustGet(t, ts.URL+"/api/v1/pools/proton2/configs/us34.conf?variant=awg"))
	if !strings.Contains(awgBack, "Jc = 3") || strings.Contains(awgBack, "Jc = 4") {
		t.Error("AWG-вариант поверх 2.0 должен отдавать конфиг без изменений")
	}

	// 3.x: variant=plain - честный отказ
	resp := mustStatus(t, ts.URL+"/api/v1/pools/proton2/configs/awg3.conf?variant=plain")
	if resp.statusCode != http.StatusBadRequest {
		t.Fatalf("3.x plain: %d, ожидали 400", resp.statusCode)
	}
	if !strings.Contains(resp.body, "3.x") {
		t.Errorf("в отказе нет диагноза: %s", resp.body)
	}
	// 3.x: raw без вариантов - отдаётся как есть (кнопок в панели нет)
	rawBody := warpGetBody(mustGet(t, ts.URL+"/api/v1/pools/proton2/configs/awg3.conf"))
	if !strings.Contains(rawBody, "HeaderProtectionKey") {
		t.Error("raw 3.x конфиг изменился")
	}
}

type statusResp struct {
	statusCode int
	body       string
}

func mustStatus(t *testing.T, url string) statusResp {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<16)
	n, _ := resp.Body.Read(buf)
	return statusResp{statusCode: resp.StatusCode, body: string(buf[:n])}
}
