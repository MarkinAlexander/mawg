package web

// Черновик записей MagiTrickle (114-док): при выключенном «Автосохранении»
// правки вкладки «Группы» копятся в base/mt-draft.json и пишутся в MT ОДНИМ
// массовым PUT по команде (дизайн как у нативной панели MT - слово владельца
// 08.11: щёлкаешь свободно - жмёшь «Сохранить»). Фоновые писатели (каскады,
// ротатор, подписки, политики групп, умный каскад) в MT не гейтятся.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"mawg/internal/magitrickle"
)

// mtDraftFile - формат base/mt-draft.json: base - состояние MT на момент
// старта черновика (дифф для панели и merge при применении), groups -
// правки пользователя. Сырые массивы групп: store тип magitrickle не знает.
type mtDraftFile struct {
	Base   json.RawMessage `json:"base"`
	Groups json.RawMessage `json:"groups"`
	At     time.Time       `json:"at"`
}

// mtDraftIDPrefix - локальные ID групп/правил, созданных в черновике: ID
// назначает сам magitrickle, при применении они выбрасываются (как при
// импорте групп файлом).
const mtDraftIDPrefix = "draft-"

func (s *Server) autoSaveOn() bool { return s.store.AutoSaveMT() }

// draftLocalID - свободный локальный ID новой группы черновика.
func draftLocalID(groups []magitrickle.Group) string {
	n := 1
	for _, g := range groups {
		if rest, ok := strings.CutPrefix(g.ID, mtDraftIDPrefix); ok {
			if v, err := strconv.Atoi(rest); err == nil && v >= n {
				n = v + 1
			}
		}
	}
	return mtDraftIDPrefix + strconv.Itoa(n)
}

// draftLocalRuleID - свободный локальный ID нового правила черновика.
func draftLocalRuleID(rules []magitrickle.Rule) string {
	n := 1
	for _, rl := range rules {
		if rest, ok := strings.CutPrefix(rl.ID, mtDraftIDPrefix+"r"); ok {
			if v, err := strconv.Atoi(rest); err == nil && v >= n {
				n = v + 1
			}
		}
	}
	return mtDraftIDPrefix + "r" + strconv.Itoa(n)
}

func marshalDraftFile(base, groups json.RawMessage) json.RawMessage {
	env, _ := json.MarshalIndent(mtDraftFile{Base: base, Groups: groups, At: time.Now()}, "", "  ")
	return env
}

