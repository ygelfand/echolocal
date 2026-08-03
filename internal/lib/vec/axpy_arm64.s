//go:build arm64 && !noasm

#include "textflag.h"

// func axpyNEON(dst, x *float32, n int, gain float32)
//
// dst[i] += gain*x[i], sixteen floats a block. The scale is not named g, which is the goroutine
// register and will not parse as an argument.
//
// All eight loads of a block are issued before any of the arithmetic, and all four stores come after
// all four multiply-accumulates, which leaves three instructions between a value being computed and
// being stored. The target is a Cortex-A53 — two-wide and in-order, so that slack has to be arranged
// rather than found.
//
// It is not pipelined across iterations the way the dot product is, which was measured rather than
// assumed: doing that here is worth 3% at 512 floats and 7% at 1024, against 1.4x for the dot. The
// difference is what each one waits on. The dot is latency bound, so covering a load with arithmetic is
// the whole game. This is bandwidth bound — three memory operations per four floats against the dot's
// two, running at about 6.6 GB/s of real traffic — and no amount of scheduling makes the memory system
// faster. The pipelined version needed a second dst pointer and two drain paths to save 0.2% of a core,
// so it went in the bin.
//
// R0 both reads and writes dst: the read leaves it where it is, the store advances it.
TEXT ·axpyNEON(SB), NOSPLIT, $0-28
	MOVD	dst+0(FP), R0
	MOVD	x+8(FP), R1
	MOVD	n+16(FP), R2
	FMOVS	gain+24(FP), F16
	VDUP	V16.S[0], V16.S4

sixteen:
	CMP	$16, R2
	BLT	four

	VLD1.P	64(R1), [V4.S4, V5.S4, V6.S4, V7.S4]
	VLD1	(R0), [V8.S4, V9.S4, V10.S4, V11.S4]

	VFMLA	V16.S4, V4.S4, V8.S4
	VFMLA	V16.S4, V5.S4, V9.S4
	VFMLA	V16.S4, V6.S4, V10.S4
	VFMLA	V16.S4, V7.S4, V11.S4

	VST1.P	[V8.S4, V9.S4, V10.S4, V11.S4], 64(R0)
	SUB	$16, R2
	B	sixteen

four:
	CMP	$4, R2
	BLT	one

	VLD1.P	16(R1), [V4.S4]
	VLD1	(R0), [V8.S4]
	VFMLA	V16.S4, V4.S4, V8.S4
	VST1.P	[V8.S4], 16(R0)
	SUB	$4, R2
	B	four

one:
	CBZ	R2, done

single:
	FMOVS	(R0), F0
	FMOVS	(R1), F1
	FMADDS	F16, F0, F1, F0
	FMOVS	F0, (R0)
	ADD	$4, R0
	ADD	$4, R1
	SUB	$1, R2
	CBNZ	R2, single

done:
	RET
