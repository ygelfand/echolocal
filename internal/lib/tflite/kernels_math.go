package tflite

import (
	"fmt"
	"math"
)

func broadcastShape(a, b []int) ([]int, error) {
	rank := max(len(a), len(b))
	out := make([]int, rank)
	for i := range rank {
		da, db := 1, 1
		if d := len(a) - rank + i; d >= 0 {
			da = a[d]
		}
		if d := len(b) - rank + i; d >= 0 {
			db = b[d]
		}
		switch {
		case da == db, db == 1:
			out[i] = da
		case da == 1:
			out[i] = db
		default:
			return nil, fmt.Errorf("shapes %v and %v do not broadcast", a, b)
		}
	}
	return out, nil
}

// strides maps src onto the axes of out, giving the offset to add when walking each output axis.
// A broadcast axis gets a stride of zero so it reads the same element repeatedly.
func strides(src, out []int) []int {
	s := make([]int, len(out))
	stride := 1
	for i := range src {
		d := len(src) - 1 - i
		o := len(out) - 1 - i
		if o < 0 {
			break
		}
		if src[d] != 1 {
			s[o] = stride
		}
		stride *= src[d]
	}
	return s
}

// step1 advances an odometer over shape and returns the source offset for the new position.
func step1(idx, shape, strides []int, off int) int {
	for d := len(shape) - 1; d >= 0; d-- {
		idx[d]++
		off += strides[d]
		if idx[d] < shape[d] {
			return off
		}
		idx[d] = 0
		off -= strides[d] * shape[d]
	}
	return off
}

func contiguous(shape []int) []int {
	s := make([]int, len(shape))
	stride := 1
	for d := len(shape) - 1; d >= 0; d-- {
		s[d] = stride
		stride *= shape[d]
	}
	return s
}

func unary(f func(float32) float32) kernel {
	return func(o *OpDesc, in, out []*Tensor) error {
		x, y := in[0], out[0]
		y.resize(x.Shape)
		for i, v := range x.F32 {
			y.F32[i] = f(v)
		}
		return nil
	}
}

func elementwise(f func(a, b float32) float32) kernel {
	return func(o *OpDesc, in, out []*Tensor) error {
		a, b, y := in[0], in[1], out[0]
		shape, err := broadcastShape(a.Shape, b.Shape)
		if err != nil {
			return err
		}
		y.resize(shape)
		act := o.act
		n := y.Count()

		if a.Count() == n && b.Count() == n {
			for i := range n {
				y.F32[i] = activate(f(a.F32[i], b.F32[i]), act)
			}
			return nil
		}

		as, bs := strides(a.Shape, shape), strides(b.Shape, shape)
		idx := make([]int, len(shape))
		ao, bo := 0, 0
		for i := range n {
			y.F32[i] = activate(f(a.F32[ao], b.F32[bo]), act)
			ao, bo = step(idx, shape, as, bs, ao, bo)
		}
		return nil
	}
}

func leakyRelu(o *OpDesc, in, out []*Tensor) error {
	alpha := o.alpha
	x, y := in[0], out[0]
	y.resize(x.Shape)
	for i, v := range x.F32 {
		if v < 0 {
			v *= alpha
		}
		y.F32[i] = v
	}
	return nil
}

