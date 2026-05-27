package warframe

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

// ─── Data File Embedding & Lookups ─────────────────────────────────────────────

//go:embed solNodes.json
var solNodesJSON []byte

//go:embed sortieData.json
var sortieDataJSON []byte

//go:embed arcanes.json
var arcanesJSON []byte

//go:embed factionsData.json
var factionsDataJSON []byte

//go:embed missionTypes.json
var missionTypesJSON []byte

//go:embed archonShards.json
var archonShardsJSON []byte

// ─── SolNode Name Lookup ─────────────────────────────────────────────────────

// solNodeNames maps internal DE node codes ("SolNode27", "SettlementNode3", etc.)
// to human-readable names like "E Prime (Earth)".
var solNodeNames map[string]string

// solNodeInfo is the raw structure of each entry in solNodes.json.
type solNodeInfo struct {
	Value string `json:"value"`
	Enemy string `json:"enemy"`
	Type  string `json:"type"`
}

// ─── Sortie Data (from sortieData.json) ──────────────────────────────────────

// sortieBossInfo holds boss details from sortieData.json.
type sortieBossInfo struct {
	Name    string `json:"name"`
	Faction string `json:"faction"`
}

// sortieDataFile is the top-level structure of sortieData.json.
type sortieDataFile struct {
	ModifierTypes        map[string]string            `json:"modifierTypes"`
	ModifierDescriptions map[string]string            `json:"modifierDescriptions"`
	Bosses               map[string]sortieBossInfo    `json:"bosses"`
}

// sortieModifierDescMap maps sortie modifier codes to detailed descriptions.
var sortieModifierDescMap map[string]string

// sortieBossInfoMap maps boss codes to their name and faction.
var sortieBossInfoMap map[string]sortieBossInfo

// ─── Arcane Data (from arcanes.json) ─────────────────────────────────────────

// arcaneEntry holds one arcane's data from arcanes.json.
type arcaneEntry struct {
	Regex     string `json:"regex"`
	Name      string `json:"name"`
	Effect    string `json:"effect"`
	Rarity    string `json:"rarity"`
	Location  string `json:"location"`
	Thumbnail string `json:"thumbnail"`
	Info      string `json:"info"`
}

// arcanesList holds all parsed arcane entries.
var arcanesList []arcaneEntry

// factionNameMap is loaded from factionsData.json, replacing the hardcoded factionMap.
var factionNameMap map[string]string

// arcaneNameMap maps lowercase arcane names to their effect descriptions from arcanes.json.
var arcaneNameMap map[string]string

// archonShardNames maps Archon Shard codes ("ACC_BLUE", "ACC_RED", etc.) to display names.
var archonShardNames map[string]string

// GetArcanesList returns the full parsed arcanes list as JSON.
func GetArcanesList() json.RawMessage {
	if len(arcanesList) == 0 {
		return json.RawMessage("[]")
	}
	return toJSON(arcanesList)
}

// GetArchonShardsMap returns the archon shard name map as JSON.
func GetArchonShardsMap() json.RawMessage {
	if len(archonShardNames) == 0 {
		return json.RawMessage("{}")
	}
	return toJSON(archonShardNames)
}

func init() {
	// Parse solNodes.json
	var raw map[string]solNodeInfo
	if err := json.Unmarshal(solNodesJSON, &raw); err != nil {
		log.Printf("warframe: failed to parse solNodes.json: %v", err)
		solNodeNames = make(map[string]string)
		return
	}
	solNodeNames = make(map[string]string, len(raw))
	for code, info := range raw {
		solNodeNames[code] = info.Value
	}

	// Parse sortieData.json
	var sortieRaw sortieDataFile
	if err := json.Unmarshal(sortieDataJSON, &sortieRaw); err != nil {
		log.Printf("warframe: failed to parse sortieData.json: %v", err)
	}
	sortieModifierDescMap = sortieRaw.ModifierDescriptions
	sortieBossInfoMap = sortieRaw.Bosses

	// Parse arcanes.json
	if err := json.Unmarshal(arcanesJSON, &arcanesList); err != nil {
		log.Printf("warframe: failed to parse arcanes.json: %v", err)
	} else {
		arcaneNameMap = make(map[string]string, len(arcanesList))
		for _, a := range arcanesList {
			arcaneNameMap[strings.ToLower(a.Name)] = a.Effect
		}
	}

	// Parse factionsData.json
	var factionRaw map[string]interface{}
	if err := json.Unmarshal(factionsDataJSON, &factionRaw); err != nil {
		log.Printf("warframe: failed to parse factionsData.json: %v", err)
		factionNameMap = make(map[string]string)
		for k, v := range factionMap {
			factionNameMap[k] = v
		}
	} else {
		factionNameMap = make(map[string]string, len(factionRaw))
		for code, info := range factionRaw {
			if obj, ok := info.(map[string]interface{}); ok {
				if val, ok := obj["value"].(string); ok {
					factionNameMap[code] = val
				}
			}
		}
	}

	// Parse missionTypes.json and replace hardcoded missionTypeMap
	var missionTypesRaw map[string]struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(missionTypesJSON, &missionTypesRaw); err != nil {
		log.Printf("warframe: failed to parse missionTypes.json: %v", err)
	} else {
		newMap := make(map[string]string, len(missionTypesRaw))
		for k, v := range missionTypesRaw {
			newMap[k] = v.Value
		}
		missionTypeMap = newMap
	}

	// Parse archonShards.json
	var archonShardsRaw map[string]struct {
		Value        string            `json:"value"`
		UpgradeTypes map[string]struct {
			Value string `json:"value"`
		} `json:"upgradeTypes"`
	}
	if err := json.Unmarshal(archonShardsJSON, &archonShardsRaw); err != nil {
		log.Printf("warframe: failed to parse archonShards.json: %v", err)
	} else {
		archonShardNames = make(map[string]string, len(archonShardsRaw))
		for k, v := range archonShardsRaw {
			archonShardNames[k] = v.Value
		}
	}

	log.Printf("warframe: loaded %d solNodes, %d sortie bosses, %d arcane entries, %d factions, %d mission types, %d archon shards",
		len(solNodeNames), len(sortieBossInfoMap), len(arcanesList), len(factionNameMap), len(missionTypeMap), len(archonShardNames))
}

// lookupNode converts an internal DE node code to a human-readable name.
// Returns the original code if no mapping is found.
func lookupNode(code string) string {
	if code == "" {
		return ""
	}
	if name, ok := solNodeNames[code]; ok {
		return name
	}
	return code
}

// ─── Mapping tables ───────────────────────────────────────────────────────────

var missionTypeMap = map[string]string{
	"MT_ARENA":            "Rathuum",
	"MT_ARTIFACT":         "Disruption",
	"MT_ASSAULT":          "Assault",
	"MT_ASSASSINATION":    "Assassination",
	"MT_CAPTURE":          "Capture",
	"MT_CORRUPTION":       "Void Flood",
	"MT_DEFENSE":          "Defense",
	"MT_DISRUPTION":       "Disruption",
	"MT_EVACUATION":       "Defection",
	"MT_EXCAVATE":         "Excavation",
	"MT_EXTERMINATION":    "Extermination",
	"MT_HIVE":             "Hive",
	"MT_INTEL":            "Spy",
	"MT_LANDSCAPE":        "Free Roam",
	"MT_MOBILE_DEFENSE":   "Mobile Defense",
	"MT_PVP":              "Conclave",
	"MT_RESCUE":           "Rescue",
	"MT_RETRIEVAL":        "Hijack",
	"MT_SABOTAGE":         "Sabotage",
	"MT_SECTOR":           "Dark Sector",
	"MT_SURVIVAL":         "Survival",
	"MT_TERRITORY":        "Interception",
	"MT_VOID_CASCADE":     "Void Cascade",
	"MT_VOID_ARMAGEDDON":  "Void Armageddon",
	"MT_ASCENSION":        "Ascension",
	"MT_ALCHEMY":          "Alchemy",
	"MT_ENDLESS_CAPTURE":  "Legacyte Harvest",
	"MT_DEFAULT":          "Unknown",
}

