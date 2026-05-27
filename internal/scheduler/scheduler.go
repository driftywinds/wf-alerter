package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"warframe-portal/internal/db"
	"warframe-portal/internal/notifier"
	"warframe-portal/internal/warframe"
)

// Scheduler polls the Warframe API and fires alert notifications.
type Scheduler struct {
	db         *db.DB
	wf         *warframe.Client
	appriseURL string
}

// New creates a Scheduler.
func New(database *db.DB, wfClient *warframe.Client, appriseURL string) *Scheduler {
	return &Scheduler{
		db:         database,
		wf:         wfClient,
		appriseURL: appriseURL,
	}
}

// Run starts the polling loop; call in a goroutine.
func (s *Scheduler) Run() {
	log.Println("Scheduler: starting")
	s.tick() // run immediately

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.tick()
	}
}

func (s *Scheduler) tick() {
	rules, err := s.db.GetAllEnabledAlertRules()
	if err != nil {
		log.Printf("Scheduler: get rules: %v", err)
		return
	}
	if len(rules) == 0 {
		return
	}

	ws, err := s.wf.GetWorldstate()
	if err != nil {
		log.Printf("Scheduler: get worldstate: %v", err)
		return
	}

	now := time.Now().UTC()

	for _, rule := range rules {
		switch rule.EventType {
		case "fissure":
			s.checkFissures(rule, ws)
		case "sortie":
			s.checkSortie(rule, ws)
		case "arbitration":
			s.checkArbitration(rule, ws)
		case "event":
			s.checkEvents(rule, ws)
		case "voidTrader":
			s.checkVoidTrader(rule, ws)
		case "archonHunt":
			s.checkArchonHunt(rule, ws)
		case "nightwave":
			s.checkNightwave(rule, ws)
		case "invasion":
			s.checkInvasions(rule, ws)
		case "alert":
			s.checkGameAlerts(rule, ws)
		case "steelPath":
			s.checkSteelPath(rule, ws)
		case "dailyReset":
			s.checkDailyReset(rule, now)
		case "weeklyReset":
			s.checkWeeklyReset(rule, now)
		}
	}
}

// ─── Condition types ─────────────────────────────────────────────────────────

// AlertCondition holds the filter criteria parsed from a rule's JSON conditions.
type AlertCondition struct {
	// Fissure / Arbitration / Invasion
	Tier        string `json:"tier"`        // "Lith","Meso","Neo","Axi","Requiem","Omnia" – empty=any
	MissionType string `json:"missionType"` // "Defense","Survival",… – empty=any
	IsHard      string `json:"isHard"`      // "true","false","" (Steel Path)
	IsStorm     string `json:"isStorm"`     // "true","false","" (Void Storm fissures)
	Enemy       string `json:"enemy"`       // substring match on enemy faction

	// Event / Invasion / Alert / Steel Path
	Keyword        string `json:"keyword"`        // substring match on description / node / acolyte name
	RotationAlert  string `json:"rotationAlert"`  // "true","false" — Steel Path: if false, skip acolyte alerts

	// Void Trader
	ItemKeyword string `json:"itemKeyword"` // substring match on trader inventory item names
}

func parseCond(condJSON string) AlertCondition {
	var c AlertCondition
	if condJSON == "" {
		condJSON = "{}"
	}
	_ = json.Unmarshal([]byte(condJSON), &c)
	return c
}

// ─── Notification helpers ─────────────────────────────────────────────────────

func (s *Scheduler) notifURLs(rule *db.AlertRule) (urls []string, apiURL string) {
	apiURL = s.appriseURL
	raw := rule.AppriseURLs

	user, err := s.db.GetUserByID(rule.UserID)
	if err == nil && user != nil {
		if user.AppriseAPIURL != "" {
			apiURL = user.AppriseAPIURL
		}
		if raw == "" {
			raw = user.DefaultURLs
		}
	}

	for _, u := range strings.Split(raw, "\n") {
		u = strings.TrimSpace(u)
		if u != "" {
			urls = append(urls, u)
		}
	}
	return
}

