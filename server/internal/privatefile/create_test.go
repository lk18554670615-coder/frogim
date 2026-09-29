package privatefile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreateDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	f, e := Create(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteString("private fixture"); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	if f, e = Create(path); e == nil {
		f.Close()
		t.Fatal("existing artifact overwritten")
	}
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatal("excessive permissions")
	}
}
