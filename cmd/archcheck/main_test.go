package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	var out, errs bytes.Buffer
	if c := run(context.Background(), []string{"init", "--project", root, "--preset", "rails"}, &out, &errs); c != 0 {
		t.Fatal(c, errs.String())
	}
	path := filepath.Join(root, "archcheck.yaml")
	first, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if c := run(context.Background(), []string{"init", "--project", root}, &out, &errs); c != 2 {
		t.Fatal(c)
	}
	after, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(first, after) {
		t.Fatal("overwrote configuration")
	}
}
func TestJSONErrorExit(t *testing.T) {
	var out, errs bytes.Buffer
	c := run(context.Background(), []string{"scan", "--project", t.TempDir(), "--format", "json"}, &out, &errs)
	if c != 2 {
		t.Fatal(c)
	}
	var r map[string]any
	if e := json.Unmarshal(out.Bytes(), &r); e != nil {
		t.Fatal(e, out.String())
	}
	if r["status"] != "blocked" {
		t.Fatal(r)
	}
}
func TestHelpAndUnknownCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{{nil, 0}, {[]string{"scan", "--help"}, 0}, {[]string{"version"}, 0}, {[]string{"unknown"}, 2}, {[]string{"scan", "extra"}, 2}} {
		var out, errs bytes.Buffer
		if c := run(context.Background(), tc.args, &out, &errs); c != tc.code {
			t.Fatal(tc, c, errs.String())
		}
	}
}
