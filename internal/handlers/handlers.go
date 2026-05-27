package handlers

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"

	"warframe-portal/internal/db"
	"warframe-portal/internal/notifier"
	"warframe-portal/internal/warframe"
)

const sessionName = "wfp-session"

func init() {
	gob.Register(int64(0))
}

type Handler struct {
	db         *db.DB
	wf         *warframe.Client
	store      *sessions.CookieStore
	appriseURL string
}

func New(database *db.DB, wfClient *warframe.Client, store *sessions.CookieStore, appriseURL string) *Handler {
	return &Handler{db: database, wf: wfClient, store: store, appriseURL: appriseURL}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (h *Handler) sessionUser(r *http.Request) (*db.User, error) {
	sess, err := h.store.Get(r, sessionName)
	if err != nil {
		return nil, nil // treat as unauthenticated
	}
	uid, ok := sess.Values["user_id"].(int64)
	if !ok {
		return nil, nil
	}
	return h.db.GetUserByID(uid)
}

// AuthMiddleware rejects unauthenticated requests.
func (h *Handler) AuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _ := h.sessionUser(r)
		if u == nil {
			writeErr(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next(w, r)
	}
}

func (h *Handler) mustAdmin(w http.ResponseWriter, r *http.Request) *db.User {
	u, _ := h.sessionUser(r)
	if u == nil || u.Role != "admin" {
		writeErr(w, http.StatusForbidden, "admin required")
		return nil
	}
	return u
}

// ─── Auth ────────────────────────────────────────────────────────────────────

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeErr(w, 400, "username, email and password are required")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, 400, "password must be at least 8 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, 500, "server error")
		return
	}

	count, _ := h.db.CountUsers()
	role := "user"
	if count == 0 {
		role = "admin"
	}

	user, err := h.db.CreateUser(req.Username, req.Email, string(hash), role)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, 409, "username or email already taken")
		} else {
			writeErr(w, 500, "could not create user")
		}
		return
	}

	sess, _ := h.store.Get(r, sessionName)
	sess.Values["user_id"] = user.ID
	sess.Save(r, w)
	writeJSON(w, 201, user)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}

	user, err := h.db.GetUserByUsername(req.Username)
	if err != nil || user == nil {
		writeErr(w, 401, "invalid credentials")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		writeErr(w, 401, "invalid credentials")
		return
	}

	sess, _ := h.store.Get(r, sessionName)
	sess.Values["user_id"] = user.ID
	sess.Save(r, w)
	writeJSON(w, 200, user)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sess, _ := h.store.Get(r, sessionName)
	sess.Options.MaxAge = -1
	sess.Save(r, w)
	writeJSON(w, 200, map[string]string{"message": "logged out"})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	writeJSON(w, 200, u) // null if not logged in
}

// ─── Data Endpoints ───────────────────────────────────────────────────────────

func (h *Handler) GetArcanes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, json.RawMessage(warframe.GetArcanesList()))
}

func (h *Handler) GetArchonShards(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, json.RawMessage(warframe.GetArchonShardsMap()))
}

// ─── Worldstate ───────────────────────────────────────────────────────────────

func (h *Handler) GetWorldstate(w http.ResponseWriter, r *http.Request) {
	data, err := h.wf.GetWorldstate()
	if err != nil {
		writeErr(w, 500, fmt.Sprintf("worldstate error: %v", err))
		return
	}
	writeJSON(w, 200, data)
}

// ─── Alerts ───────────────────────────────────────────────────────────────────

func (h *Handler) GetAlerts(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	rules, err := h.db.GetUserAlertRules(u.ID)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if rules == nil {
		rules = []*db.AlertRule{}
	}
	writeJSON(w, 200, rules)
}

