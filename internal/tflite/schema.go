package tflite

import (
	"errors"
	"fmt"
)

// Field slots of the schema tables we read. Slot numbers are part of the file format: they are
// assigned by field order in schema.fbs and never reused, so appended fields do not disturb them.
const (
	modelOperatorCodes = 1
	modelSubgraphs     = 2
	modelDescription   = 3
	modelBuffers       = 4

	opcodeDeprecatedBuiltin = 0
	opcodeCustom            = 1
	opcodeBuiltin           = 3

	subgraphTensors   = 0
	subgraphInputs    = 1
	subgraphOutputs   = 2
	subgraphOperators = 3
	subgraphName      = 4

	tensorShape          = 0
	tensorType           = 1
	tensorBuffer         = 2
	tensorName           = 3
	tensorIsVariable     = 5
	tensorShapeSignature = 7

	operatorOpcodeIndex = 0
	operatorInputs      = 1
	operatorOutputs     = 2
	operatorOptions     = 4

	bufferData   = 0
	bufferOffset = 1
)

// TensorType is the schema's TensorType enum.
type TensorType uint8

const (
	Float32 TensorType = 0
	Int32   TensorType = 2
	Int64   TensorType = 4
	Bool    TensorType = 6
	Int8    TensorType = 9
)

func (t TensorType) String() string {
	switch t {
	case Float32:
		return "float32"
	case Int32:
		return "int32"
	case Int64:
		return "int64"
	case Bool:
		return "bool"
	case Int8:
		return "int8"
	}
	return fmt.Sprintf("type%d", uint8(t))
}

// Op is the schema's BuiltinOperator enum, limited to the codes this package names.
type Op int32

const (
	OpAdd            Op = 0
	OpConv2D         Op = 3
	OpDepthwiseConv  Op = 4
	OpFullyConnected Op = 9
	OpLogistic       Op = 14
	OpMaxPool2D      Op = 17
	OpMul            Op = 18
	OpRelu           Op = 19
	OpRelu6          Op = 21
	OpReshape        Op = 22
	OpSoftmax        Op = 25
	OpConcatenation  Op = 2
	OpPad            Op = 34
	OpTranspose      Op = 39
	OpMean           Op = 40
	OpSub            Op = 41
	OpDiv            Op = 42
	OpSqueeze        Op = 43
	OpStridedSlice   Op = 45
	OpExp            Op = 47
	OpMaximum        Op = 55
	OpMinimum        Op = 57
	OpExpandDims     Op = 70
	OpLog            Op = 73
	OpSum            Op = 74
	OpSqrt           Op = 75
	OpRsqrt          Op = 76
	OpReduceMax      Op = 82
	OpSquare         Op = 92
	OpLeakyRelu      Op = 98
	OpSquaredDiff    Op = 99
	OpBatchMatMul    Op = 126
)

var opNames = map[Op]string{
	OpAdd: "ADD", OpConcatenation: "CONCATENATION", OpConv2D: "CONV_2D",
	OpDepthwiseConv: "DEPTHWISE_CONV_2D", OpFullyConnected: "FULLY_CONNECTED",
	OpLogistic: "LOGISTIC", OpMaxPool2D: "MAX_POOL_2D", OpMul: "MUL", OpRelu: "RELU",
	OpRelu6: "RELU6", OpReshape: "RESHAPE", OpSoftmax: "SOFTMAX", OpPad: "PAD",
	OpTranspose: "TRANSPOSE", OpMean: "MEAN", OpSub: "SUB", OpDiv: "DIV",
	OpSqueeze: "SQUEEZE", OpStridedSlice: "STRIDED_SLICE", OpExp: "EXP",
	OpMaximum: "MAXIMUM", OpMinimum: "MINIMUM", OpExpandDims: "EXPAND_DIMS",
	OpLog: "LOG", OpSum: "SUM", OpSqrt: "SQRT", OpRsqrt: "RSQRT",
	OpReduceMax: "REDUCE_MAX", OpSquare: "SQUARE", OpLeakyRelu: "LEAKY_RELU",
	OpSquaredDiff: "SQUARED_DIFFERENCE", OpBatchMatMul: "BATCH_MATMUL",
}

func (o Op) String() string {
	if n, ok := opNames[o]; ok {
		return n
	}
	return fmt.Sprintf("OP_%d", int32(o))
}

// Activation is the schema's ActivationFunctionType enum.
type Activation uint8

const (
	ActNone Activation = iota
	ActRelu
	ActRelu1
	ActRelu6
	ActTanh
	ActSignBit
)

// Model is a parsed .tflite file. Weight data is referenced in place, so the byte slice handed
// to Parse must outlive the model.
type Model struct {
	Description string
	Subgraphs   []*Subgraph

	buffers []vector
}

type Subgraph struct {
	Name    string
	Tensors []*TensorDesc
	Ops     []*OpDesc
	Inputs  []int
	Outputs []int
}

type TensorDesc struct {
	Name string
	Type TensorType
	// Shape is the shape recorded at conversion time. Dimensions the signature marks dynamic
	// hold whatever value the converter traced, so they are a starting point, not a promise.
	Shape    []int
	Dynamic  []bool
	Buffer   int
	Variable bool
}

// OpDesc is one operator with its builtin options already decoded. Options are read at load so
// that a kernel invoked every audio frame is not walking flatbuffer vtables to find its stride.
type OpDesc struct {
	Op      Op
	Custom  string
	Inputs  []int
	Outputs []int

	conv     convParams
	pool     poolParams
	act      Activation
	keepDims bool
	alpha    float32
	dims     []int
	adjX     bool
	adjY     bool
}

