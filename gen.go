// +build generate

//go:generate go run gen.go

package main

import (
	"bufio"
	"os"
	"os/exec"
)

func stage1_preprocessing(STANDALONE bool) string {

	code := `
//+build !noasm !appengine

#include "common.h"
`

	if STANDALONE {
		code += `
#define UNPACK_BITMASK(_R1, _XR1, _YR1) \
	\ // source: https://stackoverflow.com/a/24242696
	VMOVQ        _R1, _XR1                            \
	VPBROADCASTD _XR1, _YR1                           \
	VPSHUFB      Y_SHUFMASK, _YR1, _YR1               \
	VPANDN       Y_ANDMASK, _YR1, _YR1                \
	VPCMPEQB     Y_ZERO, _YR1, _YR1                   \
`
	}

	code += `
#define ADD_TRAILING_NEWLINE \
	MOVQ $1, AX \
	SHLQ CX, AX \ // only lower 6 bits are taken into account, which is good for current and next YMM words
	ORQ  AX, BX
`
	code += `
// See stage1Input struct
#define QUOTE_MASK_IN           0
#define SEPARATOR_MASK_IN       8
#define CARRIAGE_RETURN_MASK_IN 16
#define QUOTE_MASK_IN_NEXT      24
#define QUOTED                  32
#define NEWLINE_MASK_IN         40
#define NEWLINE_MASK_IN_NEXT    48

// See stage1Output struct
#define QUOTE_MASK_OUT            0
#define SEPARATOR_MASK_OUT        8
#define CARRIAGE_RETURN_MASK_OUT  16
#define NEEDS_POST_PROCESSING_OUT 24
`
	code  += `
#define Y_ANDMASK     Y15
#define Y_SHUFMASK    Y14
#define Y_ZERO        Y13
#define Y_PREPROC_SEP Y12
#define Y_PREPROC_QUO Y11
#define Y_PREPROC_NWL Y10
`
	code += `
#define Y_QUOTE_CHAR  Y5
#define Y_SEPARATOR   Y4
#define Y_CARRIAGE_R  Y3
#define Y_NEWLINE     Y2
`
	code += `
// func stage1_preprocess_buffer(buf []byte, separatorChar uint64, input *stage1Input, output *stage1Output)
TEXT ·stage1_preprocess_buffer(SB), 7, $0
`
	code += `
	LEAQ         ANDMASK<>(SB), AX
	VMOVDQU      (AX), Y_ANDMASK
	LEAQ         SHUFMASK<>(SB), AX
	VMOVDQU      (AX), Y_SHUFMASK
	VPXOR        Y_ZERO, Y_ZERO, Y_ZERO
	MOVQ         $0x2, AX               // preprocessedSeparator
	MOVQ         AX, X12
	VPBROADCASTB X12, Y_PREPROC_SEP
	MOVQ         $0x3, AX               // preprocessedQuote
	MOVQ         AX, X11
	VPBROADCASTB X11, Y_PREPROC_QUO
	MOVQ         $0x0a, AX              // new line
	MOVQ         AX, X10
	VPBROADCASTB X10, Y_PREPROC_NWL
`
	code += `
	MOVQ         $0x0a, AX                // character for newline
	MOVQ         AX, X2
	VPBROADCASTB X2, Y_NEWLINE
	MOVQ         $0x0d, AX                // character for carriage return
	MOVQ         AX, X3
	VPBROADCASTB X3, Y_CARRIAGE_R
	MOVQ         separatorChar+24(FP), AX // get character for separator
	MOVQ         AX, X4
	VPBROADCASTB X4, Y_SEPARATOR
	MOVQ         $0x22, AX                // character for quote
	MOVQ         AX, X5
	VPBROADCASTB X5, Y_QUOTE_CHAR
`
	code += `
	MOVQ buf+0(FP), DI
	MOVQ offset+56(FP), DX

	MOVQ DX, CX
	ADDQ $0x40, CX
	CMPQ CX, buf_len+8(FP)
	JLE  fullLoadPrologue
	MOVQ buf_len+8(FP), BX
	CALL ·partialLoad(SB)
	JMP  skipFullLoadPrologue

fullLoadPrologue:
	VMOVDQU (DI)(DX*1), Y6     // load low 32-bytes
	VMOVDQU 0x20(DI)(DX*1), Y7 // load high 32-bytes

skipFullLoadPrologue:
`
	code += `
	MOVQ input1+32(FP), SI

	// quote mask
	VPCMPEQB Y6, Y_QUOTE_CHAR, Y0
	VPCMPEQB Y7, Y_QUOTE_CHAR, Y1
	CREATE_MASK(Y0, Y1, AX, CX)
	MOVQ     CX, QUOTE_MASK_IN_NEXT(SI) // store in next slot, so that it gets copied back

	// newline
	VPCMPEQB Y6, Y_NEWLINE, Y0
	VPCMPEQB Y7, Y_NEWLINE, Y1
	CREATE_MASK(Y0, Y1, AX, BX)
`
	code += `
	MOVQ buf_len+8(FP), CX
	CMPQ CX, $64
	JGE  skipAddTrailingNewlinePrologue
	ADD_TRAILING_NEWLINE

skipAddTrailingNewlinePrologue:
	MOVQ BX, NEWLINE_MASK_IN_NEXT(SI) // store in next slot, so that it gets copied back
`
	code += `
loop:
	VMOVDQU Y6, Y8 // get low 32-bytes
	VMOVDQU Y7, Y9 // get high 32-bytes

	MOVQ input1+32(FP), SI

	// copy next masks to current slot (for quote mask and newline mask)
	MOVQ QUOTE_MASK_IN_NEXT(SI), CX
	MOVQ CX, QUOTE_MASK_IN(SI)
	MOVQ NEWLINE_MASK_IN_NEXT(SI), CX
	MOVQ CX, NEWLINE_MASK_IN(SI)

	// separator mask
	VPCMPEQB Y8, Y_SEPARATOR, Y0
	VPCMPEQB Y9, Y_SEPARATOR, Y1
	CREATE_MASK(Y0, Y1, AX, CX)
	MOVQ     CX, SEPARATOR_MASK_IN(SI)

	// carriage return
	VPCMPEQB Y8, Y_CARRIAGE_R, Y0
	VPCMPEQB Y9, Y_CARRIAGE_R, Y1
	CREATE_MASK(Y0, Y1, AX, CX)
	MOVQ     CX, CARRIAGE_RETURN_MASK_IN(SI)

	// do we need to do a partial load?
	MOVQ DX, CX
	ADDQ $0x80, CX
	CMPQ CX, buf_len+8(FP)
	JLE  fullLoad
	MOVQ buf_len+8(FP), BX
	CALL ·partialLoad(SB)
	JMP  skipFullLoad

fullLoad:
	// load next pair of YMM words
	VMOVDQU 0x40(DI)(DX*1), Y6 // load low 32-bytes of next pair
	VMOVDQU 0x60(DI)(DX*1), Y7 // load high 32-bytes of next pair

skipFullLoad:
	VPCMPEQB Y6, Y_QUOTE_CHAR, Y0
	VPCMPEQB Y7, Y_QUOTE_CHAR, Y1
	CREATE_MASK(Y0, Y1, AX, CX)
	MOVQ     CX, QUOTE_MASK_IN_NEXT(SI)

	// quote mask next for next YMM word
	VPCMPEQB Y6, Y_NEWLINE, Y0
	VPCMPEQB Y7, Y_NEWLINE, Y1
	CREATE_MASK(Y0, Y1, AX, BX)

	MOVQ buf_len+8(FP), CX
	SUBQ DX, CX
	JLT  skipAddTrailingNewline
	ADD_TRAILING_NEWLINE

skipAddTrailingNewline:
	MOVQ BX, NEWLINE_MASK_IN_NEXT(SI)

	PUSHQ DX
	MOVQ  input1+32(FP), AX
	MOVQ  output1+40(FP), R10
	CALL  ·stage1_preprocess(SB)
	POPQ  DX
`
	code += `
	MOVQ output1+40(FP), R10

	// Replace quotes
	MOVQ      QUOTE_MASK_OUT(R10), AX
	UNPACK_BITMASK(AX, X0, Y0)
	SHRQ      $32, AX
	UNPACK_BITMASK(AX, X1, Y1)
	VPBLENDVB Y0, Y_PREPROC_QUO, Y8, Y8
	VPBLENDVB Y1, Y_PREPROC_QUO, Y9, Y9

	// Replace separators
	MOVQ      SEPARATOR_MASK_OUT(R10), AX
	UNPACK_BITMASK(AX, X0, Y0)
	SHRQ      $32, AX
	UNPACK_BITMASK(AX, X1, Y1)
	VPBLENDVB Y0, Y_PREPROC_SEP, Y8, Y8
	VPBLENDVB Y1, Y_PREPROC_SEP, Y9, Y9

	// Replace carriage returns
	MOVQ      CARRIAGE_RETURN_MASK_OUT(R10), AX
	UNPACK_BITMASK(AX, X0, Y0)
	SHRQ      $32, AX
	UNPACK_BITMASK(AX, X1, Y1)
	VPBLENDVB Y0, Y_PREPROC_NWL, Y8, Y8
	VPBLENDVB Y1, Y_PREPROC_NWL, Y9, Y9

	// Store updated result
	MOVQ    buf+0(FP), DI
	VMOVDQU Y8, (DI)(DX*1)
	VMOVDQU Y9, 0x20(DI)(DX*1)
`
	code += `
	MOVQ output1+40(FP), R10
	CMPQ NEEDS_POST_PROCESSING_OUT(R10), $1
	JNZ  unmodified

	MOVQ postProc+48(FP), AX
	MOVQ 0(AX), BX
	MOVQ 8(AX), CX
	MOVQ DX, (BX)(CX*8)
	INCQ 8(AX)
	INCQ CX
	ADDQ $0x40, DX
	CMPQ CX, 16(AX)          // slice is full?
	JGE  exit
	SUBQ $0x40, DX

unmodified:
`
	code += `
	ADDQ $0x40, DX
	CMPQ DX, buf_len+8(FP)
	JLT  loop

exit:
	VZEROUPPER
	MOVQ DX, processed+64(FP)
	RET
`
	return code
}

