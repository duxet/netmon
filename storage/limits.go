package storage

import (
	"fmt"
	"strings"
)

// Limits bound the resources the database uses. Their zero values leave
// DuckDB's defaults: 80% of the machine's memory, and a thread per core.
type Limits struct {
	// Memory is an amount such as 256MB or 1GiB, or a share of the memory
	// such as 50%.
	Memory  string
	Threads int
}

// Statements returns the statements that set the limits, which apply to the
// whole database, whichever connection runs them.
func (l Limits) Statements() []string {
	memory := "RESET memory_limit"
	if l.Memory != "" {
		memory = "SET memory_limit = '" + strings.ReplaceAll(strings.TrimSpace(l.Memory), "'", "''") + "'"
	}
	threads := "RESET threads"
	if l.Threads > 0 {
		threads = fmt.Sprintf("SET threads = %d", l.Threads)
	}
	return []string{memory, threads}
}
