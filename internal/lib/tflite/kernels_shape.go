package tflite

import "fmt"

// Ops that compute shapes rather than activations. A converter emits them when it cannot fold a
// reshape at conversion time, so they carry dimensions and scalars, and their tensors are int32.

func shapeOf(o *OpDesc, in, out []*Tensor) error {
	x, y := in[0], out[0]
	if y.Type != Int32 {
		return fmt.Errorf("shape wants an int32 output, got %s", y.Type)
	}

	y.resize([]int{len(x.Shape)})
	for i, d := range x.Shape {
		y.I32[i] = int32(d)
	}
	return nil
}

// fill spreads one value over the shape its first input holds.
func fill(o *OpDesc, in, out []*Tensor) error {
	dims, value, y := in[0], in[1], out[0]

	shape := make([]int, 0, len(dims.I32))
	for _, d := range dims.I32 {
		shape = append(shape, int(d))
	}
	y.resize(shape)

	switch {
	case y.Type == Float32 && len(value.F32) > 0:
		for i := range y.F32 {
			y.F32[i] = value.F32[0]
		}
	case y.Type != Float32 && len(value.I32) > 0:
		for i := range y.I32 {
			y.I32[i] = value.I32[0]
		}
	default:
		return fmt.Errorf("fill has no %s value to spread", y.Type)
	}
	return nil
}

// pack stacks its inputs along a new axis. Every input has the same shape, so the output is that
// shape with the count inserted.
func pack(o *OpDesc, in, out []*Tensor) error {
	y := out[0]
	if len(in) == 0 {
		return fmt.Errorf("pack has nothing to stack")
	}
	first := in[0]

	axis := o.axis
	if axis < 0 {
		axis += len(first.Shape) + 1
	}
	if axis < 0 || axis > len(first.Shape) {
		return fmt.Errorf("pack axis %d out of range for shape %v", o.axis, first.Shape)
	}

	shape := make([]int, 0, len(first.Shape)+1)
	shape = append(shape, first.Shape[:axis]...)
	shape = append(shape, len(in))
	shape = append(shape, first.Shape[axis:]...)
	y.resize(shape)

	// Inputs vary fastest over everything after the new axis, so each contributes one run per
	// slice of the axes before it.
	inner, outer := 1, 1
	for _, d := range first.Shape[axis:] {
		inner *= d
	}
	for _, d := range first.Shape[:axis] {
		outer *= d
	}

	for i, x := range in {
		if x.Count() != inner*outer {
			return fmt.Errorf("pack input %d has shape %v, want %v", i, x.Shape, first.Shape)
		}
		for o := range outer {
			at := (o*len(in) + i) * inner
			if y.Type == Float32 {
				copy(y.F32[at:at+inner], x.F32[o*inner:])
				continue
			}
			copy(y.I32[at:at+inner], x.I32[o*inner:])
		}
	}
	return nil
}

// stridedSlice takes a rectangular region. Only forward strides are handled: the masks a converter
// emits for a folded reshape are begin, end and shrink, and a negative stride means a reversal that
// nothing here produces.
func stridedSlice(o *OpDesc, in, out []*Tensor) error {
	x, y := in[0], out[0]
	if o.ellipsisMask != 0 || o.newAxisMask != 0 {
		return fmt.Errorf("strided slice with ellipsis or new axis is not implemented")
	}
	if len(in) < 4 || in[1] == nil || in[2] == nil || in[3] == nil {
		return fmt.Errorf("strided slice wants begin, end and strides")
	}

	rank := len(x.Shape)
	begin, end, stride := make([]int, rank), make([]int, rank), make([]int, rank)
	for d := range rank {
		begin[d], end[d], stride[d] = 0, x.Shape[d], 1
		if d >= len(in[1].I32) {
			continue
		}

		stride[d] = int(in[3].I32[d])
		if stride[d] < 1 {
			return fmt.Errorf("strided slice stride %d is not forward", stride[d])
		}
		if o.beginMask&(1<<d) == 0 {
			begin[d] = clamp(int(in[1].I32[d]), x.Shape[d])
		}
		if o.endMask&(1<<d) == 0 {
			end[d] = clamp(int(in[2].I32[d]), x.Shape[d])
		}
	}

	var shape []int
	for d := range rank {
		n := 0
		if end[d] > begin[d] {
			n = (end[d] - begin[d] + stride[d] - 1) / stride[d]
		}
		if o.shrinkMask&(1<<d) != 0 {
			continue
		}
		shape = append(shape, n)
	}
	y.resize(shape)

	xs := contiguous(x.Shape)
	idx := make([]int, rank)
	for i := range y.Count() {
		at := 0
		for d := range rank {
			at += (begin[d] + idx[d]*stride[d]) * xs[d]
		}
		if y.Type == Float32 {
			y.F32[i] = x.F32[at]
		} else {
			y.I32[i] = x.I32[at]
		}

		for d := rank - 1; d >= 0; d-- {
			idx[d]++
			if begin[d]+idx[d]*stride[d] < end[d] {
				break
			}
			idx[d] = 0
		}
	}
	return nil
}

// clamp resolves one of a slice's bounds, which may count from the end.
func clamp(v, n int) int {
	if v < 0 {
		v += n
	}
	return max(0, min(v, n))
}

// reduceProdI32 is REDUCE_PROD over int32, which is the only shape arithmetic that appears.
func reduceProdI32(o *OpDesc, in, out []*Tensor) error {
	x, y := in[0], out[0]
	if x.Type == Float32 {
		return fmt.Errorf("reduce_prod over %s is not implemented", x.Type)
	}

	rank := len(x.Shape)
	axes := make([]bool, rank)
	if len(in) > 1 && in[1] != nil && len(in[1].I32) > 0 {
		for _, a := range in[1].I32 {
			d := int(a)
			if d < 0 {
				d += rank
			}
			if d < 0 || d >= rank {
				return fmt.Errorf("axis %d out of range for shape %v", a, x.Shape)
			}
			axes[d] = true
		}
	} else {
		for d := range axes {
			axes[d] = true
		}
	}

	var shape []int
	for d, n := range x.Shape {
		switch {
		case !axes[d]:
			shape = append(shape, n)
		case o.keepDims:
			shape = append(shape, 1)
		}
	}
	y.resize(shape)

	os := make([]int, rank)
	stride := 1
	for d := rank - 1; d >= 0; d-- {
		if axes[d] {
			continue
		}
		os[d] = stride
		stride *= x.Shape[d]
	}

	for i := range y.I32 {
		y.I32[i] = 1
	}
	idx := make([]int, rank)
	yo := 0
	for i := range x.Count() {
		y.I32[yo] *= x.I32[i]
		yo = step1(idx, x.Shape, os, yo)
	}
	return nil
}
