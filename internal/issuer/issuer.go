package issuer

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims interface {
	jwt.Claims
}

type Issuer interface {
	IssueAccessToken(client string, exp time.Duration) (string, error)
	IssueRefreshToken(client string, exp time.Duration) (string, error)
	ValidateAccessToken(token string) (Claims, error)
	ValidateRefreshToken(token string) (Claims, error)
}