func (s *Scheduler) fire(rule *db.AlertRule, title, message string) {
	// Cooldown check
	if rule.LastFired != nil {
		if time.Since(*rule.LastFired) < time.Duration(rule.CooldownMinutes)*time.Minute {
			return
		}
	}

	urls, apiURL := s.notifURLs(rule)
	logMsg := fmt.Sprintf("[%s] %s", title, message)

	if len(urls) == 0 {
		log.Printf("Scheduler: rule %d (%s): no URLs – skipping", rule.ID, rule.Name)
		_ = s.db.CreateAlertLog(&db.AlertLog{
			AlertRuleID: rule.ID, UserID: rule.UserID, RuleName: rule.Name,
			Message: logMsg + " (skipped: no notification URLs)",
		})
		return
	}

	n := notifier.New(apiURL)
	err := n.Send(notifier.Notification{Title: title, Body: message, URLs: urls})

	entry := &db.AlertLog{
		AlertRuleID: rule.ID, UserID: rule.UserID,
		RuleName: rule.Name, Message: logMsg,
	}
	if err != nil {
		entry.Message += fmt.Sprintf(" (ERROR: %v)", err)
		log.Printf("Scheduler: rule %d: send failed: %v", rule.ID, err)
	} else {
		_ = s.db.UpdateAlertRuleLastFired(rule.ID, time.Now())
		log.Printf("Scheduler: rule %d (%s) fired: %s", rule.ID, rule.Name, title)
	}
	_ = s.db.CreateAlertLog(entry)
}

// ─── Fissures ────────────────────────────────────────────────────────────────

type fissure struct {
	ID          string `json:"id"`
	Node        string `json:"node"`
	MissionType string `json:"missionType"`
	Enemy       string `json:"enemy"`
	Tier        string `json:"tier"`
	TierNum     int    `json:"tierNum"`
	IsHard      bool   `json:"isHard"`
	IsStorm     bool   `json:"isStorm"`
	Expired     bool   `json:"expired"`
}

func (s *Scheduler) checkFissures(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var fissures []fissure
	if err := json.Unmarshal(ws["fissures"], &fissures); err != nil {
		return
	}
	cond := parseCond(rule.Conditions)

	seenKey := fmt.Sprintf("fissure_seen_%d", rule.ID)
	seenJSON, _ := s.db.GetState(seenKey)
	seen := map[string]bool{}
	if seenJSON != "" {
		var ids []string
		_ = json.Unmarshal([]byte(seenJSON), &ids)
		for _, id := range ids {
			seen[id] = true
		}
	}

	var newIDs []string
	for _, f := range fissures {
		if f.Expired {
			continue
		}
		newIDs = append(newIDs, f.ID)
		if seen[f.ID] {
			continue
		}
		if !matchFissure(f, cond) {
			continue
		}
		hard := ""
		if f.IsHard {
			hard = " [Steel Path]"
		}
		storm := ""
		if f.IsStorm {
			storm = " [Void Storm]"
		}
		title := fmt.Sprintf("🌀 Fissure: %s", rule.Name)
		msg := fmt.Sprintf("%s | %s | %s%s%s", f.Tier, f.Node, f.MissionType, hard, storm)
		s.fire(rule, title, msg)
	}

	b, _ := json.Marshal(newIDs)
	_ = s.db.SetState(seenKey, string(b))
}

func matchFissure(f fissure, c AlertCondition) bool {
	if c.Tier != "" && !strings.EqualFold(f.Tier, c.Tier) {
		return false
	}
	if c.MissionType != "" && !strings.EqualFold(f.MissionType, c.MissionType) {
		return false
	}
	if c.IsHard == "true" && !f.IsHard {
		return false
	}
	if c.IsHard == "false" && f.IsHard {
		return false
	}
	if c.IsStorm == "true" && !f.IsStorm {
		return false
	}
	if c.IsStorm == "false" && f.IsStorm {
		return false
	}
	if c.Enemy != "" && !strings.Contains(strings.ToLower(f.Enemy), strings.ToLower(c.Enemy)) {
		return false
	}
	return true
}

