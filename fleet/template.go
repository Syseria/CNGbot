package fleet

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"evemaildiscord/db"
)

type FleetTemplate struct {
	Name  string         `json:"name"`
	Wings []WingTemplate `json:"wings"`
}

type WingTemplate struct {
	Name   string   `json:"name"`
	Squads []string `json:"squads"`
}

type esiMember struct {
	CharacterID int64  `json:"character_id"`
	Role        string `json:"role"`
	SquadID     int64  `json:"squad_id"`
	WingID      int64  `json:"wing_id"`
}

// Validate ensures the template conforms to ESI's 5x5 hard limits.
func (t *FleetTemplate) Validate() error {
	if len(t.Wings) > 5 {
		return fmt.Errorf("a fleet can have a maximum of 5 wings")
	}
	for i, w := range t.Wings {
		if len(w.Squads) > 5 {
			return fmt.Errorf("wing '%s' (index %d) exceeds the maximum of 5 squads", w.Name, i)
		}
	}
	return nil
}

// SaveFleetTemplate stores a template JSON in the config table.
func SaveFleetTemplate(guildID string, tpl FleetTemplate) error {
	jsonBytes, err := json.Marshal(tpl)
	if err != nil {
		return err
	}
	key := "fleet_template_" + strings.ToLower(tpl.Name)
	_, err = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, ?, ?)", guildID, key, string(jsonBytes))
	return err
}

// GetFleetTemplate retrieves a specific template, or the default if name is empty.
func GetFleetTemplate(guildID, name string) (FleetTemplate, error) {
	if name == "" {
		err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'fleet_default_template'", guildID).Scan(&name)
		if err != nil {
			return FleetTemplate{}, err
		}
		if name == "" {
			return FleetTemplate{}, fmt.Errorf("no default template is set and no name was provided")
		}
	}

	key := "fleet_template_" + strings.ToLower(name)
	var rawJSON string
	err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = ?", guildID, key).Scan(&rawJSON)
	if err != nil {
		return FleetTemplate{}, fmt.Errorf("template '%s' not found", name)
	}

	var tpl FleetTemplate
	err = json.Unmarshal([]byte(rawJSON), &tpl)
	return tpl, err
}

// ListFleetTemplates returns the names of all saved templates for a guild.
func ListFleetTemplates(guildID string) ([]string, error) {
	rows, err := db.DB.Query("SELECT key FROM config WHERE guild_id = ? AND key LIKE 'fleet_template_%'", guildID)
	if err != nil {
		return nil, err
	}
	defer func(rows *sql.Rows) {
		err := rows.Close()
		if err != nil {

		}
	}(rows)

	var names []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err == nil {
			names = append(names, strings.TrimPrefix(key, "fleet_template_"))
		}
	}
	return names, nil
}

// DeleteFleetTemplate removes a specific fleet template from the config table.
func DeleteFleetTemplate(guildID string, templateName string) error {
	configKey := "fleet_template_" + strings.ToLower(templateName)

	result, err := db.DB.Exec("DELETE FROM config WHERE guild_id = ? AND key = ?", guildID, configKey)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return fmt.Errorf("template '%s' not found", templateName)
	}

	return nil
}