var factionMap = map[string]string{
	"FC_CORPUS":       "Corpus",
	"FC_CORRUPTED":    "Corrupted",
	"FC_GRINEER":      "Grineer",
	"FC_INFESTATION":  "Infested",
	"FC_OROKIN":       "Orokin",
	"FC_SENTIENT":     "Sentient",
	"FC_MITW":         "Man in the Wall",
	"FC_NARMER":       "Narmer",
	"FC_SCALDRA":      "Scaldra",
	"FC_TECHROT":      "Techrot",
}

var fissureTierMap = map[string]struct {
	Name string
	Num  int
}{
	"VoidT1": {"Lith", 1},
	"VoidT2": {"Meso", 2},
	"VoidT3": {"Neo", 3},
	"VoidT4": {"Axi", 4},
	"VoidT5": {"Requiem", 5},
	"VoidT6": {"Omnia", 6},
}

var sortieModifierMap = map[string]string{
	"SORTIE_MODIFIER_LOW_ENERGY":           "Low Energy",
	"SORTIE_MODIFIER_EXIMUS":               "Eximus Stronghold",
	"SORTIE_MODIFIER_HAZARD_RADIATION":     "Radiation Hazard",
	"SORTIE_MODIFIER_HAZARD_FIRE":          "Fire Hazard",
	"SORTIE_MODIFIER_HAZARD_ICE":           "Ice Hazard",
	"SORTIE_MODIFIER_VIRAL":                "Viral",
	"SORTIE_MODIFIER_ELECTROMAGNETIC":      "Electromagnetic",
	"SORTIE_MODIFIER_ARMOR":               "Enhanced Armor",
	"SORTIE_MODIFIER_SHIELD":              "Enhanced Shields",
	"SORTIE_MODIFIER_CLOAK":               "Cloaked Enemy",
	"SORTIE_MODIFIER_SNIPER_ONLY":          "Sniper Only",
	"SORTIE_MODIFIER_BOW_ONLY":             "Bow Only",
	"SORTIE_MODIFIER_SHOTGUN_ONLY":         "Shotgun Only",
	"SORTIE_MODIFIER_PISTOL_ONLY":          "Pistol Only",
	"SORTIE_MODIFIER_MELEE_ONLY":           "Melee Only",
	"SORTIE_MODIFIER_ENERGY_WEAPON_ONLY":   "Energy Weapon Only",
	"SORTIE_MODIFIER_MAGNETIC":             "Magnetic",
	"SORTIE_MODIFIER_NO_SHIELD":            "No Shields",
	"SORTIE_MODIFIER_NO_ENERGY":            "No Energy",
	"SORTIE_MODIFIER_TOXIN":               "Toxin",
	"SORTIE_MODIFIER_CORROSIVE":            "Corrosive",
	"SORTIE_MODIFIER_BLUNT":               "Blunt",
}

var bossMap = map[string]string{
	"SORTIE_BOSS_HYENA":           "Hyena",
	"SORTIE_BOSS_KELA":            "Kela De Thaym",
	"SORTIE_BOSS_RUK":             "General Sargas Ruk",
	"SORTIE_BOSS_ALAD":            "Alad V",
	"SORTIE_BOSS_LEPHANTIS":       "Lephantis",
	"SORTIE_BOSS_VOR_LEHTO":       "Captain Vor & Lech Kril",
	"SORTIE_BOSS_KRIL":            "Lech Kril",
	"SORTIE_BOSS_PHORID":          "Phorid",
	"SORTIE_BOSS_HEK":             "Councilor Vay Hek",
	"SORTIE_BOSS_JACKAL":          "Jackal",
	"SORTIE_BOSS_RAPTOR":          "Raptor",
	"SORTIE_BOSS_AMPHIS":          "Mutalist Alad V",
	"SORTIE_BOSS_KEK":             "Infested Alad V",
	"SORTIE_BOSS_HYENA_PACK":      "Hyena Pack",
	"SORTIE_BOSS_ZEALOID":         "Zealoid Prelate",
	"SORTIE_BOSS_JOAN":            "The Sergeant",
	"SORTIE_BOSS_AMAR":            "Amar",
	"SORTIE_BOSS_ERRA":            "Erra",
	"SORTIE_BOSS_NIRA":            "Nira",
	"SORTIE_BOSS_PHENE":           "Phene",
	"SORTIE_BOSS_GRUNK":           "Grunks",
	"SORTIE_BOSS_AJAY":            "Ajay",
	"SORTIE_BOSS_LIEUTENANT_LECH_KRIL": "Lech Kril",
}

// cycle time constants (in seconds)
// wfcdHTTPClient is a reusable HTTP client for fetching supplementary data from the
// WFCD community API (api.warframestat.us). It follows redirects and has a short
// timeout so failures don't block the polling cycle.
var wfcdHTTPClient = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		return nil
	},
}

// cycle time constants (in seconds)
const (
	cetusCycleDuration   = 150 * 60 // 150 min total (100 day, 50 night)
	vallisCycleDuration  = 150 * 60 // 150 min total (100 warm, 50 cold)
	cambionCycleDuration = 200 * 60 // 200 min total (100 fass, 100 vome)
	earthCycleDuration   = 150 * 60 // matches Cetus

	cetusDayDuration    = 100 * 60
	vallisWarmDuration  = 100 * 60
)

// Known cycle epoch start times (UTC timestamps when a specific phase began)
// These are approximate reference points that can be tuned.
const (
	// Cetus/Earth: known day started at this epoch
	cetusEpoch = int64(1575960000)   // ~Dec 10 2019 - a known day start
	// Vallis: known warm started at this epoch
	vallisEpoch = int64(1575960000)
	// Cambion: known Fass started at this epoch
	cambionEpoch = int64(1593120000) // ~Jun 26 2020 - a known Fass start
)

// ─── Helpers ───────────────────────────────────────────────────────────────────

// deMongoTime converts a DE MongoDB-style timestamp ($date.$numberLong ms since epoch)
// Returns an empty string if the input doesn't match the expected format.
// DE format: {"$date":{"$numberLong":"1779814800000"}}
func deMongoTime(v interface{}) string {
	if v == nil {
		return ""
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return ""
	}
	dateVal, ok := m["$date"]
	if !ok {
		return ""
	}
	dateMap, ok := dateVal.(map[string]interface{})
	if !ok {
		// Try string format
		if s, ok := dateVal.(string); ok {
			return s
		}
		return ""
	}
	msStr, ok := dateMap["$numberLong"]
	if !ok {
		return ""
	}
	var ms int64
	if f, ok := msStr.(float64); ok {
		ms = int64(f)
	} else if s, ok := msStr.(string); ok {
		fmt.Sscanf(s, "%d", &ms)
	} else {
		return ""
	}
	if ms == 0 {
		return ""
	}
	t := time.UnixMilli(ms).UTC()
	return t.Format(time.RFC3339)
}

// deObjectID extracts a string ID from {"$oid":"..."} format.
func deObjectID(v interface{}) string {
	if v == nil {
		return ""
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return ""
	}
	oid, ok := m["$oid"]
	if !ok {
		return ""
	}
	if s, ok := oid.(string); ok {
		return s
	}
	return ""
}

// mapStr applies a string mapping, returning the mapped value or the original if not found.
func mapStr(val string, m map[string]string) string {
	if mapped, ok := m[val]; ok {
		return mapped
	}
	// Try stripping trailing/leading whitespace
	trimmed := strings.TrimSpace(val)
	if trimmed != val {
		if mapped, ok := m[trimmed]; ok {
			return mapped
		}
	}
	return val
}

// toJSON re-encodes a value as json.RawMessage.
func toJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

// ─── DE Raw Worldstate ─────────────────────────────────────────────────────────

