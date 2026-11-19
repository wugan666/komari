package public

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/web/api"
	"github.com/komari-monitor/komari/web/security"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TwoFa    string `json:"2fa_code"`
}

const sessionCookieMaxAge = 2592000

func setSessionCookie(c *gin.Context, value string, maxAge int) {
	utils.SetAuthCookie(c, "session_token", value, maxAge, http.SameSiteLaxMode)
}

func Login(c *gin.Context) {
	DisablePasswordLogin, _ := config.GetAs[bool](config.DisablePasswordLoginKey, false)
	if DisablePasswordLogin {
		api.RespondError(c, http.StatusForbidden, "Password login is disabled")
		return
	}
	c.Header("Cache-Control", "no-store")
	if !passwordLoginLimiter.allow(c.ClientIP(), time.Now()) {
		c.Header("Retry-After", "60")
		api.RespondError(c, http.StatusTooManyRequests, "Too many login attempts; retry later")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxLoginBytes)
	control := http.NewResponseController(c.Writer)
	_ = control.SetReadDeadline(time.Now().Add(security.LoginBodyTimeout))
	defer control.SetReadDeadline(time.Time{})

	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			api.RespondError(c, http.StatusRequestEntityTooLarge, "Login request is too large")
		} else {
			api.RespondError(c, http.StatusBadRequest, "Invalid request body")
		}
		return
	}
	var data LoginRequest
	err = json.Unmarshal(bodyBytes, &data)
	if err != nil {
		api.RespondError(c, http.StatusBadRequest, "Invalid request body: "+err.Error())
		return
	}
	if data.Username == "" || data.Password == "" {
		api.RespondError(c, http.StatusBadRequest, "Invalid request body: Username and password are required")
		return
	}

	ua := auditlog.Truncate(strings.TrimSpace(c.Request.UserAgent()), 160)

	// A slow body must not occupy a password-work slot. Only fully received,
	// valid credential requests compete for the bounded Argon2 work budget.
	select {
	case passwordLoginSlots <- struct{}{}:
		defer func() { <-passwordLoginSlots }()
	default:
		c.Header("Retry-After", "1")
		api.RespondError(c, http.StatusTooManyRequests, "Login is busy; retry later")
		return
	}
	uuid, passwordProof, success := accounts.CheckPasswordWithProof(data.Username, data.Password)
	if !success {
		msg := fmt.Sprintf("Login failed (password): username=%s, reason=invalid credentials", data.Username)
		if ua != "" {
			msg += ", ua=" + ua
		}
		auditlog.Log(c.ClientIP(), "", msg, "warn")
		api.RespondError(c, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	// 2FA
	user, err := accounts.GetUserByUUID(uuid)
	if err != nil {
		api.RespondError(c, http.StatusUnauthorized, "Invalid credentials")
		return
	}
	userLabel := auditlog.FormatUser(user.Username, uuid)
	used2FA := user.TwoFactor != ""
	if used2FA { // 开启了2FA
		if data.TwoFa == "" {
			api.RespondError(c, http.StatusUnauthorized, "2FA code is required")
			return
		}
		if ok, err := accounts.Verify2Fa(uuid, data.TwoFa); err != nil || !ok {
			msg := fmt.Sprintf("Login failed (password): user=%s, reason=invalid 2FA code", userLabel)
			if ua != "" {
				msg += ", ua=" + ua
			}
			auditlog.Log(c.ClientIP(), uuid, msg, "warn")
			api.RespondError(c, http.StatusUnauthorized, "Invalid 2FA code")
			return
		}
	}
	// Create session
	session, err := accounts.CreatePasswordSession(uuid, passwordProof, sessionCookieMaxAge, c.Request.UserAgent(), c.ClientIP())
	if err != nil {
		if errors.Is(err, accounts.ErrCredentialsChanged) {
			api.RespondError(c, http.StatusUnauthorized, "Invalid credentials")
		} else {
			api.RespondError(c, http.StatusInternalServerError, "Failed to create session")
		}
		return
	}
	setSessionCookie(c, session, sessionCookieMaxAge)
	msg := fmt.Sprintf("Logged in (password): user=%s, 2fa=%t", userLabel, used2FA)
	if ua != "" {
		msg += ", ua=" + ua
	}
	auditlog.Log(c.ClientIP(), uuid, msg, "login")
	api.RespondSuccess(c, gin.H{"set-cookie": gin.H{"session_token": session}})
}
func Logout(c *gin.Context) {
	session, _ := c.Cookie("session_token")
	actorUUID := ""
	msg := "Logged out"
	if session != "" {
		if user, err := accounts.GetUserBySession(session); err == nil {
			actorUUID = user.UUID
			msg = fmt.Sprintf("Logged out: user=%s, session=%s", auditlog.FormatUser(user.Username, user.UUID), auditlog.MaskToken(session))
		} else {
			msg = fmt.Sprintf("Logged out: session=%s", auditlog.MaskToken(session))
		}
	}
	accounts.DeleteSession(session)
	setSessionCookie(c, "", -1)
	auditlog.Log(c.ClientIP(), actorUUID, msg, "logout")
	c.Redirect(302, "/")
}
