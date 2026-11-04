package singbox

// CoreCaps - честная матрица возможностей ядра движка (112-док 2.2):
// источник + минимальная версия -> способности, вместо отдельных гейтов
// по source-id. Рендер honest-skip ссылается на конкретную способность:
// пользователь видит, ЧЕГО не хватает его ядру, а не «не подходит».
//
// Данные: lx-CHANGELOG (§281 SPEC 083 mlkem - патч с v1.14.0-lx.36;
// §457 tls.reality.key_share hybrid|classical - с v1.14.1-lx.4);
// extended 2.7.3 - xhttp/vless-шифрование/AWG есть, REALITY без SPEC 083
// (живая проверка 03.11: PQ-узел уводится на камуфляж, x509 *.azureedge.net;
// grep 'reality key_share' по бинарю = 0); апстрим - без форк-расширений;
// mihomo - своя матрица в пакете mihomo (без mlkem, живая проверка:
// empty reply).

type CoreCaps struct {
	// XHTTP - транспорт vless xhttp
	XHTTP bool
	// VlessEncrypt - vless encryption= (mlkem768x25519plus... и прочие)
	VlessEncrypt bool
	// MlkemReality - REALITY несёт X25519MLKEM768 key share (SPEC 083):
	// пост-квантовые узлы (Xray 26.9.8+) работают только на таких ядрах
	MlkemReality bool
	// KeyShareParam - конфиг понимает tls.reality.key_share (LxBox §457)
	KeyShareParam bool
	// AWG - обфускация AmneziaWG в wireguard-эндпоинтах
	AWG bool
	// Hysteria2 - исходящий hysteria2 (в апстриме с 1.9)
	Hysteria2 bool
}

// минимальные версии линии lx (CHANGELOG форка)
const (
	lxMlkemSince    = "1.14.0-lx.36" // SPEC 083: возврат X25519MLKEM768
	lxKeyShareSince = "1.14.1-lx.4"  // tls.reality.key_share
)

// lxVerSrc - источник lx для семвер-сравнения суффиксных версий (-lx.N)
var lxVerSrc = Source{ID: SourceLXID}

// lxAtLeast - версия ядра lx не ниже минимальной.
func lxAtLeast(ver, min string) bool {
	return ver == min || lxVerSrc.Newer(ver, min)
}

// Caps - способности этого ядра по источнику и версии.
func (e Engine) Caps() CoreCaps {
	c := CoreCaps{Hysteria2: true}
	switch e.SourceID {
	case SourceLXID, SourceLXOwnID:
		c.XHTTP, c.VlessEncrypt, c.AWG = true, true, true
		c.MlkemReality = lxAtLeast(e.Version, lxMlkemSince)
		c.KeyShareParam = lxAtLeast(e.Version, lxKeyShareSince)
	case SourceExtID:
		c.XHTTP, c.VlessEncrypt, c.AWG = true, true, true
	}
	return c
}
