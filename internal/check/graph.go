package check

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

const SupportedCodeGraph = "1.6.2"
const SupportedSchema = 11

type Node struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	Language      string `json:"language"`
	File          string `json:"file"`
	Line          int    `json:"line"`
}
type Finding struct {
	RuleID      string `json:"rule_id"`
	Description string `json:"description"`
	Edge        string `json:"edge"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	From        Node   `json:"from"`
	To          Node   `json:"to"`
	Provenance  string `json:"provenance,omitempty"`
}
type RuleResult struct {
	ID        string `json:"id"`
	FromNodes int    `json:"from_nodes"`
	ToNodes   int    `json:"to_nodes"`
	Findings  int    `json:"findings"`
}
type Coverage struct {
	Files                int    `json:"files"`
	Nodes                int    `json:"nodes"`
	Edges                int    `json:"edges"`
	UnresolvedReferences int    `json:"unresolved_references"`
	ParseErrorFiles      int    `json:"parse_error_files"`
	CodeGraphVersion     string `json:"codegraph_version"`
	SchemaVersion        int    `json:"schema_version"`
	Synced               bool   `json:"synced"`
}
type Report struct {
	Status   string       `json:"status"`
	Coverage Coverage     `json:"coverage"`
	Rules    []RuleResult `json:"rules"`
	Findings []Finding    `json:"findings"`
	Warnings []string     `json:"warnings"`
	Errors   []string     `json:"errors"`
}

func Scan(ctx context.Context, project, database string, config Config, synced, strict bool) (Report, error) {
	r := Report{Status: "blocked", Rules: []RuleResult{}, Findings: []Finding{}, Warnings: []string{}, Errors: []string{}}
	r.Coverage.Synced = synced
	project, err := filepath.Abs(project)
	if err != nil {
		return r, err
	}
	if _, err = os.Stat(database); err != nil {
		return r, fmt.Errorf("индекс недоступен: выполните codegraph init: %w", err)
	}
	absolute, err := filepath.Abs(database)
	if err != nil {
		return r, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	q := uri.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	uri.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return r, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	// Pin the snapshot before reading graph tables. Never accept an in-progress index.
	var state string
	if err = tx.QueryRowContext(ctx, "SELECT value FROM project_metadata WHERE key='index_state'").Scan(&state); err != nil {
		return r, fmt.Errorf("несовместимый индекс CodeGraph (index_state): %w", err)
	}
	if state != "complete" {
		return r, fmt.Errorf("индекс не завершён: %s; выполните codegraph index", state)
	}
	if err = tx.QueryRowContext(ctx, "SELECT value FROM project_metadata WHERE key='indexed_with_version'").Scan(&r.Coverage.CodeGraphVersion); err != nil {
		return r, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT max(version) FROM schema_versions").Scan(&r.Coverage.SchemaVersion); err != nil {
		return r, err
	}
	if r.Coverage.CodeGraphVersion != SupportedCodeGraph || r.Coverage.SchemaVersion != SupportedSchema {
		return r, fmt.Errorf("несовместимый индекс: CodeGraph %s, схема %d; проверены %s / %d", r.Coverage.CodeGraphVersion, r.Coverage.SchemaVersion, SupportedCodeGraph, SupportedSchema)
	}
	if err = checkFiles(ctx, tx, project, &r); err != nil {
		return r, err
	}
	if r.Coverage.Files == 0 {
		return r, fmt.Errorf("индекс не содержит исходных файлов")
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM unresolved_refs").Scan(&r.Coverage.UnresolvedReferences); err != nil {
		return r, err
	}
	if r.Coverage.UnresolvedReferences > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Не разрешено ссылок: %d. Отсутствие нарушения в графе не доказывает его отсутствие в коде.", r.Coverage.UnresolvedReferences))
	}
	if !synced {
		r.Warnings = append(r.Warnings, "Синхронизация пропущена: проверены хеши индексированных файлов, новые файлы могут отсутствовать в графе.")
	}
	if strict && (!synced || r.Coverage.UnresolvedReferences > 0 || r.Coverage.ParseErrorFiles > 0) {
		r.Errors = append(r.Errors, "Строгий режим требует синхронизации, отсутствия ошибок парсинга и неразрешённых ссылок.")
	}
	nodes, err := loadNodes(ctx, tx)
	if err != nil {
		return r, err
	}
	r.Coverage.Nodes = len(nodes)
	fromSets := make([]map[string]bool, len(config.Rules))
	toSets := make([]map[string]bool, len(config.Rules))
	for i, rule := range config.Rules {
		fromSets[i] = map[string]bool{}
		toSets[i] = map[string]bool{}
		for id, n := range nodes {
			if rule.From.Match(n) {
				fromSets[i][id] = true
			}
			if rule.To.Match(n) {
				toSets[i][id] = true
			}
		}
		r.Rules = append(r.Rules, RuleResult{ID: rule.ID, FromNodes: len(fromSets[i]), ToNodes: len(toSets[i])})
		if len(fromSets[i]) == 0 || len(toSets[i]) == 0 {
			r.Errors = append(r.Errors, fmt.Sprintf("%s: селектор не нашёл символов (from=%d, to=%d); проверьте пути и индекс", rule.ID, len(fromSets[i]), len(toSets[i])))
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT source,target,kind,coalesce(line,0),coalesce(provenance,'') FROM edges ORDER BY source,target,kind,line")
	if err != nil {
		return r, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var source, target, kind, provenance string
		var line int
		if err = rows.Scan(&source, &target, &kind, &line, &provenance); err != nil {
			rows.Close()
			return r, err
		}
		r.Coverage.Edges++
		from, ok := nodes[source]
		if !ok {
			rows.Close()
			return r, fmt.Errorf("нарушена целостность графа: неизвестный source %q", source)
		}
		to, ok := nodes[target]
		if !ok {
			rows.Close()
			return r, fmt.Errorf("нарушена целостность графа: неизвестный target %q", target)
		}
		for i, rule := range config.Rules {
			if !fromSets[i][source] || !toSets[i][target] || !exactAny(rule.Edges, kind) {
				continue
			}
			at := line
			if at <= 0 {
				at = from.Line
			}
			key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", rule.ID, source, target, kind, at)
			if seen[key] {
				continue
			}
			seen[key] = true
			r.Findings = append(r.Findings, Finding{RuleID: rule.ID, Description: rule.Description, Edge: kind, File: from.File, Line: at, From: from, To: to, Provenance: provenance})
			r.Rules[i].Findings++
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, err
	}
	sort.Slice(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.RuleID != b.RuleID {
			return a.RuleID < b.RuleID
		}
		if a.Edge != b.Edge {
			return a.Edge < b.Edge
		}
		if a.From.ID != b.From.ID {
			return a.From.ID < b.From.ID
		}
		return a.To.ID < b.To.ID
	})
	if len(r.Errors) > 0 {
		r.Status = "blocked"
	} else if len(r.Findings) > 0 {
		r.Status = "violations"
	} else {
		r.Status = "no_observed_violations"
	}
	return r, nil
}

func safePath(project, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, "\\") {
		return "", fmt.Errorf("некорректный путь в индексе: %q", relative)
	}
	p := filepath.Join(project, filepath.FromSlash(relative))
	rel, err := filepath.Rel(project, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("путь выходит за корень проекта: %q", relative)
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(project)
	if err != nil {
		return "", err
	}
	rel, err = filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("симлинк выходит за корень проекта: %q", relative)
	}
	return real, nil
}
func checkFiles(ctx context.Context, tx *sql.Tx, project string, r *Report) error {
	rows, err := tx.QueryContext(ctx, "SELECT path,content_hash,coalesce(errors,'') FROM files ORDER BY path")
	if err != nil {
		return fmt.Errorf("несовместимая таблица files: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path, hash, parseErrors string
		if err = rows.Scan(&path, &hash, &parseErrors); err != nil {
			return err
		}
		r.Coverage.Files++
		absolute, err := safePath(project, path)
		if err != nil {
			return fmt.Errorf("файл %s недоступен или небезопасен; выполните codegraph sync: %w", path, err)
		}
		content, err := os.ReadFile(absolute)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		if hex.EncodeToString(sum[:]) != hash {
			return fmt.Errorf("индекс устарел: %s; выполните codegraph sync", path)
		}
		if parseErrors != "" && parseErrors != "[]" && parseErrors != "null" {
			r.Coverage.ParseErrorFiles++
			r.Errors = append(r.Errors, fmt.Sprintf("Ошибка парсинга: %s; восстановите полный индекс", path))
		}
	}
	return rows.Err()
}
func loadNodes(ctx context.Context, tx *sql.Tx) (map[string]Node, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,name,qualified_name,kind,language,file_path,start_line FROM nodes ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("несовместимая таблица nodes: %w", err)
	}
	defer rows.Close()
	nodes := map[string]Node{}
	for rows.Next() {
		var n Node
		if err = rows.Scan(&n.ID, &n.Name, &n.QualifiedName, &n.Kind, &n.Language, &n.File, &n.Line); err != nil {
			return nil, err
		}
		nodes[n.ID] = n
	}
	return nodes, rows.Err()
}
