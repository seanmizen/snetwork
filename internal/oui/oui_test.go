package oui

import "testing"

func TestLookup(t *testing.T) {
	cases := map[string]string{
		"0c:4d:e9:00:00:01": "Apple",
		"0C-4D-E9-00-00-01": "Apple",
		"7c:39:53:d0:ed:67": "zte",
		// an MA-S block (36 bits) names its owner, not the MA-L holder
		"00:1b:c5:00:00:01": "Converging Systems",
		"ff:ff:ff:00:00:00": "",
		"":                  "",
	}
	for mac, want := range cases {
		if got := Lookup(mac); got != want {
			t.Errorf("Lookup(%q) = %q, want %q", mac, got, want)
		}
	}
	if n := Size(); n < 50000 {
		t.Errorf("table has %d blocks; the registry has over 50,000", n)
	}
}
