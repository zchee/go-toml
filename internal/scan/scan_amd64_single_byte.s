// Copyright 2026 The go-toml Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build !force_swar && goexperiment.simd && amd64

// scan_amd64_single_byte.s — hand-rolled SSE2 + AVX2 implementations of
// LocateNewline ('\n', returns -1 on miss) and ScanLiteralString ('\'',
// returns len(s) on miss).
//
// Modeled byte-for-byte on $GOROOT/src/internal/bytealg/indexbyte_amd64.s
// (Go 1.27-devel snapshot from /opt/local/go.simd). The deliberate choice
// to re-roll these as fused single-TEXT assembly (rather than archsimd
// intrinsics, which T2 used) is because T5's AC-SIMD-5 perf gate showed
// the intrinsics-based path losing to stdlib `bytes.IndexByte`. We want
// the same fused PCMPEQB+PMOVMSKB+BSFL inner loop (SSE2) and
// VPCMPEQB+VPTEST+VPMOVMSKB inner loop (AVX2) that stdlib uses, with no
// intermediate function-call overhead.
//
// Structural identity with stdlib's indexbyte_amd64.s:
//   - SSE2 inner loop: MOVOU + PCMPEQB + PMOVMSKB + BSFL — same instr
//     sequence stdlib uses.
//   - AVX2 inner loop: VMOVDQU + VPCMPEQB + VPTEST + (on hit) VPMOVMSKB
//     + BSFL — same sequence.
//   - "Last chunk may overlap" tail handler — same trick.
//   - Page-cross-safe small (<16) path with TESTW $0xff0 / endofpage
//     overlap-from-end — copied verbatim shape.
//
// Per-TEXT deltas vs stdlib indexbytebody<>:
//   - Target byte is a compile-time constant ($0x0A or $0x27), not an
//     argument register. One fewer arg load.
//   - Return slot is `ret+24(FP)` directly (not the indirect R8 stdlib
//     uses to share the body across IndexByte / IndexByteString).
//   - Miss return value differs: -1 for LocateNewline (matches stdlib
//     IndexByte semantics), `BX` (= len(s)) for ScanLiteralString.
//   - PCALIGN $16 / $32 on the inner loops to keep the hot path entry on
//     a cache-line boundary (matches stdlib).
//
// Calling convention: ABI0 (frame-pointer based). The Go compiler emits
// a wrapper that translates the caller's ABIInternal register args into
// ABI0 stack args at `s_base+0(FP)`, `s_len+8(FP)`. This matches stdlib
// bytes.IndexByte exactly, so the per-call wrapper cost is identical
// for both — like-for-like comparison.

#include "textflag.h"

// =====================================================================
// LocateNewline — find first '\n' (0x0A) byte, return -1 on miss.
// =====================================================================

// func locateNewlineSSE2(s []byte) int
TEXT ·locateNewlineSSE2(SB), NOSPLIT, $0-32
	MOVQ s_base+0(FP), SI
	MOVQ s_len+8(FP), BX

	// Broadcast '\n' (0x0A) to every byte of X0.
	MOVQ      $0x0A0A0A0A0A0A0A0A, AX
	MOVQ      AX, X0
	PUNPCKLBW X0, X0
	PSHUFL    $0, X0, X0

	CMPQ BX, $16
	JLT  small

	MOVQ SI, DI
	LEAQ -16(SI)(BX*1), AX
	JMP  sseloopentry

	PCALIGN $16

sseloop:
	MOVOU    (DI), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JNZ      ssesuccess
	ADDQ     $16, DI

sseloopentry:
	CMPQ DI, AX
	JB   sseloop

	// Final overlapping 16-byte chunk.
	MOVQ     AX, DI
	MOVOU    (AX), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JNZ      ssesuccess

	MOVQ $-1, ret+24(FP)
	RET

ssesuccess:
	SUBQ SI, DI
	ADDQ DX, DI
	MOVQ DI, ret+24(FP)
	RET

small:
	TESTQ BX, BX
	JEQ   smallmiss

	LEAQ  16(SI), AX
	TESTW $0xff0, AX
	JEQ   endofpage

	MOVOU    (SI), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JZ       smallmiss
	CMPL     DX, BX
	JAE      smallmiss
	MOVQ     DX, ret+24(FP)
	RET

