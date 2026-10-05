package check

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Version int    `yaml:"version"`
	Rules   []Rule `yaml:"rules"`
}
type Rule struct {
	ID          string   `yaml:"id"`
	Description string   `yaml:"description"`
	From        Selector `yaml:"from"`
	To          Selector `yaml:"to"`
	Edges       []string `yaml:"edges"`
}
type Selector struct {
	Paths     []string `yaml:"paths"`
	Symbols   []string `yaml:"symbols"`
	Kinds     []string `yaml:"kinds"`
	Languages []string `yaml:"languages"`
}

var edgeKinds = map[string]bool{"calls": true, "imports": true, "extends": true, "implements": true, "references": true, "instantiates": true}

func Decode(r io.Reader) (Config, error) {
	var c Config
	data, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil {
		return c, err
	}
	if len(data) > 1<<20 {
		return c, fmt.Errorf("конфигурация превышает 1 MiB")
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("конфигурация YAML: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("ожидается один YAML-документ")
	}
	if c.Version != 1 {
		return c, fmt.Errorf("неподдерживаемая версия правил %d; требуется 1", c.Version)
	}
	if len(c.Rules) == 0 {
		return c, fmt.Errorf("список rules пуст")
	}
	seen := map[string]bool{}
	for _, rule := range c.Rules {
		if strings.TrimSpace(rule.ID) == "" || seen[rule.ID] {
			return c, fmt.Errorf("пустой или повторяющийся id правила: %q", rule.ID)
		}
		seen[rule.ID] = true
		if len(rule.Edges) == 0 {
			return c, fmt.Errorf("%s: укажите edges", rule.ID)
		}
		for _, e := range rule.Edges {
			if !edgeKinds[e] {
				return c, fmt.Errorf("%s: неподдерживаемая связь %q", rule.ID, e)
			}
		}
		for _, s := range []Selector{rule.From, rule.To} {
			if len(s.Paths)+len(s.Symbols)+len(s.Kinds)+len(s.Languages) == 0 {
				return c, fmt.Errorf("%s: пустой селектор", rule.ID)
			}
			for _, g := range append(append([]string{}, s.Paths...), s.Symbols...) {
				if g == "" || !doublestar.ValidatePattern(g) {
					return c, fmt.Errorf("%s: некорректный glob %q", rule.ID, g)
				}
			}
			for _, g := range s.Paths {
				if strings.HasPrefix(g, "/") || strings.Contains(g, "\\") || strings.Contains("/"+g+"/", "/../") {
					return c, fmt.Errorf("%s: paths должны быть относительными и использовать /", rule.ID)
				}
			}
		}
	}
	return c, nil
}

func (s Selector) Match(n Node) bool {
	return globAny(s.Paths, n.File) && globAny(s.Symbols, n.Name, n.QualifiedName) && exactAny(s.Kinds, n.Kind) && exactAny(s.Languages, n.Language)
}
func globAny(patterns []string, values ...string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		for _, v := range values {
			ok, _ := doublestar.Match(p, v)
			if ok {
				return true
			}
		}
	}
	return false
}
func exactAny(values []string, v string) bool {
	if len(values) == 0 {
		return true
	}
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

const JSPreset = `version: 1
rules:
  - id: ui-no-direct-domain
    description: Интерфейс не должен обращаться к домену напрямую; используйте прикладной слой.
    from:
      paths: ["src/ui/**"]
    to:
      paths: ["src/domain/**"]
    edges: [imports, calls]
`
const RailsPreset = `version: 1
rules:
  - id: controllers-no-direct-clients
    description: Контроллеры должны обращаться к внешним клиентам через сервисный слой.
    from:
      paths: ["app/controllers/**"]
    to:
      paths: ["app/clients/**"]
    edges: [calls, instantiates]
`
