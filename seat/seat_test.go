package seat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestFetchActiveUserCharacterIDs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/users" {
			http.NotFound(w, r)
			return
		}

		page := r.URL.Query().Get("page")
		w.Header().Set("Content-Type", "application/json")

		if page == "" || page == "1" {
			resp := SeatUsersResponse{
				Data: []SeatUser{
					{
						ID:                     1,
						Name:                   "Active User 1",
						Active:                 true,
						MainCharacterID:        1001,
						AssociatedCharacterIDs: []int{1001, 1002},
					},
					{
						ID:                     2,
						Name:                   "Inactive User",
						Active:                 false,
						MainCharacterID:        2001,
						AssociatedCharacterIDs: []int{2001},
					},
				},
			}
			resp.Meta.CurrentPage = 1
			resp.Meta.LastPage = 2
			resp.Links.Next = "/api/v2/users?page=2"
			_ = json.NewEncoder(w).Encode(resp)
			return
		} else if page == "2" {
			resp := SeatUsersResponse{
				Data: []SeatUser{
					{
						ID:                     3,
						Name:                   "Active User 2",
						Active:                 true,
						MainCharacterID:        3001,
						AssociatedCharacterIDs: []int{3001, 3002},
					},
				},
			}
			resp.Meta.CurrentPage = 2
			resp.Meta.LastPage = 2
			resp.Links.Next = ""
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
	}))
	defer server.Close()

	chars, err := FetchActiveUserCharacterIDs(server.URL, "key")
	if err != nil {
		t.Fatalf("FetchActiveUserCharacterIDs failed: %v", err)
	}

	expectedActive := []int{1001, 1002, 3001, 3002}
	for _, id := range expectedActive {
		if !chars[id] {
			t.Errorf("expected character %d to be active and valid", id)
		}
	}

	if chars[2001] {
		t.Errorf("character 2001 belongs to inactive user, should not be valid")
	}
}

func TestGetValidSeatCharacters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v2/users" {
			resp := SeatUsersResponse{
				Data: []SeatUser{
					{
						ID:                     1,
						Active:                 true,
						MainCharacterID:        9999,
						AssociatedCharacterIDs: []int{9999},
					},
				},
			}
			resp.Meta.CurrentPage = 1
			resp.Meta.LastPage = 1
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	os.Setenv("SEAT_URL", server.URL)
	os.Setenv("SEAT_API", "test-token")
	defer func() {
		os.Unsetenv("SEAT_URL")
		os.Unsetenv("SEAT_API")
	}()

	chars, err := GetValidSeatCharacters("")
	if err != nil {
		t.Fatalf("GetValidSeatCharacters failed: %v", err)
	}
	if !chars[9999] {
		t.Errorf("expected character 9999 to be valid in SeAT")
	}
}

func TestGetSeatConfigNotConfigured(t *testing.T) {
	os.Unsetenv("SEAT_URL")
	os.Unsetenv("SEAT_API")
	os.Unsetenv("SEAT_API_KEY")

	_, _, err := GetSeatConfig("")
	if err == nil {
		t.Fatal("expected error when SeAT is not configured")
	}
	if err.Error() != "SeAT is not configured (missing SEAT_URL or SEAT_API)" {
		t.Errorf("unexpected error: %v", err)
	}
}
