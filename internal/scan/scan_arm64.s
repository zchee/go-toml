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

//go:build !force_swar && arm64

#include "textflag.h"

// Plan 9 ARM64 NEON implementation of the scan kernels. Each kernel
// uses two related shapes: older tail/specialized kernels may narrow a
// single 16-byte 0xFF/0x00 mask via VSHRN $4, while hot paths use a
// 32-byte dual-vector loop ported from internal/memchr/bytealg shapes.
// The 32-byte path first OR-reduces masks with VADDP.D2 to keep the no-hit
// path cheap, then reconstructs a precise magic-constant syndrome only
// for the candidate block.
//
// "Find first non-match" kernels use the same RBIT+CLZ sequence as match
// kernels after constructing an invalid-byte mask. ScanBareKey classifies
// bytes with low/high-nibble VTBL lookups; SkipWhitespace builds a membership
// mask with equality compares and then inverts it with VMVN (emitted
// as a WORD encoding for Go 1.26 assembler compatibility; see note below).
//
// Common register layout (per-kernel deviations noted in comments):
//
//   R0 — current data pointer (post-incremented by 16 or 32 per iter)
//   R1 — remaining length
//   R2 — saved original data pointer (for offset = R0 - R2)
//   R3 — scratch (constants, byte values in tail)
//   R4 — syndrome / lane count
//   V0 — current 16-byte chunk (VLD1 dest)
//   V1..V8 — target constants (broadcasts of literal bytes)
//   V9..V14 — working membership masks
//
// Frame layout for func(s []byte) int: ptr+0, len+8, cap+16, ret+24 → $0-32.
//
// Go 1.26 assembler compatibility: this file targets the Go 1.26
// toolchain, whose ARM64 assembler does not recognize the VMVN (NEON NOT)
// or VCMHS (unsigned compare higher-or-same) mnemonics; both were added in
// Go 1.27. Those instructions are emitted here as raw WORD encodings with
// the original mnemonic in a trailing comment. The encodings are the exact
// bytes the Go 1.27 assembler produces for the same operands (verified by
// round-trip objdump), so behavior is identical on every toolchain:
//
//   WORD $0x6e2058c6  // VMVN  V6.B16, V6.B16
//   WORD $0x6e2058e7  // VMVN  V7.B16, V7.B16
//   WORD $0x6e2059ad  // VMVN  V13.B16, V13.B16
//   WORD $0x6e213c46  // VCMHS V1.B16, V2.B16, V6.B16
//   WORD $0x6e213c67  // VCMHS V1.B16, V3.B16, V7.B16
//   WORD $0x6e213d2c  // VCMHS V1.B16, V9.B16, V12.B16
//   WORD $0x6e223d2c  // VCMHS V2.B16, V9.B16, V12.B16

// ============================================================================
// scanBareKeyNEON — first index whose byte is NOT in [A-Za-z0-9_-], or len(s).
// ============================================================================

// func scanBareKeyNEON(s []byte) int
TEXT ·scanBareKeyNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R2

	// Magic syndrome constant used only after a candidate 32-byte block hits.
	MOVD $0x40100401, R5
	VMOV R5, V15.S4

	// Bare-key membership is a conjunction of high-nibble and low-nibble
	// class bitsets. VTBL maps each nibble to its bitset; a byte is valid
	// when lowMask & highMask is non-zero. The classes are:
	//
	//   bit 0: low nibble 0..9       with high 3      -> digits
	//   bit 1: low nibble 1..15      with high 4 or 6 -> A-O / a-o
	//   bit 2: low nibble 0..10      with high 5 or 7 -> P-Z / p-z
	//   bit 3: low nibble 15         with high 5      -> underscore
	//   bit 4: low nibble 13         with high 2      -> hyphen
	//
	// V0 = low-nibble bitset table, V1 = high-nibble bitset table,
	// V2 = 0x0f mask, V14 = zero vector.
	MOVD $0x0707070707070705, R3
	VMOV R3, V0.D[0]
	MOVD $0x0a02120202060707, R3
	VMOV R3, V0.D[1]
	VEOR V1.B16, V1.B16, V1.B16
	MOVD $0x04020c0201100000, R3
	VMOV R3, V1.D[0]
	VEOR V14.B16, V14.B16, V14.B16
	MOVD $0x0f, R3
	VMOV R3, V2.B16