// DERawWorldstate represents the parsed DE endpoint response.
type DERawWorldstate struct {
	Raw map[string]json.RawMessage `json:"-"`

	Alerts              []map[string]interface{} `json:"Alerts"`
	Sorties             []map[string]interface{} `json:"Sorties"`
	LiteSorties         []map[string]interface{} `json:"LiteSorties"`
	ActiveMissions      []map[string]interface{} `json:"ActiveMissions"`
	VoidTraders         []map[string]interface{} `json:"VoidTraders"`
	PrimeVaultTraders   []map[string]interface{} `json:"PrimeVaultTraders"`
	DailyDeals          []map[string]interface{} `json:"DailyDeals"`
	Invasions           []map[string]interface{} `json:"Invasions"`
	Events              []map[string]interface{} `json:"Events"`
	Goals               []map[string]interface{} `json:"Goals"`
	HubEvents           []map[string]interface{} `json:"HubEvents"`
	SeasonInfo          map[string]interface{}   `json:"SeasonInfo"`
	SyndicateMissions   []map[string]interface{} `json:"SyndicateMissions"`
	GlobalUpgrades      []map[string]interface{} `json:"GlobalUpgrades"`
	PVPChallengeInstances []map[string]interface{} `json:"PVPChallengeInstances"`
	ConstructionProjects []map[string]interface{} `json:"ConstructionProjects"`
	ProjectPct          []interface{}            `json:"ProjectPct"`
	PersistentEnemies   []map[string]interface{} `json:"PersistentEnemies"`
	Descents            []map[string]interface{} `json:"Descents"`
	FlashSales          []map[string]interface{} `json:"FlashSales"`
	VoidStorms          []map[string]interface{} `json:"VoidStorms"`

	// Cycle-related: from Goals for events, computed for cycles
}

// TransformWorldstate transforms raw DE worldstate data into the WFCD API format
// that the app expects (map keyed by endpoint names like "fissures", "alerts", etc.)
func TransformWorldstate(data json.RawMessage, platform string) map[string]json.RawMessage {
	var raw DERawWorldstate
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Printf("warframe: parse DE worldstate: %v", err)
		return nil
	}
	// Also keep the raw map for direct access
	var rawMap map[string]json.RawMessage
	json.Unmarshal(data, &rawMap)
	raw.Raw = rawMap

	result := make(map[string]json.RawMessage, len(WorldstateEndpoints))
	for _, ep := range WorldstateEndpoints {
		result[ep] = json.RawMessage("null")
	}

	// Transform each field
	result["alerts"] = toJSON(transformAlerts(raw.Alerts))
	result["fissures"] = toJSON(transformFissures(raw.ActiveMissions, raw.VoidStorms))
	result["sortie"] = toJSON(transformSortie(raw.Sorties))
	result["archonHunt"] = toJSON(transformArchonHunt(raw.LiteSorties))
	result["invasions"] = toJSON(transformInvasions(raw.Invasions))
	result["voidTrader"] = toJSON(transformVoidTrader(raw.VoidTraders, "Baro Ki'Teer"))
	result["vaultTrader"] = toJSON(transformVoidTrader(raw.PrimeVaultTraders, "Varzia"))
	result["voidTraders"] = toJSON(append(
		transformVoidTradersList(raw.VoidTraders, "Baro Ki'Teer"),
		transformVoidTradersList(raw.PrimeVaultTraders, "Varzia")...,
	))
	result["dailyDeals"] = toJSON(transformDailyDeals(raw.DailyDeals))
	result["events"] = toJSON(transformEvents(raw.Goals, raw.Events))
	result["nightwave"] = toJSON(transformNightwave(raw.SeasonInfo))
	result["syndicateMissions"] = toJSON(transformSyndicateMissions(raw.SyndicateMissions))
	result["globalUpgrades"] = toJSON(transformGlobalUpgrades(raw.GlobalUpgrades))
	result["conclaveChallenges"] = toJSON(transformConclaveChallenges(raw.PVPChallengeInstances))
	result["constructionProgress"] = toJSON(transformConstruction(raw.ConstructionProjects, raw.ProjectPct))
	result["sentientOutposts"] = toJSON(transformSentientOutposts(raw.PersistentEnemies))
	result["deepArchimedea"] = toJSON(transformDescents(raw.Descents))
	result["news"] = toJSON(transformNews(raw.Events))
	result["simaris"] = json.RawMessage("null")

	// Compute cycles (HubEvents is often empty, so we compute from known patterns)
	result["cetusCycle"] = toJSON(computeCetusCycle())
	result["earthCycle"] = toJSON(computeEarthCycle())
	result["vallisCycle"] = toJSON(computeVallisCycle())
	result["cambionCycle"] = toJSON(computeCambionCycle())

	// Arbitration - not directly in DE data, compute best-effort
	result["arbitration"] = toJSON(computeArbitration(raw.ActiveMissions))

	result["steelPath"] = toJSON(transformSteelPath(raw.PersistentEnemies))

	return result
}

// ─── Alert transformation ──────────────────────────────────────────────────────

func transformAlerts(alerts []map[string]interface{}) []interface{} {
	var result []interface{}
	for _, a := range alerts {
		// Expired alerts have negative time remaining
		expiry := deMongoTime(a["Expiry"])
		if expiry == "" {
			continue
		}
		expTime, err := time.Parse(time.RFC3339, expiry)
		if err == nil && expTime.Before(time.Now().Add(-1*time.Hour)) {
			continue // skip expired
		}

		mi, _ := a["MissionInfo"].(map[string]interface{})
		missionType := mapStr(getStr(mi, "missionType"), missionTypeMap)
		faction := mapStr(getStr(mi, "faction"), factionNameMap)
		node := lookupNode(getStr(mi, "location"))
		minLvl := getFloat(mi, "minEnemyLevel")
		maxLvl := getFloat(mi, "maxEnemyLevel")

		reward := buildReward(mi)

		entry := map[string]interface{}{
			"id":         deObjectID(a["_id"]),
			"activation": deMongoTime(a["Activation"]),
			"expiry":     expiry,
			"mission": map[string]interface{}{
				"node":          node,
				"faction":       faction,
				"type":          missionType,
				"minEnemyLevel": minLvl,
				"maxEnemyLevel": maxLvl,
				"reward":        reward,
			},
			"missionType": missionType,
			"node":        node,
			"enemy":       faction,
			"reward":      reward,
		}
		result = append(result, entry)
	}
	return result
}

// ─── Fissure transformation ───────────────────────────────────────────────────

// Arbitrations appear as ActiveMissions with no Void modifier
// Steel Path fissures: some have "Hard" in the modifier
// Void Storms come from the VoidStorms array

func transformFissures(missions []map[string]interface{}, storms []map[string]interface{}) []interface{} {
	var result []interface{}

	// Build a set of storm nodes from VoidStorms
	stormNodes := make(map[string]bool)
	for _, s := range storms {
		if n, ok := s["Node"].(string); ok {
			stormNodes[n] = true
		}
	}

	for _, m := range missions {
		modifier, _ := m["Modifier"].(string)
		tierInfo, isFissure := fissureTierMap[modifier]
		if !isFissure {
			continue
		}

		missionType := mapStr(getStr(m, "MissionType"), missionTypeMap)
		rawNode := getStr(m, "Node")
		node := lookupNode(rawNode)
		seed := getFloat(m, "Seed")

		isStorm := stormNodes[rawNode]
		isHard, _ := m["Hard"].(bool) // Steel Path indicator from DE API

		expiry := deMongoTime(m["Expiry"])
		activation := deMongoTime(m["Activation"])

		entry := map[string]interface{}{
			"id":          deObjectID(m["_id"]),
			"activation":  activation,
			"expiry":      expiry,
			"node":        node,
			"missionType": missionType,
			"enemy":       "Corrupted",
			"tier":        tierInfo.Name,
			"tierNum":     tierInfo.Num,
			"isHard":      isHard,
			"isStorm":     isStorm,
			"hard":        isHard,
			"expired":     false,
			"seed":        seed,
		}
		result = append(result, entry)
	}
	return result
}

// ─── Sortie transformation ─────────────────────────────────────────────────────