func (h *Handler) CreateAlert(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	var req struct {
		Name            string `json:"name"`
		EventType       string `json:"eventType"`
		Conditions      string `json:"conditions"`
		AppriseURLs     string `json:"appriseUrls"`
		Enabled         bool   `json:"enabled"`
		CooldownMinutes int    `json:"cooldownMinutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if req.Name == "" || req.EventType == "" {
		writeErr(w, 400, "name and eventType required")
		return
	}
	valid := map[string]bool{
		"fissure": true, "sortie": true, "arbitration": true,
		"event": true, "voidTrader": true, "archonHunt": true,
		"nightwave": true, "invasion": true, "alert": true,
		"steelPath": true, "dailyReset": true, "weeklyReset": true,
	}
	if !valid[req.EventType] {
		writeErr(w, 400, "invalid eventType")
		return
	}
	if req.Conditions == "" {
		req.Conditions = "{}"
	}
	if req.CooldownMinutes <= 0 {
		req.CooldownMinutes = 5
	}

	rule, err := h.db.CreateAlertRule(&db.AlertRule{
		UserID: u.ID, Name: req.Name, EventType: req.EventType,
		Conditions: req.Conditions, AppriseURLs: req.AppriseURLs,
		Enabled: req.Enabled, CooldownMinutes: req.CooldownMinutes,
	})
	if err != nil {
		writeErr(w, 500, "could not create alert")
		return
	}
	writeJSON(w, 201, rule)
}

func (h *Handler) UpdateAlert(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)

	var req struct {
		Name            string `json:"name"`
		EventType       string `json:"eventType"`
		Conditions      string `json:"conditions"`
		AppriseURLs     string `json:"appriseUrls"`
		Enabled         bool   `json:"enabled"`
		CooldownMinutes int    `json:"cooldownMinutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}

	rule, _ := h.db.GetAlertRule(id, u.ID)
	if rule == nil {
		writeErr(w, 404, "alert not found")
		return
	}
	if req.Conditions == "" {
		req.Conditions = "{}"
	}
	if req.CooldownMinutes <= 0 {
		req.CooldownMinutes = 5
	}

	rule.Name = req.Name
	rule.EventType = req.EventType
	rule.Conditions = req.Conditions
	rule.AppriseURLs = req.AppriseURLs
	rule.Enabled = req.Enabled
	rule.CooldownMinutes = req.CooldownMinutes

	if err := h.db.UpdateAlertRule(rule); err != nil {
		writeErr(w, 500, "could not update alert")
		return
	}
	writeJSON(w, 200, rule)
}

func (h *Handler) DeleteAlert(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err := h.db.DeleteAlertRule(id, u.ID); err != nil {
		writeErr(w, 500, "could not delete alert")
		return
	}
	writeJSON(w, 200, map[string]string{"message": "deleted"})
}

func (h *Handler) TestAlert(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)

	rule, _ := h.db.GetAlertRule(id, u.ID)
	if rule == nil {
		writeErr(w, 404, "alert not found")
		return
	}

	raw := rule.AppriseURLs
	if raw == "" {
		raw = u.DefaultURLs
	}
	var urls []string
	for _, line := range strings.Split(raw, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			urls = append(urls, l)
		}
	}
	if len(urls) == 0 {
		writeErr(w, 400, "no notification URLs configured for this rule or in your default settings")
		return
	}

	apiURL := u.AppriseAPIURL
	if apiURL == "" {
		apiURL = h.appriseURL
	}

	err := notifier.New(apiURL).Send(notifier.Notification{
		Title: fmt.Sprintf("🧪 Test: %s", rule.Name),
		Body:  fmt.Sprintf("Test notification for rule '%s' (type: %s). Everything is working!", rule.Name, rule.EventType),
		URLs:  urls,
	})
	if err != nil {
		writeErr(w, 500, fmt.Sprintf("notification failed: %v", err))
		return
	}
	writeJSON(w, 200, map[string]string{"message": "test notification sent successfully"})
}

func (h *Handler) GetAlertLog(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	logs, err := h.db.GetUserAlertLog(u.ID, 200)
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if logs == nil {
		logs = []*db.AlertLog{}
	}
	writeJSON(w, 200, logs)
}

// ─── Settings ─────────────────────────────────────────────────────────────────

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	writeJSON(w, 200, u)
}

func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	var req struct {
		AppriseAPIURL string `json:"appriseApiUrl"`
		DefaultURLs   string `json:"defaultUrls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if err := h.db.UpdateUserSettings(u.ID, req.AppriseAPIURL, req.DefaultURLs); err != nil {
		writeErr(w, 500, "could not save settings")
		return
	}
	updated, _ := h.db.GetUserByID(u.ID)
	writeJSON(w, 200, updated)
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	u, _ := h.sessionUser(r)
	var req struct {
		Current string `json:"currentPassword"`
		New     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Current)) != nil {
		writeErr(w, 401, "current password is incorrect")
		return
	}
	if len(req.New) < 8 {
		writeErr(w, 400, "new password must be at least 8 characters")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	if err := h.db.UpdateUserPassword(u.ID, string(hash)); err != nil {
		writeErr(w, 500, "could not update password")
		return
	}
	writeJSON(w, 200, map[string]string{"message": "password updated"})
}

// ─── Admin ────────────────────────────────────────────────────────────────────

func (h *Handler) AdminGetUsers(w http.ResponseWriter, r *http.Request) {
	if h.mustAdmin(w, r) == nil {
		return
	}
	users, err := h.db.GetAllUsers()
	if err != nil {
		writeErr(w, 500, "db error")
		return
	}
	if users == nil {
		users = []*db.User{}
	}
	writeJSON(w, 200, users)
}

func (h *Handler) AdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	admin := h.mustAdmin(w, r)
	if admin == nil {
		return
	}
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if id == admin.ID {
		writeErr(w, 400, "cannot delete yourself")
		return
	}
	if err := h.db.DeleteUser(id); err != nil {
		writeErr(w, 500, "could not delete user")
		return
	}
	writeJSON(w, 200, map[string]string{"message": "user deleted"})
}

func (h *Handler) AdminSetUserRole(w http.ResponseWriter, r *http.Request) {
	if h.mustAdmin(w, r) == nil {
		return
	}
	id, _ := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	var req struct{ Role string `json:"role"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if req.Role != "user" && req.Role != "admin" {
		writeErr(w, 400, "role must be 'user' or 'admin'")
		return
	}
	if err := h.db.UpdateUserRole(id, req.Role); err != nil {
		writeErr(w, 500, "could not update role")
		return
	}
	writeJSON(w, 200, map[string]string{"message": "role updated"})
}