bare_loop32:
	CMP    $32, R1
	BLT    bare_tail
	VLD1.P (R0), [V12.B16, V13.B16]
	SUB    $32, R1, R1

	// First 16 bytes invalid mask -> V9.
	VAND  V2.B16, V12.B16, V3.B16
	VUSHR $4, V12.B16, V4.B16
	VTBL  V3.B16, [V0.B16], V9.B16
	VTBL  V4.B16, [V1.B16], V10.B16
	VAND  V10.B16, V9.B16, V9.B16
	VCMEQ V14.B16, V9.B16, V9.B16

	// Second 16 bytes invalid mask -> V10.
	VAND  V2.B16, V13.B16, V3.B16
	VUSHR $4, V13.B16, V4.B16
	VTBL  V3.B16, [V0.B16], V10.B16
	VTBL  V4.B16, [V1.B16], V11.B16
	VAND  V11.B16, V10.B16, V10.B16
	VCMEQ V14.B16, V10.B16, V10.B16

	VORR  V10.B16, V9.B16, V11.B16
	VADDP V11.D2, V11.D2, V11.D2
	VMOV  V11.D[0], R6
	CBNZ  R6, bare_construct
	CBNZ  R1, bare_loop32
	B     bare_done

bare_construct:
	VAND  V15.B16, V9.B16, V9.B16
	VAND  V15.B16, V10.B16, V10.B16
	VADDP V10.B16, V9.B16, V11.B16
	VADDP V11.B16, V11.B16, V11.B16
	VMOV  V11.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6
	SUB   $32, R0, R0
	ADD   R6>>1, R0, R0
	SUB   R2, R0, R0
	MOVD  R0, ret+24(FP)
	RET

bare_tail:
	CBZ R1, bare_done

bare_tail_loop:
	MOVBU (R0), R3

	// In class?
	// 'A' <= b <= 'Z'?
	SUB $'A', R3, R4
	CMP $25, R4
	BLS bare_tail_next

	// 'a' <= b <= 'z'?
	SUB $'a', R3, R4
	CMP $25, R4
	BLS bare_tail_next

	// '0' <= b <= '9'?
	SUB $'0', R3, R4
	CMP $9, R4
	BLS bare_tail_next

	// _ or -?
	CMP $'_', R3
	BEQ bare_tail_next
	CMP $'-', R3
	BEQ bare_tail_next

	// Not in class.
	SUB  R2, R0, R0
	MOVD R0, ret+24(FP)
	RET

bare_tail_next:
	ADD  $1, R0
	SUB  $1, R1
	CBNZ R1, bare_tail_loop

bare_done:
	SUB  R2, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// scanBasicStringNEON — first index of '"' or '\\', or len(s).
// ============================================================================

// func scanBasicStringNEON(s []byte) int
TEXT ·scanBasicStringNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R2

	MOVD $0x40100401, R5
	VMOV R5, V5.S4
	MOVD $'"', R3
	VMOV R3, V1.B16
	MOVD $'\\', R3
	VMOV R3, V2.B16

bstr_loop32:
	CMP    $32, R1
	BLT    bstr_tail
	VLD1.P (R0), [V3.B16, V4.B16]
	SUB    $32, R1, R1
	VCMEQ  V1.B16, V3.B16, V6.B16
	VCMEQ  V2.B16, V3.B16, V8.B16
	VORR   V8.B16, V6.B16, V6.B16
	VCMEQ  V1.B16, V4.B16, V7.B16
	VCMEQ  V2.B16, V4.B16, V8.B16
	VORR   V8.B16, V7.B16, V7.B16
	VORR   V7.B16, V6.B16, V8.B16
	VADDP  V8.D2, V8.D2, V8.D2
	VMOV   V8.D[0], R6
	CBNZ   R6, bstr_construct
	CBNZ   R1, bstr_loop32
	B      bstr_done