func transformSortie(sorties []map[string]interface{}) interface{} {
	if len(sorties) == 0 {
		return nil
	}
	s := sorties[0]
	bossCode, _ := s["Boss"].(string)
	boss := mapStr(bossCode, bossMap)
	bossFaction := ""
	if bi, ok := sortieBossInfoMap[bossCode]; ok {
		bossFaction = bi.Faction
	}

	variantsRaw, _ := s["Variants"].([]interface{})
	var variants []map[string]interface{}
	for _, v := range variantsRaw {
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		modType := getStr(vm, "modifierType")
		mod := mapStr(modType, sortieModifierMap)
		desc := sortieModifierDescMap[modType]

		variants = append(variants, map[string]interface{}{
			"missionType":    mapStr(getStr(vm, "missionType"), missionTypeMap),
			"node":           lookupNode(getStr(vm, "node")),
			"modifier":       mod,
			"modifierType":   modType,
			"modifierDesc":   desc,
		})
	}

	return map[string]interface{}{
		"id":         deObjectID(s["_id"]),
		"activation": deMongoTime(s["Activation"]),
		"expiry":     deMongoTime(s["Expiry"]),
		"boss":       boss,
		"bossCode":   bossCode,
		"bossFaction":  bossFaction,
		"seed":       getFloat(s, "Seed"),
		"variants":   variants,
	}
}

// ─── Archon Hunt transformation ───────────────────────────────────────────────

func transformArchonHunt(liteSorties []map[string]interface{}) interface{} {
	if len(liteSorties) == 0 {
		return nil
	}
	s := liteSorties[0]
	bossCode, _ := s["Boss"].(string)
	boss := mapStr(bossCode, bossMap)

	missionsRaw, _ := s["Missions"].([]interface{})
	var missions []map[string]interface{}
	for _, mr := range missionsRaw {
		mm, ok := mr.(map[string]interface{})
		if !ok {
			continue
		}
		mt := mapStr(getStr(mm, "missionType"), missionTypeMap)
		missions = append(missions, map[string]interface{}{
			"type": mt,
			"node": lookupNode(getStr(mm, "node")),
		})
	}

	return map[string]interface{}{
		"id":         deObjectID(s["_id"]),
		"activation": deMongoTime(s["Activation"]),
		"expiry":     deMongoTime(s["Expiry"]),
		"boss":       boss,
		"bossCode":   bossCode,
		"missions":   missions,
	}
}

// ─── Invasion transformation ──────────────────────────────────────────────────

func transformInvasions(invasions []map[string]interface{}) []interface{} {
	var result []interface{}
	for _, inv := range invasions {
		completed := false
		if c, ok := inv["Completed"].(bool); ok {
			completed = c
		}

		attackerFaction := mapStr(getStr(inv, "Faction"), factionNameMap)
		defenderFaction := mapStr(getStr(inv, "DefenderFaction"), factionNameMap)
		locTag, _ := inv["LocTag"].(string)
		desc := locTag
		// Shorten locTag paths
		if idx := strings.LastIndex(locTag, "/"); idx >= 0 {
			desc = locTag[idx+1:]
		}

		count := getFloat(inv, "Count")
		goal := getFloat(inv, "Goal")
		completion := 0.0
		if goal > 0 {
			completion = (count / goal) * 100.0
		}

		// Extract attacker reward
		attReward := extractCountedReward(inv["AttackerReward"])
		defReward := extractCountedReward(inv["DefenderReward"])

		entry := map[string]interface{}{
			"id":               deObjectID(inv["_id"]),
			"activation":       deMongoTime(inv["Activation"]),
			"node":             lookupNode(getStr(inv, "Node")),
			"desc":             desc,
			"attackingFaction": attackerFaction,
			"defendingFaction": defenderFaction,
			"completed":        completed,
			"completion":       math.Round(completion*10) / 10,
			"count":            count,
			"goal":             goal,
			"attackerReward":   attReward,
			"defenderReward":   defReward,
		}
		result = append(result, entry)
	}
	return result
}

// ─── Void Trader transformation ───────────────────────────────────────────────

func transformVoidTrader(traders []map[string]interface{}, name string) interface{} {
	if len(traders) == 0 {
		return map[string]interface{}{
			"active":    false,
			"character": name,
			"location":  "",
			"inventory": []interface{}{},
		}
	}
	t := traders[0]
	activation := deMongoTime(t["Activation"])
	expiry := deMongoTime(t["Expiry"])

	now := time.Now().UTC()
	actTime, _ := time.Parse(time.RFC3339, activation)
	expTime, _ := time.Parse(time.RFC3339, expiry)
	active := !actTime.IsZero() && !expTime.IsZero() && now.After(actTime) && now.Before(expTime)

	node := lookupNode(getStr(t, "Node"))
	character, _ := t["Character"].(string)
	if character == "" {
		character = name
	}

	// Extract manifest/inventory for PrimeVaultTraders
	var inventory []map[string]interface{}
	if manifest, ok := t["Manifest"].([]interface{}); ok {
		for _, mi := range manifest {
			if item, ok := mi.(map[string]interface{}); ok {
				itemType, _ := item["ItemType"].(string)
				itemName := itemType
				if idx := strings.LastIndex(itemType, "/"); idx >= 0 {
					itemName = itemType[idx+1:]
				}
				primePrice := getFloat(item, "PrimePrice")
				credits := getFloat(item, "Credits")
				ducats := getFloat(item, "Ducats")
				inventory = append(inventory, map[string]interface{}{
					"item":       itemName,
					"itemType":   itemType,
					"primePrice": primePrice,
					"credits":    credits,
					"ducats":     ducats,
				})
			}
		}
	}

	return map[string]interface{}{
		"active":     active,
		"character":  character,
		"location":   node,
		"activation": activation,
		"expiry":     expiry,
		"inventory":  inventory,
		"node":       node,
	}
}

func transformVoidTradersList(traders []map[string]interface{}, name string) []interface{} {
	if len(traders) == 0 {
		return nil
	}
	vt := transformVoidTrader(traders, name)
	if vt == nil {
		return nil
	}
	return []interface{}{vt}
}

// ─── Daily Deals transformation ───────────────────────────────────────────────

func transformDailyDeals(deals []map[string]interface{}) []interface{} {
	var result []interface{}
	for _, d := range deals {
		storeItem, _ := d["StoreItem"].(string)
		itemName := storeItem
		if idx := strings.LastIndex(storeItem, "/"); idx >= 0 {
			itemName = storeItem[idx+1:]
		}

		total := getFloat(d, "AmountTotal")
		sold := getFloat(d, "AmountSold")
		orig := getFloat(d, "OriginalPrice")
		sale := getFloat(d, "SalePrice")
		discount := getFloat(d, "Discount")

		entry := map[string]interface{}{
			"id":            deObjectID(d["_id"]),
			"item":          itemName,
			"storeItem":     storeItem,
			"activation":    deMongoTime(d["Activation"]),
			"expiry":        deMongoTime(d["Expiry"]),
			"originalPrice": orig,
			"salePrice":     sale,
			"discount":      discount,
			"total":         total,
			"amountSold":    sold,
			"sold":          sold,
		}
		result = append(result, entry)
	}
	return result
}

// ─── Events transformation ──────────────────────────────────────────────────

