package tflite

import (
	"fmt"
	"os"
	"testing"
)

// TestDumpEmbeddingLayers prints the geometry of the embedding model. Whether its convolutions
// pad along time decides whether the model can be evaluated incrementally.
func TestDumpEmbeddingLayers(t *testing.T) {
	raw, err := os.ReadFile("../oww/assets/embedding_model.tflite")
	if err != nil {
		t.Skip(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	in, err := New(m)
	if err != nil {
		t.Fatal(err)
	}
	in.ResizeInput(0, []int{1, 76, 32, 1})
	if err := in.Invoke(); err != nil {
		t.Fatal(err)
	}

	for i, o := range m.Subgraphs[0].Ops {
		x := in.Tensor(o.Inputs[0])
		y := in.Tensor(o.Outputs[0])
		switch o.Op {
		case OpConv2D:
			w := in.Tensor(o.Inputs[1])
			t.Logf("%2d CONV_2D     in %-16v filter %-16v out %-16v samePad=%-5v stride=%dx%d",
				i, x.Shape, w.Shape, y.Shape, o.conv.samePad, o.conv.strideH, o.conv.strideW)
		case OpMaxPool2D:
			t.Logf("%2d MAX_POOL_2D in %-16v filter %dx%d            out %-16v samePad=%-5v stride=%dx%d",
				i, x.Shape, o.pool.filterH, o.pool.filterW, y.Shape, o.pool.samePad, o.pool.strideH, o.pool.strideW)
		case OpPad:
			p := in.Tensor(o.Inputs[1])
			t.Logf("%2d PAD         in %-16v pads %v      out %-16v", i, x.Shape, p.I32, y.Shape)
		}
	}
}

// TestDumpClassifierOps prints one classifier's operators and shapes. Point it at a model with
// DUMP_MODEL.
func TestDumpClassifierOps(t *testing.T) {
	path := os.Getenv("DUMP_MODEL")
	if path == "" {
		t.Skip("set DUMP_MODEL")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	g := m.Subgraphs[0]
	for i, o := range g.Ops {
		var ins, outs []string
		for _, x := range o.Inputs {
			if x < 0 {
				ins = append(ins, "-")
				continue
			}
			ins = append(ins, fmt.Sprintf("%v", g.Tensors[x].Shape))
		}
		for _, y := range o.Outputs {
			outs = append(outs, fmt.Sprintf("%v", g.Tensors[y].Shape))
		}
		t.Logf("%2d %-16s in %v out %v", i, o.Op, ins, outs)
	}
}