bstr_construct:
	VAND  V5.B16, V6.B16, V6.B16
	VAND  V5.B16, V7.B16, V7.B16
	VADDP V7.B16, V6.B16, V8.B16
	VADDP V8.B16, V8.B16, V8.B16
	VMOV  V8.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6
	SUB   $32, R0, R0
	ADD   R6>>1, R0, R0
	SUB   R2, R0, R0
	MOVD  R0, ret+24(FP)
	RET

bstr_tail:
	CBZ R1, bstr_done

bstr_tail_loop:
	MOVBU (R0), R3
	CMP   $'"', R3
	BEQ   bstr_match
	CMP   $'\\', R3
	BEQ   bstr_match
	ADD   $1, R0
	SUB   $1, R1
	CBNZ  R1, bstr_tail_loop

bstr_done:
	SUB  R2, R0, R0
	MOVD R0, ret+24(FP)
	RET

bstr_match:
	SUB  R2, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// scanLiteralStringNEON — first index of single-quote (0x27), or len(s).
//
// T3.1 rewrite (perf): mirrors stdlib internal/bytealg/indexbyte_arm64.s as
// closely as is safe — fused single TEXT, 32-byte stride (two V regs per
// VLD1), fast termination check (VORR + VADDP.D2 + VMOV + CBNZ) that skips
// the magic-constant syndrome on the no-match fast path, magic syndrome
// reconstructed only when a hit is found. No BIC-align-down dance — that
// stdlib trick reads up to 31 bytes before the slice start which is safe
// on commodity arm64 but a future hazard on MTE-enabled platforms; the
// ~11% gap we are closing comes from the stride/fast-check work, not from
// alignment. Per-byte tail handled inline.
// ============================================================================

// func scanLiteralStringNEON(s []byte) int
TEXT ·scanLiteralStringNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R11          // R11 = saved base, for final offset

	// Empty input: 0 (offset of "past end").
	CBZ R1, lstr_drained

	// Magic constant 0x40100401: broadcast to V5.S4 yields the 16-byte
	// pattern {0x01, 0x04, 0x10, 0x40, …}. Used to AND with VCMEQ
	// results so each matched lane contributes 2 bits to the syndrome.
	MOVD $0x40100401, R5
	VMOV R5, V5.S4

	// Broadcast target byte ('\'') to all 16 lanes of V0.
	MOVD $'\'', R3
	VMOV R3, V0.B16

lstr_loop32:
	CMP    $32, R1
	BLT    lstr_tail
	VLD1.P (R0), [V1.B16, V2.B16] // load 32 B, post-increment R0 by 32
	SUB    $32, R1, R1
	VCMEQ  V0.B16, V1.B16, V3.B16
	VCMEQ  V0.B16, V2.B16, V4.B16

	// Fast termination check: VORR collapses the two compare masks; the
	// VADDP.D2 pair-adds the two 64-bit halves, producing a value that
	// is zero iff every byte mismatched (every byte in V_or is 0x00).
	VORR  V4.B16, V3.B16, V6.B16
	VADDP V6.D2, V6.D2, V6.D2
	VMOV  V6.D[0], R6
	CBNZ  R6, lstr_construct
	CBNZ  R1, lstr_loop32

	// Exactly 32-aligned drain: nothing left, no match.
	B lstr_drained

lstr_construct:
	// Build the precise 2-bit-per-lane syndrome (lane k → bit 2k).
	VAND  V5.B16, V3.B16, V3.B16
	VAND  V5.B16, V4.B16, V4.B16
	VADDP V4.B16, V3.B16, V6.B16 // 32 B → 16 B (interleaved)
	VADDP V6.B16, V6.B16, V6.B16 // 16 B → 8 B
	VMOV  V6.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6                 // R6 = 2 * lane index of first match
	SUB   $32, R0, R0            // R0 was post-incremented; rewind 32
	ADD   R6>>1, R0, R0          // R0 = match address (lane index = R6/2)
	SUB   R11, R0, R0
	MOVD  R0, ret+24(FP)
	RET