func decodeDraftGroups(raw json.RawMessage) ([]magitrickle.Group, error) {
	var out []magitrickle.Group
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// draftEnsureLocked - (base, raw-cur, cur) черновика; mutex s.mtDraftMu
// держится. Файла нет - черновик создаётся: base = groups = снапшот MT.
func (s *Server) draftEnsureLocked(ctx context.Context) (json.RawMessage, json.RawMessage, []magitrickle.Group, error) {
	if raw, ok := s.store.MTDraftGet(); ok {
		var df mtDraftFile
		if err := json.Unmarshal(raw, &df); err != nil {
			return nil, nil, nil, fmt.Errorf("черновик повреждён, применить нельзя - сбросьте его: %v", err)
		}
		cur, err := decodeDraftGroups(df.Groups)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("черновик повреждён, применить нельзя - сбросьте его: %v", err)
		}
		return df.Base, df.Groups, cur, nil
	}
	groups, err := s.mtClient().GroupsWithRules(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	data, err := json.Marshal(groups)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := s.store.MTDraftSave(marshalDraftFile(data, data)); err != nil {
		return nil, nil, nil, err
	}
	// cur - отдельная декодировка: мутации черновика не должны трогать base
	cur, err := decodeDraftGroups(data)
	if err != nil {
		return nil, nil, nil, err
	}
	return data, data, cur, nil
}

// draftEnsure - (base, cur) черновика; файла нет - создаётся с базой из
// снапшота MT.
func (s *Server) draftEnsure(ctx context.Context) ([]magitrickle.Group, []magitrickle.Group, error) {
	s.mtDraftMu.Lock()
	defer s.mtDraftMu.Unlock()
	baseRaw, _, cur, err := s.draftEnsureLocked(ctx)
	if err != nil {
		return nil, nil, err
	}
	base, err := decodeDraftGroups(baseRaw)
	if err != nil {
		return nil, nil, fmt.Errorf("черновик повреждён: %v", err)
	}
	return base, cur, nil
}

// draftMutate - атомарный цикл правки черновика: ensure -> мутация -> файл
// (base в файле нетронут). mutate возвращает (возможно новый) список: append
// и удаление в срезе-значении наружу не выходят.
func (s *Server) draftMutate(ctx context.Context, mutate func([]magitrickle.Group) []magitrickle.Group) error {
	s.mtDraftMu.Lock()
	defer s.mtDraftMu.Unlock()
	baseRaw, _, cur, err := s.draftEnsureLocked(ctx)
	if err != nil {
		return err
	}
	cur = mutate(cur)
	data, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	return s.store.MTDraftSave(marshalDraftFile(baseRaw, data))
}

// draftRead - группы черновика для mtGetGroups; false = черновика нет.
// Повреждённый файл читается как «нет»: панель покажет актуальный MT, а
// мутации/применение отдадут ошибку - сбросом черновик снимается.
func (s *Server) draftRead() ([]magitrickle.Group, bool) {
	s.mtDraftMu.Lock()
	defer s.mtDraftMu.Unlock()
	raw, ok := s.store.MTDraftGet()
	if !ok {
		return nil, false
	}
	var df mtDraftFile
	if err := json.Unmarshal(raw, &df); err != nil {
		return nil, false
	}
	groups, err := decodeDraftGroups(df.Groups)
	if err != nil {
		return nil, false
	}
	return groups, true
}

// draftDirty - число правок черновика против базы: изменённые/добавленные/
// удалённые группы и правила плюс один балл за изменившийся порядок
// (порядок - тоже правка пользователя, 114-док).
func (s *Server) draftDirty(base, cur []magitrickle.Group) int {
	baseByID := make(map[string]magitrickle.Group, len(base))
	baseOrder := make([]string, 0, len(base))
	for _, g := range base {
		baseByID[g.ID] = g
		baseOrder = append(baseOrder, g.ID)
	}
	curByID := make(map[string]bool, len(cur))
	curOrder := make([]string, 0, len(cur))
	dirty := 0
	for _, g := range cur {
		curByID[g.ID] = true
		curOrder = append(curOrder, g.ID)
		bg, ok := baseByID[g.ID]
		if !ok {
			dirty++ // добавленная группа
			continue
		}
		if draftGroupChanged(bg, g) {
			dirty++ // поля группы
		}
		dirty += draftRulesDirty(bg.Rules, g.Rules)
	}
	for _, g := range base {
		if !curByID[g.ID] {
			dirty++ // удалённая группа
		}
	}
	if draftOrderChanged(baseOrder, curOrder) {
		dirty++
	}
	return dirty
}

func draftGroupChanged(a, b magitrickle.Group) bool {
	return a.Name != b.Name || a.Color != b.Color || a.Interface != b.Interface ||
		a.Enable != b.Enable || !slices.Equal(a.Sources, b.Sources)
}

func draftRulesDirty(base, cur []magitrickle.Rule) int {
	baseByID := make(map[string]magitrickle.Rule, len(base))
	baseOrder := make([]string, 0, len(base))
	for _, rl := range base {
		baseByID[rl.ID] = rl
		baseOrder = append(baseOrder, rl.ID)
	}
	curByID := make(map[string]bool, len(cur))
	curOrder := make([]string, 0, len(cur))
	dirty := 0
	for _, rl := range cur {
		curByID[rl.ID] = true
		curOrder = append(curOrder, rl.ID)
		br, ok := baseByID[rl.ID]
		if !ok {
			dirty++ // добавленное правило
			continue
		}
		if rl.Type != br.Type || rl.Rule != br.Rule || rl.Name != br.Name || rl.Enable != br.Enable {
			dirty++ // поля правила
		}
	}
	for _, rl := range base {
		if !curByID[rl.ID] {
			dirty++ // удалённое правило
		}
	}
	if draftOrderChanged(baseOrder, curOrder) {
		dirty++
	}
	return dirty
}

// draftOrderChanged - взаимный порядок общих ID изменился; добавленные/
// удалённые не в счёт: они дают свои баллы отдельно.
func draftOrderChanged(baseOrder, curOrder []string) bool {
	baseSet := make(map[string]bool, len(baseOrder))
	for _, id := range baseOrder {
		baseSet[id] = true
	}
	curSet := make(map[string]bool, len(curOrder))
	for _, id := range curOrder {
		curSet[id] = true
	}
	commonBase := make([]string, 0, len(baseOrder))
	for _, id := range baseOrder {
		if curSet[id] {
			commonBase = append(commonBase, id)
		}
	}
	commonCur := make([]string, 0, len(curOrder))
	for _, id := range curOrder {
		if baseSet[id] {
			commonCur = append(commonCur, id)
		}
	}
	return !slices.Equal(commonBase, commonCur)
}

// draftStatus - статус для ответов панели: активен ли черновик и сколько
// в нём правок против базы.
func (s *Server) draftStatus() map[string]any {
	s.mtDraftMu.Lock()
	defer s.mtDraftMu.Unlock()
	raw, ok := s.store.MTDraftGet()
	if !ok {
		return map[string]any{"active": false, "dirty": 0}
	}
	var df mtDraftFile
	if err := json.Unmarshal(raw, &df); err != nil {
		return map[string]any{"active": true, "dirty": 0}
	}
	base, errBase := decodeDraftGroups(df.Base)
	cur, errCur := decodeDraftGroups(df.Groups)
	if errBase != nil || errCur != nil {
		return map[string]any{"active": true, "dirty": 0}
	}
	return map[string]any{"active": true, "dirty": s.draftDirty(base, cur)}
}

// draftResponse - ответ мутационного хендлера с добавленным статусом
// черновика: панель по draft.active/dirty рисует «Сохранить/Сбросить».
func (s *Server) draftResponse(w http.ResponseWriter, code int, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		writeErr(w, err)
		return
	}
	m := map[string]any{}
	if err := json.Unmarshal(data, &m); err != nil {
		writeJSON(w, code, map[string]any{"payload": json.RawMessage(data), "draft": s.draftStatus()})
		return
	}
	m["draft"] = s.draftStatus()
	writeJSON(w, code, m)
}