var errShort = errors.New("tflite: buffer too short")

// Parse reads a model. Malformed input reaches the accessors as out-of-range indexing, so the
// panic is converted rather than checked at every offset.
func Parse(b []byte) (m *Model, err error) {
	if len(b) < 8 {
		return nil, errShort
	}
	defer func() {
		if r := recover(); r != nil {
			m, err = nil, fmt.Errorf("tflite: malformed model: %v", r)
		}
	}()

	data := buf(b)
	r := root(data)

	codes := r.vec(modelOperatorCodes)
	m = &Model{Description: r.str(modelDescription)}

	for i, bufs := 0, r.vec(modelBuffers); i < bufs.len(); i++ {
		t := bufs.table(i)
		if t.u64f(bufferOffset, 0) != 0 {
			return nil, fmt.Errorf("tflite: buffer %d is stored outside the file", i)
		}
		m.buffers = append(m.buffers, t.vec(bufferData))
	}

	for i, subs := 0, r.vec(modelSubgraphs); i < subs.len(); i++ {
		st := subs.table(i)
		g := &Subgraph{
			Name:    st.str(subgraphName),
			Inputs:  st.vec(subgraphInputs).ints(),
			Outputs: st.vec(subgraphOutputs).ints(),
		}

		for j, ts := 0, st.vec(subgraphTensors); j < ts.len(); j++ {
			tt := ts.table(j)
			d := &TensorDesc{
				Name:     tt.str(tensorName),
				Type:     TensorType(tt.u8f(tensorType, 0)),
				Shape:    tt.vec(tensorShape).ints(),
				Buffer:   int(tt.u32f(tensorBuffer, 0)),
				Variable: tt.boolf(tensorIsVariable, false),
			}
			d.Dynamic = make([]bool, len(d.Shape))
			if sig := tt.vec(tensorShapeSignature); sig.len() == len(d.Shape) {
				for k := range d.Shape {
					d.Dynamic[k] = sig.i32(k) < 0
				}
			}
			g.Tensors = append(g.Tensors, d)
		}

		for j, os := 0, st.vec(subgraphOperators); j < os.len(); j++ {
			ot := os.table(j)
			idx := int(ot.u32f(operatorOpcodeIndex, 0))
			if idx >= codes.len() {
				return nil, fmt.Errorf("tflite: operator %d has opcode index %d of %d", j, idx, codes.len())
			}
			ct := codes.table(idx)
			code := Op(ct.i32f(opcodeBuiltin, 0))
			if code == 0 {
				// Models written before builtin_code widened to int32 carry the code in the
				// deprecated byte field, and ADD is genuinely zero in both.
				code = Op(ct.u8f(opcodeDeprecatedBuiltin, 0))
			}
			o := &OpDesc{
				Op:      code,
				Custom:  ct.str(opcodeCustom),
				Inputs:  ot.vec(operatorInputs).ints(),
				Outputs: ot.vec(operatorOutputs).ints(),
			}
			o.decodeOptions(ot.table(operatorOptions))
			g.Ops = append(g.Ops, o)
		}

		m.Subgraphs = append(m.Subgraphs, g)
	}

	if len(m.Subgraphs) == 0 {
		return nil, errors.New("tflite: model has no subgraphs")
	}
	return m, nil
}

// buffer returns the constant data for a tensor, or nil when the tensor is computed.
func (m *Model) buffer(i int) []byte {
	if i <= 0 || i >= len(m.buffers) {
		return nil
	}
	return m.buffers[i].bytes()
}

type convParams struct {
	samePad   bool
	strideW   int
	strideH   int
	dilationW int
	dilationH int
	act       Activation
}

// decodeOptions reads one operator's builtin options. Slot numbers are per options type and
// mean nothing outside the operator that uses them, hence the switch.
func (o *OpDesc) decodeOptions(t table, present bool) {
	o.conv = convParams{strideW: 1, strideH: 1, dilationW: 1, dilationH: 1}
	o.pool = poolParams{strideW: 1, strideH: 1, filterW: 1, filterH: 1}
	if !present {
		return
	}

	switch o.Op {
	case OpConv2D, OpDepthwiseConv:
		o.conv.samePad = t.u8f(0, 0) == 0
		o.conv.strideW = int(t.i32f(1, 1))
		o.conv.strideH = int(t.i32f(2, 1))
		o.conv.act = Activation(t.u8f(3, 0))
		o.conv.dilationW = int(t.i32f(4, 1))
		o.conv.dilationH = int(t.i32f(5, 1))

	case OpMaxPool2D:
		o.pool.samePad = t.u8f(0, 0) == 0
		o.pool.strideW = int(t.i32f(1, 1))
		o.pool.strideH = int(t.i32f(2, 1))
		o.pool.filterW = int(t.i32f(3, 1))
		o.pool.filterH = int(t.i32f(4, 1))
		o.pool.act = Activation(t.u8f(5, 0))

	case OpAdd, OpSub, OpMul, OpDiv, OpFullyConnected, OpConcatenation:
		o.act = Activation(t.u8f(0, 0))

	case OpMean, OpSum, OpReduceMax:
		o.keepDims = t.boolf(0, false)

	case OpLeakyRelu:
		o.alpha = t.f32f(0, 0)

	case OpReshape, OpSqueeze:
		o.dims = t.vec(0).ints()

	case OpBatchMatMul:
		o.adjX = t.boolf(0, false)
		o.adjY = t.boolf(1, false)
	}
}

type poolParams struct {
	samePad bool
	strideW int
	strideH int
	filterW int
	filterH int
	act     Activation
}
