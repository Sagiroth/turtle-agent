// Package opcode is the 1.12.1 world opcode table, generated from the core's
// src/game/Protocol/Opcodes_1_12_1.h by tools/opcodegen (make opcodes).
package opcode

import "fmt"

// Name returns the core's name for op, or UNKNOWN_0x### for gaps and values past the table.
func Name(op uint16) string {
	if n, ok := names[op]; ok {
		return n
	}
	return fmt.Sprintf("UNKNOWN_0x%03X", op)
}
