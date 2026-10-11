package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventsPersistAcrossReopen(t *testing.T) {
	base := t.TempDir()
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	st.LogEvent("p", "test", "первое")
	st.LogEvent("p", "test", "второе")

	st2, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	evs := st2.Events()
	if len(evs) != 2 {
		t.Fatalf("хочу 2 события после переоткрытия, есть %d", len(evs))
	}
	if evs[0].Message != "первое" || evs[1].Message != "второе" {
		t.Fatalf("порядок потерян: %q, %q", evs[0].Message, evs[1].Message)
	}
}

func TestEventsRotationAndTailLoad(t *testing.T) {
	base := t.TempDir()
	st, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("х", 4096)
	for i := 0; i < 70; i++ { // 70 * ~4.1КБ > 256КБ - должен сработать ротейт
		st.LogEvent("p", "test", big)
	}
	if _, err := os.Stat(filepath.Join(base, "events.json.old")); err != nil {
		t.Fatalf("ротация не сработала: %v", err)
	}
	st2, err := Open(base)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(st2.Events()); n == 0 {
		t.Fatal("после ротации хвост журнала не поднялся")
	}
}