// ─── Sortie ──────────────────────────────────────────────────────────────────

type sortie struct {
	ID   string `json:"id"`
	Boss string `json:"boss"`
}

func (s *Scheduler) checkSortie(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var st sortie
	if err := json.Unmarshal(ws["sortie"], &st); err != nil || st.ID == "" {
		return
	}
	key := fmt.Sprintf("sortie_seen_%d", rule.ID)
	last, _ := s.db.GetState(key)
	if last == st.ID {
		return
	}
	_ = s.db.SetState(key, st.ID)
	s.fire(rule, fmt.Sprintf("⚔️ New Sortie: %s", rule.Name),
		fmt.Sprintf("New Sortie is live! Boss: %s", st.Boss))
}

// ─── Arbitration ─────────────────────────────────────────────────────────────

type arbitration struct {
	Node        string `json:"node"`
	MissionType string `json:"missionType"`
	Enemy       string `json:"enemy"`
	Expiry      string `json:"expiry"`
}

func (s *Scheduler) checkArbitration(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var arb arbitration
	if err := json.Unmarshal(ws["arbitration"], &arb); err != nil || arb.Node == "" {
		return
	}
	cond := parseCond(rule.Conditions)
	if cond.MissionType != "" && !strings.EqualFold(arb.MissionType, cond.MissionType) {
		return
	}
	if cond.Enemy != "" && !strings.Contains(strings.ToLower(arb.Enemy), strings.ToLower(cond.Enemy)) {
		return
	}

	key := fmt.Sprintf("arb_seen_%d", rule.ID)
	fingerprint := arb.Node + "|" + arb.Expiry
	last, _ := s.db.GetState(key)
	if last == fingerprint {
		return
	}
	_ = s.db.SetState(key, fingerprint)
	s.fire(rule, fmt.Sprintf("🎯 Arbitration: %s", rule.Name),
		fmt.Sprintf("%s | %s | %s", arb.Node, arb.MissionType, arb.Enemy))
}

// ─── Events ───────────────────────────────────────────────────────────────────

type event struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Node        string `json:"node"`
}

func (s *Scheduler) checkEvents(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var events []event
	if err := json.Unmarshal(ws["events"], &events); err != nil {
		return
	}
	cond := parseCond(rule.Conditions)

	key := fmt.Sprintf("events_seen_%d", rule.ID)
	seenJSON, _ := s.db.GetState(key)
	seen := map[string]bool{}
	if seenJSON != "" {
		var ids []string
		_ = json.Unmarshal([]byte(seenJSON), &ids)
		for _, id := range ids {
			seen[id] = true
		}
	}

	var newIDs []string
	for _, e := range events {
		newIDs = append(newIDs, e.ID)
		if seen[e.ID] {
			continue
		}
		if cond.Keyword != "" {
			kw := strings.ToLower(cond.Keyword)
			if !strings.Contains(strings.ToLower(e.Description), kw) &&
				!strings.Contains(strings.ToLower(e.Node), kw) {
				continue
			}
		}
		s.fire(rule, fmt.Sprintf("📣 Event: %s", rule.Name),
			fmt.Sprintf("New Event: %s @ %s", e.Description, e.Node))
	}
	b, _ := json.Marshal(newIDs)
	_ = s.db.SetState(key, string(b))
}

// ─── Void Trader ──────────────────────────────────────────────────────────────

type voidTrader struct {
	Active   bool   `json:"active"`
	Location string `json:"location"`
	Inventory []struct {
		Item string `json:"item"`
	} `json:"inventory"`
}

