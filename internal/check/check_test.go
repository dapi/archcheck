package check

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, string, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, ".codegraph")
	if err := os.Mkdir(p, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p, "codegraph.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	schema := `CREATE TABLE schema_versions(version INTEGER); INSERT INTO schema_versions VALUES(11);
 CREATE TABLE project_metadata(key TEXT PRIMARY KEY,value TEXT); INSERT INTO project_metadata VALUES('index_state','complete'),('indexed_with_version','1.6.2');
 CREATE TABLE files(path TEXT PRIMARY KEY,content_hash TEXT,errors TEXT);
 CREATE TABLE nodes(id TEXT PRIMARY KEY,name TEXT,qualified_name TEXT,kind TEXT,language TEXT,file_path TEXT,start_line INTEGER);
 CREATE TABLE edges(source TEXT,target TEXT,kind TEXT,line INTEGER,provenance TEXT,metadata TEXT);
 CREATE TABLE unresolved_refs(id INTEGER);
 INSERT INTO nodes VALUES('a','submit','src/ui/orders.js::submit','function','javascript','src/ui/orders.js',1),('b','store','src/domain/store.js::store','function','javascript','src/domain/store.js',1);
 INSERT INTO edges VALUES('a','b','calls',3,NULL,NULL),('a','b','calls',3,NULL,NULL);`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"src/ui/orders.js", "src/domain/store.js"} {
		path := filepath.Join(root, rel)
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		body := []byte("export function example() {}\n")
		if err = os.WriteFile(path, body, 0644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if _, err = db.Exec("INSERT INTO files VALUES(?,?,'[]')", rel, hex.EncodeToString(sum[:])); err != nil {
			t.Fatal(err)
		}
	}
	return root, path, db
}
func config(t *testing.T) Config {
	t.Helper()
	c, e := Decode(strings.NewReader(JSPreset))
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestFindingsAndReadOnly(t *testing.T) {
	root, path, _ := fixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, e := Scan(context.Background(), root, path, config(t), true, false)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "violations" || len(r.Findings) != 1 || r.Findings[0].Line != 3 || r.Findings[0].Edge != "calls" {
		t.Fatalf("unexpected report: %+v", r)
	}
	after, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if string(before) != string(after) {
		t.Fatal("checker changed database")
	}
}
func TestNoObservedViolations(t *testing.T) {
	root, path, db := fixture(t)
	if _, e := db.Exec("DELETE FROM edges"); e != nil {
		t.Fatal(e)
	}
	r, e := Scan(context.Background(), root, path, config(t), true, true)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "no_observed_violations" {
		t.Fatalf("%+v", r)
	}
}
func TestBlockedInputs(t *testing.T) {
	for _, tc := range []struct{ name, sql, want string }{
		{"in-progress", "UPDATE project_metadata SET value='building' WHERE key='index_state'", "не завершён"},
		{"version", "UPDATE project_metadata SET value='9.0' WHERE key='indexed_with_version'", "несовместимый"},
		{"schema", "UPDATE schema_versions SET version=12", "несовместимый"},
		{"dangling", "INSERT INTO edges VALUES('missing','b','calls',1,NULL,NULL)", "целостность"},
		{"dangling-dispatch", `INSERT INTO edges VALUES('missing','b','calls',1,NULL,'{"synthesizedBy":"interface-impl"}')`, "целостность"},
		{"invalid-edge-metadata", `UPDATE edges SET metadata='{'`, "metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, path, db := fixture(t)
			if _, e := db.Exec(tc.sql); e != nil {
				t.Fatal(e)
			}
			_, e := Scan(context.Background(), root, path, config(t), true, false)
			if e == nil || !strings.Contains(e.Error(), tc.want) {
				t.Fatalf("got %v", e)
			}
		})
	}
}
func TestStaleAndDeletedFiles(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "deleted"}[deleted], func(t *testing.T) {
			root, path, _ := fixture(t)
			file := filepath.Join(root, "src/ui/orders.js")
			var e error
			if deleted {
				e = os.Remove(file)
			} else {
				e = os.WriteFile(file, []byte("changed"), 0644)
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = Scan(context.Background(), root, path, config(t), true, false); e == nil {
				t.Fatal("accepted stale graph")
			}
		})
	}
}
func TestEmptySelectorBlocked(t *testing.T) {
	root, path, _ := fixture(t)
	c := config(t)
	c.Rules[0].From.Paths = []string{"absent/**"}
	r, e := Scan(context.Background(), root, path, c, true, false)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "blocked" || len(r.Errors) != 1 {
		t.Fatalf("%+v", r)
	}
}
func TestIncompleteAnalysis(t *testing.T) {
	root, path, db := fixture(t)
	if _, e := db.Exec("INSERT INTO unresolved_refs VALUES(1)"); e != nil {
		t.Fatal(e)
	}
	r, e := Scan(context.Background(), root, path, config(t), true, true)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != "blocked" || r.Coverage.UnresolvedReferences != 1 {
		t.Fatalf("%+v", r)
	}
	if _, e = db.Exec("DELETE FROM unresolved_refs; UPDATE files SET errors='[\"syntax error\"]' WHERE path='src/ui/orders.js'"); e != nil {
		t.Fatal(e)
	}
	r, e = Scan(context.Background(), root, path, config(t), true, false)
	if e != nil || r.Status != "blocked" {
		t.Fatalf("%+v, %v", r, e)
	}
}
func TestSafePath(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if e := os.WriteFile(outside, []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Skip(e)
	}
	for _, p := range []string{"../secret", outside, "escape"} {
		if _, e := safePath(root, p); e == nil {
			t.Fatalf("accepted %q", p)
		}
	}
}
func TestInvalidConfig(t *testing.T) {
	for _, body := range []string{
		"version: 2\nrules: []", "version: 1\nrules: []", JSPreset + "typo: true\n", JSPreset + "---\nversion: 1\n",
		strings.Replace(JSPreset, "edges: [imports, calls]", "edges: [made_up]", 1), strings.Replace(JSPreset, "src/ui/**", "[", 1), strings.Replace(JSPreset, "src/ui/**", "../src/**", 1), strings.Replace(JSPreset, "paths: [\"src/ui/**\"]", "wrong: true", 1),
	} {
		if _, e := Decode(strings.NewReader(body)); e == nil {
			t.Fatalf("accepted %q", body)
		}
	}
}
func TestSelectorFields(t *testing.T) {
	n := Node{Name: "charge", QualifiedName: "app/clients/payment.rb::PaymentGateway.charge", File: "app/clients/payment.rb", Kind: "method", Language: "ruby"}
	s := Selector{Paths: []string{"app/clients/**"}, Symbols: []string{"**/*PaymentGateway.charge"}, Kinds: []string{"method"}, Languages: []string{"ruby"}}
	if !s.Match(n) {
		t.Fatal("expected AND match")
	}
	s.Languages = []string{"javascript"}
	if s.Match(n) {
		t.Fatal("ignored language restriction")
	}
}

func TestDispatchBridgesAreNotDirectCalls(t *testing.T) {
	root, path, db := fixture(t)
	_, err := db.Exec(`DELETE FROM edges;
 INSERT INTO edges VALUES('a','b','calls',1,'heuristic','{"synthesizedBy":"interface-impl"}');
 INSERT INTO edges VALUES('a','b','calls',3,'heuristic','{"resolvedBy":"framework"}');`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Scan(context.Background(), root, path, config(t), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Findings) != 1 || r.Findings[0].Line != 3 || r.Coverage.ExcludedDispatchEdges != 1 || r.Coverage.Edges != 2 {
		t.Fatalf("%+v", r)
	}
}
