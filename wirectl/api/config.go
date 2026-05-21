package api

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	yaml2 "gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/yaml"
)

type Config struct {
	Topology Topology `json:"topology"`
}

type Topology struct {
	Nodes map[string]*Node `json:"nodes"`
	Links []Link           `json:"links,omitempty"`
}

type Node struct {
	Kind       string                `json:"kind"`
	Interfaces map[string]*Interface `json:"interfaces,omitempty"`
}

type Interface struct {
}

type Link struct {
	Endpoints []Endpoint `json:"endpoints"`
}

type Endpoint struct {
	Node      string
	Interface string
}

func (e *Endpoint) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*e = Endpoint{}
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		node, iface, ok := strings.Cut(s, ":")
		if !ok {
			return fmt.Errorf("endpoint string form must be <node>:<interface>")
		}

		e.Node = node
		e.Interface = iface
		return nil
	}

	ep := &struct {
		Node      string `json:"node"`
		Interface string `json:"interface"`
	}{}
	if err := json.Unmarshal(data, &ep); err != nil {
		return err
	}
	e.Node = ep.Node
	e.Interface = ep.Interface
	return nil
}

func ReadConfig(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func ReadConfigFile(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	return ReadConfig(data)
}

func IsTemplateFilename(filename string) bool {
	ext := filepath.Ext(filename)
	ext = strings.ToLower(ext)
	return ext == ".gotmpl" || ext == ".tmpl"
}

func VarsFilename(filename string) string {
	ext := filepath.Ext(filename)
	trimmed := strings.TrimSuffix(filename, ext)
	ext = filepath.Ext(trimmed)
	trimmed = strings.TrimSuffix(trimmed, ext)
	return fmt.Sprintf("%s.vars.yaml", trimmed)
}

func FuncMap() template.FuncMap {
	return template.FuncMap{
		"seq": func(start, end int) []int {
			out := make([]int, 0, end-start+1)
			for i := start; i <= end; i++ {
				out = append(out, i)
			}
			return out
		},
	}
}

func ReadAndOptionallyRenderConfig(filename string, varsFilename string, noTemplate bool) (*Config, error) {
	if noTemplate || !IsTemplateFilename(filename) {
		return ReadConfigFile(filename)
	}

	tmplData, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}

	tmpl, err := template.New("config").Funcs(FuncMap()).Parse(string(tmplData))
	if err != nil {
		return nil, fmt.Errorf("parsing template file: %w", err)
	}

	if varsFilename == "" {
		varsFilename = VarsFilename(filename)
	}

	varsData, err := os.ReadFile(varsFilename)
	if err != nil {
		return nil, fmt.Errorf("reading vars file %s: %w", varsFilename, err)
	}

	var vars any
	if err := yaml2.Unmarshal(varsData, &vars); err != nil {
		return nil, fmt.Errorf("parsing vars file: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return nil, fmt.Errorf("executing template: %w", err)
	}

	return ReadConfig(buf.Bytes())
}