// stripDraftIDs - локальные draft-ID выбрасываются перед записью в MT:
// ID назначает сам magitrickle.
func stripDraftIDs(g magitrickle.Group) magitrickle.Group {
	if strings.HasPrefix(g.ID, mtDraftIDPrefix) {
		g.ID = ""
	}
	for j := range g.Rules {
		if strings.HasPrefix(g.Rules[j].ID, mtDraftIDPrefix) {
			g.Rules[j].ID = ""
		}
	}
	return g
}

// draftMerge - черновик поверх АКТУАЛЬНОГО MT (114-док): фоновые писатели
// правят служебные группы (dnszone-*/dns-tunnel/«Служебная: ») между
// правками пользователя, поэтому их версии берутся из рантайма по ID, а
// не из черновика; служебная, удалённая фоном, не воскрешается. Группы
// рантайма, которых в черновике нет (созданы фоном после старта черновика),
// дописываются в конец; дубли по ID сворачиваются.
func draftMerge(draft, current []magitrickle.Group) []magitrickle.Group {
	curByID := make(map[string]magitrickle.Group, len(current))
	for _, g := range current {
		curByID[g.ID] = g
	}
	seen := make(map[string]bool, len(draft)+len(current))
	merged := make([]magitrickle.Group, 0, len(draft)+len(current))
	for _, g := range draft {
		if seen[g.ID] {
			continue
		}
		seen[g.ID] = true
		if isServiceGroup(g.Name) {
			if cg, ok := curByID[g.ID]; ok {
				merged = append(merged, cg)
			}
			continue
		}
		merged = append(merged, stripDraftIDs(g))
	}
	for _, g := range current {
		if seen[g.ID] {
			continue
		}
		seen[g.ID] = true
		merged = append(merged, g)
	}
	return merged
}

