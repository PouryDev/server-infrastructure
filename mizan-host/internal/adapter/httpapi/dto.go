package httpapi

import (
	kitauth "github.com/PouryDev/server-infrastructure/gokit/auth"

	"github.com/PouryDev/mizan-host/internal/domain/user"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// profileResponse is what the UI shows about the signed-in user (the shape mizan-ui expects).
type profileResponse struct {
	Username string   `json:"username"`
	Name     string   `json:"name"`
	Title    string   `json:"title"`
	Company  string   `json:"company"`
	Roles    []string `json:"roles"`
}

// tokenResponse is the body for API clients (browsers never get tokens in a body).
type tokenResponse struct {
	User   profileResponse   `json:"user"`
	Tokens kitauth.TokenPair `json:"tokens"`
}

func toProfile(u user.User, company string) profileResponse {
	return profileResponse{Username: u.Username, Name: u.Name, Title: u.Title, Company: company, Roles: []string{string(u.Role)}}
}
