package wanderer

import "testing"

func TestSystemCategory(t *testing.T) {
	cases := map[int]string{
		1: "WH", 3: "WH", 6: "WH", // C1-C6
		7: "HS", 8: "LS", 9: "NS",
		12: "WH", 13: "WH", 15: "WH", // Thera, C13, drifter
		25: "WH", // Pochven
	}
	for class, want := range cases {
		if got := SystemCategory(class); got != want {
			t.Errorf("SystemCategory(%d) = %q, want %q", class, got, want)
		}
	}
}

func TestFormatConnection(t *testing.T) {
	wh := systemInfo{ID: 31001234, Name: "J100809", SystemClass: 3, Security: "-1.0", ClassTitle: "C3"}
	got := formatConnection("J111011", wh)
	want := "New connection to J111011\nC3 [J100809](https://anoik.is/systems/J100809) - [zkillboard link](https://zkillboard.com/system/31001234/)"
	if got != want {
		t.Fatalf("wormhole message mismatch:\n got: %s\nwant: %s", got, want)
	}

	hs := systemInfo{ID: 30000142, Name: "Amarr", SystemClass: 7, Security: "1.0", ClassTitle: "High-sec"}
	got = formatConnection("J111011", hs)
	want = "New connection to J111011\n1.0 [Amarr](https://evemaps.dotlan.net/system/Amarr) - [zkillboard link](https://zkillboard.com/system/30000142/)"
	if got != want {
		t.Fatalf("k-space line mismatch:\n got: %s\nwant: %s", got, want)
	}
}
