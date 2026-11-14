package singbox

// Выбор узла режима "fallback" (первый живой по порядку): общая логика
// для обоих ядер - sing-box (manager_linux.go) и mihomo. Чистая функция
// без build-тегов - тестируется на любой платформе.

// FallbackOkThreshold - сколько успехов подряд должен дать старший узел,
// чтобы группа вернулась на него: одиночное «ок» флапающего узла не
// должно гонять переключения (та же философия, что у бандлов:
// extMemberOkThreshold, кейс владельца 12.11 про флап tun9).
const FallbackOkThreshold = 3

// FallbackPick - выбор активного узла fallback-группы. order - теги узлов
// по ручному порядку (NodeOrder), alive - кто ответил замеру, current -
// текущий выбор группы (ClashProxies.Now), okStreak - накопленные подряд
// успехи старшего. Возвращает: кого выбрать, нужно ли переключение и
// новый стрик. Никто не жив - остаёмся на текущем.
func FallbackPick(order []string, alive map[string]bool, current string, okStreak int) (pick string, sw bool, streak int) {
	if len(order) == 0 {
		return current, false, okStreak
	}
	senior := order[0]
	if alive[senior] {
		streak = okStreak + 1
		if current == senior {
			return senior, false, streak
		}
		if streak >= FallbackOkThreshold {
			return senior, true, 0
		}
		if alive[current] {
			// старший ожил недавно - ждём доверия, текущий живой
			return current, false, streak
		}
		// текущий мёртв, старший жив - переходим немедленно
		return senior, true, 0
	}
	streak = 0
	for _, t := range order[1:] {
		if alive[t] {
			if t == current {
				return t, false, 0
			}
			return t, true, 0
		}
	}
	return current, false, 0
}