func transformEvents(goals []map[string]interface{}, messages []map[string]interface{}) []interface{} {
	var result []interface{}

	// Goals contain running operations like Fomorian, tactical alerts, etc.
	for _, g := range goals {
		desc, _ := g["Desc"].(string)
		if desc == "" {
			continue
		}
		// Shorten path
		if idx := strings.LastIndex(desc, "/"); idx >= 0 {
			desc = desc[idx+1:]
		}

		toolTip, _ := g["ToolTip"].(string)
		if idx := strings.LastIndex(toolTip, "/"); idx >= 0 {
			toolTip = toolTip[idx+1:]
		}

		health := getFloat(g, "HealthPct") * 100

		// Extract rewards
		var rewards []interface{}
		if reward, ok := g["Reward"].(map[string]interface{}); ok {
			r := buildGoalReward(reward)
			if r != nil {
				rewards = append(rewards, r)
			}
		}

		node := lookupNode(getStr(g, "Node"))
		tag, _ := g["Tag"].(string)

		entry := map[string]interface{}{
			"id":          deObjectID(g["_id"]),
			"description": desc,
			"toolTip":     toolTip,
			"node":        node,
			"tag":         tag,
			"activation":  deMongoTime(g["Activation"]),
			"expiry":      deMongoTime(g["Expiry"]),
			"health":      health,
			"rewards":     rewards,
		}
		result = append(result, entry)
	}

	return result
}

// ─── Nightwave transformation ────────────────────────────────────────────────

// nightwaveTagMap maps DE's internal AffiliationTag strings to human-readable
// Nightwave season names shown in the UI.
var nightwaveTagMap = map[string]string{
	// Numbered series
	"RadioLegionSyndicate":  "Nightwave: Series 1 – Children of the Sun",
	"RadioLegion2Syndicate": "Nightwave: Series 2 – The Emissary",
	"RadioLegion3Syndicate": "Nightwave: Series 3 – The Glass Maker",
	// Intermissions
	"RadioLegionIntermissionSyndicate":    "Nightwave: Intermission I",
	"RadioLegionIntermission2Syndicate":   "Nightwave: Intermission II",
	"RadioLegionIntermission3Syndicate":   "Nightwave: Intermission III",
	"RadioLegionIntermission4Syndicate":   "Nightwave: Intermission IV",
	"RadioLegionIntermission5Syndicate":   "Nightwave: Intermission V",
	"RadioLegionIntermission6Syndicate":   "Nightwave: Intermission VI",
	"RadioLegionIntermission7Syndicate":   "Nightwave: Intermission VII",
	"RadioLegionIntermission8Syndicate":   "Nightwave: Intermission VIII",
	"RadioLegionIntermission9Syndicate":   "Nightwave: Intermission IX",
	"RadioLegionIntermission10Syndicate":  "Nightwave: Intermission X",
	"RadioLegionIntermission11Syndicate":  "Nightwave: Intermission XI",
	"RadioLegionIntermission12Syndicate":  "Nightwave: Intermission XII",
	"RadioLegionIntermission13Syndicate":  "Nightwave: Intermission XIII",
	"RadioLegionIntermission14Syndicate":  "Nightwave: Intermission XIV",
	"RadioLegionIntermission15Syndicate":  "Nightwave: Intermission XV",
	"RadioLegionIntermission16Syndicate":  "Nightwave: Intermission XVI",
	"RadioLegionIntermission17Syndicate":  "Nightwave: Intermission XVII",
	"RadioLegionIntermission18Syndicate":  "Nightwave: Intermission XVIII",
	"RadioLegionIntermission19Syndicate":  "Nightwave: Intermission XIX",
	"RadioLegionIntermission20Syndicate":  "Nightwave: Intermission XX",
}

// nightwaveChallengeMap maps known challenge leaf names (after path stripping)
// to their in-game display titles. This covers challenges that benefit from
// exact phrasing, and acts as a fast path before the camelCase fallback.
var nightwaveChallengeMap = map[string]string{
	// ── Daily challenges ────────────────────────────────────────────────────
	"SeasonDailyThePersonalTouch":              "The Personal Touch",
	"SeasonDailyKillEnemiesWithElectricity":    "Kill Enemies with Electricity",
	"SeasonDailySwatter":                       "Swatter",
	"SeasonDailyCompleteMission":               "Complete a Mission",
	"SeasonDailyCompleteMissions3":             "Complete 3 Missions",
	"SeasonDailyKillEnemies150":                "Kill 150 Enemies",
	"SeasonDailyKillEnemies500":                "Kill 500 Enemies",
	"SeasonDailyGatherResources":               "Gather Resources",
	"SeasonDailyKillGrineer":                   "Kill Grineer Enemies",
	"SeasonDailyKillCorpus":                    "Kill Corpus Enemies",
	"SeasonDailyKillInfested":                  "Kill Infested Enemies",
	"SeasonDailyKillCorrputed":                 "Kill Corrupted Enemies",
	"SeasonDailyCompleteSyndicateMission":      "Complete a Syndicate Mission",
	"SeasonDailyDoSomeGoodDeeds":               "Do Some Good Deeds",
	"SeasonDailyRepairGear":                    "Repair Gear",
	"SeasonDailyExtractResources":              "Extract Resources",
	"SeasonDailyHealAlly":                      "Heal an Ally",
	"SeasonDailyUsePowerful":                   "Use a Powerful Ability",
	"SeasonDailyScanEnemy":                     "Scan an Enemy",
	"SeasonDailyShieldBreak":                   "Break Enemy Shields",
	"SeasonDailyKillWithHeadshot":              "Kill with Headshots",
	"SeasonDailyKillWithFinisher":              "Kill with Finisher",
	"SeasonDailyKillWithSlide":                 "Kill with Slide Attack",
	"SeasonDailyKillWithAbility":               "Kill with Ability",
	"SeasonDailyKillEnemiesWithFire":           "Kill Enemies with Fire",
	"SeasonDailyKillEnemiesWithPoison":         "Kill Enemies with Poison",
	"SeasonDailyKillEnemiesWithCold":           "Kill Enemies with Cold",
	"SeasonDailyKillEnemiesWithBlast":          "Kill Enemies with Blast",
	"SeasonDailyKillEnemiesWithCorrode":        "Kill Enemies with Corrosive",
	"SeasonDailyKillWithPrimary":               "Kill with Primary Weapon",
	"SeasonDailyKillWithSecondary":             "Kill with Secondary Weapon",
	"SeasonDailyKillWithMelee":                 "Kill with Melee Weapon",
	// ── Weekly (standard) challenges ────────────────────────────────────────
	"SeasonWeeklyTheOldWays":                   "The Old Ways",
	"SeasonWeeklyMineRareVenusResources":        "Mine Rare Venus Resources",
	"SeasonWeeklyMineRareEarthResources":        "Mine Rare Earth Resources",
	"SeasonWeeklyMineRareMercuryResources":      "Mine Rare Mercury Resources",
	"SeasonWeeklyPermanentCompleteMissions8":    "Complete 8 Missions",
	"SeasonWeeklyPermanentKillEximus8":          "Kill 8 Eximus Enemies",
	"SeasonWeeklyPermanentKillEnemies8":         "Kill Enemies",
	"SeasonWeeklyPermanentCompleteMissions5":    "Complete 5 Missions",
	"SeasonWeeklyPermanentKillEximus5":          "Kill 5 Eximus Enemies",
	"SeasonWeeklyPermanentKillEnemies5":         "Kill Enemies",
	"SeasonWeeklyCompleteSyndicateMissions5":    "Complete 5 Syndicate Missions",
	"SeasonWeeklyKillScorpions":                 "Kill Scorpions",
	"SeasonWeeklyCompleteCaptures3":             "Complete 3 Capture Missions",
	"SeasonWeeklyCompleteDefense3":              "Complete 3 Defense Missions",
	"SeasonWeeklyCompleteSurvival3":             "Complete 3 Survival Missions",
	"SeasonWeeklyCompleteExcavation3":           "Complete 3 Excavation Missions",
	"SeasonWeeklyCompleteInterception3":         "Complete 3 Interception Missions",
	"SeasonWeeklyCatchRareFish":                 "Catch Rare Fish",
	"SeasonWeeklyKillWithStealth":               "Kill with Stealth",
	"SeasonWeeklyCompleteRaid":                  "Complete a Raid",
	"SeasonWeeklyCompleteArbitraion":            "Complete an Arbitration",
	"SeasonWeeklyCompleteNightmareMode":         "Complete a Nightmare Mission",
	"SeasonWeeklyCompleteOrokinDerelict":        "Complete an Orokin Derelict Mission",
	"SeasonWeeklyCraftGear":                     "Craft Gear",
	"SeasonWeeklyKillWithStatuses5":             "Cause 5 Status Effects",
	"SeasonWeeklyKillWithStatuses3":             "Cause 3 Status Effects",
	"SeasonWeeklyKillCorpusWithPoison":          "Kill Corpus with Poison",
	"SeasonWeeklyKillGrineerWithFire":           "Kill Grineer with Fire",
	"SeasonWeeklyFishAndCut":                    "Fish and Process Catches",
	"SeasonWeeklyHarvestPlants":                 "Harvest Plants",
	"SeasonWeeklyKillHighLevel":                 "Kill High-Level Enemies",
	"SeasonWeeklyCompleteVoidFissure3":          "Complete 3 Void Fissures",
	"SeasonWeeklyCompleteVoidFissure5":          "Complete 5 Void Fissures",
	"SeasonWeeklyCaptureTargets":                "Capture Targets",
	"SeasonWeeklyCompleteAnyMission":            "Complete Any Mission",
	"SeasonWeeklyCompleteAlert":                 "Complete an Alert",
	"SeasonWeeklyBuildModularItem":              "Build a Modular Item",
	"SeasonWeeklyLevelItem":                     "Level Up a Weapon or Warframe",
	"SeasonWeeklyCatchFishEidolon":              "Catch Fish on the Plains of Eidolon",
	"SeasonWeeklyCatchFishOrb":                  "Catch Fish in the Orb Vallis",
	// ── Weekly Hard (Elite) challenges ──────────────────────────────────────
	"SeasonWeeklyHardKillOrCaptureRainalyst":   "Kill or Capture Rainalyst",
	"SeasonWeeklyHardCompleteConquest":          "Complete Conquest",
	"SeasonWeeklyHardKillOrCaptureThumper":      "Kill or Capture Thumper",
	"SeasonWeeklyHardCompleteSortie":            "Complete a Sortie",
	"SeasonWeeklyHardCompleteOrokinRaid":        "Complete an Orokin Vault Run",
	"SeasonWeeklyHardCompleteRaid":              "Complete a Raid (Hard)",
	"SeasonWeeklyHardKillOrCaptureWolves":       "Kill or Capture Wolfs",
	"SeasonWeeklyHardKillHighLevel":             "Kill Level 60+ Enemies",
	"SeasonWeeklyHardKillWithStatuses10":        "Cause 10 Status Effects",
	"SeasonWeeklyHardPurifyMaps":               "Purify Kuva",
	"SeasonWeeklyHardCompleteArchonHunt":        "Complete an Archon Hunt",
	"SeasonWeeklyHardCompleteArbitration":       "Complete an Arbitration",
	"SeasonWeeklyHardCompleteVoidStorm":         "Complete 3 Void Storms",
	"SeasonWeeklyHardCompleteVoidFissure":       "Complete 5 Void Fissures",
	"SeasonWeeklyHardCompleteSteelPath":         "Complete a Steel Path Mission",
	"SeasonWeeklyHardKillStalker":               "Kill the Stalker",
	"SeasonWeeklyHardKillAscendant":             "Kill an Ascendant",
}

