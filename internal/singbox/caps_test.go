package singbox

// Матрица возможностей ядер (112-док 2.2): источник+минимальная версия ->
// способности. Пороговые lx-версии - SPEC 083 (mlkem) и key_share-параметр.

import "testing"

func TestEngineCaps(t *testing.T) {
	cases := []struct {
		src, ver string
		want     CoreCaps
	}{
		// свежий lx: всё
		{SourceLXID, "1.14.4-lx.30", CoreCaps{XHTTP: true, VlessEncrypt: true, MlkemReality: true, KeyShareParam: true, AWG: true, Hysteria2: true}},
		// наш lx-форк той же линии
		{SourceLXOwnID, "1.14.2-lx.12", CoreCaps{XHTTP: true, VlessEncrypt: true, MlkemReality: true, KeyShareParam: true, AWG: true, Hysteria2: true}},
		// lx после SPEC 083, но до key_share-параметра
		{SourceLXID, "1.14.0-lx.36", CoreCaps{XHTTP: true, VlessEncrypt: true, MlkemReality: true, AWG: true, Hysteria2: true}},
		// lx до SPEC 083: без mlkem
		{SourceLXID, "1.14.0-lx.35", CoreCaps{XHTTP: true, VlessEncrypt: true, AWG: true, Hysteria2: true}},
		// ровно порог key_share
		{SourceLXID, "1.14.1-lx.4", CoreCaps{XHTTP: true, VlessEncrypt: true, MlkemReality: true, KeyShareParam: true, AWG: true, Hysteria2: true}},
		// extended: xhttp/шифрование/AWG есть, REALITY апстрим-линия
		{SourceExtID, "1.14.3-extended-2.7.3", CoreCaps{XHTTP: true, VlessEncrypt: true, AWG: true, Hysteria2: true}},
		// апстрим: только базовые протоколы
		{SourceUpID, "1.14.0", CoreCaps{Hysteria2: true}},
	}
	for _, tc := range cases {
		got := Engine{SourceID: tc.src, Version: tc.ver}.Caps()
		if got != tc.want {
			t.Fatalf("%s %s: матрица %+v, хочу %+v", tc.src, tc.ver, got, tc.want)
		}
	}
	// mihomo: своя матрица - без mlkem (empty reply, живая проверка 03.11)
	// проверяется тестами пакета mihomo (Engine.Caps)
}