endofpage:
	MOVOU    -16(SI)(BX*1), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	MOVL     BX, CX
	SHLL     CX, DX
	SHRL     $16, DX
	BSFL     DX, DX
	JZ       smallmiss
	MOVQ     DX, ret+24(FP)
	RET

smallmiss:
	MOVQ $-1, ret+24(FP)
	RET

// func locateNewlineAVX2(s []byte) int
TEXT ·locateNewlineAVX2(SB), NOSPLIT, $0-32
	MOVQ s_base+0(FP), SI
	MOVQ s_len+8(FP), BX

	// Broadcast '\n' to X0 (used for both SSE2 fallback and AVX2 init).
	MOVQ      $0x0A0A0A0A0A0A0A0A, AX
	MOVQ      AX, X0
	PUNPCKLBW X0, X0
	PSHUFL    $0, X0, X0

	CMPQ BX, $16
	JLT  small
	CMPQ BX, $32
	JA   avx2

	// 16 <= len <= 32: fall to SSE2 16-byte loop.
	MOVQ SI, DI
	LEAQ -16(SI)(BX*1), AX
	JMP  sseloopentry

	PCALIGN $16

sseloop:
	MOVOU    (DI), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JNZ      ssesuccess
	ADDQ     $16, DI

sseloopentry:
	CMPQ DI, AX
	JB   sseloop

	MOVQ     AX, DI
	MOVOU    (AX), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JNZ      ssesuccess

	MOVQ $-1, ret+24(FP)
	RET

ssesuccess:
	SUBQ SI, DI
	ADDQ DX, DI
	MOVQ DI, ret+24(FP)
	RET

avx2:
	VPBROADCASTB X0, Y1
	MOVQ         SI, DI
	LEAQ         -32(SI)(BX*1), R11

	PCALIGN $32

avx2_loop:
	VMOVDQU  (DI), Y2
	VPCMPEQB Y1, Y2, Y3
	VPTEST   Y3, Y3
	JNZ      avx2success
	ADDQ     $32, DI
	CMPQ     DI, R11
	JLT      avx2_loop

	// Final overlapping 32-byte chunk.
	MOVQ     R11, DI
	VMOVDQU  (DI), Y2
	VPCMPEQB Y1, Y2, Y3
	VPTEST   Y3, Y3
	JNZ      avx2success

	VZEROUPPER
	MOVQ $-1, ret+24(FP)
	RET

avx2success:
	VPMOVMSKB Y3, DX
	BSFL      DX, DX
	SUBQ      SI, DI
	ADDQ      DI, DX
	VZEROUPPER
	MOVQ      DX, ret+24(FP)
	RET

small:
	TESTQ BX, BX
	JEQ   smallmiss

	LEAQ  16(SI), AX
	TESTW $0xff0, AX
	JEQ   endofpage

	MOVOU    (SI), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JZ       smallmiss
	CMPL     DX, BX
	JAE      smallmiss
	MOVQ     DX, ret+24(FP)
	RET

endofpage:
	MOVOU    -16(SI)(BX*1), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	MOVL     BX, CX
	SHLL     CX, DX
	SHRL     $16, DX
	BSFL     DX, DX
	JZ       smallmiss
	MOVQ     DX, ret+24(FP)
	RET

smallmiss:
	MOVQ $-1, ret+24(FP)
	RET

// =====================================================================
// ScanLiteralString — find first '\'' (0x27) byte, return len(s) on miss.
// Identical shape to LocateNewline above except for the target byte and
// the miss return value (BX instead of -1).
// =====================================================================

// func scanLiteralStringSSE2(s []byte) int
TEXT ·scanLiteralStringSSE2(SB), NOSPLIT, $0-32
	MOVQ      s_base+0(FP), SI
	MOVQ      s_len+8(FP), BX
	MOVQ      $0x2727272727272727, AX
	MOVQ      AX, X0
	PUNPCKLBW X0, X0
	PSHUFL    $0, X0, X0

	CMPQ BX, $16
	JLT  small

	MOVQ SI, DI
	LEAQ -16(SI)(BX*1), AX
	JMP  sseloopentry

	PCALIGN $16