// nightwaveChallengeTitle converts a challenge leaf name (already stripped of
// the Lotus path) into a human-readable title.
//
// Priority:
//  1. Exact match in nightwaveChallengeMap
//  2. Strip well-known prefixes, then split CamelCase words
func nightwaveChallengeTitle(leaf string) string {
	if v, ok := nightwaveChallengeMap[leaf]; ok {
		return v
	}
	// Strip known prefixes to get the semantic part
	for _, pfx := range []string{
		"SeasonWeeklyHard",
		"SeasonWeeklyPermanent",
		"SeasonWeekly",
		"SeasonDaily",
		"Season",
	} {
		if strings.HasPrefix(leaf, pfx) {
			leaf = leaf[len(pfx):]
			break
		}
	}
	return splitCamelCase(leaf)
}

// splitCamelCase inserts a space before each uppercase letter that follows a
// lowercase letter or digit, turning "ThePersonalTouch" → "The Personal Touch".
func splitCamelCase(s string) string {
	if s == "" {
		return s
	}
	var out []rune
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			// Insert space when transitioning lower→upper or digit→upper
			if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') {
				out = append(out, ' ')
			}
		}
		out = append(out, r)
	}
	return string(out)
}

// nightwaveSeasonName converts an AffiliationTag such as
// "RadioLegionIntermission15Syndicate" into a display name.
// Falls back to the raw tag if not in the map.
func nightwaveSeasonName(tag string) string {
	if v, ok := nightwaveTagMap[tag]; ok {
		return v
	}
	return tag
}

func transformNightwave(si map[string]interface{}) interface{} {
	if si == nil {
		return nil
	}
	affTag, _ := si["AffiliationTag"].(string)
	season := getFloat(si, "Season")

	challengesRaw, _ := si["ActiveChallenges"].([]interface{})
	var challenges []map[string]interface{}
	for _, cr := range challengesRaw {
		cm, ok := cr.(map[string]interface{})
		if !ok {
			continue
		}
		isDaily := false
		if d, ok := cm["Daily"].(bool); ok {
			isDaily = d
		}
		isElite := false
		category, _ := cm["Category"].(string)
		if strings.Contains(category, "Elite") || strings.Contains(category, "ELITE") {
			isElite = true
		}

		challengePath, _ := cm["Challenge"].(string)
		leaf := challengePath
		if idx := strings.LastIndex(challengePath, "/"); idx >= 0 {
			leaf = challengePath[idx+1:]
		}
		title := nightwaveChallengeTitle(leaf)

		reputation := 0.0
		if params, ok := cm["Params"].([]interface{}); ok {
			for _, p := range params {
				if pm, ok := p.(map[string]interface{}); ok {
					if pm["n"] == "reputation" || pm["n"] == "ScriptParamValue" {
						reputation, _ = pm["v"].(float64)
					}
				}
			}
		}
		if isDaily && reputation == 0 {
			reputation = 1000
		} else if !isDaily && !isElite && reputation == 0 {
			reputation = 4500
		} else if isElite && reputation == 0 {
			reputation = 7500
		}

		challenges = append(challenges, map[string]interface{}{
			"id":         deObjectID(cm["_id"]),
			"isDaily":    isDaily,
			"isElite":    isElite,
			"title":      title,
			"desc":       title,
			"reputation": reputation,
			"activation": deMongoTime(cm["Activation"]),
			"expiry":     deMongoTime(cm["Expiry"]),
		})
	}

	return map[string]interface{}{
		"id":               deObjectID(si["_id"]),
		"activation":       deMongoTime(si["Activation"]),
		"expiry":           deMongoTime(si["Expiry"]),
		"season":           season,
		"tag":              nightwaveSeasonName(affTag),
		"affiliationTag":   affTag,
		"activeChallenges": challenges,
	}
}

// ─── Syndicate Missions ──────────────────────────────────────────────────────

func transformSyndicateMissions(missions []map[string]interface{}) []interface{} {
	var result []interface{}
	for _, m := range missions {
		nodesRaw, _ := m["Nodes"].([]interface{})
		var nodes []string
		for _, n := range nodesRaw {
			if s, ok := n.(string); ok {
				nodes = append(nodes, lookupNode(s))
			}
		}

		entry := map[string]interface{}{
			"id":         deObjectID(m["_id"]),
			"activation": deMongoTime(m["Activation"]),
			"expiry":     deMongoTime(m["Expiry"]),
			"tag":        getStr(m, "Tag"),
			"seed":       getFloat(m, "Seed"),
			"nodes":      nodes,
		}
		result = append(result, entry)
	}
	return result
}

// ─── Global Upgrades ────────────────────────────────────────────────────────