lstr_tail:
	CBZ R1, lstr_drained

lstr_tail_loop:
	MOVBU.P 1(R0), R3
	CMP     $'\'', R3
	BEQ     lstr_tail_match
	SUBS    $1, R1, R1
	BNE     lstr_tail_loop

lstr_drained:
	SUB  R11, R0, R0    // R0 - base = len(s) consumed
	MOVD R0, ret+24(FP)
	RET

lstr_tail_match:
	SUB  $1, R0, R0     // rewind MOVBU.P to point at match
	SUB  R11, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// skipWhitespaceNEON — first index that is not ' ' or '\t', or len(s).
// ============================================================================

// func skipWhitespaceNEON(s []byte) int
TEXT ·skipWhitespaceNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R2

	MOVD $0x40100401, R5
	VMOV R5, V5.S4
	MOVD $' ', R3
	VMOV R3, V1.B16
	MOVD $9, R3
	VMOV R3, V2.B16

ws_loop32:
	CMP    $32, R1
	BLT    ws_tail
	VLD1.P (R0), [V3.B16, V4.B16]
	SUB    $32, R1, R1
	VCMEQ  V1.B16, V3.B16, V6.B16
	VCMEQ  V2.B16, V3.B16, V8.B16
	VORR   V8.B16, V6.B16, V6.B16
	WORD   $0x6e2058c6            // VMVN V6.B16, V6.B16
	VCMEQ  V1.B16, V4.B16, V7.B16
	VCMEQ  V2.B16, V4.B16, V8.B16
	VORR   V8.B16, V7.B16, V7.B16
	WORD   $0x6e2058e7            // VMVN V7.B16, V7.B16
	VORR   V7.B16, V6.B16, V8.B16
	VADDP  V8.D2, V8.D2, V8.D2
	VMOV   V8.D[0], R6
	CBNZ   R6, ws_construct
	CBNZ   R1, ws_loop32
	B      ws_done

ws_construct:
	VAND  V5.B16, V6.B16, V6.B16
	VAND  V5.B16, V7.B16, V7.B16
	VADDP V7.B16, V6.B16, V8.B16
	VADDP V8.B16, V8.B16, V8.B16
	VMOV  V8.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6
	SUB   $32, R0, R0
	ADD   R6>>1, R0, R0
	SUB   R2, R0, R0
	MOVD  R0, ret+24(FP)
	RET

ws_tail:
	CBZ R1, ws_done

ws_tail_loop:
	MOVBU (R0), R3
	CMP   $' ', R3
	BEQ   ws_tail_next
	CMP   $9, R3
	BEQ   ws_tail_next
	SUB   R2, R0, R0
	MOVD  R0, ret+24(FP)
	RET

ws_tail_next:
	ADD  $1, R0
	SUB  $1, R1
	CBNZ R1, ws_tail_loop

ws_done:
	SUB  R2, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// locateNewlineNEON — first index of '\n', or -1 (NOT len(s)) on miss.
//
// T3.1 rewrite (perf): same fused / 32-byte / fast-term-check / magic-syndrome
// shape as scanLiteralStringNEON above; differs only in the target byte and
// the "miss → -1" return convention (vs ScanLiteralString's "miss → len(s)").
// See the comment block on scanLiteralStringNEON for the algorithmic
// rationale, lane-reduction math, and MTE-safety note on skipping the
// stdlib BIC-align-down trick.
// ============================================================================

// func locateNewlineNEON(s []byte) int
TEXT ·locateNewlineNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R11

	CBZ R1, nl_notfound

	MOVD $0x40100401, R5
	VMOV R5, V5.S4
	MOVD $'\n', R3
	VMOV R3, V0.B16

