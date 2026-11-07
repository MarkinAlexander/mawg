package web

// Хендлеры/логика слотов WireGuard Keenetic (из server.go).

import (
	"errors"
	"fmt"
	"net/http"

	"mawg/internal/platform"
	"mawg/internal/platform/keenetic"
	"mawg/internal/store"
)

func (s *Server) validFallback(name, fallback string) error {
	if fallback == "" || fallback == store.FallbackDirect || fallback == store.FallbackHold {
		return nil
	}
	fbName, ok := store.FallbackPoolName(fallback)
	if !ok {
		return fmt.Errorf("фоллбек должен быть direct, hold или pool:<имя пула>")
	}
	if fbName == name {
		return fmt.Errorf("фоллбек на самого себя")
	}
	seen := map[string]bool{name: true}
	cur := fbName
	for i := 0; i < 16; i++ {
		if seen[cur] {
			return fmt.Errorf("цикл фоллбеков через пул %s", cur)
		}
		seen[cur] = true
		p, ok := s.store.Pool(cur)
		if !ok {
			return fmt.Errorf("пул %q не найден", cur)
		}
		next, ok := store.FallbackPoolName(p.Settings.Fallback)
		if !ok {
			return nil
		}
		cur = next
	}
	return fmt.Errorf("слишком длинная цепочка фоллбеков")
}

func (s *Server) createSlot(w http.ResponseWriter, r *http.Request) {
	kb, ok := s.backend.(*keenetic.Backend)
	if !ok {
		writeErr(w, fmt.Errorf("создание слотов доступно только на Keenetic"))
		return
	}
	id, err := kb.CreateSlot()
	if err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("slots", "create", "создан слот "+id)
	if err := s.backend.SaveConfig(); err != nil {
		s.store.LogEvent("slots", "create", "конфиг не сохранился: "+err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]string{"slot": id, "device": keenetic.DeviceName(id)})
}

// resolveKeeneticSlot - слот пула для Keenetic: явно выбранный проверяется
// на существование (выпадение выбора в панели больше не роняет создание
// пула с путаным «не удалось получить список слотов»), пустой выбор
// добирается первым свободным слотом - как их подсвечивает панель.
func (s *Server) resolveKeeneticSlot(slot string) (string, error) {
	if s.backend.Name() != store.PlatformKeenetic {
		return "", nil
	}
	slots, err := s.backend.Slots()
	if err != nil {
		return "", fmt.Errorf("список слотов WireGuard недоступен: %v", err)
	}
	taken := map[string]bool{}
	for _, p := range s.store.Pools() {
		if p.Settings.KeeneticSlot != "" {
			taken[p.Settings.KeeneticSlot] = true
		}
	}
	if slot != "" {
		for _, sl := range slots {
			if sl.ID != slot {
				continue
			}
			if taken[slot] {
				return "", fmt.Errorf("слот %s уже используется другим пулом mawg", slot)
			}
			return slot, nil
		}
		return "", fmt.Errorf("слот %s не найден на роутере, обновите список слотов", slot)
	}
	for _, sl := range slots {
		if taken[sl.ID] || sl.LinkUp || sl.Connected || sl.Description != "" {
			continue
		}
		return sl.ID, nil
	}
	return "", fmt.Errorf("%w - создайте новый кнопкой «+ слот» в диалоге пула", errNoFreeKeeneticSlot)
}

// errNoFreeKeeneticSlot - маркер исчерпания свободных слотов: генератор
// WARP ловит его и создаёт слот сам, прочие вызывающие показывают текст.
var errNoFreeKeeneticSlot = errors.New("нет свободных слотов WireGuard")

// resolveKeeneticSlotAuto - как resolveKeeneticSlot, но при исчерпании
// свободных слотов создаёт новый сама (генератор WARP: первый warp-пул на
// Кинетике не должен отправлять в диалог пула за кнопкой «+ слот» посреди
// мастера). Автосоздание - только на «нет свободных» с пустым выбором:
// занятый/несуществующий слот остаётся честной ошибкой, как раньше.
func (s *Server) resolveKeeneticSlotAuto(slot string) (string, error) {
	resolved, err := s.resolveKeeneticSlot(slot)
	if err == nil || slot != "" || !errors.Is(err, errNoFreeKeeneticSlot) {
		return resolved, err
	}
	kb, ok := s.backend.(platform.SlotCreator)
	if !ok {
		return resolved, err
	}
	id, cerr := kb.CreateSlot()
	if cerr != nil {
		return "", fmt.Errorf("свободных слотов WireGuard нет, создать новый не удалось: %v", cerr)
	}
	s.store.LogEvent("slots", "create", "создан слот "+id+" (автоматически для пула WARP)")
	if serr := s.backend.SaveConfig(); serr != nil {
		s.store.LogEvent("slots", "create", "конфиг не сохранился: "+serr.Error())
	}
	return id, nil
}
