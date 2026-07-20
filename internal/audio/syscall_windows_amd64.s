#include "textflag.h"

// func syscallVolume(fn, this uintptr, volume float32, eventContext uintptr) uintptr
TEXT ·syscallVolume(SB), NOSPLIT, $40
	// Go ABI: AX=fn, BX=this, X0=volume, CX=eventContext
	// Win ABI: CX=this, X1=volume, R8=eventContext
	MOVQ CX, R8
	MOVQ BX, CX
	MOVSS X0, X1
	CALL AX
	RET