nl_loop32:
	CMP    $32, R1
	BLT    nl_tail
	VLD1.P (R0), [V1.B16, V2.B16]
	SUB    $32, R1, R1
	VCMEQ  V0.B16, V1.B16, V3.B16
	VCMEQ  V0.B16, V2.B16, V4.B16
	VORR   V4.B16, V3.B16, V6.B16
	VADDP  V6.D2, V6.D2, V6.D2
	VMOV   V6.D[0], R6
	CBNZ   R6, nl_construct
	CBNZ   R1, nl_loop32
	B      nl_notfound

nl_construct:
	VAND  V5.B16, V3.B16, V3.B16
	VAND  V5.B16, V4.B16, V4.B16
	VADDP V4.B16, V3.B16, V6.B16
	VADDP V6.B16, V6.B16, V6.B16
	VMOV  V6.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6
	SUB   $32, R0, R0
	ADD   R6>>1, R0, R0
	SUB   R11, R0, R0
	MOVD  R0, ret+24(FP)
	RET

nl_tail:
	CBZ R1, nl_notfound

nl_tail_loop:
	MOVBU.P 1(R0), R3
	CMP     $'\n', R3
	BEQ     nl_tail_match
	SUBS    $1, R1, R1
	BNE     nl_tail_loop

nl_notfound:
	MOVD $-1, R0
	MOVD R0, ret+24(FP)
	RET

nl_tail_match:
	SUB  $1, R0, R0
	SUB  R11, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// validateUTF8NEONBulk — first index whose byte has high bit set, or len(s).
// The Go-side wrapper validateUTF8NEON continues from this index with a
// scalar unicode/utf8.DecodeRune loop (mirrors the SSE2/AVX2 pattern).
// ============================================================================