func reduceOp(init float32, combine func(acc, v float32) float32, mean bool) kernel {
	return func(o *OpDesc, in, out []*Tensor) error {
		x, y := in[0], out[0]
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

		keep := o.keepDims
		var shape []int
		for d, n := range x.Shape {
			switch {
			case !axes[d]:
				shape = append(shape, n)
			case keep:
				shape = append(shape, 1)
			}
		}
		y.resize(shape)

		// Output strides indexed by input axis: reduced axes contribute nothing, and the
		// remaining axes keep their relative order, which is what the output shape is.
		os := make([]int, rank)
		stride := 1
		for d := rank - 1; d >= 0; d-- {
			if axes[d] {
				continue
			}
			os[d] = stride
			stride *= x.Shape[d]
		}

		for i := range y.F32 {
			y.F32[i] = init
		}
		idx := make([]int, rank)
		yo := 0
		for i := range x.Count() {
			y.F32[yo] = combine(y.F32[yo], x.F32[i])
			yo = step1(idx, x.Shape, os, yo)
		}

		if mean {
			n := 1
			for d, r := range axes {
				if r {
					n *= x.Shape[d]
				}
			}
			inv := 1 / float32(n)
			for i := range y.F32 {
				y.F32[i] *= inv
			}
		}
		return nil
	}
}

func padZero(o *OpDesc, in, out []*Tensor) error {
	x, p, y := in[0], in[1], out[0]
	rank := len(x.Shape)
	if len(p.I32) < 2*rank {
		return fmt.Errorf("padding has %d values for a %d-D input", len(p.I32), rank)
	}

	shape := make([]int, rank)
	for d := range rank {
		shape[d] = int(p.I32[2*d]) + x.Shape[d] + int(p.I32[2*d+1])
	}
	y.resize(shape)
	for i := range y.F32 {
		y.F32[i] = 0
	}

	os := contiguous(shape)
	base := 0
	for d := range rank {
		base += int(p.I32[2*d]) * os[d]
	}

	idx := make([]int, rank)
	yo := base
	for i := range x.Count() {
		y.F32[yo] = x.F32[i]
		yo = step1(idx, x.Shape, os, yo)
	}
	return nil
}

func transpose(o *OpDesc, in, out []*Tensor) error {
	x, p, y := in[0], in[1], out[0]
	rank := len(x.Shape)
	if len(p.I32) != rank {
		return fmt.Errorf("permutation has %d values for a %d-D input", len(p.I32), rank)
	}

	perm := make([]int, rank)
	shape := make([]int, rank)
	for i := range rank {
		d := int(p.I32[i])
		if d < 0 {
			d += rank
		}
		if d < 0 || d >= rank {
			return fmt.Errorf("permutation %v out of range for shape %v", p.I32, x.Shape)
		}
		perm[i] = d
		shape[i] = x.Shape[d]
	}
	y.resize(shape)

	xs := contiguous(x.Shape)
	ps := make([]int, rank)
	for i := range rank {
		ps[i] = xs[perm[i]]
	}

	idx := make([]int, rank)
	xo := 0
	for i := range y.F32 {
		y.F32[i] = x.F32[xo]
		xo = step1(idx, shape, ps, xo)
	}
	return nil
}

func copyData(y, x *Tensor) {
	if x.Type == Float32 {
		copy(y.F32, x.F32)
		return
	}
	copy(y.I32, x.I32)
}

func reshape(o *OpDesc, in, out []*Tensor) error {
	x, y := in[0], out[0]

	var target []int
	if len(in) > 1 && in[1] != nil && len(in[1].I32) > 0 {
		for _, v := range in[1].I32 {
			target = append(target, int(v))
		}
	} else {
		target = append(target, o.dims...)
	}
	if target == nil {
		return fmt.Errorf("no target shape")
	}

	// One dimension may be -1, standing for whatever makes the count work out.
	total := x.Count()
	known, free := 1, -1
	for i, d := range target {
		if d < 0 {
			free = i
			continue
		}
		known *= d
	}
	if free >= 0 {
		if known == 0 || total%known != 0 {
			return fmt.Errorf("cannot reshape %d values into %v", total, target)
		}
		target[free] = total / known
	} else if known != total {
		return fmt.Errorf("cannot reshape %d values into %v", total, target)
	}

	y.resize(target)
	copyData(y, x)
	return nil
}

