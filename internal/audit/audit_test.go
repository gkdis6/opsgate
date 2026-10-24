package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpen(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mode          os.FileMode
		link, wantErr bool
	}{
		{"private", 0600, false, false}, {"readable", 0644, false, false},
		{"group writable", 0620, false, true}, {"world writable", 0602, false, true},
		{"symlink", 0600, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			if tc.link {
				target := path
				path += ".link"
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			l, err := Open(path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Open error = %v, wantErr %v", err, tc.wantErr)
			}
			if l != nil {
				if err := l.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	path := filepath.Join(t.TempDir(), "new")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestConcurrentRecordAndAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit")
	for pass := 0; pass < 2; pass++ {
		l, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				l.Record(Event{Time: time.Unix(123, 0).UTC(), Tool: "test\nline", Args: map[string]any{"i": i}, Outcome: "ok"})
			}(i)
		}
		wg.Wait()
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if e.Tool != "test\nline" || !e.Time.Equal(time.Unix(123, 0)) {
			t.Fatalf("event = %+v", e)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("lines = %d", count)
	}
}

type panicJSON struct{}

func (panicJSON) MarshalJSON() ([]byte, error) { panic("invalid event") }

func TestRecordFailures(t *testing.T) {
	// Keep stderr capture sequential: os.Stderr is process global.
	old := os.Stderr
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = f
	defer func() { os.Stderr = old; f.Close() }()
	for _, tc := range []struct {
		name   string
		logger *Logger
		args   map[string]any
	}{
		{"nil", nil, nil}, {"zero", &Logger{}, nil},
		{"unsupported JSON", &Logger{file: f}, map[string]any{"bad": make(chan int)}},
		{"panic", &Logger{file: f}, map[string]any{"bad": panicJSON{}}},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.logger.Record(Event{Args: tc.args}) })
	}
	l, err := Open(filepath.Join(t.TempDir(), "audit"))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	l.Record(Event{})
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("missing error diagnostics")
	}
	stderrLogger, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := stderrLogger.Close(); err != nil {
		t.Fatal(err)
	}
	stderrLogger.Record(Event{Tool: "stderr"})
	if _, err := f.Stat(); err != nil {
		t.Fatalf("stderr closed: %v", err)
	}
}
