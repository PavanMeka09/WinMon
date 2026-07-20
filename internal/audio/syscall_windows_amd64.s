#include "textflag.h"

// func syscallVolume(fn, this uintptr, volume float32, eventContext uintptr) uintptr
TEXT ·syscallVolume(SB), NOSPLIT, $40
	MOVQ fn+0(FP), AX
	MOVQ this+8(FP), CX
	MOVSS volume+16(FP), X1
	MOVQ eventContext+24(FP), R8
	CALL AX
	MOVQ AX, ret+32(FP)
	RET

