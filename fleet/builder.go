package fleet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

// ==========================================
// 1. High-Level Orchestrator
// ==========================================

// BuildFleetFromTemplate synchronizes the live EVE fleet to the JSON template.
func BuildFleetFromTemplate(client *http.Client, fleetID int64, tpl FleetTemplate) error {
	if err := tpl.Validate(); err != nil {
		return err
	}

	// Fetch current state
	currentWings, err := fetchCurrentLayout(client, fleetID)
	if err != nil {
		return err
	}

	// Align wing count (create missing / delete excess)
	currentWings, err = syncWingCount(client, fleetID, currentWings, len(tpl.Wings))
	if err != nil {
		return err
	}

	// Sync names and squads for each wing
	for i, targetWing := range tpl.Wings {
		wing := currentWings[i]

		// Rename wing if it differs
		if wing.Name != targetWing.Name {
			url := fmt.Sprintf("%s/fleets/%d/wings/%d/", esiBase, fleetID, wing.ID)
			if err := renameFleetNode(client, url, targetWing.Name); err != nil {
				return fmt.Errorf("failed to rename wing %d: %w", i+1, err)
			}
		}

		// Align squad count for this wing
		wing.Squads, err = syncSquadCount(client, fleetID, wing.ID, wing.Squads, len(targetWing.Squads))
		if err != nil {
			return err
		}

		// Rename squads if they differ
		for j, targetSquadName := range targetWing.Squads {
			squad := wing.Squads[j]
			if squad.Name != targetSquadName {
				url := fmt.Sprintf("%s/fleets/%d/squads/%d/", esiBase, fleetID, squad.ID)
				if err := renameFleetNode(client, url, targetSquadName); err != nil {
					return fmt.Errorf("failed to rename squad %d in wing %d: %w", j+1, i+1, err)
				}
			}
		}
	}

	return nil
}

// ==========================================
// 2. State Alignment Logic
// ==========================================

func syncWingCount(client *http.Client, fleetID int64, current []esiWing, targetCount int) ([]esiWing, error) {
	// Delete excess wings (from the bottom up)
	for len(current) > targetCount {
		lastIdx := len(current) - 1
		wingID := current[lastIdx].ID

		if err := deleteNode(client, fmt.Sprintf("%s/fleets/%d/wings/%d/", esiBase, fleetID, wingID)); err != nil {
			return nil, fmt.Errorf("failed to delete wing: %w", err)
		}
		current = current[:lastIdx]
	}

	// Create missing wings
	for len(current) < targetCount {
		newID, err := createNode(client, fmt.Sprintf("%s/fleets/%d/wings/", esiBase, fleetID), "wing_id")
		if err != nil {
			return nil, fmt.Errorf("failed to create wing: %w", err)
		}
		current = append(current, esiWing{ID: newID, Squads: []esiSquad{}}) // Start with empty squads
	}

	return current, nil
}

func syncSquadCount(client *http.Client, fleetID, wingID int64, current []esiSquad, targetCount int) ([]esiSquad, error) {
	// Delete excess squads
	for len(current) > targetCount {
		lastIdx := len(current) - 1
		squadID := current[lastIdx].ID

		if err := deleteNode(client, fmt.Sprintf("%s/fleets/%d/squads/%d/", esiBase, fleetID, squadID)); err != nil {
			return nil, fmt.Errorf("failed to delete squad: %w", err)
		}
		current = current[:lastIdx]
	}

	// Create missing squads
	for len(current) < targetCount {
		newID, err := createNode(client, fmt.Sprintf("%s/fleets/%d/wings/%d/squads/", esiBase, fleetID, wingID), "squad_id")
		if err != nil {
			return nil, fmt.Errorf("failed to create squad: %w", err)
		}
		current = append(current, esiSquad{ID: newID})
	}

	return current, nil
}

// MoveMembersToFirstSquad sweeps all standard fleet members into Wing 1, Squad 1.
func MoveMembersToFirstSquad(client *http.Client, fleet ownFleet) error {
	wings, err := fetchCurrentLayout(client, fleet.FleetID)
	if err != nil {
		return fmt.Errorf("failed to fetch wings for movement: %w", err)
	}

	if len(wings) == 0 || len(wings[0].Squads) == 0 {
		return nil
	}

	targetWingID := wings[0].ID
	targetSquadID := wings[0].Squads[0].ID

	members, err := fetchFleetMembers(client, fleet.FleetID)
	if err != nil {
		return fmt.Errorf("failed to fetch fleet members: %w", err)
	}

	for _, member := range members {
		// Preserve the Fleet Boss
		if member.CharacterID == fleet.FleetBossID {
			err := moveFleetMember(client, fleet.FleetID, member.CharacterID, 0, 0, "fleet_commander")
			if err != nil {
				fmt.Printf("Warning: Failed to move character %d: %v\n", member.CharacterID, err)
			}
			continue
		}

		if member.SquadID == targetSquadID {
			continue
		}

		err := moveFleetMember(client, fleet.FleetID, member.CharacterID, targetWingID, targetSquadID, "squad_member")
		if err != nil {
			fmt.Printf("Warning: Failed to move character %d: %v\n", member.CharacterID, err)
		}
	}

	return nil
}

// ==========================================
// 3. ESI Network Wrappers
// ==========================================

func fetchCurrentLayout(client *http.Client, fleetID int64) ([]esiWing, error) {
	resp, err := client.Get(fmt.Sprintf("%s/fleets/%d/wings/", esiBase, fleetID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch wings, status: %d", resp.StatusCode)
	}

	var wings []esiWing
	if err := json.NewDecoder(resp.Body).Decode(&wings); err != nil {
		return nil, err
	}
	return wings, nil
}

// createNode sends a POST to ESI and extracts the resulting ID (works for both wings and squads)
func createNode(client *http.Client, url, idKey string) (int64, error) {
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 201 {
		return 0, fmt.Errorf("creation failed with status: %d", resp.StatusCode)
	}

	var result map[string]int64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result[idKey], nil
}

func deleteNode(client *http.Client, url string) error {
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return fmt.Errorf("deletion failed with status: %d", resp.StatusCode)
	}
	return nil
}

func renameFleetNode(client *http.Client, url, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return fmt.Errorf("rename failed with status: %d", resp.StatusCode)
	}
	return nil
}

func fetchFleetMembers(client *http.Client, fleetID int64) ([]esiMember, error) {
	resp, err := client.Get(fmt.Sprintf("%s/fleets/%d/members/", esiBase, fleetID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch fleet members, status: %d", resp.StatusCode)
	}

	var members []esiMember
	if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
		return nil, err
	}
	return members, nil
}

func moveFleetMember(client *http.Client, fleetID, charID, wingID, squadID int64, role string) error {
	url := fmt.Sprintf("%s/fleets/%d/members/%d/", esiBase, fleetID, charID)

	payload := map[string]interface{}{
		"role": role,
	}

	if wingID > 0 {
		payload["wing_id"] = wingID
	}
	if squadID > 0 {
		payload["squad_id"] = squadID
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return fmt.Errorf("move failed with status: %d", resp.StatusCode)
	}
	return nil
}
