package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthenticatedRestoreAndNoOverwrite(t *testing.T) {
	d := t.TempDir()
	key, src, enc, dst := filepath.Join(d, "key"), filepath.Join(d, "src"), filepath.Join(d, "sealed"), filepath.Join(d, "restored")
	for p, b := range map[string][]byte{key: bytes.Repeat([]byte{3}, 32), src: bytes.Repeat([]byte{0, 255, 1}, 700000)} {
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := transform("encrypt", src, enc, key); err != nil {
		t.Fatal(err)
	}
	if err := transform("encrypt", src, enc, key); err == nil {
		t.Fatal("overwrote archive")
	}
	if err := transform("decrypt", enc, dst, key); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(src)
	b, _ := os.ReadFile(dst)
	if !bytes.Equal(a, b) {
		t.Fatal("round trip changed bytes")
	}
	sealed, _ := os.ReadFile(enc)
	for _, bad := range [][]byte{sealed[:len(sealed)-20], append(append([]byte{}, sealed...), 1)} {
		badIn, badOut := filepath.Join(t.TempDir(), "input"), filepath.Join(t.TempDir(), "output")
		if err := os.WriteFile(badIn, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if transform("decrypt", badIn, badOut, key) == nil {
			t.Fatal("accepted corrupt archive")
		}
		if _, err := os.Stat(badOut); !os.IsNotExist(err) {
			t.Fatal("retained partial restore")
		}
	}
}