func transformGlobalUpgrades(upgrades []map[string]interface{}) []interface{} {
	var result []interface{}
	for _, u := range upgrades {
		entry := map[string]interface{}{
			"id":         deObjectID(u["_id"]),
			"activation": deMongoTime(u["Activation"]),
			"expiry":     deMongoTime(u["Expiry"]),
			"upgrade":    getStr(u, "Upgrade"),
			"operation":  getStr(u, "Operation"),
			"operationType": getStr(u, "OperationType"),
		}
		result = append(result, entry)
	}
	return result
}

// ─── Conclave Challenges ────────────────────────────────────────────────────

func transformConclaveChallenges(challenges []map[string]interface{}) []interface{} {
	var result []interface{}
	for _, c := range challenges {
		category, _ := c["Category"].(string)
		isDaily := strings.Contains(category, "DAILY")
		pvpMode, _ := c["PVPMode"].(string)

		challengeType, _ := c["challengeTypeRefID"].(string)
		desc := challengeType
		if idx := strings.LastIndex(challengeType, "/"); idx >= 0 {
			desc = challengeType[idx+1:]
		}

		entry := map[string]interface{}{
			"id":                deObjectID(c["_id"]),
			"activation":        deMongoTime(c["startDate"]),
			"expiry":            deMongoTime(c["endDate"]),
			"description":       desc,
			"category":          category,
			"daily":             isDaily,
			"mode":              pvpMode,
			"rootChallenge":     true,
		}
		result = append(result, entry)
	}
	return result
}

// ─── Construction Progress ───────────────────────────────────────────────────

func transformConstruction(projects []map[string]interface{}, pct []interface{}) map[string]interface{} {
	fomorianPct := "0%"
	razorbackPct := "0%"

	if len(pct) > 0 {
		if v, ok := pct[0].(float64); ok {
			fomorianPct = fmt.Sprintf("%.1f%%", v*100)
		}
	}
	if len(pct) > 1 {
		if v, ok := pct[1].(float64); ok {
			razorbackPct = fmt.Sprintf("%.1f%%", v*100)
		}
	}

	// Also check ConstructionProjects for more details
	for _, p := range projects {
		tag, _ := p["Tag"].(string)
		pctVal := getFloat(p, "Pct")
		if strings.Contains(tag, "Fomorian") || strings.Contains(tag, "FOMORIAN") {
			fomorianPct = fmt.Sprintf("%.1f%%", pctVal*100)
		} else if strings.Contains(tag, "Razorback") || strings.Contains(tag, "RAZORBACK") {
			razorbackPct = fmt.Sprintf("%.1f%%", pctVal*100)
		}
	}

	return map[string]interface{}{
		"id":               "",
		"fomorianProgress": fomorianPct,
		"razorbackProgress": razorbackPct,
	}
}

// ─── Sentient Outposts ──────────────────────────────────────────────────────

func transformSentientOutposts(enemies []map[string]interface{}) []interface{} {
	var result []interface{}
	for _, e := range enemies {
		agentType, _ := e["AgentType"].(string)
		// Skip Steel Path Acolytes — they belong in steelPath/incursions
		if isSteelPathAcolyte(agentType) {
			continue
		}
		entry := map[string]interface{}{
			"id":         deObjectID(e["_id"]),
			"activation": deMongoTime(e["Activation"]),
			"expiry":     deMongoTime(e["Expiry"]),
			"agentType":  agentType,
			"location":   lookupNode(getStr(e, "Location")),
			"node":       lookupNode(getStr(e, "Node")),
			"health":     getFloat(e, "HealthPct"),
		}
		result = append(result, entry)
	}
	return result
}

// ─── Steel Path ────────────────────────────────────────────────────────────────

// isSteelPathAcolyte checks if a PersistentEnemy agent type is a Steel Path Acolyte.
// Steel Path Acolytes (Torment, Malice, Angst, Mania, Stalker) spawn on Steel Path
// nodes and drop Steel Essence. They appear in the DE API's PersistentEnemies array
// alongside Sentient Outposts and other persistent enemies.
func isSteelPathAcolyte(agentType string) bool {
	return strings.Contains(agentType, "/Acolyte") || strings.Contains(agentType, "/Stalker")
}

// transformSteelPath builds the Steel Path Honors response by combining:
// 1. Shop rotation data (currentReward, evergreens, rotation) fetched from the
//    WFCD community API (api.warframestat.us/pc/steelPath), which follows redirects.
// 2. Active Acolyte incursions from the DE endpoint's PersistentEnemies array.
//
// The WFCD data provides the weekly shop rotation (not available from the DE endpoint).
// The PersistentEnemies data provides real-time Acolyte spawn locations when active.
func transformSteelPath(persistentEnemies []map[string]interface{}) interface{} {
	// Start with a sensible default structure
	result := map[string]interface{}{
		"currentReward": nil,
		"activation":    "",
		"expiry":        "",
		"remaining":     "",
		"rotation":      []interface{}{},
		"evergreens":    []interface{}{},
		"incursions":    nil,
	}

	// Try to fetch from WFCD API for shop rotation data.
	// Note: hardcoded to "pc" platform; the DE endpoint shares the same limitation
	// (deApiURL is also single-platform).
	resp, err := wfcdHTTPClient.Get("https://api.warframestat.us/pc/steelPath")
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		body, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			var wfcdData map[string]interface{}
			if json.Unmarshal(body, &wfcdData) == nil && wfcdData != nil {
				for k, v := range wfcdData {
					result[k] = v
				}
			}
		}
	} else if err == nil && resp != nil {
		resp.Body.Close()
	}

	// Build incursions from PersistentEnemies (Steel Path Acolytes)
	// Each Steel Path Acolyte (Torment, Malice, Angst, Mania, Stalker) spawns on
	// a specific Steel Path node and drops Steel Essence when killed by the squad.
	var acolytes []map[string]interface{}
	for _, pe := range persistentEnemies {
		agentType, _ := pe["AgentType"].(string)
		if isSteelPathAcolyte(agentType) {
			acolytes = append(acolytes, map[string]interface{}{
				"id":         deObjectID(pe["_id"]),
				"agentType":  agentType,
				"name":       extractAcolyteName(agentType),
				"node":       lookupNode(getStr(pe, "Node")),
				"location":   lookupNode(getStr(pe, "Location")),
				"health":     getFloat(pe, "HealthPct"),
				"activation": deMongoTime(pe["Activation"]),
				"expiry":     deMongoTime(pe["Expiry"]),
			})
		}
	}

	if len(acolytes) > 0 {
		result["incursions"] = map[string]interface{}{
			"acolytes":   acolytes,
			"active":     true,
			"count":      len(acolytes),
			"activation": deMongoTime(persistentEnemies[0]["Activation"]),
			"expiry":     deMongoTime(persistentEnemies[0]["Expiry"]),
		}
	} else if currentInc, ok := result["incursions"].(map[string]interface{}); ok && currentInc != nil {
		// Keep WFCD incursions timing if available, just mark not active
		currentInc["active"] = false
		currentInc["acolytes"] = []interface{}{}
	}

	// Enrich rewards with arcane effect descriptions from arcanes.json
	enrichWithArcaneEffect := func(item map[string]interface{}) {
		if name, ok := item["name"].(string); ok && name != "" {
			if effect, found := arcaneNameMap[strings.ToLower(name)]; found {
				item["effect"] = effect
			}
		}
	}
	if cr, ok := result["currentReward"].(map[string]interface{}); ok && cr != nil {
		enrichWithArcaneEffect(cr)
	}
	if rotation, ok := result["rotation"].([]interface{}); ok {
		for i := range rotation {
			if item, ok := rotation[i].(map[string]interface{}); ok {
				enrichWithArcaneEffect(item)
			}
		}
	}

	return result
}

// extractAcolyteName pulls a human-readable name from the Acolyte agent type path.
// DE paths look like: /Lotus/Space/PersistentEnemies/Acolytes/AcolyteTorment
func extractAcolyteName(agentType string) string {
	parts := strings.Split(agentType, "/")
	if len(parts) == 0 {
		return "Unknown"
	}
	name := parts[len(parts)-1]
	// Strip "Acolyte" prefix for cleaner names
	name = strings.TrimPrefix(name, "Acolyte")
	if name == "" {
		return "Unknown"
	}
	return name
}

