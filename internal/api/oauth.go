package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/invoiceparser/api/internal/db"
)

type OAuthHandler struct {
	db *db.DB
}

func NewOAuthHandler(database *db.DB) *OAuthHandler {
	return &OAuthHandler{db: database}
}

// GitHubLogin redirects to GitHub OAuth
func (o *OAuthHandler) GitHubLogin(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("GITHUB_CLIENT_ID")
	if clientID == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "GitHub OAuth not configured",
		})
		return
	}
	redirectURI := baseURL(r) + "/auth/github/callback"
	state := generateSessionID()
	// Store state in a short-lived cookie for CSRF protection
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	authURL := fmt.Sprintf("https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=user:email&state=%s",
		clientID, url.QueryEscape(redirectURI), state)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// GitHubCallback handles the OAuth callback from GitHub
func (o *OAuthHandler) GitHubCallback(w http.ResponseWriter, r *http.Request) {
	// Verify state
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "Invalid state", http.StatusForbidden)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code", http.StatusBadRequest)
		return
	}

	clientID := os.Getenv("GITHUB_CLIENT_ID")
	clientSecret := os.Getenv("GITHUB_CLIENT_SECRET")

	// Exchange code for token
	data := url.Values{}
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("code", code)

	req, _ := http.NewRequest("POST", "https://github.com/login/oauth/access_token", strings.NewReader(data.Encode()))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("github token exchange failed", "error", err)
		http.Error(w, "OAuth failed", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &tokenResp)

	if tokenResp.AccessToken == "" {
		slog.Error("github token empty", "response", string(body))
		http.Error(w, "OAuth failed", http.StatusInternalServerError)
		return
	}

	// Get user info
	userReq, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userResp, err := client.Do(userReq)
	if err != nil {
		http.Error(w, "Failed to get user info", http.StatusInternalServerError)
		return
	}
	defer userResp.Body.Close()

	var ghUser struct {
		ID        int    `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}
	body, _ = io.ReadAll(userResp.Body)
	json.Unmarshal(body, &ghUser)

	// If email is private, fetch from emails endpoint
	if ghUser.Email == "" {
		emailReq, _ := http.NewRequest("GET", "https://api.github.com/user/emails", nil)
		emailReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
		emailResp, err := client.Do(emailReq)
		if err == nil {
			defer emailResp.Body.Close()
			var emails []struct {
				Email   string `json:"email"`
				Primary bool   `json:"primary"`
			}
			body, _ = io.ReadAll(emailResp.Body)
			json.Unmarshal(body, &emails)
			for _, e := range emails {
				if e.Primary {
					ghUser.Email = e.Email
					break
				}
			}
		}
	}

	if ghUser.Email == "" {
		http.Error(w, "Could not get email from GitHub", http.StatusBadRequest)
		return
	}

	o.finishOAuth(w, r, "github", fmt.Sprintf("%d", ghUser.ID), ghUser.Email, ghUser.Name, ghUser.AvatarURL)
}

// GoogleLogin redirects to Google OAuth
func (o *OAuthHandler) GoogleLogin(w http.ResponseWriter, r *http.Request) {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	if clientID == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "Google OAuth not configured",
		})
		return
	}
	redirectURI := baseURL(r) + "/auth/google/callback"
	state := generateSessionID()
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	authURL := fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=email+profile&state=%s",
		clientID, url.QueryEscape(redirectURI), state)
	http.Redirect(w, r, authURL, http.StatusTemporaryRedirect)
}

// GoogleCallback handles the OAuth callback from Google
func (o *OAuthHandler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "Invalid state", http.StatusForbidden)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code", http.StatusBadRequest)
		return
	}

	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	redirectURI := baseURL(r) + "/auth/google/callback"

	data := url.Values{}
	data.Set("code", code)
	data.Set("client_id", clientID)
	data.Set("client_secret", clientSecret)
	data.Set("redirect_uri", redirectURI)
	data.Set("grant_type", "authorization_code")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post("https://oauth2.googleapis.com/token", "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		http.Error(w, "OAuth failed", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
	}
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &tokenResp)

	if tokenResp.AccessToken == "" {
		slog.Error("google token empty", "response", string(body))
		http.Error(w, "OAuth failed", http.StatusInternalServerError)
		return
	}

	// Get user info
	userReq, _ := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userResp, err := client.Do(userReq)
	if err != nil {
		http.Error(w, "Failed to get user info", http.StatusInternalServerError)
		return
	}
	defer userResp.Body.Close()

	var gUser struct {
		ID      string `json:"id"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	body, _ = io.ReadAll(userResp.Body)
	json.Unmarshal(body, &gUser)

	if gUser.Email == "" {
		http.Error(w, "Could not get email from Google", http.StatusBadRequest)
		return
	}

	o.finishOAuth(w, r, "google", gUser.ID, gUser.Email, gUser.Name, gUser.Picture)
}

// finishOAuth creates/finds the user, creates a session, and redirects to the portal
func (o *OAuthHandler) finishOAuth(w http.ResponseWriter, r *http.Request, provider, providerID, email, name, avatarURL string) {
	ak, err := o.db.GetOrCreateAPIKeyByOAuth(email, provider)
	if err != nil {
		slog.Error("oauth user creation failed", "error", err, "email", email)
		http.Error(w, "Failed to create account", http.StatusInternalServerError)
		return
	}

	sessionID := generateSessionID()
	_, err = o.db.CreateSession(sessionID, ak.ID, provider, providerID, email, name, avatarURL)
	if err != nil {
		slog.Error("session creation failed", "error", err)
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	// Set session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionID,
		Path:     "/",
		MaxAge:   30 * 24 * 3600, // 30 days
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	slog.Info("oauth login", "provider", provider, "email", email)
	http.Redirect(w, r, "/portal/", http.StatusTemporaryRedirect)
}

// GetMe returns the current session user's info (for the portal)
func (o *OAuthHandler) GetMe(w http.ResponseWriter, r *http.Request) {
	session := o.sessionFromRequest(r)
	if session == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "not_logged_in"})
		return
	}

	ak, err := o.db.GetAPIKeyByID(session.APIKeyID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"error": "user_not_found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"email":      session.Email,
		"name":       session.Name,
		"avatar_url": session.AvatarURL,
		"provider":   session.Provider,
		"api_key":    ak.Key,
		"plan":       ak.Plan,
		"used_calls": ak.UsedCalls,
		"max_calls":  ak.MaxCalls,
		"member_since": ak.CreatedAt.Format(time.RFC3339),
	})
}

// Logout clears the session
func (o *OAuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		o.db.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   "session",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

// SessionAuthMiddleware creates an API key context from a session cookie
// This allows portal pages to use the same API handlers
func (o *OAuthHandler) SessionAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := o.sessionFromRequest(r)
		if session == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "not_logged_in"})
			return
		}
		ak, err := o.db.GetAPIKeyByID(session.APIKeyID)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"error": "user_not_found"})
			return
		}
		ctx := r.Context()
		ctx = setAPIKeyContext(ctx, ak)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (o *OAuthHandler) sessionFromRequest(r *http.Request) *db.Session {
	cookie, err := r.Cookie("session")
	if err != nil {
		return nil
	}
	session, err := o.db.GetSession(cookie.Value)
	if err != nil {
		return nil
	}
	return session
}

func generateSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func baseURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil {
		if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
			scheme = fwd
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + r.Host
}
