package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunStoreAppendLoad(t *testing.T) {
	dir := t.TempDir()
	s := newRunStore(dir, func(string) {})
	for i := 0; i < 3; i++ {
		if err := s.append(TurnRecord{Type: "turn", Model: "m", User: "q"}); err != nil {
			t.Fatal(err)
		}
	}
	// 쓰다 끊긴 줄은 건너뛴다.
	path := filepath.Join(dir, time.Now().Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"turn","model":`)
	f.Close()

	items, err := s.load(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d records, want 3", len(items))
	}
	var rec TurnRecord
	if err := json.Unmarshal(items[0], &rec); err != nil || rec.Model != "m" {
		t.Fatalf("bad record %s: %v", items[0], err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, want 0600", st.Mode().Perm())
	}
}

func TestRunStoreLoadDays(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	os.WriteFile(filepath.Join(dir, old+".jsonl"), []byte(`{"type":"turn"}`+"\n"), 0o600)
	s := newRunStore(dir, func(string) {})
	s.append(TurnRecord{Type: "turn"})
	if items, _ := s.load(7); len(items) != 1 {
		t.Fatalf("days=7 got %d, want 1", len(items))
	}
	if items, _ := s.load(0); len(items) != 2 {
		t.Fatalf("days=0 got %d, want 2", len(items))
	}
}

func TestRunStoreSlowWarning(t *testing.T) {
	defer func(r, w time.Duration) { slowRead, slowWrite = r, w }(slowRead, slowWrite)
	slowRead, slowWrite = 0, 0
	var msgs []string
	s := newRunStore(t.TempDir(), func(m string) { msgs = append(msgs, m) })
	s.append(TurnRecord{Type: "turn"})
	s.load(1)
	if len(msgs) != 2 || !strings.Contains(msgs[0], "쓰기 느림") || !strings.Contains(msgs[1], "SQLite") {
		t.Fatalf("slow warnings = %q", msgs)
	}
}
