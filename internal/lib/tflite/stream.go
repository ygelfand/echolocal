package tflite

import "fmt"

// Stream evaluates a model incrementally along the time axis.
//
// It applies to a graph that is a chain of operators, each of which either transforms a row on
// its own or reads a fixed window of rows with no padding along time. Every openWakeWord
// embedding layer qualifies: its time convolutions are VALID with separable 3x1 filters, and the
// only padded axis is frequency, which lives inside a row. Under those conditions feeding rows
// through per-stage carry buffers computes the same dot products, in the same order, as running
// the whole window, so the results are identical rather than merely close.
//
// Where the windowed model recomputes 76 rows to emit one, this recomputes only what the new
// rows touch.
type Stream struct {
	stages []stage
	warmup int
	rowIn  int
	rowOut int
}

type stage struct {
	op     *OpDesc
	consts []*Tensor
	out    *Tensor

	// need is how many input rows one output row reads, consume how many are retired per output
	// row. Row-local operators are (1, 1).
	need    int
	consume int

	rowSize  int
	width    int
	channels int

	pending []float32
}

// NewStream prepares incremental evaluation of a model. window is the input row count the model
// was built for, and the shape of every intermediate tensor is derived from it.
func NewStream(m *Model, window []int) (*Stream, error) {
	in, err := New(m)
	if err != nil {
		return nil, err
	}
	in.ResizeInput(0, window)
	if err := in.Invoke(); err != nil {
		return nil, fmt.Errorf("tflite: shape inference: %w", err)
	}

	g := m.Subgraphs[0]
	s := &Stream{}
	prev := g.Inputs[0]

	for i, o := range g.Ops {
		if len(o.Inputs) == 0 || o.Inputs[0] != prev {
			return nil, fmt.Errorf("tflite: op %d (%s) does not continue the chain", i, o.Op)
		}
		x, y := in.Tensor(o.Inputs[0]), in.Tensor(o.Outputs[0])
		if len(x.Shape) != 4 || len(y.Shape) != 4 {
			return nil, fmt.Errorf("tflite: op %d (%s) is not 4-D", i, o.Op)
		}

		st := stage{
			op:       o,
			need:     1,
			consume:  1,
			width:    x.Shape[2],
			channels: x.Shape[3],
			out:      &Tensor{Type: Float32},
		}
		st.rowSize = st.width * st.channels

		for _, idx := range o.Inputs[1:] {
			if idx < 0 {
				st.consts = append(st.consts, nil)
				continue
			}
			st.consts = append(st.consts, in.Tensor(idx))
		}

		switch o.Op {
		case OpConv2D:
			w := in.Tensor(o.Inputs[1])
			kh := w.Shape[1]
			if kh > 1 && o.conv.samePad {
				return nil, fmt.Errorf("tflite: op %d pads %d rows along time", i, kh)
			}
			st.need, st.consume = kh, o.conv.strideH

		case OpMaxPool2D:
			if o.pool.filterH > 1 && o.pool.samePad {
				return nil, fmt.Errorf("tflite: op %d pads %d rows along time", i, o.pool.filterH)
			}
			st.need, st.consume = o.pool.filterH, o.pool.strideH

		case OpPad:
			p := in.Tensor(o.Inputs[1])
			if len(p.I32) < 4 || p.I32[2] != 0 || p.I32[3] != 0 {
				return nil, fmt.Errorf("tflite: op %d pads along time", i)
			}

		default:
			// Everything else has to be row-local, which for a binary operator means its other
			// operand cannot span rows.
			for _, c := range st.consts {
				if c != nil && len(c.Shape) == 4 && c.Shape[1] > 1 {
					return nil, fmt.Errorf("tflite: op %d (%s) reads %d rows of a constant", i, o.Op, c.Shape[1])
				}
			}
			if _, ok := kernels[o.Op]; !ok {
				return nil, fmt.Errorf("tflite: op %d is %s, which is not implemented", i, o.Op)
			}
		}

		if st.consume < 1 {
			return nil, fmt.Errorf("tflite: op %d has a time stride of %d", i, st.consume)
		}
		s.stages = append(s.stages, st)
		prev = o.Outputs[0]
	}

	if len(s.stages) == 0 {
		return nil, fmt.Errorf("tflite: model has no operators")
	}
	s.rowIn = window[2] * window[3]
	out := in.Tensor(g.Outputs[0])
	s.rowOut = count(out.Shape) / max(out.Shape[1], 1)
	s.warmup = window[1]
	return s, nil
}

// Warmup is the number of rows to feed before the first output row appears.
func (s *Stream) Warmup() int { return s.warmup }

// Write feeds rows and returns whatever came out, flat and oldest first. The returned slice is
// reused by the next call.
func (s *Stream) Write(rows []float32) ([]float32, error) {
	cur := rows
	for i := range s.stages {
		out, err := s.stages[i].run(cur)
		if err != nil {
			return nil, fmt.Errorf("tflite: stage %d (%s): %w", i, s.stages[i].op.Op, err)
		}
		if len(out) == 0 {
			return nil, nil
		}
		cur = out
	}
	return cur, nil
}

// Reset drops all buffered history.
func (s *Stream) Reset() {
	for i := range s.stages {
		s.stages[i].pending = s.stages[i].pending[:0]
	}
}

func (st *stage) run(rows []float32) ([]float32, error) {
	if len(rows)%st.rowSize != 0 {
		return nil, fmt.Errorf("got %d values, not a whole number of %d-value rows", len(rows), st.rowSize)
	}
	st.pending = append(st.pending, rows...)

	have := len(st.pending) / st.rowSize
	if have < st.need {
		return nil, nil
	}
	emit := (have-st.need)/st.consume + 1
	used := st.need + (emit-1)*st.consume

	in := &Tensor{
		Type:  Float32,
		Shape: []int{1, used, st.width, st.channels},
		F32:   st.pending[:used*st.rowSize],
	}
	if err := kernels[st.op.Op](st.op, append([]*Tensor{in}, st.consts...), []*Tensor{st.out}); err != nil {
		return nil, err
	}
	if got := st.out.Shape[1]; got != emit {
		return nil, fmt.Errorf("produced %d rows from %d, want %d", got, used, emit)
	}

	drop := emit * st.consume * st.rowSize
	st.pending = append(st.pending[:0], st.pending[drop:]...)
	return st.out.F32, nil
}
