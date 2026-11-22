package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"

	"github.com/PouryDev/mizan-host/internal/usecase/account"
)

const (
	accessCookie  = "mz_at"
	refreshCookie = "mz_rt"
)

func (h *handler) options(w http.ResponseWriter, r *http.Request) error {
	years := []FiscalYear{}
	if h.deps.FiscalYears != nil {
		var err error
		if years, err = h.deps.FiscalYears(r.Context()); err != nil {
			return err
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"company": h.deps.Company, "fiscal_years": years})
	return nil
}

// login is the browser flow: the tokens never reach the page's JavaScript, they are httpOnly cookies.
func (h *handler) login(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[loginRequest](r)
	if err != nil {
		return err
	}
	s, err := h.accounts.Login(r.Context(), account.LoginInput{Username: req.Username, Password: req.Password})
	if err != nil {
		return err
	}
	h.setCookies(w, s.Tokens)
	httpx.JSON(w, http.StatusOK, toProfile(s.User, h.deps.Company))
	return nil
}

func (h *handler) refresh(w http.ResponseWriter, r *http.Request) error {
	c, err := r.Cookie(refreshCookie)
	if err != nil || c.Value == "" {
		return apperr.Unauthorized("no session")
	}
	s, err := h.accounts.Refresh(r.Context(), c.Value)
	if err != nil {
		if apperr.Is(err, apperr.KindUnauthorized) {
			h.clearCookies(w)
		}
		return err
	}
	h.setCookies(w, s.Tokens)
	httpx.JSON(w, http.StatusOK, toProfile(s.User, h.deps.Company))
	return nil
}

// logout always drops the cookies; it is public so that an expired access token cannot keep a session alive.
func (h *handler) logout(w http.ResponseWriter, r *http.Request) error {
	var access, refresh string
	if c, err := r.Cookie(accessCookie); err == nil {
		access = c.Value
	}
	if c, err := r.Cookie(refreshCookie); err == nil {
		refresh = c.Value
	}
	h.accounts.Logout(r.Context(), access, refresh)
	h.clearCookies(w)
	httpx.NoContent(w)
	return nil
}

func (h *handler) me(w http.ResponseWriter, r *http.Request) error {
	claims, ok := kitauth.ClaimsFrom(r.Context())
	if !ok {
		return apperr.Unauthorized("authentication required")
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		return apperr.Unauthorized("invalid token")
	}
	u, err := h.accounts.Me(r.Context(), id)
	if err != nil {
		if apperr.Is(err, apperr.KindNotFound) {
			return apperr.Unauthorized("invalid token")
		}
		return err
	}
	httpx.JSON(w, http.StatusOK, toProfile(u, h.deps.Company))
	return nil
}

// token is the flow for API clients and scripts: the token pair is in the body, nothing is set as a cookie, and
// the client sends "Authorization: Bearer <access_token>" itself.
func (h *handler) token(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[loginRequest](r)
	if err != nil {
		return err
	}
	s, err := h.accounts.Login(r.Context(), account.LoginInput{Username: req.Username, Password: req.Password})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, tokenResponse{User: toProfile(s.User, h.deps.Company), Tokens: s.Tokens})
	return nil
}

func (h *handler) tokenRefresh(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[refreshRequest](r)
	if err != nil {
		return err
	}
	s, err := h.accounts.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, tokenResponse{User: toProfile(s.User, h.deps.Company), Tokens: s.Tokens})
	return nil
}

func (h *handler) setCookies(w http.ResponseWriter, p kitauth.TokenPair) {
	h.cookie(w, accessCookie, p.AccessToken, "/", p.AccessExpiresAt, 0)
	h.cookie(w, refreshCookie, p.RefreshToken, "/auth", p.RefreshExpiresAt, 0)
}

func (h *handler) clearCookies(w http.ResponseWriter) {
	h.cookie(w, accessCookie, "", "/", time.Time{}, -1)
	h.cookie(w, refreshCookie, "", "/auth", time.Time{}, -1)
}

func (h *handler) cookie(w http.ResponseWriter, name, value, path string, exp time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is false only for plain-http local development (APP_ENV=development)
		Name: name, Value: value, Path: path, Expires: exp, MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: h.deps.SecureCookies,
	})
}
