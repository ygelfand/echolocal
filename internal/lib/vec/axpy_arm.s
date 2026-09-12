//go:build arm && !noasm

#include "textflag.h"

// func axpyVFP(dst, x *float32, n int, gain float32)
//
// dst[i] += gain*x[i], eight floats an iteration. Every load of a block is issued before any of the
// arithmetic and every store after all of it, which leaves slack between a value being computed and
// being stored — the target is a Cortex-A53, two-wide and in-order, so that has to be arranged rather
// than found.
//
// The pointers move by hand: VLDR and VSTR have no post-indexed form, and MOVF.P assembles without the
// writeback rather than refusing.
TEXT ·axpyVFP(SB), NOSPLIT, $0-16
	MOVW	dst+0(FP), R0
	MOVW	x+4(FP), R1
	MOVW	n+8(FP), R2
	MOVF	gain+12(FP), F0

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

	MULAF	F0, F5, F4
	MULAF	F0, F7, F6
	MULAF	F0, F9, F8
	MULAF	F0, F11, F10

	MOVF	F4, 0(R0)
	MOVF	F6, 4(R0)
	MOVF	F8, 8(R0)
	MOVF	F10, 12(R0)

	MOVF	16(R0), F4
	MOVF	16(R1), F5
	MOVF	20(R0), F6
	MOVF	20(R1), F7
	MOVF	24(R0), F8
	MOVF	24(R1), F9
	MOVF	28(R0), F10
	MOVF	28(R1), F11

	MULAF	F0, F5, F4
	MULAF	F0, F7, F6
	MULAF	F0, F9, F8
	MULAF	F0, F11, F10

	MOVF	F4, 16(R0)
	MOVF	F6, 20(R0)
	MOVF	F8, 24(R0)
	MOVF	F10, 28(R0)

	ADD	$32, R0
	ADD	$32, R1
	SUB	$8, R2
	B	eight

one:
	CMP	$0, R2
	BEQ	done

	MOVF	0(R0), F4
	MOVF	0(R1), F5
	MULAF	F0, F5, F4
	MOVF	F4, 0(R0)
	ADD	$4, R0
	ADD	$4, R1
	SUB	$1, R2
	B	one

done:
	RET
