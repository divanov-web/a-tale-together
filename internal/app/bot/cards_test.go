package bot

import (
	"reflect"
	"testing"

	"taletogether/internal/model"
)

// TestCharacterCard — эталон карточки (B40): одно поле на строку, ключи жирным, строка без ключа — заголовок, строка с
// пустым значением пропускается, без завершающего перевода строки.
func TestCharacterCard(t *testing.T) {
	t.Parallel()
	got := characterCard(cardRows{{"", "Элис — Воин"}, {"Пол", "женский"}, {"Внешность", ""}, {"HP", "9"}})
	want := model.FormattedText{Text: "Элис — Воин\nПол: женский\nHP: 9", Entities: []model.TextEntity{{Kind: model.EntityBold, Offset: 12, Length: 3}, {Kind: model.EntityBold, Offset: 25, Length: 2}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("card = %+v, want %+v", got, want)
	}
	if empty := characterCard(nil); empty.Text != "" || empty.Entities != nil {
		t.Fatalf("empty card = %+v", empty)
	}
}