func (s *Scheduler) checkVoidTrader(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var vt voidTrader
	if err := json.Unmarshal(ws["voidTrader"], &vt); err != nil {
		return
	}
	cond := parseCond(rule.Conditions)

	key := fmt.Sprintf("vt_seen_%d", rule.ID)
	last, _ := s.db.GetState(key)

	if !vt.Active {
		if last == "active" {
			_ = s.db.SetState(key, "gone")
		}
		return
	}

	// Trader is active
	if last == "active" {
		// Already notified for this visit; check item keyword
		if cond.ItemKeyword != "" {
			kw := strings.ToLower(cond.ItemKeyword)
			for _, inv := range vt.Inventory {
				if strings.Contains(strings.ToLower(inv.Item), kw) {
					itemKey := fmt.Sprintf("vt_item_%d_%s", rule.ID, inv.Item)
					itemSeen, _ := s.db.GetState(itemKey)
					if itemSeen == "" {
						_ = s.db.SetState(itemKey, "1")
						s.fire(rule, fmt.Sprintf("🛒 Void Trader Item: %s", rule.Name),
							fmt.Sprintf("Baro has '%s' at %s!", inv.Item, vt.Location))
					}
				}
			}
		}
		return
	}

	_ = s.db.SetState(key, "active")
	s.fire(rule, fmt.Sprintf("🛒 Void Trader Arrived: %s", rule.Name),
		fmt.Sprintf("Baro Ki'Teer has arrived at %s!", vt.Location))
}

// ─── Archon Hunt ──────────────────────────────────────────────────────────────

type archonHunt struct {
	Boss   string `json:"boss"`
	Expiry string `json:"expiry"`
}

func (s *Scheduler) checkArchonHunt(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var ah archonHunt
	if err := json.Unmarshal(ws["archonHunt"], &ah); err != nil || ah.Boss == "" {
		return
	}
	key := fmt.Sprintf("archon_seen_%d", rule.ID)
	fingerprint := ah.Boss + "|" + ah.Expiry
	last, _ := s.db.GetState(key)
	if last == fingerprint {
		return
	}
	_ = s.db.SetState(key, fingerprint)
	s.fire(rule, fmt.Sprintf("🦅 Archon Hunt: %s", rule.Name),
		fmt.Sprintf("New Archon Hunt is live! Boss: %s", ah.Boss))
}

// ─── Nightwave ────────────────────────────────────────────────────────────────

type nightwave struct {
	Tag    string `json:"tag"`
	Expiry string `json:"expiry"`
}

func (s *Scheduler) checkNightwave(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var nw nightwave
	if err := json.Unmarshal(ws["nightwave"], &nw); err != nil || nw.Tag == "" {
		return
	}
	key := fmt.Sprintf("nw_seen_%d", rule.ID)
	last, _ := s.db.GetState(key)
	if last == nw.Tag {
		return
	}
	_ = s.db.SetState(key, nw.Tag)
	s.fire(rule, fmt.Sprintf("🌊 Nightwave: %s", rule.Name),
		fmt.Sprintf("Nightwave season '%s' is now active!", nw.Tag))
}

// ─── Invasions ───────────────────────────────────────────────────────────────

type invasion struct {
	ID          string `json:"id"`
	Node        string `json:"node"`
	Desc        string `json:"desc"`
	Completed   bool   `json:"completed"`
}

func (s *Scheduler) checkInvasions(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var invasions []invasion
	if err := json.Unmarshal(ws["invasions"], &invasions); err != nil {
		return
	}
	cond := parseCond(rule.Conditions)

	key := fmt.Sprintf("inv_seen_%d", rule.ID)
	seenJSON, _ := s.db.GetState(key)
	seen := map[string]bool{}
	if seenJSON != "" {
		var ids []string
		_ = json.Unmarshal([]byte(seenJSON), &ids)
		for _, id := range ids {
			seen[id] = true
		}
	}

	var newIDs []string
	for _, inv := range invasions {
		if inv.Completed {
			continue
		}
		newIDs = append(newIDs, inv.ID)
		if seen[inv.ID] {
			continue
		}
		if cond.Keyword != "" {
			kw := strings.ToLower(cond.Keyword)
			if !strings.Contains(strings.ToLower(inv.Desc), kw) &&
				!strings.Contains(strings.ToLower(inv.Node), kw) {
				continue
			}
		}
		s.fire(rule, fmt.Sprintf("⚡ Invasion: %s", rule.Name),
			fmt.Sprintf("New invasion at %s: %s", inv.Node, inv.Desc))
	}
	b, _ := json.Marshal(newIDs)
	_ = s.db.SetState(key, string(b))
}

