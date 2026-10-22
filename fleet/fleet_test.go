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
		if r.URL.Path == "/fleets/12345/wings/" {
			_, _ = w.Write([]byte(`[{"id":7,"name":"Wing 1","squads":[{"id":9,"name":"Squad 1"}]}]`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	origBase := esiBase
	esiBase = server.URL
	defer func() { esiBase = origBase }()

	if err := sendInvite(http.DefaultClient, 12345, 7, 9, 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{`"character_id":42`, `"role":"squad_member"`, `"wing_id":7`, `"squad_id":9`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("expected body to contain %s, got: %s", want, gotBody)
		}
	}

	rejectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Character is not eligible for the fleet"}`))
	}))
	defer rejectServer.Close()
	esiBase = rejectServer.URL

	err := sendInvite(http.DefaultClient, 12345, 7, 9, 42)
	if err == nil {
		t.Error("expected error for 400 response, got nil")
	} else if !strings.Contains(err.Error(), "Character is not eligible for the fleet") {
		t.Errorf("expected error to include the ESI error body, got: %v", err)
	}
}

func TestGetFleetSquad(t *testing.T) {
	origBase := esiBase
	defer func() { esiBase = origBase }()

	t.Run("existing squad", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/fleets/12345/wings/" {
				t.Errorf("unexpected path %q", r.URL.Path)
			}
			_, _ = w.Write([]byte(`[{"id":7,"name":"Wing 1","squads":[{"id":9,"name":"Squad 1"}]}]`))
		}))
		defer server.Close()
		esiBase = server.URL

		wingID, squadID, err := getFleetSquad(http.DefaultClient, 12345)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wingID != 7 || squadID != 9 {
			t.Errorf("expected wing 7 squad 9, got %d/%d", wingID, squadID)
		}
	})

	t.Run("creates squad when wing has none", func(t *testing.T) {
		var created bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/fleets/12345/wings/":
				if created {
					_, _ = w.Write([]byte(`[{"id":7,"name":"Wing 1","squads":[{"id":11,"name":"Squad 2"}]}]`))
				} else {
					_, _ = w.Write([]byte(`[{"id":7,"name":"Wing 1","squads":[]}]`))
				}
			case "/fleets/12345/wings/7/squads/":
				created = true
				w.WriteHeader(http.StatusOK)
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
			}
		}))
		defer server.Close()
		esiBase = server.URL

		wingID, squadID, err := getFleetSquad(http.DefaultClient, 12345)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wingID != 7 || squadID != 11 {
			t.Errorf("expected wing 7 squad 11, got %d/%d", wingID, squadID)
		}
	})

	t.Run("wings error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()
		esiBase = server.URL

		if _, _, err := getFleetSquad(http.DefaultClient, 12345); err == nil {
			t.Error("expected error for 403 response, got nil")
		}
	})
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

func TestFetchESILocation(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"ok", http.StatusOK, `{"solar_system_id":31000513}`, 31000513},
		{"offline", http.StatusNotFound, `{"error":"Character not found"}`, 0},
		{"server error", http.StatusInternalServerError, `{}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/characters/12345/location/" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			origBase := esiBase
			esiBase = server.URL
			defer func() { esiBase = origBase }()

			got, err := fetchESILocation(server.Client(), 12345)
			if tc.status == http.StatusInternalServerError {
				if err == nil {
					t.Fatal("expected error for 500 response, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected solar system %d, got %d", tc.want, got)
			}
		})
	}
}