// ─── Deep Archimedea (Descents) ─────────────────────────────────────────────

func transformDescents(descents []map[string]interface{}) interface{} {
	if len(descents) == 0 {
		return nil
	}
	d := descents[0]

	challengesRaw, _ := d["Challenges"].([]interface{})
	var missions []map[string]interface{}
	for _, cr := range challengesRaw {
		cm, ok := cr.(map[string]interface{})
		if !ok {
			continue
		}
		challengeType, _ := cm["Type"].(string)
		challenge, _ := cm["Challenge"].(string)
		level, _ := cm["Level"].(string)

		// Extract auras as personal modifiers
		var modifiers []string
		if auras, ok := cm["Auras"].([]interface{}); ok {
			for _, a := range auras {
				if s, ok := a.(string); ok {
					if idx := strings.LastIndex(s, "/"); idx >= 0 {
						modifiers = append(modifiers, s[idx+1:])
					} else {
						modifiers = append(modifiers, s)
					}
				}
			}
		}

		missions = append(missions, map[string]interface{}{
			"type":             challengeType,
			"challenge":        challenge,
			"node":             lookupNode(level),
			"location":         lookupNode(level),
			"personalModifiers": modifiers,
		})
	}

	return map[string]interface{}{
		"activation": deMongoTime(d["Activation"]),
		"expiry":     deMongoTime(d["Expiry"]),
		"seed":       getFloat(d, "RandSeed"),
		"missions":   missions,
	}
}

// ─── News ─────────────────────────────────────────────────────────────────────

func transformNews(events []map[string]interface{}) []interface{} {
	var result []interface{}
	for _, e := range events {
		messages, _ := e["Messages"].([]interface{})
		message := ""
		for _, mr := range messages {
			if mm, ok := mr.(map[string]interface{}); ok {
				if lang, _ := mm["LanguageCode"].(string); lang == "en" {
					message, _ = mm["Message"].(string)
					if idx := strings.LastIndex(message, "/"); idx >= 0 {
						message = message[idx+1:]
					}
					break
				}
			}
		}
		if message == "" {
			continue
		}

		entry := map[string]interface{}{
			"id":      deObjectID(e["_id"]),
			"message": message,
			"date":    deMongoTime(e["_id"]), // approximate
			"link":    "",
		}
		result = append(result, entry)
	}
	return result
}

// ─── Cycle computation (when HubEvents is empty) ──────────────────────────────

func computeCetusCycle() map[string]interface{} {
	return computeGenericCycle("cetus", cetusEpoch, cetusCycleDuration, cetusDayDuration,
		"isDay", "day", "night", "timeLeft")
}

func computeEarthCycle() map[string]interface{} {
	return computeGenericCycle("earth", cetusEpoch, earthCycleDuration, cetusDayDuration,
		"isDay", "day", "night", "timeLeft")
}

func computeVallisCycle() map[string]interface{} {
	return computeGenericCycle("vallis", vallisEpoch, vallisCycleDuration, vallisWarmDuration,
		"isWarm", "warm", "cold", "timeLeft")
}

func computeCambionCycle() map[string]interface{} {
	now := time.Now().Unix()
	elapsed := now - cambionEpoch
	if elapsed < 0 {
		elapsed = 0
	}
	cyclePos := elapsed % cambionCycleDuration
	halfCycle := int64(cambionCycleDuration / 2)
	isFass := cyclePos < halfCycle
	state := "fass"
	if !isFass {
		state = "vome"
	}
	timeLeft := halfCycle - (cyclePos % halfCycle)

	expiry := time.Now().UTC().Add(time.Duration(timeLeft) * time.Second).Format(time.RFC3339)

	return map[string]interface{}{
		"id":       "cambionCycle",
		"active":   state,
		"state":    state,
		"timeLeft": fmt.Sprintf("%dh %dm %ds", timeLeft/3600, (timeLeft%3600)/60, timeLeft%60),
		"expiry":   expiry,
	}
}

func computeGenericCycle(name string, epoch, duration, phaseDuration int64, dayKey, dayLabel, nightLabel, timeLeftKey string) map[string]interface{} {
	now := time.Now().Unix()
	elapsed := now - epoch
	if elapsed < 0 {
		elapsed = 0
	}
	cyclePos := elapsed % duration
	isDay := cyclePos < phaseDuration
	timeLeft := phaseDuration - cyclePos
	if !isDay {
		timeLeft = duration - cyclePos
	}

	state := dayLabel
	if !isDay {
		state = nightLabel
	}

	expiry := time.Now().UTC().Add(time.Duration(timeLeft) * time.Second).Format(time.RFC3339)

	result := map[string]interface{}{
		"id":         name + "Cycle",
		"state":      state,
		"timeLeft":   fmt.Sprintf("%dh %dm %ds", timeLeft/3600, (timeLeft%3600)/60, timeLeft%60),
		"expiry":     expiry,
		dayKey:       isDay,
	}
	return result
}

// ─── Arbitration (best-effort) ──────────────────────────────────────────────

func computeArbitration(missions []map[string]interface{}) interface{} {
	// Arbitration missions don't have a clear marker in DE ActiveMissions.
	// They're typically Survival, Defense, Interception, Excavation, or Disruption
	// on specific nodes. Without a dedicated field, we return null.
	// The arbitration field is optional for the scheduler to work.
	return nil
}

// ─── Reward helpers ─────────────────────────────────────────────────────────

func buildReward(mi map[string]interface{}) map[string]interface{} {
	if mi == nil {
		return nil
	}
	// Try to extract rewards from MissionInfo
	reward := map[string]interface{}{
		"asString":     "",
		"items":        []interface{}{},
		"countedItems": []interface{}{},
		"credits":      0,
		"itemString":   "",
	}

	// Check for countdItems at the mission level
	if countedItems, ok := mi["countedItems"].([]interface{}); ok {
		var items []string
		for _, ci := range countedItems {
			if item, ok := ci.(map[string]interface{}); ok {
				itemType, _ := item["ItemType"].(string)
				itemCount, _ := item["ItemCount"].(float64)
				name := itemType
				if idx := strings.LastIndex(itemType, "/"); idx >= 0 {
					name = itemType[idx+1:]
				}
				items = append(items, fmt.Sprintf("%dx %s", int(itemCount), name))
			}
		}
		if len(items) > 0 {
			reward["asString"] = strings.Join(items, ", ")
			reward["itemString"] = strings.Join(items, ", ")
		}
	}

	return reward
}

func buildGoalReward(reward map[string]interface{}) map[string]interface{} {
	if reward == nil {
		return nil
	}
	credits := getFloat(reward, "credits")

	var items []string
	if itemsRaw, ok := reward["items"].([]interface{}); ok {
		for _, ir := range itemsRaw {
			if s, ok := ir.(string); ok {
				if idx := strings.LastIndex(s, "/"); idx >= 0 {
					items = append(items, s[idx+1:])
				} else {
					items = append(items, s)
				}
			}
		}
	}

	return map[string]interface{}{
		"asString": strings.Join(items, ", "),
		"items":    items,
		"credits":  credits,
	}
}

func extractCountedReward(v interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	rm, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	var items []string
	if countedItems, ok := rm["countedItems"].([]interface{}); ok {
		for _, ci := range countedItems {
			if item, ok := ci.(map[string]interface{}); ok {
				itemType, _ := item["ItemType"].(string)
				itemCount, _ := item["ItemCount"].(float64)
				name := itemType
				if idx := strings.LastIndex(itemType, "/"); idx >= 0 {
					name = itemType[idx+1:]
				}
				items = append(items, fmt.Sprintf("%dx %s", int(itemCount), name))
			}
		}
	}
	return map[string]interface{}{
		"asString":   strings.Join(items, ", "),
		"items":      items,
		"itemString": strings.Join(items, ", "),
	}
}

// ─── Generic helpers ──────────────────────────────────────────────────────────

func getStr(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func getFloat(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}