// ─── In-Game Alerts ───────────────────────────────────────────────────────────

type gameAlert struct {
	ID        string `json:"id"`
	Node      string `json:"node"`
	MissionType string `json:"missionType"`
	Reward    struct {
		AsString string `json:"asString"`
	} `json:"reward"`
}

func (s *Scheduler) checkGameAlerts(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var alerts []gameAlert
	if err := json.Unmarshal(ws["alerts"], &alerts); err != nil {
		return
	}
	cond := parseCond(rule.Conditions)

	key := fmt.Sprintf("galert_seen_%d", rule.ID)
	seenJSON, _ := s.db.GetState(key)
	seen := map[string]bool{}
	if seenJSON != "" {
		var ids []string
		_ = json.Unmarshal([]byte(seenJSON), &ids)
		for _, id := range ids {
			seen[id] = true
		}
	}

	var newIDs []string
	for _, a := range alerts {
		newIDs = append(newIDs, a.ID)
		if seen[a.ID] {
			continue
		}
		if cond.MissionType != "" && !strings.EqualFold(a.MissionType, cond.MissionType) {
			continue
		}
		if cond.Keyword != "" {
			kw := strings.ToLower(cond.Keyword)
			if !strings.Contains(strings.ToLower(a.Reward.AsString), kw) &&
				!strings.Contains(strings.ToLower(a.Node), kw) {
				continue
			}
		}
		s.fire(rule, fmt.Sprintf("🔔 Alert: %s", rule.Name),
			fmt.Sprintf("%s | %s | Reward: %s", a.Node, a.MissionType, a.Reward.AsString))
	}
	b, _ := json.Marshal(newIDs)
	_ = s.db.SetState(key, string(b))
}

// ─── Steel Path ─────────────────────────────────────────────────────────────

type steelPathData struct {
	CurrentReward *steelPathReward `json:"currentReward"`
	Activation    string            `json:"activation"`
	Expiry        string            `json:"expiry"`
	Rotation      []interface{}     `json:"rotation"`
	Incursions    *spIncursions     `json:"incursions"`
}

type steelPathReward struct {
	Name  string `json:"name"`
	Cost  int    `json:"cost"`
}

type spIncursions struct {
	Active   bool          `json:"active"`
	Acolytes []spAcolyte   `json:"acolytes"`
	Count    int           `json:"count"`
}

type spAcolyte struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Node       string `json:"node"`
	Location   string `json:"location"`
	Health     float64 `json:"health"`
	Activation string `json:"activation"`
	Expiry     string `json:"expiry"`
}

