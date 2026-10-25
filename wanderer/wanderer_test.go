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
	want := "New connection to J111011 ⇢ C3 [J100809](https://zkillboard.com/system/31001234/)"
	if got != want {
		t.Fatalf("wormhole message mismatch:\n got: %s\nwant: %s", got, want)
	}

	hs := systemInfo{ID: 30000142, Name: "Amarr", SystemClass: 7, Security: "1.0", ClassTitle: "High-sec"}
	got = formatConnection("J111011", hs)
	want = "New connection to J111011 ⇢ 1.0 [Amarr](https://zkillboard.com/system/30000142/)"
	if got != want {
		t.Fatalf("k-space line mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestRouteSuffix(t *testing.T) {
	hs := systemInfo{ID: 30002541, Name: "Todaki", SystemClass: 7, Security: "0.6", ClassTitle: "High-sec", Region: "The Forge"}
	ls := systemInfo{ID: 30002540, Name: "Jark", SystemClass: 8, Security: "0.3", ClassTitle: "Low-sec", Region: "Molden Heath"}

	// suffix is highsec-only (0.5+)
	if got := routeSuffix(ls, 4, 5); got != "" {
		t.Fatalf("lowsec should have no suffix, got %q", got)
	}
	bad := hs
	bad.Security = "-1.0"
	if got := routeSuffix(bad, 4, 5); got != "" {
		t.Fatalf("wormhole should have no suffix, got %q", got)
	}
	bad.Security = "not-a-number"
	if got := routeSuffix(bad, 4, 5); got != "" {
		t.Fatalf("unparseable security should have no suffix, got %q", got)
	}

	if got := routeSuffix(hs, 4, 5); got != " - The Forge\n4 jumps to Jita" {
		t.Fatalf("jita closer mismatch: %q", got)
	}
	if got := routeSuffix(hs, 4, 2); got != " - The Forge\n2 jumps to Amarr" {
		t.Fatalf("amarr closer mismatch: %q", got)
	}
	if got := routeSuffix(hs, -1, 6); got != " - The Forge\n6 jumps to Amarr" {
		t.Fatalf("amarr fallback mismatch: %q", got)
	}
	if got := routeSuffix(hs, 3, -1); got != " - The Forge\n3 jumps to Jita" {
		t.Fatalf("jita fallback mismatch: %q", got)
	}
	if got := routeSuffix(hs, -1, -1); got != " - The Forge\nno safe route to Jita/Amarr" {
		t.Fatalf("no-route mismatch: %q", got)
	}
}
