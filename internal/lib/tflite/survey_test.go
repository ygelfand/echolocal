package tflite

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestSurveyModels reports what a directory of wake word models needs from this package: which
// operators are missing, how many carry more than one subgraph, and what input shapes appear.
// Point it somewhere else with SURVEY.
func TestSurveyModels(t *testing.T) {
	dir := os.Getenv("SURVEY")
	if dir == "" {
		dir = "../../../evalmodels"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skip(err)
	}

	var total, runnable, multi, failed int
	missing := map[string]int{}
	example := map[string]string{}
	shapes := map[string]int{}
	opsets := map[string]int{}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tflite") {
			continue
		}
		total++

		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		m, err := Parse(raw)
		if err != nil {
			failed++
			t.Errorf("%s: parse: %v", e.Name(), err)
			continue
		}

		if len(m.Subgraphs) > 1 {
			multi++
		}
		g := m.Subgraphs[0]
		shapes[fmt.Sprint(g.Tensors[g.Inputs[0]].Shape)]++

		var names []string
		ok := len(m.Subgraphs) == 1
		for _, sub := range m.Subgraphs {
			for _, o := range sub.Ops {
				name := o.Op.String()
				names = append(names, name)
				if _, have := kernels[o.Op]; !have {
					missing[name]++
					if example[name] == "" {
						example[name] = e.Name()
					}
					ok = false
				}
			}
		}
		sort.Strings(names)
		opsets[strings.Join(unique(names), " ")]++
		if ok {
			runnable++
		}
	}

	t.Logf("%d models: %d runnable as-is, %d with extra subgraphs, %d unparseable", total, runnable, multi, failed)
	t.Logf("input shapes: %v", shapes)
	if len(missing) > 0 {
		t.Log("missing operators:")
		for op, n := range missing {
			t.Logf("  %-16s %3d models (e.g. %s)", op, n, example[op])
		}
	}
	t.Logf("%d distinct operator sets:", len(opsets))
	for set, n := range opsets {
		t.Logf("  %3d x %s", n, set)
	}
}

func unique(sorted []string) []string {
	out := sorted[:0]
	for i, v := range sorted {
		if i == 0 || v != sorted[i-1] {
			out = append(out, v)
		}
	}
	return out
}
