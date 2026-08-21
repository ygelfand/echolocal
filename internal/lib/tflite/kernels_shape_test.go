package tflite

import "testing"

func outI() *Tensor { return &Tensor{Type: Int32} }

func i32(t *testing.T, name string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d values %v, want %d %v", name, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", name, got, want)
		}
	}
}

func TestShapeOf(t *testing.T) {
	y := outI()
	if err := shapeOf(&OpDesc{}, []*Tensor{tf([]int{1, 16, 96})}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "shape", y.Shape, []int{3})
	i32(t, "shape", y.I32, []int32{1, 16, 96})
}

func TestFillSpreadsAValue(t *testing.T) {
	y := out()
	in := []*Tensor{ti([]int{2}, 2, 3), tf(nil, 7)}
	if err := fill(&OpDesc{}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "fill", y.Shape, []int{2, 3})
	f32(t, "fill", y.F32, []float32{7, 7, 7, 7, 7, 7})
}

func TestPackStacksOnANewAxis(t *testing.T) {
	for _, c := range []struct {
		axis int
		want []int32
		dims []int
	}{
		{axis: 0, want: []int32{1, 2, 3, 4}, dims: []int{2, 2}},
		{axis: 1, want: []int32{1, 3, 2, 4}, dims: []int{2, 2}},
	} {
		y := outI()
		in := []*Tensor{ti([]int{2}, 1, 2), ti([]int{2}, 3, 4)}
		if err := pack(&OpDesc{axis: c.axis, count: 2}, in, []*Tensor{y}); err != nil {
			t.Fatal(err)
		}
		shape(t, "pack", y.Shape, c.dims)
		i32(t, "pack", y.I32, c.want)
	}
}

func TestPackOfScalarsIsTheShapeAReshapeWants(t *testing.T) {
	y := outI()
	in := []*Tensor{ti(nil, 1), ti(nil, 1), ti(nil, 64), ti(nil, 1)}
	if err := pack(&OpDesc{count: 4}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "pack", y.Shape, []int{4})
	i32(t, "pack", y.I32, []int32{1, 1, 64, 1})
}

func TestStridedSliceTakesARegion(t *testing.T) {
	y := outI()
	in := []*Tensor{ti([]int{4}, 10, 20, 30, 40), ti([]int{1}, 1), ti([]int{1}, 3), ti([]int{1}, 1)}
	if err := stridedSlice(&OpDesc{}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "slice", y.Shape, []int{2})
	i32(t, "slice", y.I32, []int32{20, 30})
}

func TestStridedSliceStrideAndNegativeBound(t *testing.T) {
	y := outI()
	in := []*Tensor{ti([]int{5}, 1, 2, 3, 4, 5), ti([]int{1}, 0), ti([]int{1}, -1), ti([]int{1}, 2)}
	if err := stridedSlice(&OpDesc{}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	i32(t, "slice", y.I32, []int32{1, 3})
}

func TestStridedSliceShrinksTheAxisItIsToldTo(t *testing.T) {
	y := outI()
	in := []*Tensor{ti([]int{4}, 10, 20, 30, 40), ti([]int{1}, 2), ti([]int{1}, 3), ti([]int{1}, 1)}
	if err := stridedSlice(&OpDesc{shrinkMask: 1}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "slice", y.Shape, nil)
	i32(t, "slice", y.I32, []int32{30})
}

func TestStridedSliceOverTwoAxes(t *testing.T) {
	y := outI()
	in := []*Tensor{
		ti([]int{2, 3}, 1, 2, 3, 4, 5, 6),
		ti([]int{2}, 0, 1), ti([]int{2}, 2, 3), ti([]int{2}, 1, 1),
	}
	if err := stridedSlice(&OpDesc{}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "slice", y.Shape, []int{2, 2})
	i32(t, "slice", y.I32, []int32{2, 3, 5, 6})
}

func TestStridedSliceMasksIgnoreTheBound(t *testing.T) {
	y := outI()
	in := []*Tensor{ti([]int{4}, 10, 20, 30, 40), ti([]int{1}, 2), ti([]int{1}, 3), ti([]int{1}, 1)}
	if err := stridedSlice(&OpDesc{beginMask: 1, endMask: 1}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	i32(t, "slice", y.I32, []int32{10, 20, 30, 40})
}

func TestReduceProd(t *testing.T) {
	y := outI()
	if err := reduceProdI32(&OpDesc{}, []*Tensor{ti([]int{3}, 2, 3, 4)}, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "prod", y.Shape, nil)
	i32(t, "prod", y.I32, []int32{24})
}

func TestReduceProdOverOneAxis(t *testing.T) {
	y := outI()
	in := []*Tensor{ti([]int{2, 2}, 1, 2, 3, 4), ti([]int{1}, 0)}
	if err := reduceProdI32(&OpDesc{}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "prod", y.Shape, []int{2})
	i32(t, "prod", y.I32, []int32{3, 8})
}

func TestReduceProdKeepsDims(t *testing.T) {
	y := outI()
	in := []*Tensor{ti([]int{2, 2}, 1, 2, 3, 4), ti([]int{1}, 1)}
	if err := reduceProdI32(&OpDesc{keepDims: true}, in, []*Tensor{y}); err != nil {
		t.Fatal(err)
	}
	shape(t, "prod", y.Shape, []int{2, 1})
	i32(t, "prod", y.I32, []int32{2, 12})
}