func (s *Scheduler) checkSteelPath(rule *db.AlertRule, ws map[string]json.RawMessage) {
	var sp steelPathData
	if err := json.Unmarshal(ws["steelPath"], &sp); err != nil {
		log.Printf("Scheduler: parse steelPath: %v", err)
		return
	}
	// If no data at all, nothing to check
	if sp.CurrentReward == nil && sp.Expiry == "" && (sp.Incursions == nil || !sp.Incursions.Active) {
		return
	}

	cond := parseCond(rule.Conditions)

	// Check for new acolytes (incursions) — skip if rotationAlert flag is "false" (rotation only mode)
	if cond.RotationAlert != "false" && sp.Incursions != nil && sp.Incursions.Active && len(sp.Incursions.Acolytes) > 0 {
		seenKey := fmt.Sprintf("sp_acolytes_seen_%d", rule.ID)
		seenJSON, _ := s.db.GetState(seenKey)
		seen := map[string]bool{}
		if seenJSON != "" {
			_ = json.Unmarshal([]byte(seenJSON), &seen)
		}

		var newIDs []string
		for _, a := range sp.Incursions.Acolytes {
			newIDs = append(newIDs, a.ID)
			if seen[a.ID] {
				continue
			}
			// Apply condition filter — keyword matches acolyte name
			if cond.Keyword != "" {
				kw := strings.ToLower(cond.Keyword)
				if !strings.Contains(strings.ToLower(a.Name), kw) {
					continue
				}
			}
			s.fire(rule,
				fmt.Sprintf("⚔️ Steel Path Acolyte: %s", rule.Name),
				fmt.Sprintf("%s has spawned at %s!", a.Name, a.Node))
		}

		b, _ := json.Marshal(newIDs)
		_ = s.db.SetState(seenKey, string(b))
	}

	// Check for weekly rotation change
	if sp.Expiry != "" {
		rotKey := fmt.Sprintf("sp_rotation_%d", rule.ID)
		lastExpiry, _ := s.db.GetState(rotKey)
		if lastExpiry != sp.Expiry {
			// Only fire if the new rotation has a currentReward (i.e. it's active)
			// and we haven't seen this expiry before (ignore initial boot)
			if lastExpiry != "" && sp.CurrentReward != nil {
				s.fire(rule,
					fmt.Sprintf("🏆 Steel Path Rotation: %s", rule.Name),
					fmt.Sprintf("New Steel Path weekly rotation! Featured reward: %s (%d Steel Essence)",
						sp.CurrentReward.Name, sp.CurrentReward.Cost))
			}
			_ = s.db.SetState(rotKey, sp.Expiry)
		}
	}

	// Also re-notify if acolytes disappear (incursion cycle ended)
	if sp.Incursions == nil || !sp.Incursions.Active {
		acolyteKey := fmt.Sprintf("sp_acolytes_active_%d", rule.ID)
		wasActive, _ := s.db.GetState(acolyteKey)
		if wasActive == "true" {
			_ = s.db.SetState(acolyteKey, "false")
			s.fire(rule,
				fmt.Sprintf("✅ Steel Path Clear: %s", rule.Name),
				"Steel Path incursion cycle has ended — all Acolytes have despawned.")
		}
	} else {
		_ = s.db.SetState(fmt.Sprintf("sp_acolytes_active_%d", rule.ID), "true")
	}
}

// ─── Timer Resets ─────────────────────────────────────────────────────────────

func (s *Scheduler) checkDailyReset(rule *db.AlertRule, now time.Time) {
	// Daily reset at 00:00 UTC
	if now.Hour() != 0 || now.Minute() > 1 {
		return
	}
	key := fmt.Sprintf("daily_reset_%d", rule.ID)
	today := now.Format("2006-01-02")
	last, _ := s.db.GetState(key)
	if last == today {
		return
	}
	_ = s.db.SetState(key, today)
	s.fire(rule, fmt.Sprintf("🌅 Daily Reset: %s", rule.Name),
		"Daily reset has occurred! New dailies are available.")
}

func (s *Scheduler) checkWeeklyReset(rule *db.AlertRule, now time.Time) {
	// Weekly reset Monday 00:00 UTC
	if now.Weekday() != time.Monday || now.Hour() != 0 || now.Minute() > 1 {
		return
	}
	_, week := now.ISOWeek()
	key := fmt.Sprintf("weekly_reset_%d", rule.ID)
	thisWeek := fmt.Sprintf("%d-W%02d", now.Year(), week)
	last, _ := s.db.GetState(key)
	if last == thisWeek {
		return
	}
	_ = s.db.SetState(key, thisWeek)
	s.fire(rule, fmt.Sprintf("📅 Weekly Reset: %s", rule.Name),
		"Weekly reset! New Archon Hunt, weekly challenges, and Steel Path honors are available.")
}
