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

// TestTextBuilder_KeyAppendCut (B40, ADR-0030): ключ жирным с двоеточием, склейка размеченных текстов со сдвигом
// смещений, вырезка отрезка с обрезкой пересекающих границу выделений.
func TestTextBuilder_KeyAppendCut(t *testing.T) {
	t.Parallel()
	var b TextBuilder
	b.Key("Пол")
	b.WriteString("женский\n")
	var tail TextBuilder
	tail.Key("HP")
	tail.WriteString("9")
	b.Append(tail.Build())
	got := b.Build()
	want := FormattedText{Text: "Пол: женский\nHP: 9", Entities: []TextEntity{{Kind: EntityBold, Offset: 0, Length: 3}, {Kind: EntityBold, Offset: 13, Length: 2}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("built = %+v, want %+v", got, want)
	}
	if b.Len() != 18 {
		t.Fatalf("Len = %d", b.Len())
	}
	cut := got.Cut(2, 15)
	wantCut := FormattedText{Text: "л: женский\nHP", Entities: []TextEntity{{Kind: EntityBold, Offset: 0, Length: 1}, {Kind: EntityBold, Offset: 11, Length: 2}}}
	if !reflect.DeepEqual(cut, wantCut) {
		t.Fatalf("cut = %+v, want %+v", cut, wantCut)
	}
	if empty := got.Cut(10, 5); empty.Text != "" || empty.Entities != nil {
		t.Fatalf("empty cut = %+v", empty)
	}
	if whole := got.Cut(-3, 100); !reflect.DeepEqual(whole, want) {
		t.Fatalf("whole cut = %+v", whole)
	}
}
