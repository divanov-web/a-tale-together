package model

import (
	"reflect"
	"testing"
)

// TestTextBuilder (ADR-0029): смещения отрезков считаются в рунах, пустой жирный отрезок не добавляется, обычный текст
// разметки не несёт.
func TestTextBuilder(t *testing.T) {
	t.Parallel()
	var b TextBuilder
	b.Printf("Ёж %d: ", 1)
	b.Bold("на север")
	b.WriteString(", ")
	b.Bold("")
	b.Bold("юг")
	got := b.Build()
	want := FormattedText{Text: "Ёж 1: на север, юг", Entities: []TextEntity{{Kind: EntityBold, Offset: 6, Length: 8}, {Kind: EntityBold, Offset: 16, Length: 2}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("built = %+v, want %+v", got, want)
	}
	if p := Plain("x"); p.Text != "x" || p.Entities != nil {
		t.Fatalf("plain = %+v", p)
	}
}
