package projects

// itemCategories maps EVE item type names to buyback category names.
// The percentage for each category is set per guild via the /buyback_set_* commands.
var itemCategories = map[string]string{}

func registerItems(category string, names []string) {
	for _, n := range names {
		itemCategories[n] = category
	}
}

func init() {
	registerItems("ore", []string{
		"Compressed Omber IV-Grade",
		"Compressed Omber III-Grade",
		"Compressed Omber II-Grade",
		"Compressed Plagioclase IV-Grade",
		"Compressed Plagioclase III-Grade",
		"Compressed Plagioclase II-Grade",
		"Compressed Pyroxeres IV-Grade",
		"Compressed Pyroxeres III-Grade",
		"Compressed Pyroxeres II-Grade",
		"Compressed Scordite IV-Grade",
		"Compressed Scordite III-Grade",
		"Compressed Scordite II-Grade",
		"Compressed Spodumain IV-Grade",
		"Compressed Spodumain III-Grade",
		"Compressed Spodumain II-Grade",
		"Compressed Ueganite IV-Grade",
		"Compressed Ueganite III-Grade",
		"Compressed Ueganite II-Grade",
		"Compressed Veldspar IV-Grade",
		"Compressed Veldspar III-Grade",
		"Compressed Veldspar II-Grade",
		"Compressed Bezdnacine II-Grade",
		"Compressed Bezdnacine",
		"Compressed Bistot IV-Grade",
		"Compressed Bistot III-Grade",
		"Compressed Bistot II-Grade",
		"Compressed Crokite IV-Grade",
		"Compressed Crokite III-Grade",
		"Compressed Crokite II-Grade",
		"Compressed Gneiss IV-Grade",
		"Compressed Gneiss III-Grade",
		"Compressed Gneiss II-Grade",
		"Compressed Hedbergite IV-Grade",
		"Compressed Hedbergite III-Grade",
		"Compressed Hedbergite II-Grade",
		"Compressed Hedbergite",
		"Compressed Jaspet IV-Grade",
		"Compressed Jaspet III-Grade",
		"Compressed Jaspet II-Grade",
		"Compressed Kernite IV-Grade",
		"Compressed Kernite III-Grade",
		"Compressed Kernite II-Grade",
		"Compressed Nocxite IV-Grade",
		"Compressed Nocxite III-Grade",
		"Compressed Arkonor IV-Grade",
		"Compressed Bezdnacine III-Grade",
	})

	registerItems("gas", []string{
		"Compressed Fullerite-C60",
		"Fullerite-C70",
		"Compressed Fullerite-C70",
		"Fullerite-C72",
		"Compressed Fullerite-C72",
		"Fullerite-C84",
		"Compressed Fullerite-C84",
		"Fullerite-C28",
		"Compressed Fullerite-C28",
		"Fullerite-C32",
		"Compressed Fullerite-C32",
		"Fullerite-C320",
		"Compressed Fullerite-C320",
		"Fullerite-C50",
		"Compressed Fullerite-C50",
		"Fullerite-C540",
		"Compressed Fullerite-C540",
		"Fullerite-C60",
	})

	registerItems("blue_loot", []string{
		"Neural Network Analyzer",
		"Sleeper Data Library",
		"Ancient Coordinates Database",
		"Sleeper Drone AI Nexus",
	})

	registerItems("salvage", []string{
		"Cartesian Temporal Coordinator",
		"Electromechanical Hull Sheeting",
		"Faraday Force Magnetometer",
		"Modified Fluid Router",
		"Neurovisual Input Matrix",
		"Synthetic Aperture Ladar Receiver",
		"Scorched Microgravimeter",
		"Alloyed Tritanium Bar",
		"Armor Plates",
		"Artificial Neural Network",
		"Broken Drone Transceiver",
		"Burned Logic Circuit",
		"Capacitor Console",
		"Charred Micro Circuit",
		"Conductive Polymer",
		"Conductive Thermoplastic",
		"Contaminated Lorentz Fluid",
		"Contaminated Nanite Compound",
		"Current Pump",
		"Defective Current Pump",
		"Damaged Artificial Neural Network",
		"Drone Transceiver",
		"Enhanced Ward Console",
		"Fried Interface Circuit",
		"Impetus Console",
		"Intact Armor Plates",
		"Intact Shield Emitter",
		"Interface Circuit",
		"Logic Circuit",
		"Lorentz Fluid",
		"Malfunctioning Shield Emitter",
		"Melted Capacitor Console",
		"Micro Circuit",
		"Power Circuit",
		"Power Conduit",
		"Scorched Telemetry Processor",
		"Smashed Trigger Unit",
		"Tangled Power Conduit",
		"Telemetry Processor",
		"Thruster Console",
		"Tripped Power Circuit",
		"Ward Console",
	})

	registerItems("datacores", []string{
		"Datacore - Biochemistry",
		"Datacore - Electronic Engineering",
		"Datacore - Electromagnetic Physics",
		"Datacore - Experimental Engineering",
		"Datacore - Explosive Physics",
		"Datacore - Graviton Physics",
		"Datacore - High Energy Physics",
		"Datacore - Hydromagnetic Physics",
		"Datacore - Laser Physics",
		"Datacore - Mechanical Engineering",
		"Datacore - Molecular Biology",
		"Datacore - Nanite Engineering",
		"Datacore - Nuclear Physics",
		"Datacore - Plasma Physics",
		"Datacore - Quantum Physics",
		"Datacore - Rocket Science",
	})
}

// CategoryForName returns the buyback category for an item type name, or "" if unknown.
func CategoryForName(name string) string {
	return itemCategories[name]
}