package tflite

import (
	"math"
	"testing"
)

func f32(t *testing.T, name string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d values %v, want %d %v", name, len(got), got, len(want), want)
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-5 {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
	}
}

func shape(t *testing.T, name string, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: shape %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: shape %v, want %v", name, got, want)
		}
	}
}

func tf(shape []int, data ...float32) *Tensor {
	t := &Tensor{Type: Float32, Shape: shape, F32: data}
	if data == nil {
		t.F32 = make([]float32, count(shape))
	}
	return t
}

func ti(shape []int, data ...int32) *Tensor {
	return &Tensor{Type: Int32, Shape: shape, I32: data}
}

func out() *Tensor { return &Tensor{Type: Float32} }

func TestBroadcastShape(t *testing.T) {
	got, err := broadcastShape([]int{1, 16, 96}, []int{1, 16, 1})
	if err != nil {
		t.Fatal(err)
	}
	shape(t, "broadcast", got, []int{1, 16, 96})

	if _, err := broadcastShape([]int{3}, []int{4}); err == nil {
		t.Error("3 and 4 should not broadcast")
	}
}

// A layer norm's scale step broadcasts a per-row mean across the row, which is the shape the
// classifier relies on.
func TestElementwiseBroadcastsAcrossRows(t *testing.T) {
	a := tf([]int{2, 3}, 1, 2, 3, 4, 5, 6)
	b := tf([]int{2, 1}, 10, 20)
	y := out()
	if err := kernels[OpSub](&OpDesc{}, []*Tensor{a, b}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "sub", y.Shape, []int{2, 3})
	f32(t, "sub", y.F32, []float32{-9, -8, -7, -16, -15, -14})
}

func TestReduceMeanKeepsDims(t *testing.T) {
	x := tf([]int{1, 2, 3}, 1, 2, 3, 10, 20, 30)
	axes := ti([]int{1}, -1)
	y := out()

	o := &OpDesc{Op: OpMean, keepDims: true}
	if err := kernels[OpMean](o, []*Tensor{x, axes}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "mean", y.Shape, []int{1, 2, 1})
	f32(t, "mean", y.F32, []float32{2, 20})
}

func TestReduceMaxDropsDims(t *testing.T) {
	x := tf([]int{2, 2}, 1, 7, 5, 3)
	y := out()
	if err := kernels[OpReduceMax](&OpDesc{}, []*Tensor{x, ti([]int{1}, 1)}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "reduce_max", y.Shape, []int{2})
	f32(t, "reduce_max", y.F32, []float32{7, 5})
}

func TestTransposeMovesAxes(t *testing.T) {
	x := tf([]int{2, 3}, 1, 2, 3, 4, 5, 6)
	y := out()
	if err := kernels[OpTranspose](&OpDesc{}, []*Tensor{x, ti([]int{2}, 1, 0)}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "transpose", y.Shape, []int{3, 2})
	f32(t, "transpose", y.F32, []float32{1, 4, 2, 5, 3, 6})
}

// The embedding model pads the frequency axis before its first convolution, so the pad has to
// land inside the row rather than at the end of the buffer.
func TestPadInsertsZerosPerAxis(t *testing.T) {
	x := tf([]int{1, 2, 2, 1}, 1, 2, 3, 4)
	pads := ti([]int{4, 2}, 0, 0, 0, 0, 1, 1, 0, 0)
	y := out()
	if err := kernels[OpPad](&OpDesc{}, []*Tensor{x, pads}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "pad", y.Shape, []int{1, 2, 4, 1})
	f32(t, "pad", y.F32, []float32{0, 1, 2, 0, 0, 3, 4, 0})
}

