package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/dapi/archcheck/internal/check"
)

const version = "0.1.0"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
func run(ctx context.Context, args []string, out, errout io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(out, "archcheck — архитектурные правила поверх локального CodeGraph\n\nКоманды: init, scan, version\nПомощь: archcheck scan --help\nКоды: 0 — нет наблюдаемых нарушений, 1 — нарушения, 2 — проверка заблокирована.")
		return 0
	}
	if args[0] == "version" {
		fmt.Fprintln(out, version)
		return 0
	}
	if args[0] != "scan" && args[0] != "init" {
		fmt.Fprintf(errout, "Неизвестная команда: %s\n", args[0])
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(errout)
	project := fs.String("project", ".", "корень проекта")
	config := fs.String("config", "archcheck.yaml", "правила YAML (относительно проекта)")
	preset := fs.String("preset", "js", "шаблон init: js или rails")
	database := fs.String("db", ".codegraph/codegraph.db", "индекс SQLite (относительно проекта)")
	format := fs.String("format", "text", "отчёт: text или json")
	codegraph := fs.String("codegraph", "codegraph", "исполняемый файл CodeGraph")
	noSync := fs.Bool("no-sync", false, "пропустить sync; новые файлы могут отсутствовать в графе")
	strict := fs.Bool("strict", false, "блокировать проверку при неразрешённых ссылках или без sync")
	timeout := fs.Duration("timeout", 5*time.Minute, "лимит времени проверки вместе с sync")
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(errout, "Лишние аргументы; используйте --project PATH")
		return 2
	}
	if *format != "text" && *format != "json" {
		fmt.Fprintln(errout, "format должен быть text или json")
		return 2
	}
	projectAbs, err := filepath.Abs(*project)
	if err != nil {
		return failure(out, errout, *format, err)
	}
	configPath := inProject(projectAbs, *config)
	if args[0] == "init" {
		body := check.JSPreset
		if *preset == "rails" {
			body = check.RailsPreset
		} else if *preset != "js" {
			return failure(out, errout, *format, fmt.Errorf("preset должен быть js или rails"))
		}
		f, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return failure(out, errout, *format, err)
		}
		_, err = io.WriteString(f, body)
		closeErr := f.Close()
		if err != nil {
			return failure(out, errout, *format, err)
		}
		if closeErr != nil {
			return failure(out, errout, *format, closeErr)
		}
		fmt.Fprintf(out, "Созданы правила %s. Настройте пути под проект, затем выполните codegraph init и archcheck scan.\n", *config)
		return 0
	}
	if *timeout <= 0 {
		return failure(out, errout, *format, fmt.Errorf("timeout должен быть положительным"))
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	f, err := os.Open(configPath)
	if err != nil {
		return failure(out, errout, *format, err)
	}
	c, err := check.Decode(f)
	f.Close()
	if err != nil {
		return failure(out, errout, *format, err)
	}
	dbPath := inProject(projectAbs, *database)
	if *noSync && *strict {
		return failure(out, errout, *format, fmt.Errorf("--strict несовместим с --no-sync"))
	}
	if !*noSync {
		// Verify the adapter's upstream version before running any indexing command.
		v := exec.CommandContext(ctx, *codegraph, "--version")
		v.Dir = projectAbs
		v.Env = append(os.Environ(), "DO_NOT_TRACK=1", "CODEGRAPH_TELEMETRY=0")
		b, err := v.Output()
		if err != nil {
			return failure(out, errout, *format, fmt.Errorf("CodeGraph недоступен: %w", err))
		}
		if trimVersion(b) != check.SupportedCodeGraph {
			return failure(out, errout, *format, fmt.Errorf("требуется CodeGraph %s; получено %q", check.SupportedCodeGraph, trimVersion(b)))
		}
		cmd := exec.CommandContext(ctx, *codegraph, "sync", projectAbs)
		cmd.Dir = projectAbs
		cmd.Env = append(os.Environ(), "DO_NOT_TRACK=1", "CODEGRAPH_TELEMETRY=0")
		cmd.Stdout = errout
		cmd.Stderr = errout
		if err = cmd.Run(); err != nil {
			return failure(out, errout, *format, fmt.Errorf("codegraph sync не завершён: %w", err))
		}
	}
	report, err := check.Scan(ctx, projectAbs, dbPath, c, !*noSync, *strict)
	if err != nil {
		return failure(out, errout, *format, err)
	}
	if err = printReport(out, *format, report); err != nil {
		fmt.Fprintln(errout, err)
		return 2
	}
	if report.Status == "blocked" {
		return 2
	}
	if len(report.Findings) > 0 {
		return 1
	}
	return 0
}
func inProject(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
func trimVersion(b []byte) string { // Upstream --version emits a plain semantic version.
	s := string(b)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
func failure(out, errout io.Writer, format string, err error) int {
	if format == "json" {
		r := check.Report{Status: "blocked", Rules: []check.RuleResult{}, Findings: []check.Finding{}, Warnings: []string{}, Errors: []string{err.Error()}}
		if e := json.NewEncoder(out).Encode(r); e != nil {
			fmt.Fprintln(errout, e)
		}
	} else {
		fmt.Fprintln(errout, "Проверка заблокирована:", err)
	}
	return 2
}
func printReport(out io.Writer, format string, r check.Report) error {
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	for _, f := range r.Findings {
		fmt.Fprintf(out, "%s:%d [%s] %s — %s → %s (%s)\n", f.File, f.Line, f.RuleID, f.Description, f.From.Name, f.To.Name, f.Edge)
	}
	for _, w := range r.Warnings {
		fmt.Fprintln(out, "Предупреждение:", w)
	}
	for _, e := range r.Errors {
		fmt.Fprintln(out, "Ошибка:", e)
	}
	fmt.Fprintf(out, "Проверено файлов: %d, правил: %d, нарушений: %d.\n", r.Coverage.Files, len(r.Rules), len(r.Findings))
	switch r.Status {
	case "blocked":
		fmt.Fprintln(out, "Проверка заблокирована.")
	case "no_observed_violations":
		fmt.Fprintln(out, "В доступном графе нарушений не найдено.")
	}
	return nil
}