func squeeze(o *OpDesc, in, out []*Tensor) error {
	x, y := in[0], out[0]
	rank := len(x.Shape)

	drop := make([]bool, rank)
	dims := o.dims
	if len(dims) == 0 {
		for d, n := range x.Shape {
			drop[d] = n == 1
		}
	} else {
		for _, d := range dims {
			if d < 0 {
				d += rank
			}
			if d < 0 || d >= rank {
				return fmt.Errorf("squeeze dim %d out of range for shape %v", d, x.Shape)
			}
			if x.Shape[d] != 1 {
				return fmt.Errorf("cannot squeeze axis %d of shape %v", d, x.Shape)
			}
			drop[d] = true
		}
	}

	var shape []int
	for d, n := range x.Shape {
		if !drop[d] {
			shape = append(shape, n)
		}
	}
	y.resize(shape)
	copyData(y, x)
	return nil
}

func expandDims(o *OpDesc, in, out []*Tensor) error {
	x, a, y := in[0], in[1], out[0]
	if len(a.I32) == 0 {
		return fmt.Errorf("no axis")
	}
	axis := int(a.I32[0])
	if axis < 0 {
		axis += len(x.Shape) + 1
	}
	if axis < 0 || axis > len(x.Shape) {
		return fmt.Errorf("axis %d out of range for shape %v", a.I32[0], x.Shape)
	}

	shape := make([]int, 0, len(x.Shape)+1)
	shape = append(shape, x.Shape[:axis]...)
	shape = append(shape, 1)
	shape = append(shape, x.Shape[axis:]...)
	y.resize(shape)
	copyData(y, x)
	return nil
}

func init() {
	kernels = map[Op]kernel{
		OpConv2D:         conv2D,
		OpMaxPool2D:      maxPool2D,
		OpFullyConnected: fullyConnected,
		OpBatchMatMul:    batchMatMul,

		OpAdd:     elementwise(func(a, b float32) float32 { return a + b }),
		OpSub:     elementwise(func(a, b float32) float32 { return a - b }),
		OpMul:     elementwise(func(a, b float32) float32 { return a * b }),
		OpDiv:     elementwise(func(a, b float32) float32 { return a / b }),
		OpMaximum: elementwise(func(a, b float32) float32 { return max(a, b) }),
		OpMinimum: elementwise(func(a, b float32) float32 { return min(a, b) }),
		OpSquaredDiff: elementwise(func(a, b float32) float32 {
			d := a - b
			return d * d
		}),

		OpLogistic: unary(func(v float32) float32 {
			return float32(1 / (1 + math.Exp(-float64(v))))
		}),
		OpLog:    unary(func(v float32) float32 { return float32(math.Log(float64(v))) }),
		OpExp:    unary(func(v float32) float32 { return float32(math.Exp(float64(v))) }),
		OpSqrt:   unary(func(v float32) float32 { return float32(math.Sqrt(float64(v))) }),
		OpRsqrt:  unary(func(v float32) float32 { return 1 / float32(math.Sqrt(float64(v))) }),
		OpSquare: unary(func(v float32) float32 { return v * v }),
		OpRelu:   unary(func(v float32) float32 { return max(v, 0) }),
		OpRelu6:  unary(func(v float32) float32 { return min(max(v, 0), 6) }),

		OpLeakyRelu: leakyRelu,

		OpMean:      reduceOp(0, func(a, v float32) float32 { return a + v }, true),
		OpSum:       reduceOp(0, func(a, v float32) float32 { return a + v }, false),
		OpReduceMax: reduceOp(float32(math.Inf(-1)), func(a, v float32) float32 { return max(a, v) }, false),

		OpPad:        padZero,
		OpTranspose:  transpose,
		OpReshape:    reshape,
		OpSqueeze:    squeeze,
		OpExpandDims: expandDims,

		OpShape:        shapeOf,
		OpFill:         fill,
		OpPack:         pack,
		OpStridedSlice: stridedSlice,
		OpReduceProd:   reduceProdI32,
	}
}
