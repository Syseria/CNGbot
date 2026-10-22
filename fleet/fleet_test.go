package fleet

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsJSpace(t *testing.T) {
	cases := map[int]bool{
		31000513: true,  // wormhole space
		30000142: false, // highsec (Jita)
		32000010: false, // Pochven (triglavian)
		0:        false,
		30999999: false,
	}
	for sysID, want := range cases {
		if got := isJSpace(sysID); got != want {
			t.Errorf("isJSpace(%d) = %v, want %v", sysID, got, want)
		}
	}
}

func TestGetFleetMembers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"character_id": 42, "role": "fleet_commander", "solar_system_id": 31000513},
			{"character_id": 99, "role": "fleet_member", "solar_system_id": 30000142}
		]`))
	}))
	defer server.Close()

	origBase := esiBase
	esiBase = server.URL
	defer func() { esiBase = origBase }()

	members, err := getFleetMembers(http.DefaultClient, 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	boss, ok := members[42]
	if !ok {
		t.Fatal("expected member with character_id 42")
	}
	if boss.Role != "fleet_commander" || boss.SolarSystemID != 31000513 {
		t.Errorf("unexpected member 42: %+v", boss)
	}
	member, ok := members[99]
	if !ok {
		t.Fatal("expected member with character_id 99")
	}
	if member.Role != "fleet_member" || member.SolarSystemID != 30000142 {
		t.Errorf("unexpected member 99: %+v", member)
	}
}

func TestSendInvite(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	origBase := esiBase
	esiBase = server.URL
	defer func() { esiBase = origBase }()

	if err := sendInvite(http.DefaultClient, 12345, 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotBody, `"character_id":42`) {
		t.Errorf("expected body to contain character_id 42, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"role":"fleet_member"`) {
		t.Errorf("expected body to contain role fleet_member, got: %s", gotBody)
	}

	rejectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer rejectServer.Close()
	esiBase = rejectServer.URL

	if err := sendInvite(http.DefaultClient, 12345, 42); err == nil {
		t.Error("expected error for 403 response, got nil")
	}
}

func TestFilterByAlliance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"character_id":1,"alliance_id":99000001},{"character_id":2,"alliance_id":99000002}]`))
	}))
	defer server.Close()

	origURL := affiliationURL
	affiliationURL = server.URL
	defer func() { affiliationURL = origURL }()

	got, err := filterByAlliance(map[int]bool{1: true, 2: true, 3: true}, 99000001)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !got[1] {
		t.Errorf("expected exactly {1: true}, got %v", got)
	}

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errorServer.Close()
	affiliationURL = errorServer.URL

	if _, err := filterByAlliance(map[int]bool{1: true}, 99000001); err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}