// func validateUTF8NEONBulk(s []byte) int
TEXT ·validateUTF8NEONBulk(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R2

	MOVD $0x40100401, R5
	VMOV R5, V5.S4
	MOVD $0x80, R3
	VMOV R3, V1.B16

u8_loop32:
	CMP    $32, R1
	BLT    u8_tail
	VLD1.P (R0), [V2.B16, V3.B16]
	SUB    $32, R1, R1
	WORD   $0x6e213c46            // VCMHS V1.B16, V2.B16, V6.B16
	WORD   $0x6e213c67            // VCMHS V1.B16, V3.B16, V7.B16
	VORR   V7.B16, V6.B16, V8.B16
	VADDP  V8.D2, V8.D2, V8.D2
	VMOV   V8.D[0], R6
	CBNZ   R6, u8_construct
	CBNZ   R1, u8_loop32
	B      u8_done

u8_construct:
	VAND  V5.B16, V6.B16, V6.B16
	VAND  V5.B16, V7.B16, V7.B16
	VADDP V7.B16, V6.B16, V8.B16
	VADDP V8.B16, V8.B16, V8.B16
	VMOV  V8.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6
	SUB   $32, R0, R0
	ADD   R6>>1, R0, R0
	SUB   R2, R0, R0
	MOVD  R0, ret+24(FP)
	RET

u8_tail:
	CBZ R1, u8_done

u8_tail_loop:
	MOVBU (R0), R3
	CMP   $0x80, R3
	BHS   u8_match
	ADD   $1, R0
	SUB   $1, R1
	CBNZ  R1, u8_tail_loop

u8_done:
	SUB  R2, R0, R0
	MOVD R0, ret+24(FP)
	RET

u8_match:
	SUB  R2, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// scanBasicStringStrictNEON — first slow byte in single-line basic string body.
// Stops at '"', '\\', DEL, or C0 control except tab; returns len(s) on miss.
// Uses the 32-byte memchr syndrome shape so the no-hit path checks only the
// OR-reduced compare masks and constructs the precise syndrome on hit.
// ============================================================================

// func scanBasicStringStrictNEON(s []byte) int
TEXT ·scanBasicStringStrictNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R11

	CBZ R1, strict_drained

	MOVD $0x40100401, R5
	VMOV R5, V5.S4
	MOVD $'"', R3
	VMOV R3, V0.B16
	MOVD $'\\', R3
	VMOV R3, V7.B16
	MOVD $0x7f, R3
	VMOV R3, V8.B16
	MOVD $0x1f, R3
	VMOV R3, V9.B16
	MOVD $'\t', R3
	VMOV R3, V10.B16

strict_loop32:
	CMP    $32, R1
	BLT    strict_tail
	VLD1.P (R0), [V1.B16, V2.B16]
	SUB    $32, R1, R1

	// First 16 bytes -> V3 mask.
	VCMEQ V0.B16, V1.B16, V3.B16
	VCMEQ V7.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	VCMEQ V8.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	WORD  $0x6e213d2c               // VCMHS V1.B16, V9.B16, V12.B16 (byte <= 0x1f)
	VCMEQ V10.B16, V1.B16, V13.B16
	WORD  $0x6e2059ad               // VMVN V13.B16, V13.B16
	VAND  V13.B16, V12.B16, V12.B16 // control and not tab
	VORR  V12.B16, V3.B16, V3.B16

	// Second 16 bytes -> V4 mask.
	VCMEQ V0.B16, V2.B16, V4.B16
	VCMEQ V7.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	VCMEQ V8.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	WORD  $0x6e223d2c               // VCMHS V2.B16, V9.B16, V12.B16
	VCMEQ V10.B16, V2.B16, V13.B16
	WORD  $0x6e2059ad               // VMVN V13.B16, V13.B16
	VAND  V13.B16, V12.B16, V12.B16
	VORR  V12.B16, V4.B16, V4.B16

	VORR  V4.B16, V3.B16, V6.B16
	VADDP V6.D2, V6.D2, V6.D2
	VMOV  V6.D[0], R6
	CBNZ  R6, strict_construct
	CBNZ  R1, strict_loop32
	B     strict_drained

strict_construct:
	VAND  V5.B16, V3.B16, V3.B16
	VAND  V5.B16, V4.B16, V4.B16
	VADDP V4.B16, V3.B16, V6.B16
	VADDP V6.B16, V6.B16, V6.B16
	VMOV  V6.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6
	SUB   $32, R0, R0
	ADD   R6>>1, R0, R0
	SUB   R11, R0, R0
	MOVD  R0, ret+24(FP)
	RET

strict_tail:
	CBZ R1, strict_drained

strict_tail_loop:
	MOVBU.P 1(R0), R3
	CMP     $'"', R3
	BEQ     strict_tail_match
	CMP     $'\\', R3
	BEQ     strict_tail_match
	CMP     $0x7f, R3
	BEQ     strict_tail_match
	CMP     $0x20, R3
	BHS     strict_tail_next
	CMP     $'\t', R3
	BEQ     strict_tail_next
	B       strict_tail_match

strict_tail_next:
	SUBS $1, R1, R1
	BNE  strict_tail_loop

strict_drained:
	SUB  R11, R0, R0
	MOVD R0, ret+24(FP)
	RET

strict_tail_match:
	SUB  $1, R0, R0
	SUB  R11, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// scanCommentBodyNEON — first comment body terminator/control byte.
// Stops at LF, CR, DEL, or C0 control except tab; returns len(s) on miss.
// ============================================================================

// func scanCommentBodyNEON(s []byte) int
TEXT ·scanCommentBodyNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R11

	CBZ R1, cmt_drained

	MOVD $0x40100401, R5
	VMOV R5, V5.S4
	MOVD $0x7f, R3
	VMOV R3, V8.B16
	MOVD $0x1f, R3
	VMOV R3, V9.B16
	MOVD $'\t', R3
	VMOV R3, V10.B16

cmt_loop32:
	CMP    $32, R1
	BLT    cmt_tail
	VLD1.P (R0), [V1.B16, V2.B16]
	SUB    $32, R1, R1

	VCMEQ V8.B16, V1.B16, V3.B16
	WORD  $0x6e213d2c               // VCMHS V1.B16, V9.B16, V12.B16
	VCMEQ V10.B16, V1.B16, V13.B16
	WORD  $0x6e2059ad               // VMVN V13.B16, V13.B16
	VAND  V13.B16, V12.B16, V12.B16
	VORR  V12.B16, V3.B16, V3.B16

	VCMEQ V8.B16, V2.B16, V4.B16
	WORD  $0x6e223d2c               // VCMHS V2.B16, V9.B16, V12.B16
	VCMEQ V10.B16, V2.B16, V13.B16
	WORD  $0x6e2059ad               // VMVN V13.B16, V13.B16
	VAND  V13.B16, V12.B16, V12.B16
	VORR  V12.B16, V4.B16, V4.B16

	VORR  V4.B16, V3.B16, V6.B16
	VADDP V6.D2, V6.D2, V6.D2
	VMOV  V6.D[0], R6
	CBNZ  R6, cmt_construct
	CBNZ  R1, cmt_loop32
	B     cmt_drained

cmt_construct:
	VAND  V5.B16, V3.B16, V3.B16
	VAND  V5.B16, V4.B16, V4.B16
	VADDP V4.B16, V3.B16, V6.B16
	VADDP V6.B16, V6.B16, V6.B16
	VMOV  V6.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6
	SUB   $32, R0, R0
	ADD   R6>>1, R0, R0
	SUB   R11, R0, R0
	MOVD  R0, ret+24(FP)
	RET

cmt_tail:
	CBZ R1, cmt_drained

cmt_tail_loop:
	MOVBU.P 1(R0), R3
	CMP     $0x7f, R3
	BEQ     cmt_tail_match
	CMP     $0x20, R3
	BHS     cmt_tail_next
	CMP     $'\t', R3
	BEQ     cmt_tail_next
	B       cmt_tail_match

cmt_tail_next:
	SUBS $1, R1, R1
	BNE  cmt_tail_loop

cmt_drained:
	SUB  R11, R0, R0
	MOVD R0, ret+24(FP)
	RET

cmt_tail_match:
	SUB  $1, R0, R0
	SUB  R11, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// scanBareValueEndNEON — first bare-value delimiter, or len(s) on miss.
// Delimiters: space, tab, CR, LF, comma, ']', '}', '#', '='.
// ============================================================================

// func scanBareValueEndNEON(s []byte) int
TEXT ·scanBareValueEndNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD R0, R11

	CBZ R1, bval_drained

	MOVD $0x40100401, R5
	VMOV R5, V5.S4
	MOVD $' ', R3
	VMOV R3, V0.B16
	MOVD $'\t', R3
	VMOV R3, V7.B16
	MOVD $'\r', R3
	VMOV R3, V8.B16
	MOVD $'\n', R3
	VMOV R3, V9.B16
	MOVD $',', R3
	VMOV R3, V10.B16
	MOVD $']', R3
	VMOV R3, V11.B16
	MOVD $'}', R3
	VMOV R3, V12.B16
	MOVD $'#', R3
	VMOV R3, V13.B16
	MOVD $'=', R3
	VMOV R3, V14.B16

bval_loop32:
	CMP    $32, R1
	BLT    bval_tail
	VLD1.P (R0), [V1.B16, V2.B16]
	SUB    $32, R1, R1

	VCMEQ V0.B16, V1.B16, V3.B16
	VCMEQ V7.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	VCMEQ V8.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	VCMEQ V9.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	VCMEQ V10.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	VCMEQ V11.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	VCMEQ V12.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	VCMEQ V13.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16
	VCMEQ V14.B16, V1.B16, V6.B16
	VORR  V6.B16, V3.B16, V3.B16

	VCMEQ V0.B16, V2.B16, V4.B16
	VCMEQ V7.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	VCMEQ V8.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	VCMEQ V9.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	VCMEQ V10.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	VCMEQ V11.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	VCMEQ V12.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	VCMEQ V13.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16
	VCMEQ V14.B16, V2.B16, V6.B16
	VORR  V6.B16, V4.B16, V4.B16

	VORR  V4.B16, V3.B16, V6.B16
	VADDP V6.D2, V6.D2, V6.D2
	VMOV  V6.D[0], R6
	CBNZ  R6, bval_construct
	CBNZ  R1, bval_loop32
	B     bval_drained

bval_construct:
	VAND  V5.B16, V3.B16, V3.B16
	VAND  V5.B16, V4.B16, V4.B16
	VADDP V4.B16, V3.B16, V6.B16
	VADDP V6.B16, V6.B16, V6.B16
	VMOV  V6.D[0], R6
	RBIT  R6, R6
	CLZ   R6, R6
	SUB   $32, R0, R0
	ADD   R6>>1, R0, R0
	SUB   R11, R0, R0
	MOVD  R0, ret+24(FP)
	RET

bval_tail:
	CBZ R1, bval_drained

bval_tail_loop:
	MOVBU.P 1(R0), R3
	CMP     $' ', R3
	BEQ     bval_tail_match
	CMP     $'\t', R3
	BEQ     bval_tail_match
	CMP     $'\r', R3
	BEQ     bval_tail_match
	CMP     $'\n', R3
	BEQ     bval_tail_match
	CMP     $',', R3
	BEQ     bval_tail_match
	CMP     $']', R3
	BEQ     bval_tail_match
	CMP     $'}', R3
	BEQ     bval_tail_match
	CMP     $'#', R3
	BEQ     bval_tail_match
	CMP     $'=', R3
	BEQ     bval_tail_match
	SUBS    $1, R1, R1
	BNE     bval_tail_loop

bval_drained:
	SUB  R11, R0, R0
	MOVD R0, ret+24(FP)
	RET

bval_tail_match:
	SUB  $1, R0, R0
	SUB  R11, R0, R0
	MOVD R0, ret+24(FP)
	RET

// ============================================================================
// countLinesNEON — count LF bytes. Mirrors internal/bytealg.Count's 32-byte
// vector reduction with the needle hard-coded to '\n'.
// ============================================================================

// func countLinesNEON(s []byte) int
TEXT ·countLinesNEON(SB), NOSPLIT, $0-32
	MOVD s_base+0(FP), R0
	MOVD s_len+8(FP), R1
	MOVD $0, R11
	CBZ  R1, cnt_done
	CMP  $0x20, R1
	BHS  cnt_head

cnt_tail:
	MOVBU.P 1(R0), R5
	SUB     $1, R1, R1
	CMP     $'\n', R5
	CINC    EQ, R11, R11
	CBNZ    R1, cnt_tail

cnt_done:
	MOVD R11, ret+24(FP)
	RET

cnt_head:
	ANDS $0x1f, R0, R9
	BEQ  cnt_chunk
	BIC  $0x1f, R0, R3
	ADD  $0x20, R3

cnt_head_loop:
	MOVBU.P 1(R0), R5
	CMP     $'\n', R5
	CINC    EQ, R11, R11
	SUB     $1, R1, R1
	CMP     R0, R3
	BNE     cnt_head_loop

cnt_chunk:
	BIC  $0x1f, R1, R9
	CBZ  R9, cnt_tail
	ADD  R0, R9, R3
	MOVD $1, R5
	VMOV R5, V5.B16
	MOVD $'\n', R2
	VMOV R2, V0.B16
	SUB  R9, R1, R1
	VEOR V7.B8, V7.B8, V7.B8
	VEOR V8.B8, V8.B8, V8.B8

cnt_chunk_loop:
	VLD1.P  (R0), [V1.B16, V2.B16]
	CMP     R0, R3
	VCMEQ   V0.B16, V1.B16, V3.B16
	VCMEQ   V0.B16, V2.B16, V4.B16
	VAND    V5.B16, V3.B16, V3.B16
	VAND    V5.B16, V4.B16, V4.B16
	VADDP   V4.B16, V3.B16, V6.B16
	VUADDLV V6.B16, V7
	VADD    V7, V8
	BNE     cnt_chunk_loop
	VMOV    V8.D[0], R6
	ADD     R6, R11, R11
	CBZ     R1, cnt_done
	B       cnt_tail