func partialLoad() string {

	code := `
// CX = base for loading
// BX = buf_len+8(FP)
TEXT ·partialLoad(SB), 7, $0
	VPXOR Y6, Y6, Y6 // clear lower 32-bytes
	VPXOR Y7, Y7, Y7 // clear upper 32-bytes

	SUBQ $0x40, CX

	// check whether we need to load at all?
	CMPQ CX, BX
	JGT  partialLoadDone

	// do a partial load and mask out bytes after the end of the message with whitespace
	VMOVDQU (DI)(CX*1), Y6 // always load low 32-bytes

	ANDQ $0x3f, BX
	CMPQ BX, $0x20
	JGE  maskingHigh

	// perform masking on low 32-bytes
	MASK_TRAILING_BYTES(0x1f, AX, CX, BX, Y0, Y6)
	RET

maskingHigh:
	VMOVDQU 0x20(DI)(CX*1), Y7 // load high 32-bytes
	MASK_TRAILING_BYTES(0x3f, AX, CX, BX, Y0, Y7)

partialLoadDone:
	RET
`
	return code
}

func unpackTables() string {
	code := `
DATA SHUFMASK<>+0x000(SB)/8, $0x0000000000000000
DATA SHUFMASK<>+0x008(SB)/8, $0x0101010101010101
DATA SHUFMASK<>+0x010(SB)/8, $0x0202020202020202
DATA SHUFMASK<>+0x018(SB)/8, $0x0303030303030303
GLOBL SHUFMASK<>(SB), 8, $32

DATA ANDMASK<>+0x000(SB)/8, $0x8040201008040201
DATA ANDMASK<>+0x008(SB)/8, $0x8040201008040201
DATA ANDMASK<>+0x010(SB)/8, $0x8040201008040201
DATA ANDMASK<>+0x018(SB)/8, $0x8040201008040201
GLOBL ANDMASK<>(SB), 8, $32
`
	return code
}

func main() {

	const asmfile = "stages_amd64.asm"

	{
		f, err := os.Create(asmfile)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		w := bufio.NewWriter(f)
		defer w.Flush()
		w.WriteString(`// Code generated by command: go generate ` + os.Getenv("GOFILE") + `. DO NOT EDIT.` + "\n")
		w.WriteString(stage1_preprocessing(true))
		w.WriteString("\n")
		w.WriteString(partialLoad())
		w.WriteString("\n")
		w.WriteString(unpackTables())
	}

	cmd := exec.Command("asmfmt", "-w", asmfile)
	_, err := cmd.CombinedOutput()
	if err != nil {
		panic(err)
	}
}