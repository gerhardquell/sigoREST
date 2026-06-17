//**********************************************************************
//      sigoengine/memory.go
//**********************************************************************
//  Beschreibung: Gemeinsamer MemoryBlock-Typ für Server und CLI
//**********************************************************************

package sigoengine

// MemoryBlock represents a global or per-channel memory/context block.
type MemoryBlock struct {
	Content string `json:"content"`
	Cache   bool   `json:"cache"`
}