func TestConv2DValidPadding(t *testing.T) {
	// One 2x2 filter summing a 3x3 input's windows, no bias.
	x := tf([]int{1, 3, 3, 1}, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	w := tf([]int{1, 2, 2, 1}, 1, 1, 1, 1)
	y := out()

	o := &OpDesc{Op: OpConv2D, conv: convParams{strideW: 1, strideH: 1, dilationW: 1, dilationH: 1}}
	if err := kernels[OpConv2D](o, []*Tensor{x, w, nil}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "conv", y.Shape, []int{1, 2, 2, 1})
	f32(t, "conv", y.F32, []float32{12, 16, 24, 28})
}

func TestConv2DSamePaddingAndStride(t *testing.T) {
	x := tf([]int{1, 3, 3, 1}, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	w := tf([]int{1, 2, 2, 1}, 1, 1, 1, 1)
	y := out()

	o := &OpDesc{Op: OpConv2D, conv: convParams{samePad: true, strideW: 2, strideH: 2, dilationW: 1, dilationH: 1}}
	if err := kernels[OpConv2D](o, []*Tensor{x, w, nil}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "conv", y.Shape, []int{1, 2, 2, 1})
	// SAME with stride 2 puts no padding before the data and one row and column after, so the
	// trailing windows are partly outside the input.
	f32(t, "conv", y.F32, []float32{12, 9, 15, 9})
}

func TestMaxPoolHalvesTime(t *testing.T) {
	x := tf([]int{1, 4, 1, 1}, 1, 5, 2, 4)
	y := out()
	o := &OpDesc{Op: OpMaxPool2D, pool: poolParams{strideW: 1, strideH: 2, filterW: 1, filterH: 2}}
	if err := kernels[OpMaxPool2D](o, []*Tensor{x}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "pool", y.Shape, []int{1, 2, 1, 1})
	f32(t, "pool", y.F32, []float32{5, 4})
}

func TestFullyConnected(t *testing.T) {
	x := tf([]int{1, 3}, 1, 2, 3)
	w := tf([]int{2, 3}, 1, 0, 0, 0, 1, 1)
	b := tf([]int{2}, 0.5, -1)
	y := out()
	if err := kernels[OpFullyConnected](&OpDesc{}, []*Tensor{x, w, b}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "fc", y.Shape, []int{1, 2})
	f32(t, "fc", y.F32, []float32{1.5, 4})
}

func TestBatchMatMul(t *testing.T) {
	a := tf([]int{1, 2, 3}, 1, 2, 3, 4, 5, 6)
	b := tf([]int{1, 3, 2}, 1, 2, 3, 4, 5, 6)
	y := out()
	if err := kernels[OpBatchMatMul](&OpDesc{}, []*Tensor{a, b}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "matmul", y.Shape, []int{1, 2, 2})
	f32(t, "matmul", y.F32, []float32{22, 28, 49, 64})
}

func TestReshapeInfersFreeDimension(t *testing.T) {
	x := tf([]int{2, 3}, 1, 2, 3, 4, 5, 6)
	y := out()
	if err := kernels[OpReshape](&OpDesc{}, []*Tensor{x, ti([]int{2}, 3, -1)}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "reshape", y.Shape, []int{3, 2})
	f32(t, "reshape", y.F32, []float32{1, 2, 3, 4, 5, 6})
}

func TestSqueezeAndExpandDims(t *testing.T) {
	x := tf([]int{1, 3, 1}, 1, 2, 3)
	y := out()
	if err := kernels[OpSqueeze](&OpDesc{}, []*Tensor{x}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "squeeze", y.Shape, []int{3})

	z := out()
	if err := kernels[OpExpandDims](&OpDesc{}, []*Tensor{y, ti([]int{1}, 0)}, []*Tensor{z}); err != nil {
		t.Fatal(err)
	}
	shape(t, "expand_dims", z.Shape, []int{1, 3})
}

func TestLeakyRelu(t *testing.T) {
	x := tf([]int{2}, -2, 3)
	y := out()
	o := &OpDesc{Op: OpLeakyRelu, alpha: 0.5}
	if err := kernels[OpLeakyRelu](o, []*Tensor{x}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	f32(t, "leaky_relu", y.F32, []float32{-1, 3})
}

func TestFusedReluClampsConvOutput(t *testing.T) {
	x := tf([]int{1, 1, 1, 1}, -5)
	w := tf([]int{1, 1, 1, 1}, 1)
	y := out()
	o := &OpDesc{Op: OpConv2D, conv: convParams{strideW: 1, strideH: 1, dilationW: 1, dilationH: 1, act: ActRelu}}
	if err := kernels[OpConv2D](o, []*Tensor{x, w, nil}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	f32(t, "conv+relu", y.F32, []float32{0})
}
