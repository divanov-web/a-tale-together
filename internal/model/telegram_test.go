package model

import "testing"

func TestParseCallbackData(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, data, command, args, step, text string
	}{
		{"command", CallbackCommand("act", "#3:1:well:0"), "act", "#3:1:well:0", "", "/act #3:1:well:0"},
		{"command without args", CallbackCommand("cancel", ""), "cancel", "", "", "/cancel"},
		{"command upper", "/Begin yes", "begin", "yes", "", "/Begin yes"},
		{"dialog answer", CallbackAnswer("faction", "2"), "", "", "faction", "2"},
		{"plain text", "привет", "", "", "", "привет"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			command, args, step, text := ParseCallbackData(c.data)
			if command != c.command || args != c.args || step != c.step || text != c.text {
				t.Fatalf("ParseCallbackData(%q) = %q %q %q %q", c.data, command, args, step, text)
			}
		})
	}
}