// mtReplaceGroups - массовый PUT готового списка групп под самолечением
// RetryMTWrite: черновик несёт добавленные/удалённые группы, состав списка
// меняется, in-place mutate этого не выражает.
func (s *Server) mtReplaceGroups(ctx context.Context, groups []magitrickle.Group) error {
	return s.engine.RetryMTWrite("panel", func() error {
		return s.mtClient().ReplaceGroups(ctx, groups)
	})
}

// mtApplyDraft - POST /api/v1/mt/apply-draft: черновик сливается с
// актуальным MT и пишется одним массовым PUT (114-док).
func (s *Server) mtApplyDraft(w http.ResponseWriter, r *http.Request) {
	s.mtDraftMu.Lock()
	raw, ok := s.store.MTDraftGet()
	if !ok {
		s.mtDraftMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "черновика нет"})
		return
	}
	var df mtDraftFile
	if err := json.Unmarshal(raw, &df); err != nil {
		s.mtDraftMu.Unlock()
		writeErr(w, fmt.Errorf("черновик повреждён, сбросьте его: %v", err))
		return
	}
	draft, err := decodeDraftGroups(df.Groups)
	if err != nil {
		s.mtDraftMu.Unlock()
		writeErr(w, fmt.Errorf("черновик повреждён, сбросьте его: %v", err))
		return
	}
	current, err := s.mtClient().GroupsWithRules(r.Context())
	if err != nil {
		s.mtDraftMu.Unlock()
		writeErr(w, err)
		return
	}
	merged := draftMerge(draft, current)
	if err := s.mtReplaceGroups(r.Context(), merged); err != nil {
		s.mtDraftMu.Unlock()
		writeErr(w, err)
		return
	}
	if err := s.store.MTDraftClear(); err != nil {
		s.mtDraftMu.Unlock()
		writeErr(w, err)
		return
	}
	s.store.LogEvent("rules", "magitrickle", fmt.Sprintf("apply-draft: применено групп %d", len(merged)))
	s.mtClient().RefreshShadow(r.Context())
	s.mtDraftMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "applied": len(merged)})
}

// mtDiscardDraft - POST /api/v1/mt/discard-draft: выбросить черновик,
// ничего не записывая в MT.
func (s *Server) mtDiscardDraft(w http.ResponseWriter, r *http.Request) {
	s.mtDraftMu.Lock()
	err := s.store.MTDraftClear()
	s.mtDraftMu.Unlock()
	if err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "черновик MagiTrickle сброшен")
	s.draftResponse(w, http.StatusOK, map[string]any{"ok": true})
}

// mtSetAutoSave - POST /api/v1/mt/autosave {enable}. Включать автосохранение
// при активном черновике нельзя: правки остались бы висеть в файле, который
// автосохранение игнорирует (114-док).
func (s *Server) mtSetAutoSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Enable {
		s.mtDraftMu.Lock()
		_, active := s.store.MTDraftGet()
		s.mtDraftMu.Unlock()
		if active {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "сначала примените или сбросьте черновик"})
			return
		}
	}
	if err := s.store.SetAutoSaveMT(req.Enable); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "autoSave": s.autoSaveOn(), "draft": s.draftStatus()})
}