sseloop:
	MOVOU    (DI), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JNZ      ssesuccess
	ADDQ     $16, DI

sseloopentry:
	CMPQ DI, AX
	JB   sseloop

	MOVQ     AX, DI
	MOVOU    (AX), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JNZ      ssesuccess

	MOVQ BX, ret+24(FP)
	RET

ssesuccess:
	SUBQ SI, DI
	ADDQ DX, DI
	MOVQ DI, ret+24(FP)
	RET

small:
	TESTQ BX, BX
	JEQ   smallmiss

	LEAQ  16(SI), AX
	TESTW $0xff0, AX
	JEQ   endofpage

	MOVOU    (SI), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JZ       smallmiss
	CMPL     DX, BX
	JAE      smallmiss
	MOVQ     DX, ret+24(FP)
	RET

endofpage:
	MOVOU    -16(SI)(BX*1), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	MOVL     BX, CX
	SHLL     CX, DX
	SHRL     $16, DX
	BSFL     DX, DX
	JZ       smallmiss
	MOVQ     DX, ret+24(FP)
	RET

smallmiss:
	MOVQ BX, ret+24(FP)
	RET

// func scanLiteralStringAVX2(s []byte) int
TEXT ·scanLiteralStringAVX2(SB), NOSPLIT, $0-32
	MOVQ      s_base+0(FP), SI
	MOVQ      s_len+8(FP), BX
	MOVQ      $0x2727272727272727, AX
	MOVQ      AX, X0
	PUNPCKLBW X0, X0
	PSHUFL    $0, X0, X0

	CMPQ BX, $16
	JLT  small
	CMPQ BX, $32
	JA   avx2

	MOVQ SI, DI
	LEAQ -16(SI)(BX*1), AX
	JMP  sseloopentry

	PCALIGN $16

sseloop:
	MOVOU    (DI), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JNZ      ssesuccess
	ADDQ     $16, DI

sseloopentry:
	CMPQ DI, AX
	JB   sseloop

	MOVQ     AX, DI
	MOVOU    (AX), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JNZ      ssesuccess

	MOVQ BX, ret+24(FP)
	RET

ssesuccess:
	SUBQ SI, DI
	ADDQ DX, DI
	MOVQ DI, ret+24(FP)
	RET

avx2:
	VPBROADCASTB X0, Y1
	MOVQ         SI, DI
	LEAQ         -32(SI)(BX*1), R11

	PCALIGN $32

avx2_loop:
	VMOVDQU  (DI), Y2
	VPCMPEQB Y1, Y2, Y3
	VPTEST   Y3, Y3
	JNZ      avx2success
	ADDQ     $32, DI
	CMPQ     DI, R11
	JLT      avx2_loop

	MOVQ     R11, DI
	VMOVDQU  (DI), Y2
	VPCMPEQB Y1, Y2, Y3
	VPTEST   Y3, Y3
	JNZ      avx2success

	VZEROUPPER
	MOVQ BX, ret+24(FP)
	RET

avx2success:
	VPMOVMSKB Y3, DX
	BSFL      DX, DX
	SUBQ      SI, DI
	ADDQ      DI, DX
	VZEROUPPER
	MOVQ      DX, ret+24(FP)
	RET

small:
	TESTQ BX, BX
	JEQ   smallmiss

	LEAQ  16(SI), AX
	TESTW $0xff0, AX
	JEQ   endofpage

	MOVOU    (SI), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	BSFL     DX, DX
	JZ       smallmiss
	CMPL     DX, BX
	JAE      smallmiss
	MOVQ     DX, ret+24(FP)
	RET

endofpage:
	MOVOU    -16(SI)(BX*1), X1
	PCMPEQB  X0, X1
	PMOVMSKB X1, DX
	MOVL     BX, CX
	SHLL     CX, DX
	SHRL     $16, DX
	BSFL     DX, DX
	JZ       smallmiss
	MOVQ     DX, ret+24(FP)
	RET

smallmiss:
	MOVQ BX, ret+24(FP)
	RET
