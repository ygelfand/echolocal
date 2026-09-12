//go:build arm && !noasm

#include "textflag.h"

// func dotVFP(a, b *float32, n int) float32
//
// Scalar VFP rather than NEON: Go's ARM assembler has no NEON mnemonics at all, and a kernel written
// as raw WORD encodings is one nobody can review.
//
// Four independent accumulators, eight elements an iteration. The target is a Cortex-A53, two-wide and
// in-order, and a multiply-accumulate that feeds itself stalls on its own latency — four chains cover
// it. Every load of a block is issued before any of its arithmetic so the load unit is not left
// waiting on the adder, which is the same reason the arm64 kernel alternates register sets.
//
// The pointers move by hand. VLDR has no post-indexed form — MOVF.P assembles without the writeback
// and silently reads the same element every iteration.
TEXT ·dotVFP(SB), NOSPLIT, $0-16
	MOVW	a+0(FP), R0
	MOVW	b+4(FP), R1
	MOVW	n+8(FP), R2

	MOVF	$(0.0), F0
	MOVF	$(0.0), F1
	MOVF	$(0.0), F2
	MOVF	$(0.0), F3

eight:
	CMP	$8, R2
	BLT	one

	MOVF	0(R0), F4
	MOVF	0(R1), F5
	MOVF	4(R0), F6
	MOVF	4(R1), F7
	MOVF	8(R0), F8
	MOVF	8(R1), F9
	MOVF	12(R0), F10
	MOVF	12(R1), F11

	MULAF	F5, F4, F0
	MULAF	F7, F6, F1
	MULAF	F9, F8, F2
	MULAF	F11, F10, F3

	MOVF	16(R0), F4
	MOVF	16(R1), F5
	MOVF	20(R0), F6
	MOVF	20(R1), F7
	MOVF	24(R0), F8
	MOVF	24(R1), F9
	MOVF	28(R0), F10
	MOVF	28(R1), F11

	MULAF	F5, F4, F0
	MULAF	F7, F6, F1
	MULAF	F9, F8, F2
	MULAF	F11, F10, F3

	ADD	$32, R0
	ADD	$32, R1
	SUB	$8, R2
	B	eight

one:
	CMP	$0, R2
	BEQ	fold

	MOVF	0(R0), F4
	MOVF	0(R1), F5
	MULAF	F5, F4, F0
	ADD	$4, R0
	ADD	$4, R1
	SUB	$1, R2
	B	one

fold:
	ADDF	F1, F0
	ADDF	F2, F0
	ADDF	F3, F0
	MOVF	F0, ret+12(FP)
	RET
