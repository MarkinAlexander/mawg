package rotator

import (
	"fmt"
)

// ProbeConfigNow - живая проверка конкретного конфига пула: применяется
// здесь же (как при ручной активации), после паузы на iface-операцию
// выполняется штатная проба пула. Возвращает true, если проба прошла
// (полностью или lossy) и человекочитаемый текст вердикта. Используется
// генератором WARP: каждый свежесгенерированный конфиг проверяется сразу
// (слово владельца «надо проверять самостоятельно все варианты»),
// нерабочие эндпоинты видны честно.
//
// Вызывается из web-слоя синхронно; параллельность с фоновым checkPool
// разруливается actMu. В отличие от rotate() не учитывает cooldowns -
// это осознанная проверка конкретного файла.
func (e *Engine) ProbeConfigNow(pool, file string) (bool, string, error) {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok {
		return false, "", fmt.Errorf("пул %q не найден", pool)
	}
	if p.Disabled {
		return false, "", fmt.Errorf("пул %q выключен, сначала включите его", pool)
	}
	if _, ok := p.ConfigByFile(file); !ok {
		return false, "", fmt.Errorf("конфиг %q не найден в пуле %q", file, pool)
	}
	if err := e.applyConfig(p, file); err != nil {
		return false, "", err
	}
	e.settleAfterIfaceOp()
	v, detail := e.probe(p)
	return v != probeFail, detail, nil
}
