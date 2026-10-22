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

type seatCharLocation struct {
	SolarSystemID *int `json:"solar_system_id"`
	ID            *int `json:"id"`
}

type seatCharDetail struct {
	SolarSystemID *int              `json:"solar_system_id"`
	Location      *seatCharLocation `json:"location"`
}

// FetchCharacterSolarSystem returns the current solar system ID of a character
// according to SeAT, or 0 if SeAT has no location data for the character.
func FetchCharacterSolarSystem(baseURL, apiKey string, charID int) (int, error) {
	if baseURL == "" || apiKey == "" {
		return 0, fmt.Errorf("SeAT URL or API key is not configured")
	}

	baseURLTrimmed := strings.TrimRight(baseURL, "/")

	req, err := http.NewRequest("GET", fmt.Sprintf("%s/api/v2/characters/%d/location", baseURLTrimmed, charID), nil)
	if err != nil {
		return 0, fmt.Errorf("error creating SeAT character location request: %w", err)
	}
	req.Header.Set("X-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("error requesting SeAT character location for character %d: %w", charID, err)
	}

	if resp.StatusCode == http.StatusOK {
		var loc seatCharLocation
		decodeErr := json.NewDecoder(resp.Body).Decode(&loc)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return 0, fmt.Errorf("error decoding SeAT character location for character %d: %w", charID, decodeErr)
		}
		if loc.SolarSystemID != nil && *loc.SolarSystemID > 0 {
			return *loc.SolarSystemID, nil
		}
	} else {
		statusCode := resp.StatusCode
		_ = resp.Body.Close()
		if statusCode != http.StatusNotFound {
			return 0, fmt.Errorf("SeAT characters location endpoint returned HTTP %d for character %d", statusCode, charID)
		}
	}

	// Fall back to the character detail endpoint for SeAT versions
	// that only expose location data there.
	req, err = http.NewRequest("GET", fmt.Sprintf("%s/api/v2/characters/%d", baseURLTrimmed, charID), nil)
	if err != nil {
		return 0, fmt.Errorf("error creating SeAT character request: %w", err)
	}
	req.Header.Set("X-Token", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err = httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("error requesting SeAT character %d: %w", charID, err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return 0, fmt.Errorf("SeAT characters endpoint returned HTTP %d for character %d", resp.StatusCode, charID)
	}

	var detail seatCharDetail
	decodeErr := json.NewDecoder(resp.Body).Decode(&detail)
	_ = resp.Body.Close()
	if decodeErr != nil {
		return 0, fmt.Errorf("error decoding SeAT character %d: %w", charID, decodeErr)
	}

	if detail.Location != nil && detail.Location.SolarSystemID != nil && *detail.Location.SolarSystemID > 0 {
		return *detail.Location.SolarSystemID, nil
	}
	if detail.SolarSystemID != nil && *detail.SolarSystemID > 0 {
		return *detail.SolarSystemID, nil
	}
	if detail.Location != nil && detail.Location.ID != nil {
		// SeAT sometimes stores a station/structure id in location.id,
		// so only accept values in the solar system range.
		if id := *detail.Location.ID; id >= 30000000 && id <= 32999999 {
			return id, nil
		}
	}

	return 0, nil
}
