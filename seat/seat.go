package seat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"evemaildiscord/config"
	"evemaildiscord/db"
)

var httpClient = &http.Client{
	Timeout: 15 * time.Second,
}

type SeatUser struct {
	ID                     int    `json:"id"`
	Name                   string `json:"name"`
	Email                  string `json:"email"`
	Active                 bool   `json:"active"`
	LastLogin              string `json:"last_login"`
	LastLoginSource        string `json:"last_login_source"`
	AssociatedCharacterIDs []int  `json:"associated_character_ids"`
	MainCharacterID        int    `json:"main_character_id"`
}

type SeatUsersResponse struct {
	Data  []SeatUser `json:"data"`
	Links struct {
		First string `json:"first"`
		Last  string `json:"last"`
		Prev  string `json:"prev"`
		Next  string `json:"next"`
	} `json:"links"`
	Meta struct {
		CurrentPage int    `json:"current_page"`
		From        int    `json:"from"`
		LastPage    int    `json:"last_page"`
		Path        string `json:"path"`
		PerPage     int    `json:"per_page"`
		To          int    `json:"to"`
		Total       int    `json:"total"`
	} `json:"meta"`
}

func FetchActiveUserCharacterIDs(baseURL, apiKey string) (map[int]bool, error) {
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("SeAT URL or API key is not configured")
	}

	validChars := make(map[int]bool)
	baseURLTrimmed := strings.TrimRight(baseURL, "/")

	page := 1
	for {
		url := fmt.Sprintf("%s/api/v2/users?page=%d", baseURLTrimmed, page)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("error creating SeAT users request: %w", err)
		}

		req.Header.Set("X-Token", apiKey)
		req.Header.Set("Accept", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("error requesting SeAT users page %d: %w", page, err)
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("SeAT users endpoint returned HTTP %d on page %d", resp.StatusCode, page)
		}

		var usersResp SeatUsersResponse
		if err := json.NewDecoder(resp.Body).Decode(&usersResp); err != nil {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("error decoding SeAT users response page %d: %w", page, err)
		}
		_ = resp.Body.Close()

		if len(usersResp.Data) == 0 {
			break
		}

		for _, user := range usersResp.Data {
			if !user.Active {
				continue
			}
			if user.MainCharacterID > 0 {
				validChars[user.MainCharacterID] = true
			}
			for _, charID := range user.AssociatedCharacterIDs {
				if charID > 0 {
					validChars[charID] = true
				}
			}
		}

		if usersResp.Links.Next == "" || (usersResp.Meta.LastPage > 0 && page >= usersResp.Meta.LastPage) {
			break
		}
		page++
	}

	return validChars, nil
}

// GetSeatConfig resolves the SeAT base URL and API key for a guild,
// falling back to the global environment configuration.
func GetSeatConfig(guildID string) (string, string, error) {
	var baseURL, apiKey string

	if guildID != "" {
		integ, err := db.GetGuildIntegration(guildID)
		if err == nil {
			baseURL = integ.SeatURL
			apiKey = integ.SeatAPIKey
		}
	}

	if baseURL == "" || apiKey == "" {
		baseURL = config.GetSeatURL()
		apiKey = config.GetSeatAPIKey()
	}

	if baseURL == "" || apiKey == "" {
		if guildID != "" {
			return "", "", fmt.Errorf("SeAT is not configured for guild %s", guildID)
		}
		return "", "", fmt.Errorf("SeAT is not configured (missing SEAT_URL or SEAT_API)")
	}

	return baseURL, apiKey, nil
}

func GetValidSeatCharacters(guildID string) (map[int]bool, error) {
	baseURL, apiKey, err := GetSeatConfig(guildID)
	if err != nil {
		return nil, err
	}

	chars, err := FetchActiveUserCharacterIDs(baseURL, apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SeAT characters: %w", err)
	}

	return chars, nil
}
